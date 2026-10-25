package lab

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/axm"
	"github.com/deploymenttheory/go-apple-dm/server/axmassignment"
)

// abmFailureSession supplies isolated HTTP boundaries for negative-only evidence guards.
func abmFailureSession(t *testing.T, fn http.HandlerFunc) *Session {
	t.Helper()
	srv := httptest.NewServer(fn)
	t.Cleanup(srv.Close)
	return &Session{Env: &Environment{Instance: Instance{Mode: "live", URL: srv.URL}, Client: srv.Client(), Workspace: &Workspace{Directory: t.TempDir()}}, Evidence: t.TempDir()}
}

// TestABMConfigurationAndPrivateEvidence rejects absent scope and failed private evidence writes.
func TestABMConfigurationAndPrivateEvidence(t *testing.T) {
	s := &Session{Env: &Environment{}}
	if _, err := abmConfig(s); !errors.Is(err, ErrBlocked) {
		t.Fatal(err)
	}
	s.Env.Workspace = &Workspace{Directory: t.TempDir()}
	if _, err := abmConfig(s); !errors.Is(err, ErrBlocked) {
		t.Fatal(err)
	}
	if err := os.MkdirAll(s.Env.Workspace.path("abm", "acceptance.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := abmConfig(s); err == nil || errors.Is(err, ErrBlocked) {
		t.Fatal(err)
	}
	if err := os.Remove(s.Env.Workspace.path("abm", "acceptance.json")); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`{`, `{}`, `{"account":"a","serial":"S"} {}`, `{"account":"a","serial":"S","unknown":true}`} {
		if err := os.WriteFile(s.Env.Workspace.path("abm", "acceptance.json"), []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := abmConfig(s); !errors.Is(err, ErrBlocked) {
			t.Fatal(err)
		}
	}
	for _, fn := range []func(context.Context, *Session) error{liveABMCollection, liveABMCorrelation, liveABMAssignment} {
		if err := fn(t.Context(), s); !errors.Is(err, ErrBlocked) {
			t.Fatal("configuration failure hidden", err)
		}
	}
	if err := abmSave(s, "evidence", nil); !errors.Is(err, ErrBlocked) {
		t.Fatal(err)
	}
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	s.Evidence = filepath.Join(blocker, "child")
	if err := abmSave(s, "evidence", nil); err == nil {
		t.Fatal("evidence directory error hidden")
	}
	s.Evidence = t.TempDir()
	if err := os.Mkdir(filepath.Join(s.Evidence, "evidence"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := abmSave(s, "evidence", nil); err == nil {
		t.Fatal("evidence write error hidden")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if !errors.Is(abmPause(ctx), context.Canceled) {
		t.Fatal("cancellation hidden")
	}
}

// TestABMRequestClassificationsDoNotLeak distinguishes permissions and prerequisites without exposing identifiers.
func TestABMRequestClassificationsDoNotLeak(t *testing.T) {
	for _, tc := range []struct {
		status  int
		body    string
		blocked bool
	}{
		{401, `PRIVATE SERIAL TOKEN`, false},
		{403, `PRIVATE SERIAL TOKEN`, false},
		{404, `PRIVATE SERIAL TOKEN`, true},
		{502, `{"Error":"permission_denied"}`, false},
		{502, `{"Error":"authentication_failed"}`, false},
		{500, `PRIVATE SERIAL TOKEN`, false},
		{200, `invalid`, false},
	} {
		t.Run(http.StatusText(tc.status)+tc.body, func(t *testing.T) {
			s := abmFailureSession(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			})
			var out map[string]any
			_, err := abmRequest(t.Context(), s, "GET", "/PRIVATE-IDENTIFIER", nil, &out)
			if err == nil || errors.Is(err, ErrBlocked) != tc.blocked || strings.Contains(err.Error(), "PRIVATE") {
				t.Fatalf("classification/leak: %v", err)
			}
			if _, err = abmRequest(t.Context(), s, "POST", "/anything", make(chan int), nil); err == nil {
				t.Fatal("marshal failure hidden")
			}
		})
	}
}

// TestABMEnumerationRejectsIncompleteDiscovery rejects ambiguous identities, incomplete pages and invalid detail.
func TestABMEnumerationRejectsIncompleteDiscovery(t *testing.T) {
	for _, kind := range []string{"empty", "bound", "empty id", "duplicate id", "ambiguous", "repeat cursor", "bad next", "no cursor", "detail identity", "detail error", "next link"} {
		t.Run(kind, func(t *testing.T) {
			reads := 0
			s := abmFailureSession(t, func(w http.ResponseWriter, r *http.Request) {
				reads++
				if strings.HasSuffix(r.URL.Path, "/devices/opaque") {
					if kind == "detail error" {
						w.WriteHeader(500)
						return
					}
					_, _ = io.WriteString(w, `{"id":"WRONG","attributes":{"serialNumber":"OTHER"}}`)
					return
				}
				device := axm.OrgDevice{ID: "opaque", Attributes: axm.OrgDeviceAttributes{SerialNumber: "S"}}
				p := axm.Page[axm.OrgDevice]{Items: []axm.OrgDevice{device}}
				switch kind {
				case "empty":
					p.Items = nil
				case "bound":
					p.Items = append(p.Items, device)
				case "empty id":
					p.Items[0].ID = ""
				case "duplicate id":
					p.Meta.Paging.NextCursor = "next"
				case "ambiguous":
					p.Items[0].ID = "different"
					if reads == 1 {
						p.Items[0].ID = "opaque"
						p.Meta.Paging.NextCursor = "next"
					}
				case "repeat cursor":
					p.Items = nil
					p.Meta.Paging.NextCursor = "next"
				case "bad next":
					p.Items = nil
					p.Links.Next = "http://%zz"
				case "no cursor":
					p.Items = nil
					p.Links.Next = "https://apple.example/no-cursor"
				case "next link":
					p.Items = nil
					p.Links.Next = "https://apple.example/?cursor=next"
				}
				if err := json.NewEncoder(w).Encode(p); err != nil {
					t.Error(err)
				}
			})
			_, err := abmDevice(t.Context(), s, abmAcceptance{Account: "a", Serial: "S"})
			if err == nil {
				t.Fatal("incomplete enumeration passed")
			}
			if kind == "empty" && !errors.Is(err, ErrBlocked) {
				t.Fatal("empty discovery not unexercised")
			}
		})
	}
}

// These are negative evidence guards, never substitute live or fixture success.
func TestABMAssignmentRejectsInsufficientEvidence(t *testing.T) {
	now := time.Now()
	c := abmAcceptance{Account: "a", Serial: "S", Service: "mdm"}
	d := axm.OrgDevice{ID: "opaque"}
	for _, kind := range []string{"accepted only", "uncertain", "fenced", "unknown state", "missing acceptance", "target mismatch", "log failed", "readback differs", "log unavailable", "evidence write"} {
		t.Run(kind, func(t *testing.T) {
			s := abmFailureSession(t, func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/log") {
					if kind == "log unavailable" {
						w.WriteHeader(403)
						return
					}
					if kind == "log failed" {
						_, _ = io.WriteString(w, "serial,status\nS,FAILED\n")
					} else {
						_, _ = io.WriteString(w, "serial,status\nS,OK\n")
					}
					return
				}
				if strings.HasSuffix(r.URL.Path, "/assigned-service") {
					_, _ = io.WriteString(w, `{"id":"wrong"}`)
					return
				}
				workflow := axmassignment.Workflow{ID: "request", Binding: axmassignment.Binding{AccountID: "a", Columns: axm.ActivityLogColumns{Serial: "serial", Outcome: "status", Success: "OK"}}, State: "complete", ActivityID: "activity", AcceptedAt: &now, TerminalStatus: "COMPLETED", LogComplete: true, LogSucceeded: true, Targets: []axmassignment.Target{{Serial: "S", DeviceID: "opaque", Outcome: "OK", Converged: true, ObservedAt: &now, ObservedService: "mdm"}}}
				switch kind {
				case "accepted only":
					workflow.State = "accepted"
				case "uncertain", "fenced":
					workflow.State = kind
				case "unknown state":
					workflow.State = "failed"
				case "missing acceptance":
					workflow.AcceptedAt = nil
				case "target mismatch":
					workflow.Targets[0].Serial = "OTHER"
				}
				if err := json.NewEncoder(w).Encode(workflow); err != nil {
					t.Error(err)
				}
			})
			if kind == "evidence write" {
				s.Evidence = ""
			}
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
			defer cancel()
			err := abmOperation(ctx, s, c, d, "request", "mdm")
			if err == nil {
				t.Fatal("insufficient per-device evidence passed")
			}
		})
	}
}
