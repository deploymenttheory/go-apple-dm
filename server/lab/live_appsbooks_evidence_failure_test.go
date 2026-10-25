package lab

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/appsbooks"
)

// TestAppsBooksLicensingRequiresCoherentDurableProof rejects conflicting target
// and callback evidence and unavailable private storage through the actual runtime.
func TestAppsBooksLicensingRequiresCoherentDurableProof(t *testing.T) {
	for _, name := range []string{"intent storage", "asset storage", "original phase storage", "SDK event storage", "SDK observation storage", "SDK completion storage", "SDK readback phase storage", "reference job storage", "reference callback storage", "missing callback proof storage", "reference readback phase storage", "cleanup proof storage", "final job storage", "queued intent mismatch", "callback target mismatch", "authenticated receipt mismatch", "final target mismatch", "SDK unknown status", "cleanup unknown status", "SDK assignment absent", "ownership changed after assignment"} {
		t.Run(name, func(t *testing.T) {
			s, f, c := appsRuntime(t)
			appsInstallDueFixture(t, s, f, c)
			var hit atomic.Bool
			block := func(file string) {
				if !hit.CompareAndSwap(false, true) {
					return
				}
				if err := appsBlockScopeEvidence(s, "licensing", file); err != nil {
					t.Error(err)
				}
			}
			f.mu.Lock()
			if name == "missing callback proof storage" {
				f.dropCallbackEvent = 2
			}
			f.rewrite = func(r *http.Request, m map[string]any) {
				switch name {
				case "intent storage":
					if r.URL.Path == "/assignments" && f.events == 0 {
						block("intent.json")
					}
				case "asset storage":
					if r.URL.Path == "/assets" && f.events == 0 {
						block("asset.json")
					}
				case "original phase storage":
					if r.URL.Path == "/assets" && f.events == 0 {
						block("original")
					}
				case "SDK event storage":
					if r.URL.Path == "/associate" && f.events == 1 {
						block("sdk-associate-event.json")
					}
				case "SDK observation storage":
					if r.URL.Path == "/status" && f.events == 1 {
						block("sdk-associate-observation-0000.json")
					}
				case "SDK completion storage":
					if r.URL.Path == "/status" && f.events == 1 {
						block("sdk-associate-status.json")
					}
				case "SDK readback phase storage":
					if r.URL.Path == "/status" && f.events == 1 {
						block("sdk-readback")
					}
				case "reference readback phase storage":
					if r.URL.Path == "/status" && f.events == 2 {
						block("reference-readback")
					}
				case "cleanup proof storage":
					if r.URL.Path == "/status" && f.events == 3 {
						block("cleanup.json")
					}
				case "SDK unknown status":
					if r.URL.Path == "/status" && f.events == 1 {
						hit.Store(true)
						m["eventStatus"] = "UNKNOWN"
					}
				case "cleanup unknown status":
					if r.URL.Path == "/status" && f.events == 3 {
						hit.Store(true)
						m["eventStatus"] = "UNKNOWN"
					}
				case "SDK assignment absent":
					if r.URL.Path == "/assignments" && f.events == 1 {
						hit.Store(true)
						m["assignments"] = []any{}
					}
				case "ownership changed after assignment":
					if r.URL.Path == "/config" && f.events == 1 {
						hit.Store(true)
						m["notificationUrl"] = "https://foreign.invalid/callback"
					}
				}
			}
			f.mu.Unlock()
			transport := s.Env.Client.Transport
			s.Env.Client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
				if !strings.HasPrefix(r.URL.Path, "/admin/v1/") {
					return transport.RoundTrip(r)
				}
				response, err := transport.RoundTrip(r)
				if err != nil {
					return response, err
				}
				f.mu.Lock()
				events := f.events
				f.mu.Unlock()
				if strings.Contains(r.URL.Path, "/jobs/") && strings.HasSuffix(r.URL.Path, "/evidence") {
					switch name {
					case "reference job storage":
						if events == 2 {
							block("reference-job.json")
						}
					case "missing callback proof storage":
						if events == 2 {
							block("reference-incomplete-callback.json")
						}
					case "final job storage":
						if events == 3 {
							block("reference-final-job.json")
						}
					}
					rewrite := name == "queued intent mismatch" && events == 0 || name == "callback target mismatch" && events == 2 || name == "final target mismatch" && events == 3
					if rewrite {
						raw, readErr := io.ReadAll(response.Body)
						closeErr := response.Body.Close()
						if err := errors.Join(readErr, closeErr); err != nil {
							return nil, err
						}
						var job appsbooks.LicensingJob
						if err := json.Unmarshal(raw, &job); err != nil {
							return nil, err
						}
						if name == "queued intent mismatch" {
							job.ID = "foreign-intent"
							hit.Store(true)
						} else if job.State == appsbooks.JobSucceeded {
							for key, target := range job.Targets {
								if target.Assignment != nil {
									target.Assignment.ClientUserID = "foreign-user"
									job.Targets[key] = target
									hit.Store(true)
								}
							}
						}
						raw, err = json.Marshal(job)
						if err != nil {
							return nil, err
						}
						response.Body = io.NopCloser(bytes.NewReader(raw))
						response.ContentLength = int64(len(raw))
					}
				}
				if strings.Contains(r.URL.Path, "/notifications/") && strings.HasSuffix(r.URL.Path, "/evidence") {
					if name == "reference callback storage" {
						block("reference-callback-0000.json")
					}
					if name == "authenticated receipt mismatch" {
						raw, readErr := io.ReadAll(response.Body)
						closeErr := response.Body.Close()
						if err := errors.Join(readErr, closeErr); err != nil {
							return nil, err
						}
						var receipt appsbooks.NotificationReceipt
						if err := json.Unmarshal(raw, &receipt); err != nil {
							return nil, err
						}
						receipt.AppliedJobID = "foreign-job"
						raw, err = json.Marshal(receipt)
						if err != nil {
							return nil, err
						}
						hit.Store(true)
						response.Body = io.NopCloser(bytes.NewReader(raw))
						response.ContentLength = int64(len(raw))
					}
				}
				return response, nil
			})
			err := liveAppsBooksLicensing(appsLongContext(t), s)
			if err == nil || !hit.Load() {
				t.Fatal("coherent durable proof failure was not reached or passed", err)
			}
			if strings.Contains(err.Error(), "private-user") || strings.Contains(err.Error(), "private-token") {
				t.Fatal("private evidence failure exposed identifiers")
			}
			posts := f.postCount()
			fence, readErr := os.ReadFile(s.Env.Workspace.path("appsbooks", "pending-licensing.json"))
			beforeMutation := name == "intent storage" || name == "asset storage" || name == "original phase storage" || name == "queued intent mismatch"
			if beforeMutation {
				if posts != 0 || !os.IsNotExist(readErr) {
					t.Fatal("failed original proof allowed mutation or claimed a fence")
				}
				return
			}
			if readErr != nil || len(fence) == 0 {
				t.Fatal("unresolved proof lost its recovery fence", readErr)
			}
			s.Evidence = t.TempDir()
			if err := liveAppsBooksLicensing(appsLongContext(t), s); !errors.Is(err, ErrBlocked) {
				t.Fatal("recovery evidence failure allowed replay", err)
			}
			after, err := os.ReadFile(s.Env.Workspace.path("appsbooks", "pending-licensing.json"))
			if err != nil || !bytes.Equal(after, fence) || f.postCount() != posts {
				t.Fatal("rerun replaced evidence or guessed an inverse", err)
			}
		})
	}
}
