package app_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/enroll/accountdriven"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/enroll/webauth"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/profile"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/secrets"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/simulator"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/crypt"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/testpki"
	"github.com/deploymenttheory/go-apple-dm/server/internal/app"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlite"
	"github.com/deploymenttheory/go-apple-dm/server/statestore"
)

// TestADEPendingAuthenticationReplica enrolls through the real browser, SCEP and
// MDM routes after the callback moves to an independent Build. The restart case
// closes the initiating application's pool before opening the replacement.
func TestADEPendingAuthenticationReplica(t *testing.T) {
	for _, restart := range []bool{false, true} {
		name := "replica"
		if restart {
			name = "restart"
		}
		t.Run(name, func(t *testing.T) {
			f := newEnrollFixture(t, "", func(c *app.Config) {
				c.Storage = "sqlite"
				c.DSN = filepath.Join(t.TempDir(), "replicas.sqlite")
				c.StorageKeys = []string{"replica"}
				c.Secrets = secrets.Static{"replica": bytes.Repeat([]byte{4}, 32)}
			})
			d := f.device(t, "UDID-DURABLE-"+name, "Mac16,1")
			var destination *app.App
			if !restart {
				destination = build(t, f.cfg)
			}
			var callback string
			var originalCookies []*http.Cookie
			switched := false
			d.Client.CheckRedirect = func(req *http.Request, _ []*http.Request) error {
				if req.URL.Scheme == accountdriven.CallbackScheme {
					return http.ErrUseLastResponse
				}
				if req.URL.Path == app.PathOIDCCallback && !switched {
					switched = true
					callback = req.URL.String()
					originalCookies = d.Client.Jar.Cookies(req.URL)
					if restart {
						if err := f.app.Close(); err != nil {
							t.Fatal(err)
						}
						destination = build(t, f.cfg)
					}
					f.handler.Store(destination)
				}
				return nil
			}
			if err := d.ADEEnroll(t.Context(), f.publicURL+app.PathADE, simulator.ADEOptions{WebView: func(_ context.Context, first *http.Response) (*http.Response, error) { return first, nil }}); err != nil {
				t.Fatal(err)
			}
			if !switched || destination == nil {
				t.Fatal("callback never changed replica")
			}
			if d.Identity == nil || !strings.Contains(d.Identity.Cert.Subject.CommonName, "alice@example.com") {
				t.Fatal("original authenticated identity was lost")
			}
			enrollment, err := destination.Store.Get(t.Context(), mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: d.UDID})
			if err != nil || !enrollment.Enabled {
				t.Fatal("replacement replica did not complete Authenticate/TokenUpdate", err)
			}
			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, callback, nil)
			if err != nil {
				t.Fatal(err)
			}
			for _, cookie := range originalCookies {
				req.AddCookie(cookie)
			}
			replayClient := *d.Client
			replayClient.Jar = nil
			res, err := replayClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			body, readErr := io.ReadAll(res.Body)
			closeErr := res.Body.Close()
			if err = errors.Join(readErr, closeErr); err != nil {
				t.Fatal(err)
			}
			if res.StatusCode != http.StatusBadRequest {
				t.Fatalf("callback replay: %d %s", res.StatusCode, body)
			}
		})
	}
}

