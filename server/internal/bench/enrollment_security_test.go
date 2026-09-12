package bench

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

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/profile"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/commands"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/simulator"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/testpki"
)

func TestEnrollmentEvidenceRequiresCompletedIdentityAndUserChannel(t *testing.T) {
	w := testWorkspace(t, "simulated")
	e, err := Start(t.Context(), w, "", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	d, err := e.device(t.Context(), simulator.WithResponder(func(cmd *mdm.Command) simulator.Reply {
		if cmd.RequestType == "DeviceInformation" {
			return simulator.Reply{
				Status: mdm.StatusAcknowledged,
				Payload: &commands.DeviceInformationResponse{
					QueryResponses: commands.DeviceInformationResponseQueryResponses{
						OSVersion:    new("26.0"),
						BuildVersion: new("25A1"),
					},
				},
			}
		}
		return simulator.AcknowledgeAll(cmd)
	}))
	if err != nil {
		t.Fatal(err)
	}
	if err = liveEnrollment("acme")(t.Context(), e, d.UDID); !errors.Is(err, ErrBlocked) {
		t.Fatal("wrong identity method accepted", err)
	}
	if err = liveEnrollment("scep")(t.Context(), e, "absent"); !errors.Is(err, ErrBlocked) {
		t.Fatal("unknown enrollment accepted", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		tick := time.NewTicker(20 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				_, _ = d.Connect(ctx)
			}
		}
	}()
	defer func() { cancel(); <-stopped }()
	if err = liveEnrollment("scep")(t.Context(), e, d.UDID); !errors.Is(err, ErrBlocked) {
		t.Fatal("missing user channel accepted", err)
	}
	user := d.User("alice", "alice", "Alice")
	if err = authenticateUser(t.Context(), user); err != nil {
		t.Fatal(err)
	}
	if err = liveEnrollment("scep")(t.Context(), e, d.UDID); err != nil {
		t.Fatal(err)
	}
}

func TestReplacementReportsWakeFailureWithoutHidingCreatedAttempt(t *testing.T) {
	for _, mode := range []string{"created", "denied", "wake failed", "wake refused"} {
		t.Run(mode, func(t *testing.T) {
			srv := httptest.NewServer(
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					if strings.HasSuffix(r.URL.Path, "/replacement") {
						if mode == "denied" {
							w.WriteHeader(403)
							return
						}
						_, _ = io.WriteString(w, `{"ID":"attempt"}`)
						return
					}
					if mode == "wake failed" {
						w.WriteHeader(503)
						return
					}
					if mode == "wake refused" {
						_, _ = io.WriteString(w, `{"Sent":false}`)
						return
					}
					_, _ = io.WriteString(w, `{"Sent":true}`)
				}),
			)
			defer srv.Close()
			e := &Environment{Instance: Instance{URL: srv.URL}, Client: srv.Client()}
			result, err := Replace(t.Context(), e, "device", "scep")
			if mode == "created" {
				if err != nil || result["ID"] != "attempt" {
					t.Fatal(result, err)
				}
			} else if err == nil {
				t.Fatal("replacement failure hidden")
			}
			if strings.HasPrefix(mode, "wake") && result["ID"] != "attempt" {
				t.Fatal("operator lost created attempt", result)
			}
		})
	}
}

func TestTrustExportRejectsEmptyAndNonCACertificates(t *testing.T) {
	w := testWorkspace(t, "simulated")
	leaf, err := os.ReadFile(w.path("mdm", "tls.pem"))
	if err != nil {
		t.Fatal(err)
	}
	for _, data := range [][]byte{nil, leaf, []byte("-----BEGIN CERTIFICATE-----\nAA==\n-----END CERTIFICATE-----\n")} {
		if err = os.WriteFile(w.path("mdm", "ca.pem"), data, 0o600); err != nil {
			t.Fatal(err)
		}
		dest := filepath.Join(t.TempDir(), "trust.mobileconfig")
		if err = ExportTrust(w, dest); err == nil {
			t.Fatal("invalid trust exported")
		}
		if _, err = os.Stat(dest); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("invalid trust artifact created", err)
		}
	}
}

