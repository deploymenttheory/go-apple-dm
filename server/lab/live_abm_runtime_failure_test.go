package lab

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/axm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/inventory"
)

// abmRuntimeFailures introduces a single failure at each real production boundary.
// Successful setup and every unmodified response still execute the runtime/SDK.
func abmRuntimeFailures(t *testing.T, s *Session, c abmAcceptance) {
	t.Helper()
	e := s.Env
	base := e.Client.Transport
	e.ABM.Assign("opaque-designated", c.Service)
	for _, kind := range []string{"device read", "coverage read", "coverage bound", "coverage next", "coverage no cursor", "coverage empty", "sync enqueue", "missing job", "failed job", "job read", "resource read", "missing resource", "evidence read", "evidence write", "export read", "export write", "export mismatch", "stale generation", "sync evidence write", "assertion evidence write"} {
		t.Run(kind, func(t *testing.T) {
			testSession := *s
			testSession.Evidence = t.TempDir()
			if kind == "sync evidence write" {
				if err := os.Mkdir(filepath.Join(testSession.Evidence, "synchronization.json"), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "assertion evidence write" {
				if err := os.Mkdir(filepath.Join(testSession.Evidence, "source-assertions.json"), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "evidence write" {
				if err := os.Mkdir(filepath.Join(testSession.Evidence, "axm.device-evidence.json"), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "export write" {
				if err := os.Mkdir(filepath.Join(testSession.Evidence, "axm.device-export.json"), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			injected := false
			e.Client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
				response, err := base.RoundTrip(r)
				if err != nil {
					return response, err
				}
				path := r.URL.Path
				mutate := false
				switch kind {
				case "device read":
					mutate = strings.HasSuffix(path, "/devices") && strings.Contains(path, "/axm/")
				case "coverage read", "coverage bound", "coverage next", "coverage no cursor", "coverage empty":
					mutate = strings.HasSuffix(path, "/coverage")
				case "sync enqueue", "missing job":
					mutate = strings.HasSuffix(path, "/sync")
				case "failed job", "job read":
					mutate = strings.Contains(path, "/jobs/")
				case "resource read", "missing resource", "stale generation":
					mutate = path == "/admin/v1/inventory/raw/devices"
				case "evidence read":
					mutate = strings.HasSuffix(path, "/evidence")
				case "export read", "export mismatch":
					mutate = strings.HasSuffix(path, "/exports")
				}
				if !mutate {
					return response, nil
				}
				injected = true
				raw, err := io.ReadAll(response.Body)
				if closeErr := response.Body.Close(); err == nil {
					err = closeErr
				}
				if err != nil {
					return nil, err
				}
				switch kind {
				case "coverage bound", "coverage next", "coverage no cursor", "coverage empty":
					var page axm.Page[axm.AppleCareCoverage]
					if err = json.Unmarshal(raw, &page); err != nil {
						return nil, err
					}
					switch kind {
					case "coverage bound":
						page.Items = append(page.Items, page.Items...)
					case "coverage next":
						page.Links.Next = "http://%zz"
					case "coverage no cursor":
						page.Links.Next = "https://apple.example/next"
					case "coverage empty":
						page.Items = nil
					}
					raw, err = json.Marshal(page)
				case "missing job":
					var job inventory.Job
					if err = json.Unmarshal(raw, &job); err == nil {
						job.ID = ""
						raw, err = json.Marshal(job)
					}
				case "failed job":
					var job inventory.Job
					if err = json.Unmarshal(raw, &job); err == nil {
						job.State = "failed"
						raw, err = json.Marshal(job)
					}
				case "stale generation":
					var page inventory.ResourcePage
					if err = json.Unmarshal(raw, &page); err == nil {
						for i := range page.Items {
							page.Items[i].Snapshot.Generation = "prior-attempt"
						}
						raw, err = json.Marshal(page)
					}
				case "missing resource":
					var page inventory.ResourcePage
					if err = json.Unmarshal(raw, &page); err == nil {
						page.Items = nil
						raw, err = json.Marshal(page)
					}
				case "export mismatch":
					var records []map[string]json.RawMessage
					if err = json.Unmarshal(raw, &records); err == nil {
						for _, record := range records {
							delete(record, "original_json")
						}
						raw, err = json.Marshal(records)
					}
				default:
					response.StatusCode = 403
				}
				if err != nil {
					return nil, err
				}
				response.Body = io.NopCloser(bytes.NewReader(raw))
				return response, nil
			})
			defer func() { e.Client.Transport = base }()
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			err := liveABMCollection(ctx, &testSession)
			if err == nil {
				t.Fatal("boundary failure passed")
			}
			if !injected && kind != "evidence write" && kind != "export write" && kind != "sync evidence write" && kind != "assertion evidence write" {
				t.Fatal("failure boundary not exercised", err)
			}
			if kind == "coverage empty" && !errors.Is(err, ErrBlocked) {
				t.Fatal("empty coverage not unexercised", err)
			}
		})
	}
	for _, kind := range []string{"opt in", "binding missing", "binding differs", "original read", "foreign original", "original evidence", "device unavailable", "submission denied"} {
		t.Run(kind, func(t *testing.T) {
			testSession := *s
			testSession.Evidence = t.TempDir()
			selection := c
			switch kind {
			case "opt in":
				selection.Mutate = false
			case "binding differs":
				selection.Service = "other"
			case "foreign original":
				e.ABM.Assign("opaque-designated", "foreign")
				defer e.ABM.Assign("opaque-designated", "")
			case "original evidence":
				if err := os.Mkdir(filepath.Join(testSession.Evidence, "original-assignment.json"), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			abmTestConfig(t, &testSession, selection)
			e.Client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
				if kind == "submission denied" && r.Method == "POST" {
					return &http.Response{StatusCode: 403, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("denied")), Request: r}, nil
				}
				response, err := base.RoundTrip(r)
				if err != nil {
					return response, err
				}
				if kind == "device unavailable" && strings.HasSuffix(r.URL.Path, "/devices") {
					response.StatusCode = 403
				}
				if kind == "binding missing" && strings.HasSuffix(r.URL.Path, "/assignment-service") {
					response.StatusCode = 404
				}
				if kind == "original read" && strings.HasSuffix(r.URL.Path, "/assigned-service") {
					response.StatusCode = 403
				}
				return response, nil
			})
			defer func() { e.Client.Transport = base; abmTestConfig(t, s, c) }()
			if err := liveABMAssignment(t.Context(), &testSession); err == nil {
				t.Fatal("missing assignment prerequisite passed")
			}
			if kind == "submission denied" {
				raw, err := os.ReadFile(filepath.Join(testSession.Evidence, "cleanup.json"))
				if err != nil || !bytes.Contains(raw, []byte("pending_manual_reconciliation")) {
					t.Fatal("cleanup failure not inspectable", err)
				}
			}
		})
	}
}
