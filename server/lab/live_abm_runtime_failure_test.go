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
	for _, kind := range []string{"device read", "coverage read", "coverage bound", "coverage next", "coverage no cursor", "coverage empty", "sync enqueue", "missing job", "failed job", "job read", "resource read", "missing resource", "evidence read", "evidence write", "export read", "export write", "export mismatch", "stale generation", "sync evidence write", "assertion evidence write", "coverage blank", "coverage duplicate", "coverage subset", "coverage replaced"} {
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
				case "coverage read", "coverage bound", "coverage next", "coverage no cursor", "coverage empty", "coverage blank", "coverage duplicate":
					mutate = strings.HasSuffix(path, "/coverage")
				case "sync enqueue", "missing job":
					mutate = strings.HasSuffix(path, "/sync")
				case "failed job", "job read":
					mutate = strings.Contains(path, "/jobs/")
				case "resource read", "missing resource", "stale generation", "coverage subset", "coverage replaced":
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
				case "coverage bound", "coverage next", "coverage no cursor", "coverage empty", "coverage blank", "coverage duplicate":
					var page axm.Page[axm.AppleCareCoverage]
					if err = json.Unmarshal(raw, &page); err != nil {
						return nil, err
					}
					switch kind {
					case "coverage blank":
						page.Items[0].ID = ""
					case "coverage duplicate":
						page.Items[0].ID = "repeated"
					case "coverage bound":
						page.Items = append(page.Items, page.Items...)
					case "coverage next":
						page.Meta.Paging.NextCursor = ""
						page.Links.Next = "http://PRIVATE-PAGINATION-MARKER-%zz"
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
				case "coverage subset", "coverage replaced":
					var page inventory.ResourcePage
					if err = json.Unmarshal(raw, &page); err == nil {
						for i := range page.Items {
							if page.Items[i].Source.Kind == "axm.coverage" {
								var plans []map[string]json.RawMessage
								if err = json.Unmarshal(page.Items[i].Snapshot.Raw, &plans); err != nil {
									return nil, err
								}
								if kind == "coverage subset" {
									plans = plans[:1]
								} else {
									plans[0]["id"] = json.RawMessage(`"foreign-plan"`)
								}
								page.Items[i].Snapshot.Raw, err = json.Marshal(plans)
								if err != nil {
									return nil, err
								}
							}
						}
						raw, err = json.Marshal(page)
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
			if strings.Contains(err.Error(), "PRIVATE-PAGINATION-MARKER") {
				t.Fatal("pagination URL leaked", err)
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
				if kind == "original evidence" && strings.HasSuffix(r.URL.Path, "/assigned-service") {
					scopes, globErr := filepath.Glob(filepath.Join(testSession.Evidence, "assignment-*"))
					if globErr != nil || len(scopes) != 1 {
						return nil, errors.New("test scope missing")
					}
					if mkdirErr := os.Mkdir(filepath.Join(scopes[0], "original-assignment.json"), 0o700); mkdirErr != nil {
						return nil, mkdirErr
					}
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
				manifests, globErr := filepath.Glob(filepath.Join(testSession.Evidence, "assignment-*", "cleanup.json"))
				if globErr != nil || len(manifests) != 1 {
					t.Fatal("pending manifest missing", globErr)
				}
				before, readErr := os.ReadFile(manifests[0])
				if readErr != nil || !bytes.Contains(before, []byte("pending_manual_reconciliation")) {
					t.Fatal("cleanup failure not inspectable", readErr)
				}
				originalPath := filepath.Join(filepath.Dir(manifests[0]), "original-assignment.json")
				original, readErr := os.ReadFile(originalPath)
				if readErr != nil {
					t.Fatal(readErr)
				}
				e.Client.Transport = base
				if err := liveABMAssignment(t.Context(), &testSession); err != nil {
					t.Fatal("second run with reused report directory", err)
				}
				after, readErr := os.ReadFile(manifests[0])
				if readErr != nil || !bytes.Equal(before, after) {
					t.Fatal("prior unresolved cleanup overwritten", readErr)
				}
				after, readErr = os.ReadFile(originalPath)
				if readErr != nil || !bytes.Equal(original, after) {
					t.Fatal("prior original assignment overwritten", readErr)
				}
				all, globErr := filepath.Glob(filepath.Join(testSession.Evidence, "assignment-*", "original-assignment.json"))
				if globErr != nil || len(all) != 2 {
					t.Fatal("two-run evidence not independent", globErr)
				}
			}
		})
	}
}
