package appsbooks_test

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/appsbooks"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
)

var errWorkflowStorage = errors.New("fixture storage failure")

// workflowFault injects errors at each persistence boundary, including reads and
// writes inside the existing atomic transaction, without replacing its semantics.
type workflowFault struct {
	state.Store
	at    int
	calls int
}

// fail reports a deterministic fault on the addressed persistence call.
func (s *workflowFault) fail() error {
	s.calls++
	if s.at == s.calls {
		return errWorkflowStorage
	}
	return nil
}

// Get injects failures on ordinary reads.
func (s *workflowFault) Get(ctx context.Context, key string) (state.Record, error) {
	if err := s.fail(); err != nil {
		return state.Record{}, err
	}
	return s.Store.Get(ctx, key)
}

// Update injects failures before entering or while executing atomic callbacks.
func (s *workflowFault) Update(ctx context.Context, keys []string, fn func(state.Tx) error) error {
	if err := s.fail(); err != nil {
		return err
	}
	return s.Store.Update(ctx, keys, func(tx state.Tx) error { return fn(&workflowFaultTx{Tx: tx, fault: s}) })
}

type workflowFaultTx struct {
	state.Tx
	fault *workflowFault
}

// Get injects a transactional read failure.
func (tx *workflowFaultTx) Get(ctx context.Context, key string) (state.Record, error) {
	if err := tx.fault.fail(); err != nil {
		return state.Record{}, err
	}
	return tx.Tx.Get(ctx, key)
}

// Put injects a transactional write failure.
func (tx *workflowFaultTx) Put(ctx context.Context, r state.Record) error {
	if err := tx.fault.fail(); err != nil {
		return err
	}
	return tx.Tx.Put(ctx, r)
}

// fixtureWorkflow returns a real read-only HTTP fixture and an atomic fault store.
func fixtureWorkflow(t *testing.T) (*appsbooks.Workflow, *workflowFault, *fixture) {
	t.Helper()
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
		index, err := strconv.Atoi(r.URL.Query().Get("pageIndex"))
		if err != nil {
			t.Error(err)
			return
		}
		if err := json.NewEncoder(w).Encode(map[string]any{"uId": "L", "versionId": "v", "currentPageIndex": index, "size": 0, "totalPages": 1, "assets": []any{}, "assignments": []any{}, "users": []any{}}); err != nil {
			t.Error(err)
		}
	})
	store := &workflowFault{Store: state.NewMemory()}
	cfg := f.config
	cfg.SToken = ""
	cfg.UID = ""
	cfg.ReadRetries = 0
	workflow, err := appsbooks.NewWorkflow(store, cfg)
	if err != nil {
		t.Fatal(err)
	}
	return workflow, store, f
}