// TestADEReplicaRejectsRetainedEvidence exercises the production OIDC callback on
// another Build after stored evidence or its identity binding becomes invalid.
func TestADEReplicaRejectsRetainedEvidence(t *testing.T) {
	for _, failure := range []string{"expired", "missing", "corrupt", "signature", "signer", "serial", "udid", "ciphertext"} {
		t.Run(failure, func(t *testing.T) {
			f := newEnrollFixture(t, "", func(c *app.Config) {
				c.Storage = "sqlite"
				c.DSN = filepath.Join(t.TempDir(), "evidence.sqlite")
				c.StorageKeys = []string{"evidence"}
				c.Secrets = secrets.Static{"evidence": bytes.Repeat([]byte{6}, 32)}
			})
			destination := build(t, f.cfg)
			d := f.device(t, "UDID-EVIDENCE-"+failure, "Mac16,1")
			original := d.Identity.Cert
			switched := false
			d.Client.CheckRedirect = func(req *http.Request, _ []*http.Request) error {
				if req.URL.Path == app.PathOIDCCallback && !switched {
					switched = true
					rewriteADEBrowserEvidence(t, f, failure)
					f.handler.Store(destination)
				}
				return nil
			}
			err := d.ADEEnroll(t.Context(), f.publicURL+app.PathADE, simulator.ADEOptions{WebView: func(_ context.Context, first *http.Response) (*http.Response, error) { return first, nil }})
			var protocolErr *simulator.HTTPError
			if !switched || !errors.As(err, &protocolErr) {
				t.Fatal("invalid callback did not fail", err)
			}
			want := http.StatusBadRequest
			if failure == "ciphertext" {
				want = http.StatusInternalServerError
			}
			if protocolErr.Status != want {
				t.Fatalf("%s callback: %v", failure, err)
			}
			if !bytes.Equal(d.Identity.Cert.Raw, original.Raw) {
				t.Fatal("invalid evidence issued an enrollment identity")
			}
			if _, err := destination.Store.Get(t.Context(), mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: d.UDID}); !errors.Is(err, storage.ErrNotFound) {
				t.Fatal("invalid evidence enrolled device", err)
			}
		})
	}
}

func rewriteADEBrowserEvidence(t *testing.T, f *enrollFixture, failure string) {
	t.Helper()
	store, ok := f.app.Store.(*sqlite.Store)
	if !ok {
		t.Fatalf("unexpected storage backend: %T", f.app.Store)
	}
	db := store.DB()
	ring, err := crypt.NewKeyring(t.Context(), crypt.Options{Keys: crypt.Keys{Active: "evidence"}, Provider: f.cfg.Secrets})
	if err != nil {
		t.Fatal(err)
	}
	st, err := statestore.Open(t.Context(), db, sqlite.Dialect, ring)
	if err != nil {
		t.Fatal(err)
	}
	records, err := st.List(t.Context(), "oidc/state/", "", 10)
	if err != nil || len(records) != 1 {
		t.Fatal("pending browser state", len(records), err)
	}
	record := records[0]
	var raw []byte
	if err = db.QueryRowContext(t.Context(), "SELECT value FROM protocol_state WHERE record_key = ?", record.Key).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if !crypt.IsSealed(raw) || bytes.Contains(raw, []byte("machineinfo")) {
		t.Fatal("MachineInfo browser evidence was not sealed")
	}
	if failure == "ciphertext" {
		raw[len(raw)-1] ^= 1
		if _, err = db.ExecContext(t.Context(), "UPDATE protocol_state SET value = ? WHERE record_key = ?", raw, record.Key); err != nil {
			t.Fatal(err)
		}
		return
	}
	if err = st.Update(t.Context(), []string{record.Key}, func(tx state.Tx) error {
		var browser webauth.State
		if err := json.Unmarshal(record.Value, &browser); err != nil {
			return err
		}
		switch failure {
		case "expired":
			browser.ExpiresAt = time.Now().Add(-time.Minute)
		case "serial":
			browser.Bound.Serial = "other-serial"
		case "udid":
			browser.Bound.UDID = "other-udid"
		case "missing":
			delete(browser.Bound.Extra, "machineinfo")
		case "corrupt":
			browser.Bound.Extra["machineinfo"] = "{"
		case "signature", "signer":
			var retained map[string]json.RawMessage
			if err := json.Unmarshal([]byte(browser.Bound.Extra["machineinfo"]), &retained); err != nil {
				return err
			}
			if failure == "signature" {
				var cms []byte
				if err := json.Unmarshal(retained["Raw"], &cms); err != nil {
					return err
				}
				cms = bytes.ReplaceAll(cms, []byte("SER-UDID"), []byte("BAD-UDID"))
				encoded, err := json.Marshal(cms)
				if err != nil {
					return err
				}
				retained["Raw"] = encoded
			} else {
				cert, keyErr := testpki.NewCA("another signer")
				if keyErr != nil {
					return keyErr
				}
				encoded, err := json.Marshal(cert.Cert.Raw)
				if err != nil {
					return err
				}
				retained["Signer"] = encoded
			}
			encoded, err := json.Marshal(retained)
			if err != nil {
				return err
			}
			browser.Bound.Extra["machineinfo"] = string(encoded)
		}
		encoded, err := json.Marshal(browser)
		if err != nil {
			return err
		}
		record.Value = encoded
		return tx.Put(t.Context(), record)
	}); err != nil {
		t.Fatal(err)
	}
}

