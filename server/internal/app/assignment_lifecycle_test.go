package app

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/axm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/inventory"
	"github.com/deploymenttheory/go-apple-dm/server/axmassignment"
)

// TestAssignmentAttemptExpiryKeepsAppAlive exercises the real 90-second child deadline through Run and App supervision.
// The HTTP transport stays in the synctest bubble, so the production deadline expires without wall-clock waiting.
func TestAssignmentAttemptExpiryKeepsAppAlive(t *testing.T) {
	for _, boundary := range []string{"accepted_activity", "lost_submission", "expired_lease"} {
		t.Run(boundary, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
				if e != nil {
					t.Fatal(e)
				}
				der, e := x509.MarshalECPrivateKey(key)
				if e != nil {
					t.Fatal(e)
				}
				keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
				repo, e := inventory.New(inventory.NewMemory())
				if e != nil {
					t.Fatal(e)
				}
				if e = repo.Migrate(t.Context(), time.Now()); e != nil {
					t.Fatal(e)
				}
				account, e := repo.SaveAccount(t.Context(), inventory.Account{ID: "named", Name: "Named", ClientID: "BUSINESSAPI.test", KeyID: "kid", Enabled: true}, keyPEM, time.Now())
				if e != nil {
					t.Fatal(e)
				}
				posts := 0
				expired := make(chan time.Duration, 2)
				reads := 0
				transport := setupRoundTrip(func(r *http.Request) (*http.Response, error) {
					block := false
					code, body := 200, `{"data":{"type":"orgDevices","id":"opaque","attributes":{"serialNumber":"SERIAL"}}}`
					switch {
					case r.URL.Path == "/token":
						body = `{"access_token":"fixture-token","token_type":"Bearer","expires_in":3600}`
					case r.URL.Path == "/v1/mdmServers/service":
						body = `{"data":{"type":"mdmServers","id":"service","attributes":{"serverName":"this-mdm"}}}`
					case r.URL.Path == "/v1/orgDevices":
						body = `{"data":[{"type":"orgDevices","id":"opaque","attributes":{"serialNumber":"SERIAL"}}]}`
					case r.URL.Path == "/v1/orgDeviceActivities" && r.Method == "POST":
						posts++
						code = 201
						body = `{"data":{"type":"orgDeviceActivities","id":"accepted","attributes":{"status":"IN_PROGRESS"}}}`
						block = boundary == "lost_submission"
					case strings.HasPrefix(r.URL.Path, "/v1/orgDeviceActivities/"):
						reads++
						block = true
					case strings.HasSuffix(r.URL.Path, "/assignedServer"):
						body = `{"data":{"type":"mdmServers","id":"service"}}`
					}
					if block {
						started := time.Now()
						if boundary == "expired_lease" {
							if e := repo.Backend.Update(r.Context(), func(tx inventory.Tx) error {
								raw, e := tx.Get(r.Context(), "assignment-lease/request")
								if e != nil {
									return e
								}
								var lease map[string]any
								if e = json.Unmarshal(raw, &lease); e != nil {
									return e
								}
								lease["until"] = time.Now().Add(-time.Second)
								raw, e = json.Marshal(lease)
								if e != nil {
									return e
								}
								return tx.Put(r.Context(), "assignment-lease/request", raw)
							}); e != nil {
								return nil, e
							}
						}
						<-r.Context().Done()
						expired <- time.Since(started)
						return nil, r.Context().Err()
					}
					w := httptest.NewRecorder()
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(code)
					_, _ = w.WriteString(body)
					response := w.Result()
					response.Request = r
					return response, nil
				})
				controller := &axmassignment.Controller{Repository: repo, Client: func(ctx context.Context, a inventory.Account, key []byte) (*axm.Client, error) {
					// #nosec G101 -- These .example URLs belong to the in-process fixture; key material is generated at runtime.
					return axm.New(ctx, axm.Config{ClientID: a.ClientID, KeyID: a.KeyID, PrivateKeyPEM: key, BaseURL: "https://fixture.example", TokenURL: "https://fixture.example/token", HTTPClient: &http.Client{Transport: transport, Timeout: 2 * time.Minute}})
				}}
				if _, e = controller.Configure(t.Context(), account.ID, account.Revision, "service", axm.ActivityLogColumns{Serial: "serial", Outcome: "status", Success: "SUCCESS"}); e != nil {
					t.Fatal(e)
				}
				if _, e = controller.Request(t.Context(), account.ID, "request", []string{"SERIAL"}, time.Now()); e != nil {
					t.Fatal(e)
				}
				app := &App{}
				app.addWorker("assignments", controller.Run)
				siblingStopped := make(chan struct{})
				app.addWorker("sibling", func(ctx context.Context) error { <-ctx.Done(); close(siblingStopped); return nil })
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				done := make(chan error, 1)
				go func() { done <- app.Run(ctx) }()
				synctest.Wait()
				time.Sleep(90 * time.Second)
				synctest.Wait()
				select {
				case elapsed := <-expired:
					if elapsed != 90*time.Second {
						t.Fatalf("deadline elapsed %s, want production90s", elapsed)
					}
				default:
					t.Fatal("actual attempt deadline did not expire")
				}
				if ctx.Err() != nil {
					t.Fatal("parent lifetime expired", ctx.Err())
				}
				select {
				case e := <-done:
					t.Fatalf("attempt expiry stopped application: %v", e)
				default:
				}
				select {
				case <-siblingStopped:
					t.Fatal("attempt expiry cancelled sibling")
				default:
				}
				w, e := controller.Get(t.Context(), account.ID, "request")
				if e != nil {
					t.Fatal(e)
				}
				if posts != 1 || w.SubmissionStartedAt == nil {
					t.Fatal("submission evidence lost or repeated", posts, w)
				}
				if boundary == "lost_submission" {
					if w.State != "uncertain" || w.AcceptedAt != nil || w.ActivityID != "" {
						t.Fatal("uncertain outcome invented acceptance", w)
					}
				} else if w.AcceptedAt == nil || w.ActivityID != "accepted" || w.TerminalStatus != "" {
					t.Fatal("accepted evidence lost", w)
				}
				time.Sleep(6 * time.Second)
				synctest.Wait()
				if posts != 1 {
					t.Fatal("timeout resubmitted mutation", posts)
				}
				if boundary != "lost_submission" && reads < 2 {
					t.Fatal("worker did not resume observation after expiry")
				}
				if boundary == "lost_submission" {
					w, e = controller.Get(t.Context(), account.ID, "request")
					if e != nil || w.State != "uncertain" || w.AcceptedAt != nil || w.ActivityID != "" || !w.Targets[0].Converged || w.Targets[0].ObservedAt == nil {
						t.Fatal("read-only reconciliation lost uncertainty", w, e)
					}
				}
				cancel()
				if e := <-done; e != nil {
					t.Fatal("normal shutdown failed", e)
				}
			})
		})
	}
}