// TestWorkflowPersistenceFailures verifies rollback at each read/write boundary,
// including partially written token, UID binding, metadata and successful sources.
func TestWorkflowPersistenceFailures(t *testing.T) {
	for _, operation := range []string{"register", "replace", "load", "evidence", "probe", "refresh", "configure", "recover"} {
		t.Run(operation, func(t *testing.T) {
			for failAt := 1; failAt < 40; failAt++ {
				w, s, f := fixtureWorkflow(t)
				if _, err := w.RecoverConfiguration(t.Context(), "L", 0, f.config.SToken); !errors.Is(err, appsbooks.ErrInput) {
					t.Fatal(err)
				}
				var l appsbooks.Location
				if operation != "register" {
					var err error
					l, err = w.Register(t.Context(), "location", 0, f.config.SToken)
					if err != nil {
						t.Fatal(err)
					}
				}
				if operation == "recover" {
					// The mutating call has terminated before replacement recovery.
					transport := f.config.HTTPClient.Transport
					broken := *f.config.HTTPClient
					broken.Transport = workflowRecoveryTransport(func(r *http.Request) (*http.Response, error) {
						if r.Method == http.MethodPost {
							return nil, errors.New("stopped uncertain POST")
						}
						return transport.RoundTrip(r)
					})
					cfg := f.config
					cfg.SToken = ""
					cfg.UID = ""
					cfg.HTTPClient = &broken
					bad, err := appsbooks.NewWorkflow(s, cfg)
					if err != nil {
						t.Fatal(err)
					}
					if _, err := bad.Configure(t.Context(), l.ID, l.Revision, appsbooks.ClientConfigurationRequest{MDMInfo: appsbooks.MDMInfo{ID: "ours", Name: "Ours", Metadata: "stable"}}); err == nil {
						t.Fatal("POST failure accepted")
					}
					l, err = w.Load(t.Context(), l.ID)
					if err != nil || !l.ConfigurationPending {
						t.Fatal(l, err)
					}
				}
				baseline, err := s.Store.List(t.Context(), "appsbooks/", "", 100)
				if err != nil {
					t.Fatal(err)
				}
				s.calls = 0
				s.at = failAt
				var callErr error
				switch operation {
				case "register":
					_, callErr = w.Register(t.Context(), "location", 0, f.config.SToken)
				case "replace":
					_, callErr = w.Register(t.Context(), "location", l.Revision, f.config.SToken)
				case "load":
					_, callErr = w.Load(t.Context(), "location")
				case "evidence":
					_, _, callErr = w.LoadEvidence(t.Context(), "location")
				case "probe":
					_, callErr = w.Probe(t.Context(), "location", l.Revision)
				case "refresh":
					_, callErr = w.Refresh(t.Context(), "location", l.Revision, 2)
				case "recover":
					_, callErr = w.RecoverConfiguration(t.Context(), l.ID, l.Revision, f.config.SToken)
				case "configure":
					_, callErr = w.Configure(t.Context(), "location", l.Revision, appsbooks.ClientConfigurationRequest{MDMInfo: appsbooks.MDMInfo{ID: "ours", Name: "Example", Metadata: "stable"}})
				}
				if failAt > s.calls {
					if callErr != nil {
						t.Fatal(callErr)
					}
					break
				}
				if !errors.Is(callErr, errWorkflowStorage) {
					t.Fatalf("fault %d/%d: %v", failAt, s.calls, callErr)
				}
				after, err := s.Store.List(t.Context(), "appsbooks/", "", 100)
				if err != nil {
					t.Fatal(err)
				}
				// Configuration reserves before HTTP; failures finalizing retain that
				// durable pending state. All other failure paths must leave exact bytes.
				if operation != "configure" && !reflect.DeepEqual(baseline, after) {
					t.Fatalf("%s fault %d changed persisted state", operation, failAt)
				}
			}
		})
	}
}