// TestADEOverlappingBrowserEvidence proves session identity survives a later
// request for the same physical serial, independently of the latest-record cache.
func TestADEOverlappingBrowserEvidence(t *testing.T) {
	for _, latest := range []string{"present", "missing", "corrupt"} {
		t.Run(latest, func(t *testing.T) {
			f := newEnrollFixture(t, "", func(c *app.Config) {
				c.Storage = "sqlite"
				c.DSN = filepath.Join(t.TempDir(), "overlap.sqlite")
				c.StorageKeys = []string{"overlap"}
				c.Secrets = secrets.Static{"overlap": bytes.Repeat([]byte{7}, 32)}
			})
			a := f.device(t, "UDID-ORIGINAL-A", "Mac14,7")
			b := f.device(t, "UDID-OVERLAPPING-B", "iPad13,1")
			a.SerialNumber, b.SerialNumber = "SAME-PHYSICAL-SERIAL", "SAME-PHYSICAL-SERIAL"
			if bytes.Equal(a.Identity.Cert.Raw, b.Identity.Cert.Raw) {
				t.Fatal("test needs distinct signing evidence")
			}
			callbackA := startPendingADEBrowser(t, f, a)
			callbackB := startPendingADEBrowser(t, f, b)
			// B has now replaced A in the latest-per-serial namespace. Removing or
			// corrupting that unrelated record must not invalidate either pending session.
			if latest != "present" {
				st, ok := f.app.Store.(*sqlite.Store)
				if !ok {
					t.Fatalf("unexpected store %T", f.app.Store)
				}
				var err error
				if latest == "missing" {
					_, err = st.DB().ExecContext(t.Context(), "DELETE FROM protocol_state WHERE record_key LIKE 'ade/machineinfo/%'")
				} else {
					_, err = st.DB().ExecContext(t.Context(), "UPDATE protocol_state SET value = ? WHERE record_key LIKE 'ade/machineinfo/%'", []byte("corrupt-latest-record"))
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			destination := build(t, f.cfg)
			f.handler.Store(destination)
			for _, pending := range []struct {
				device   *simulator.Device
				callback string
			}{{a, callbackA}, {b, callbackB}} {
				d := pending.device
				req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, pending.callback, nil)
				if err != nil {
					t.Fatal(err)
				}
				res, err := d.Client.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				encoded, err := io.ReadAll(res.Body)
				closeErr := res.Body.Close()
				if err != nil || closeErr != nil {
					t.Fatal(err, closeErr)
				}
				if res.StatusCode != http.StatusOK {
					t.Fatalf("%s callback: %d %s", d.UDID, res.StatusCode, encoded)
				}
				if err = d.ApplyProfile(t.Context(), encoded, profile.ParseOptions{}); err != nil {
					t.Fatal(err)
				}
				if err = d.Enroll(t.Context()); err != nil {
					t.Fatal("callback lost original issuance binding", d.UDID, err)
				}
				enrolled, err := destination.Store.Get(t.Context(), mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: d.UDID})
				if err != nil || !enrolled.Enabled {
					t.Fatal(d.UDID, err)
				}
			}
		})
	}
}

func startPendingADEBrowser(t *testing.T, f *enrollFixture, d *simulator.Device) string {
	t.Helper()
	signed, err := d.SignedMachineInfo(simulator.ADEOptions{})
	if err != nil {
		t.Fatal(err)
	}
	originalRedirect := d.Client.CheckRedirect
	d.Client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if req.URL.Path == app.PathOIDCCallback {
			return http.ErrUseLastResponse
		}
		return originalRedirect(req, via)
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, f.publicURL+app.PathADE, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(simulator.HeaderDeviceInfo, base64.StdEncoding.EncodeToString(signed))
	res, err := d.Client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusFound {
		t.Fatalf("pending browser status: %d", res.StatusCode)
	}
	callback, err := res.Location()
	if err != nil {
		t.Fatal(err)
	}
	if callback.Path != app.PathOIDCCallback {
		t.Fatal("did not pause before callback", callback)
	}
	d.Client.CheckRedirect = originalRedirect
	return callback.String()
}
