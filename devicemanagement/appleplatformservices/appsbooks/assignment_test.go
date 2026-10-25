package appsbooks_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/appsbooks"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
)

type confirmationService struct {
	store            *confirmationState
	mu               sync.Mutex
	mode             string
	entered, release chan struct{}
}

// confirmationFixture serves real OAuth-free VPP token/configuration and complete
// filtered pages, including an empty first page and exact unknown source fields.
func confirmationFixture(t *testing.T) (*confirmationService, *appsbooks.Workflow, appsbooks.Location, string) {
	t.Helper()
	f := &confirmationService{store: &confirmationState{Store: state.NewMemory()}}
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		mode, entered, release := f.mode, f.entered, f.release
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/service/config" {
			urls := map[string]string{}
			for _, key := range []string{"clientConfig", "getAssets", "getAssignments"} {
				urls[key] = server.URL + "/" + key
			}
			if err := json.NewEncoder(w).Encode(map[string]any{"urls": urls, "limits": map[string]int{"maxRequestPerSecond": 1000000}}); err != nil {
				t.Error(err)
			}
			return
		}
		out := map[string]any{"uId": "UID", "mdmInfo": map[string]string{"id": "ours"}, "future": json.RawMessage(`{"n":9007199254740993,"null":null,"false":false}`)}
		if mode == "foreign" {
			out["mdmInfo"] = map[string]string{"id": "foreign"}
		}
		if mode == "unclaimed" {
			delete(out, "mdmInfo")
		}
		if mode == "wrong-uid" {
			out["uId"] = "OTHER"
		}
		if r.URL.Path == "/clientConfig" {
			out["notificationAuthToken"] = "PRIVATE-CONFIGURATION"
			if mode == "permission" {
				http.Error(w, "PRIVATE-ERROR", http.StatusForbidden)
				return
			}
		} else {
			index := 0
			if r.URL.Query().Get("pageIndex") == "1" {
				index = 1
			}
			out["currentPageIndex"] = index
			out["totalPages"] = 2
			out["versionId"] = "v1"
			out["size"] = 0
			if index == 0 {
				out["nextPageIndex"] = 1
			}
			if mode == "cycle" {
				out["nextPageIndex"] = 0
			}
			if mode == "version" && index == 1 {
				out["versionId"] = "v2"
			}
			if mode == "missing-version" {
				delete(out, "versionId")
			}
			if mode == "truncated" {
				out["totalPages"] = 5
				delete(out, "nextPageIndex")
			}
			if mode == "next-out-of-range" && index == 0 {
				out["nextPageIndex"] = 2
			}
			if mode == "total-changed" && index == 1 {
				out["totalPages"] = 3
			}
			if mode == "no-total" {
				delete(out, "totalPages")
			}
			if mode == "null-total" {
				out["totalPages"] = nil
			}
			if mode == "zero-total" {
				out["totalPages"] = 0
			}

			asset := map[string]any{"adamId": "1", "pricingParam": "STDQ", "productType": "App", "deviceAssignable": true, "supportedPlatforms": []string{"iOS"}}
			switch r.URL.Path {
			case "/getAssets":
				out["assets"] = []any{}
				if index == 1 && mode != "missing-asset" {
					out["assets"] = []any{asset}
					if mode == "duplicate-asset" {
						out["assets"] = []any{asset, asset}
					}
					if mode == "foreign-asset" {
						asset["adamId"] = "2"
					}
				}
				if mode == "unlimited" && index == 1 {
					out["assets"] = []any{}
					out["unlimitedAssets"] = []any{asset}
				}
			case "/getAssignments":
				out["assignments"] = []any{}
				if index == 1 && mode != "missing-assignment" {
					a := map[string]any{"adamId": "1", "pricingParam": "STDQ"}
					if user := r.URL.Query().Get("clientUserId"); user != "" {
						a["clientUserId"] = user
						a["userStatus"] = "Associated"
						if mode == "inactive" {
							a["userStatus"] = "Retired"
						}
					} else {
						a["serialNumber"] = r.URL.Query().Get("serialNumber")
					}
					if mode == "wrong-target" {
						a["serialNumber"] = "wrong"
					}
					if mode == "missing-id" {
						delete(a, "adamId")
					}
					out["assignments"] = []any{a}
					if mode == "duplicate-assignment" {
						out["assignments"] = []any{a, a}
					}
				}
				if index == 1 && entered != nil {
					close(entered)
					select {
					case <-release:
					case <-r.Context().Done():
						return
					}
				}
			}
		}
		if err := json.NewEncoder(w).Encode(out); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(server.Close)
	w, err := appsbooks.NewWorkflow(f.store, appsbooks.Config{MDMID: "ours", BaseURL: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	token := base64.StdEncoding.EncodeToString([]byte(`{"token":"fixture","expDate":"2035-01-01T00:00:00Z"}`))
	l, err := w.Register(t.Context(), "location", 0, token)
	if err != nil {
		t.Fatal(err)
	}
	return f, w, l, token
}

// TestAssignmentConfirmation verifies complete owned assignment proof, coherent pagination and provenance fencing.
func TestAssignmentConfirmation(t *testing.T) {
	asset := appsbooks.Asset{AdamID: "1", PricingParam: "STDQ"}
	for _, mode := range []string{"device", "user", "unlimited", "no-total", "null-total"} {
		t.Run(mode, func(t *testing.T) {
			f, w, l, _ := confirmationFixture(t)
			f.mode = mode
			serial, user := "s", ""
			if mode == "user" {
				serial, user = "", "u"
			}
			p, err := w.ConfirmAssignment(t.Context(), l.ID, asset, serial, user, 3)
			if err != nil || p.LocationRevision != l.Revision || len(p.Assets.Pages) != 2 || len(p.Assignments.Pages) != 2 || p.Assignments.ObservedAt.IsZero() || len(p.Configuration) == 0 {
				t.Fatal(p, err)
			}
			if !strings.Contains(string(p.Assignments.Pages[1]), `9007199254740993`) || !strings.Contains(string(p.Configuration), "PRIVATE-CONFIGURATION") {
				t.Fatal("source evidence lost")
			}
			metadata, err := w.ReadAsset(t.Context(), l.ID, asset, 3)
			if err != nil || metadata.Asset.Asset != asset || metadata.LocationRevision != l.Revision {
				t.Fatal(metadata, err)
			}
		})
	}
	for _, mode := range []string{"foreign", "unclaimed", "wrong-uid", "permission", "cycle", "version", "missing-version", "missing-asset", "duplicate-asset", "foreign-asset", "missing-assignment", "wrong-target", "missing-id", "duplicate-assignment", "inactive", "truncated", "next-out-of-range", "total-changed", "zero-total"} {
		t.Run(mode, func(t *testing.T) {
			f, w, l, _ := confirmationFixture(t)
			f.mode = mode
			p, err := w.ConfirmAssignment(t.Context(), l.ID, asset, "", "u", 3)
			if err == nil || p.LocationID != "" || len(p.Assignments.Pages) != 0 {
				t.Fatal("incomplete/foreign proof published", p, err)
			}
			if mode == "unclaimed" && !errors.Is(err, appsbooks.ErrOwnership) {
				t.Fatal(err)
			}
			switch mode {
			case "foreign", "wrong-uid", "cycle", "version", "missing-version", "missing-asset", "duplicate-asset", "foreign-asset", "truncated", "next-out-of-range", "total-changed", "zero-total":
				if _, err := w.ReadAsset(t.Context(), l.ID, asset, 3); err == nil {
					t.Fatal("invalid capability proof accepted")
				}
			}
		})
	}
	t.Run("BoundedAndInvalidInputs", func(t *testing.T) {
		_, w, l, _ := confirmationFixture(t)
		for _, v := range []struct {
			asset        appsbooks.Asset
			serial, user string
			pages        int
		}{{asset, "s", "u", 3}, {asset, "", "", 3}, {asset, "s", "", 0}, {appsbooks.Asset{}, "s", "", 3}, {appsbooks.Asset{AdamID: "1", PricingParam: "invalid"}, "s", "", 3}} {
			_, err := w.ConfirmAssignment(t.Context(), l.ID, v.asset, v.serial, v.user, v.pages)
			if !errors.Is(err, appsbooks.ErrInput) {
				t.Fatal(err)
			}
		}
		if _, err := w.ConfirmAssignment(t.Context(), l.ID, asset, "s", "", 1); !errors.Is(err, appsbooks.ErrLimit) {
			t.Fatal(err)
		}
		if _, err := w.ReadAsset(t.Context(), l.ID, asset, 1); !errors.Is(err, appsbooks.ErrLimit) {
			t.Fatal(err)
		}
	})

	t.Run("ReadOnlyInputAndPersistenceFailures", func(t *testing.T) {
		f, w, l, _ := confirmationFixture(t)
		for _, v := range []struct {
			a appsbooks.Asset
			n int
		}{{appsbooks.Asset{}, 3}, {appsbooks.Asset{AdamID: "1", PricingParam: "bad"}, 3}, {asset, 0}} {
			if _, err := w.ReadAsset(t.Context(), l.ID, v.a, v.n); !errors.Is(err, appsbooks.ErrInput) {
				t.Fatal(err)
			}
		}
		if _, err := w.ReadAsset(t.Context(), "missing", asset, 3); !errors.Is(err, state.ErrNotFound) {
			t.Fatal(err)
		}
		if _, err := w.ConfirmAssignment(t.Context(), "missing", asset, "s", "", 3); !errors.Is(err, state.ErrNotFound) {
			t.Fatal(err)
		}
		f.store.failGet.Store(true)
		if _, err := w.ReadAsset(t.Context(), l.ID, asset, 3); err == nil {
			t.Fatal("failed source metadata read passed")
		}
		f.store.failGet.Store(false)
		for _, point := range []int64{1, 2} {
			f.store.calls.Store(0)
			f.store.failAt.Store(point)
			proof, err := w.ReadAsset(t.Context(), l.ID, asset, 3)
			if err == nil || proof.LocationID != "" {
				t.Fatal(proof, err)
			}
			f.store.calls.Store(0)
			assignment, err := w.ConfirmAssignment(t.Context(), l.ID, asset, "s", "", 3)
			if err == nil || assignment.LocationID != "" {
				t.Fatal(assignment, err)
			}
		}
	})
	t.Run("TokenRenewalFencesRead", func(t *testing.T) {
		f, w, l, token := confirmationFixture(t)
		entered, release := make(chan struct{}), make(chan struct{})
		f.mu.Lock()
		f.entered = entered
		f.release = release
		f.mu.Unlock()
		result := make(chan error, 1)
		go func() { _, err := w.ConfirmAssignment(t.Context(), l.ID, asset, "s", "", 3); result <- err }()
		<-entered
		if _, err := w.Register(t.Context(), l.ID, l.Revision, token); err != nil {
			t.Fatal(err)
		}
		close(release)
		if err := <-result; !errors.Is(err, appsbooks.ErrRevision) {
			t.Fatal(err)
		}
	})
}

// confirmationState preserves atomic storage while injecting metadata/session or
// final provenance-validation failure. No partially fetched proof may escape.
type confirmationState struct {
	state.Store
	failGet       atomic.Bool
	failAt, calls atomic.Int64
}

// Get reads the underlying record while preserving the injected failure or namespace boundary.
func (s *confirmationState) Get(ctx context.Context, key string) (state.Record, error) {
	if s.failGet.Load() {
		return state.Record{}, errors.New("metadata unavailable")
	}
	return s.Store.Get(ctx, key)
}

// Update joins the underlying transaction while preserving fault injection and atomic rollback.
func (s *confirmationState) Update(ctx context.Context, keys []string, fn func(state.Tx) error) error {
	if at := s.failAt.Load(); at > 0 && s.calls.Add(1) == at {
		return errors.New("provenance transaction failed")
	}
	return s.Store.Update(ctx, keys, fn)
}