// TestWorkflowValidationAndCorruption exercises malformed caller/stored inputs,
// revision exhaustion, and corrupt separate credentials without Apple mutations.
func TestWorkflowValidationAndCorruption(t *testing.T) {
	for _, cfg := range []appsbooks.Config{{}, {MDMID: "ours", SToken: "secret"}, {MDMID: "ours", UID: "global"}, {MDMID: "ours", ReadRetries: -1}, {MDMID: "ours", ReadRetries: 6}} {
		if _, err := appsbooks.NewWorkflow(state.NewMemory(), cfg); !errors.Is(err, appsbooks.ErrConfig) {
			t.Fatal(err)
		}
	}
	if _, err := appsbooks.NewWorkflow(nil, appsbooks.Config{MDMID: "ours"}); !errors.Is(err, appsbooks.ErrConfig) {
		t.Fatal(err)
	}
	w, s, f := fixtureWorkflow(t)
	if _, err := w.RecoverConfiguration(t.Context(), "L", 0, f.config.SToken); !errors.Is(err, appsbooks.ErrInput) {
		t.Fatal(err)
	}
	if _, err := w.Register(t.Context(), "", 0, f.config.SToken); !errors.Is(err, appsbooks.ErrInput) {
		t.Fatal(err)
	}
	if _, err := w.Register(t.Context(), "L", math.MaxUint64, f.config.SToken); !errors.Is(err, appsbooks.ErrInput) {
		t.Fatal(err)
	}
	if _, err := w.Register(t.Context(), "L", 0, "invalid"); !errors.Is(err, appsbooks.ErrConfig) {
		t.Fatal(err)
	}
	if _, err := w.Load(t.Context(), ""); !errors.Is(err, appsbooks.ErrInput) {
		t.Fatal(err)
	}
	if _, err := w.Refresh(t.Context(), "L", 1, 0); !errors.Is(err, appsbooks.ErrInput) {
		t.Fatal(err)
	}
	if _, err := w.Probe(t.Context(), "L", 1); !errors.Is(err, state.ErrNotFound) {
		t.Fatal(err)
	}
	l, err := w.Register(t.Context(), "L", 0, f.config.SToken)
	if err != nil {
		t.Fatal(err)
	}
	for _, call := range []func() error{
		func() error { _, err := w.Register(t.Context(), "L", 0, f.config.SToken); return err },
		func() error { _, err := w.Register(t.Context(), "L", l.Revision+1, f.config.SToken); return err },
		func() error { _, err := w.Probe(t.Context(), "L", l.Revision+1); return err },
	} {
		if err := call(); !errors.Is(err, appsbooks.ErrRevision) {
			t.Fatal(err)
		}
	}
	if _, err := w.Configure(t.Context(), "L", l.Revision, appsbooks.ClientConfigurationRequest{}); !errors.Is(err, appsbooks.ErrInput) {
		t.Fatal(err)
	}
	records, err := s.Store.List(t.Context(), "appsbooks/", "", 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		if strings.HasSuffix(record.Key, "/metadata") || strings.HasSuffix(record.Key, "/credentials") || strings.HasSuffix(record.Key, "/evidence") {
			original := record
			for _, data := range [][]byte{[]byte(`{`), []byte(`{}`)} {
				record.Value = data
				if err := s.Store.Update(t.Context(), []string{record.Key}, func(tx state.Tx) error { return tx.Put(t.Context(), record) }); err != nil {
					t.Fatal(err)
				}
				if strings.HasSuffix(record.Key, "/metadata") {
					if _, err := w.Load(t.Context(), "L"); !errors.Is(err, appsbooks.ErrProtocol) {
						t.Fatal(err)
					}
				} else if strings.HasSuffix(record.Key, "/credentials") {
					if _, err := w.Probe(t.Context(), "L", l.Revision); !errors.Is(err, appsbooks.ErrProtocol) {
						t.Fatal(err)
					}
				} else if string(data) == "{" {
					if _, _, err := w.LoadEvidence(t.Context(), "L"); !errors.Is(err, appsbooks.ErrProtocol) {
						t.Fatal(err)
					}
				}
			}
			if err := s.Store.Update(t.Context(), []string{record.Key}, func(tx state.Tx) error { return tx.Put(t.Context(), original) }); err != nil {
				t.Fatal(err)
			}
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := w.Probe(ctx, "L", l.Revision); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	// Typed raw captures reject malformed JSON without inventing source evidence.
	for _, out := range []any{&appsbooks.AssignmentsPage{}, &appsbooks.UsersPage{}, &appsbooks.ClientConfiguration{}} {
		if err := json.Unmarshal([]byte(`{"currentPageIndex":"wrong"}`), out); err == nil {
			// ClientConfiguration has no page index; force a typed string error.
			if err := json.Unmarshal([]byte(`{"locationName":42,"uId":42}`), out); err == nil {
				t.Fatal("invalid source accepted")
			}
		}
	}
	// The original credential is kept separately from safe location metadata.
	for _, record := range records {
		if strings.HasSuffix(record.Key, "/metadata") && strings.Contains(string(record.Value), f.config.SToken) {
			t.Fatal("credential leaked into metadata")
		}
	}
}

// workflowRecoveryTransport injects a terminated uncertain POST during setup.
type workflowRecoveryTransport func(*http.Request) (*http.Response, error)

// RoundTrip delegates to the recovery setup transport.
func (r workflowRecoveryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return r(req)
}