func TestDiscoveryRejectsIncompleteOrInconsistentTrust(t *testing.T) {
	for _, failure := range []string{"document", "missing URLs", "anchors", "empty anchors", "missing trust URL", "profile", "unrelated profile"} {
		t.Run(failure, func(t *testing.T) {
			srv := httptest.NewServer(
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					base := "http://" + r.Host
					switch r.URL.Path {
					case "/MDMServiceConfig":
						if failure == "document" {
							_, _ = io.WriteString(w, "invalid JSON")
							return
						}
						if failure == "missing URLs" {
							_, _ = io.WriteString(w, `{}`)
							return
						}
						doc := map[string]string{
							"dep_enrollment_url":   base + "/enroll/ade",
							"dep_anchor_certs_url": base + "/enroll/trust/anchors",
							"trust_profile_url":    base + "/enroll/trust/profile",
						}
						if failure == "missing trust URL" {
							delete(doc, "trust_profile_url")
						}
						_ = json.NewEncoder(w).Encode(doc)
					case "/enroll/trust/anchors":
						if failure == "anchors" {
							_, _ = io.WriteString(w, "invalid JSON")
							return
						}
						if failure == "empty anchors" {
							_, _ = io.WriteString(w, `[]`)
							return
						}
						_ = json.NewEncoder(w).Encode([][]byte{[]byte("anchor")})
					case "/enroll/trust/profile":
						if failure == "profile" {
							_, _ = io.WriteString(w, "invalid profile")
							return
						}
						p := &profile.Profile{Identifier: "unrelated", UUID: profile.NewUUID()}
						b, err := p.Marshal()
						if err != nil {
							t.Error(err)
							return
						}
						_, _ = w.Write(b)
					}
				}),
			)
			defer srv.Close()
			e := &Environment{Instance: Instance{URL: srv.URL}, Client: srv.Client()}
			if err := serviceDiscovery(t.Context(), e, ""); err == nil {
				t.Fatal("untrusted discovery reported success")
			}
		})
	}
}

func TestLivePreflightRequiresUsableMDMPushIdentity(t *testing.T) {
	w := testWorkspace(t, "live")
	authority, err := testpki.NewCA("test push issuer")
	if err != nil {
		t.Fatal(err)
	}
	for _, condition := range []string{"valid", "expired", "wrong key"} {
		at := time.Now().Add(-time.Minute)
		if condition == "expired" {
			at = at.Add(-400 * 24 * time.Hour)
		}
		identity, err := authority.IssuePush("com.apple.mgmt.test", at)
		if err != nil {
			t.Fatal(err)
		}
		certificate, key, err := identity.PEM()
		if err != nil {
			t.Fatal(err)
		}
		if condition == "wrong key" {
			key = []byte("invalid private key")
		}
		if err = os.WriteFile(w.path("mdm", "push.pem"), certificate, 0o600); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(w.path("mdm", "push.key"), key, 0o600); err != nil {
			t.Fatal(err)
		}
		ready, _ := EnrollmentPreflight(w, "scep")["Ready"].(bool)
		if ready != (condition == "valid") {
			t.Fatal(condition, EnrollmentPreflight(w, "scep"))
		}
	}
	if err := os.WriteFile(w.path("mdm", "tls.key"), []byte("invalid key"), 0o600); err != nil {
		t.Fatal(err)
	}
	if ready, _ := EnrollmentPreflight(w, "scep")["Ready"].(bool); ready {
		t.Fatal("broken HTTPS identity passed")
	}
}

func TestTrustBootstrapRejectsUnavailableSources(t *testing.T) {
	ctx := t.Context()
	bad := &Environment{Instance: Instance{URL: "://invalid"}, Client: http.DefaultClient}
	if _, err := publicEnrollmentGET(ctx, bad, "/MDMServiceConfig"); err == nil {
		t.Fatal("invalid discovery URL accepted")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, "untrusted content")
	}))
	defer server.Close()
	refused := &Environment{Instance: Instance{URL: server.URL}, Client: server.Client()}
	if data, err := publicEnrollmentGET(
		ctx,
		refused,
		"/enroll/trust/profile",
	); !errors.Is(err, ErrBlocked) ||
		len(data) != 0 {
		t.Fatal("denied endpoint accepted as trust material", data, err)
	}
	w := testWorkspace(t, "simulated")
	if err := os.Rename(w.path("mdm", "ca.pem"), w.path("mdm", "ca.saved")); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "trust.mobileconfig")
	if err := ExportTrust(w, dest); err == nil {
		t.Fatal("trust exported without CA material")
	}
	if _, err := os.Stat(dest); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("failed bootstrap created an artifact", err)
	}
}
