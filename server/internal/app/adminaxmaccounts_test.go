package app

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/axm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/inventory"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/secrets"
)

// accountTestKey generates an independent credential for each selected account.
func accountTestKey(t *testing.T) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: raw})
}

// TestNamedAccountOperations uses independent origins and opaque IDs to detect
// accidental default-client routing. Remote bodies and OAuth errors contain a
// sentinel that must never escape the diagnostic/error boundary.
func TestNamedAccountOperations(t *testing.T) {
	calls := 0
	mode := "present"
	var onRead func()
	transport := setupRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Path != "/token" && onRead != nil {
			callback := onRead
			onRead = nil
			callback()
		}
		code, body := 200, `{"data":[]}`
		if r.URL.Path == "/token" {
			body = `{"access_token":"private-sentinel","token_type":"Bearer","expires_in":3600}`
			if mode == "revoked" {
				code = 401
				body = `{"error":"invalid_client","error_description":"private-sentinel"}`
			}
		} else if mode == "permission" {
			code = 403
			body = `{"errors":[{"detail":"private-sentinel"}]}`
		} else if mode == "transport" {
			return nil, errors.New("private-sentinel")
		} else if strings.HasPrefix(r.URL.Path, "/v1/orgDevices/") {
			want := strings.TrimSuffix(r.URL.Host, ".example") + "-opaque"
			if !strings.Contains(r.URL.Path, want) {
				code = 404
				body = `{"errors":[{"detail":"private-sentinel"}]}`
			} else if strings.HasSuffix(r.URL.Path, "/appleCareCoverage") {
				body = `{"data":[{"id":"coverage"}]}`
			} else if strings.HasSuffix(r.URL.Path, "/assignedServer") {
				body = `{"data":{"type":"mdmServers","id":"bound-service"}}`
			} else {
				body = `{"data":{"type":"orgDevices","id":"` + want + `","attributes":{"serialNumber":"SER"}}}`
			}
		} else if r.URL.Path == "/v1/orgDevices" && mode != "empty" {
			body = `{"data":[{"type":"orgDevices","id":"` + strings.TrimSuffix(r.URL.Host, ".example") + `-opaque"}]}`
		} else if strings.HasPrefix(r.URL.Path, "/v1/mdmServers/") {
			body = `{"data":{"type":"mdmServers","id":"bound-service"}}`
		}
		w := httptest.NewRecorder()
		w.WriteHeader(code)
		_, _ = w.WriteString(body)
		response := w.Result()
		response.Request = r
		return response, nil
	})
	a, err := Build(t.Context(), Config{Storage: "inmem", BootstrapToken: "bootstrap-secret", AxM: AxMConfig{HTTPClient: &http.Client{Transport: transport}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	root := bootstrapRBAC(t, a)
	key := accountTestKey(t)
	for _, id := range []string{"first", "second"} {
		// #nosec G101 -- The HTTPS origin and token endpoint are in-process fixtures.
		_, err = a.Inventory.SaveAccount(t.Context(), inventory.Account{ID: id, Name: id, ClientID: "BUSINESSAPI." + id, KeyID: "kid", Enabled: true, BaseURL: "https://" + id + ".example", TokenURL: "https://" + id + ".example/token"}, key, time.Now())
		if err != nil {
			t.Fatal(err)
		}
	}
	request := func(path string) *httptest.ResponseRecorder { t.Helper(); return rbacRequest(a, "GET", path, root, "") }
	if w := request("/axm/accounts/first/devices/first-opaque"); w.Code != 403 || calls != 0 {
		t.Fatal("denial contacted Apple", w.Code, calls)
	}
	grant := func(source string) {
		t.Helper()
		body, _ := json.Marshal(map[string]string{"Source": source})
		if w := rbacRequest(a, "PUT", "/policies/named", root, string(body)); w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	grant(`permit(principal, action == MDM::Action::"readBusinessManager", resource);`)
	if w := request("/axm/accounts/first/devices/first-opaque"); w.Code != 403 || calls != 0 {
		t.Fatal("raw denial contacted Apple", w.Code, calls)
	}
	grant(`permit(principal, action in [MDM::Action::"readBusinessManager",MDM::Action::"readRawInventory",MDM::Action::"readInventory",MDM::Action::"manageAxMAccounts"], resource);`)
	for _, id := range []string{"first", "second"} {
		for _, suffix := range []string{"devices", "devices/" + id + "-opaque", "devices/" + id + "-opaque/coverage", "devices/" + id + "-opaque/assigned-service", "services", "services/bound-service", "diagnostics", "jobs"} {
			w := request("/axm/accounts/" + id + "/" + suffix)
			if w.Code != 200 || strings.Contains(w.Body.String(), "private-sentinel") {
				t.Fatal(suffix, w.Code, w.Body.String())
			}
		}
	}
	if w := request("/axm/accounts/second/devices/first-opaque"); w.Code != 404 || strings.Contains(w.Body.String(), "private-sentinel") {
		t.Fatal(w.Code, w.Body.String())
	}
	before := calls
	for _, path := range []string{"/axm/accounts/missing/devices", "/axm/devices", "/axm/servers", "/axm/activities/id"} {
		if w := request(path); w.Code != 404 {
			t.Fatal(path, w.Code)
		}
	}
	if calls != before {
		t.Fatal("missing account fell back")
	}
	for _, tc := range []struct{ mode, connection, inventory string }{{"present", "connected", "present"}, {"empty", "connected", "empty"}, {"revoked", "authentication_failed", "unknown"}, {"permission", "permission_denied", "unknown"}, {"transport", "connection_failed", "unknown"}} {
		mode = tc.mode
		w := request("/axm/accounts/first/diagnostics")
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"connection":"`+tc.connection+`"`) || !strings.Contains(w.Body.String(), `"inventory":"`+tc.inventory+`"`) || !strings.Contains(w.Body.String(), `"synchronization":"incomplete"`) || strings.Contains(w.Body.String(), "private-sentinel") {
			t.Fatal(tc, w.Code, w.Body.String())
		}
	}
	mode = "present"
	job, err := a.Inventory.Enqueue(t.Context(), "first", false, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	peerJob, err := a.Inventory.Enqueue(t.Context(), "second", false, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if w := request("/axm/accounts/first/jobs?limit=1"); w.Code != 200 || strings.Contains(w.Body.String(), peerJob.ID) || !strings.Contains(w.Body.String(), job.ID) {
		t.Fatal("cross-account list/cursor", w.Code, w.Body.String())
	}
	if w := request("/axm/accounts/second/jobs/" + job.ID); w.Code != 404 {
		t.Fatal("cross-account job", w.Code)
	}
	if w := request("/axm/accounts/first/jobs/" + job.ID); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := request("/axm/accounts/first/jobs?limit=bad"); w.Code != 400 {
		t.Fatal(w.Code)
	}

	// Finishing a full current-revision sync distinguishes complete empty inventory
	// from mere successful connectivity. Rotating credentials invalidates it.
	job.State = "success"
	rawJob, _ := json.Marshal(job)
	if err := a.Inventory.Backend.Update(t.Context(), func(tx inventory.Tx) error { return tx.Put(t.Context(), "job/"+job.ID, rawJob) }); err != nil {
		t.Fatal(err)
	}
	if w := request("/axm/accounts/first/diagnostics"); !strings.Contains(w.Body.String(), `"synchronization":"complete"`) {
		t.Fatal(w.Body.String())
	}
	rotate := func() {
		t.Helper()
		account, e := a.Inventory.Account(t.Context(), "first")
		if e != nil {
			t.Fatal(e)
		}
		if _, e = a.Inventory.SaveAccount(t.Context(), account, accountTestKey(t), time.Now()); e != nil {
			t.Fatal(e)
		}
	}
	rotate()
	if w := request("/axm/accounts/first/diagnostics"); !strings.Contains(w.Body.String(), `"synchronization":"incomplete"`) {
		t.Fatal(w.Body.String())
	}
	for _, path := range []string{"diagnostics", "devices/first-opaque"} {
		onRead = rotate
		if w := request("/axm/accounts/first/" + path); w.Code != 409 || strings.Contains(w.Body.String(), "SER") {
			t.Fatal("stale credential response escaped", path, w.Code, w.Body.String())
		}
	}

	if w := rbacRequest(a, "POST", "/axm/accounts/first/verify", root, ""); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	mode = "revoked"
	if w := rbacRequest(a, "POST", "/axm/accounts/first/verify", root, ""); w.Code != 502 || !strings.Contains(w.Body.String(), "authentication_failed") || strings.Contains(w.Body.String(), "private-sentinel") {
		t.Fatal("verification leaked revocation", w.Code, w.Body.String())
	}
	mode = "present"
	onRead = func() {
		old, e := a.Inventory.Account(t.Context(), "second")
		if e != nil {
			t.Fatal(e)
		}
		if e = a.Inventory.DeleteAccount(t.Context(), old.ID, old.Revision, time.Now()); e != nil {
			t.Fatal(e)
		}
		old.Revision = 0
		if _, e = a.Inventory.SaveAccount(t.Context(), old, accountTestKey(t), time.Now()); e != nil {
			t.Fatal(e)
		}
	}
	if w := rbacRequest(a, "POST", "/axm/accounts/second/verify", root, ""); w.Code != 409 {
		t.Fatal("verification stamped replacement", w.Code, w.Body.String())
	}
	replacement, e := a.Inventory.Account(t.Context(), "second")
	if e != nil || replacement.VerifiedAt != nil {
		t.Fatal("replacement inherited verification", replacement, e)
	}
	account, e := a.Inventory.Account(t.Context(), "first")
	if e != nil {
		t.Fatal(e)
	}
	account.Enabled = false
	if _, e = a.Inventory.SaveAccount(t.Context(), account, nil, time.Now()); e != nil {
		t.Fatal(e)
	}
	before = calls
	if w := request("/axm/accounts/first/diagnostics"); w.Code != 409 || calls != before {
		t.Fatal("disabled account contacted Apple", w.Code)
	}
	if w := request("/axm/accounts/first"); strings.Contains(w.Body.String(), "PRIVATE KEY") {
		t.Fatal("credential exposed")
	}
}

// TestAccountFailureRedaction rejects upstream secret disclosure in every category.
func TestAccountFailureRedaction(t *testing.T) {
	for _, err := range []error{&axm.AuthError{Description: "private"}, &axm.Error{Status: 403, Body: []byte("private")}, &axm.Error{Status: 429}, errors.New("private")} {
		if strings.Contains(accountFailure(err).Error(), "private") {
			t.Fatal("remote body leaked")
		}
	}
}

// TestNamedAccountAuditFailure checks capture before remote reads and rollback of
// durable intent, using sealed SQLite credentials and the shared local transaction.
func TestNamedAccountAuditFailure(t *testing.T) {
	calls := 0
	a, err := Build(t.Context(), Config{Storage: "sqlite", DSN: filepath.Join(t.TempDir(), "accounts.sqlite"), BootstrapToken: "bootstrap-secret", StorageKeys: []string{"test"}, Secrets: secrets.Static{"test": []byte("0123456789abcdef0123456789abcdef")}, Sinks: SinkConfig{Persist: true}, AxM: AxMConfig{HTTPClient: &http.Client{Transport: setupRoundTrip(func(*http.Request) (*http.Response, error) { calls++; return nil, errors.New("unexpected remote call") })}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	root := bootstrapRBAC(t, a)
	body, _ := json.Marshal(map[string]string{"Source": `permit(principal, action in [MDM::Action::"readBusinessManager",MDM::Action::"assignBusinessManagerDevices"],resource);`})
	if w := rbacRequest(a, "PUT", "/policies/audit-account", root, string(body)); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	// #nosec G101 -- The token URL is an in-process HTTPS fixture; credentials are generated at runtime.
	account, err := a.Inventory.SaveAccount(t.Context(), inventory.Account{ID: "sealed", Name: "Sealed", ClientID: "BUSINESSAPI.test", KeyID: "kid", Enabled: true, BaseURL: "https://sealed.example", TokenURL: "https://sealed.example/token"}, accountTestKey(t), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Assignments.Configure(t.Context(), account.ID, account.Revision, "this-mdm", axm.ActivityLogColumns{Serial: "serial", Outcome: "status", Success: "SUCCESS"}); err != nil {
		t.Fatal(err)
	}
	if _, err = a.db.ExecContext(t.Context(), `CREATE TRIGGER refuse_account_capture BEFORE INSERT ON event_records WHEN NEW.type = 'admin-action' BEGIN SELECT RAISE(ABORT, 'private failure'); END`); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ method, path, body string }{{"GET", "/axm/accounts/sealed/diagnostics", ""}, {"POST", "/axm/accounts/sealed/assignments", `{"request_id":"must-rollback","serials":["SER"]}`}} {
		w := rbacRequest(a, tc.method, tc.path, root, tc.body)
		if w.Code != 503 || calls != 0 || strings.Contains(w.Body.String(), "private") {
			t.Fatal(w.Code, w.Body.String(), calls)
		}
	}
	// A failure after the remote read keeps the durable access record but withholds
	// successful response bytes. Fail only the second record for the diagnostics path.
	if _, err = a.db.ExecContext(t.Context(), "DROP TRIGGER refuse_account_capture"); err != nil {
		t.Fatal(err)
	}
	deviceRead := false
	a.cfg.AxM.HTTPClient = &http.Client{Transport: setupRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Path == "/v1/orgDevices" {
			deviceRead = true
		}
		body := `{"data":[]}`
		if r.URL.Path == "/token" {
			body = `{"access_token":"private-token","token_type":"Bearer","expires_in":3600}`
		}
		w := httptest.NewRecorder()
		_, _ = w.WriteString(body)
		response := w.Result()
		response.Request = r
		return response, nil
	})}
	if _, err = a.db.ExecContext(t.Context(), `CREATE TRIGGER refuse_completion BEFORE INSERT ON event_records WHEN NEW.type = 'admin-action' AND (SELECT COUNT(*) FROM event_records WHERE type='admin-action' AND CAST(payload AS TEXT) LIKE '%/axm/accounts/sealed/diagnostics%') > 0 BEGIN SELECT RAISE(ABORT, 'private completion failure'); END`); err != nil {
		t.Fatal(err)
	}
	w := rbacRequest(a, "GET", "/axm/accounts/sealed/diagnostics", root, "")
	if w.Code != 503 || !deviceRead || calls == 0 || strings.Contains(w.Body.String(), "connected") {
		t.Fatal("completion failure released data", w.Code, w.Body.String(), calls)
	}
	var starts int
	if err = a.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM event_records WHERE type='admin-action' AND CAST(payload AS TEXT) LIKE '%/axm/accounts/sealed/diagnostics%'`).Scan(&starts); err != nil || starts != 1 {
		t.Fatal("start evidence missing", starts, err)
	}

	if _, err = a.Assignments.Get(t.Context(), account.ID, "must-rollback"); !errors.Is(err, inventory.ErrNotFound) {
		t.Fatal("unaudited intent persisted", err)
	}
}

// interleavedAccountBackend attempts a credential replacement immediately after
// the metadata read, whether the caller uses standalone reads or an atomic view.
type interleavedAccountBackend struct {
	inventory.Backend
	once    sync.Once
	replace func()
	started chan struct{}
	done    chan struct{}
}

// replaceAfterMetadata starts a competing writer at the exact read boundary.
func (b *interleavedAccountBackend) replaceAfterMetadata(key string) {
	if key != "account/atomic" {
		return
	}
	b.once.Do(func() { go func() { close(b.started); b.replace(); close(b.done) }(); <-b.started })
}

// Get reproduces the unsafe standalone-read interleaving deterministically.
func (b *interleavedAccountBackend) Get(ctx context.Context, key string) (json.RawMessage, error) {
	raw, err := b.Backend.Get(ctx, key)
	b.replaceAfterMetadata(key)
	if key == "account/atomic" {
		<-b.done
	}
	return raw, err
}

// Update retains the backend's serialization while arranging the competing writer.
func (b *interleavedAccountBackend) Update(ctx context.Context, fn func(inventory.Tx) error) error {
	return b.Backend.Update(ctx, func(tx inventory.Tx) error { return fn(interleavedAccountTx{Tx: tx, backend: b}) })
}

type interleavedAccountTx struct {
	inventory.Tx
	backend *interleavedAccountBackend
}

// Get starts rotation while the current transaction still owns its snapshot.
func (tx interleavedAccountTx) Get(ctx context.Context, key string) (json.RawMessage, error) {
	raw, err := tx.Tx.Get(ctx, key)
	tx.backend.replaceAfterMetadata(key)
	return raw, err
}

// TestNamedAccountAtomicCredentialSnapshot forces rotation between metadata and
// key access. The old identity retains its old signing key; the new identity is
// committed independently and causes the old connector's response fence to fail.
func TestNamedAccountAtomicCredentialSnapshot(t *testing.T) {
	repo, err := inventory.New(inventory.NewMemory())
	if err != nil {
		t.Fatal(err)
	}
	oldKey, newKey := accountTestKey(t), accountTestKey(t)
	// #nosec G101 -- Fixture URLs never leave the supplied in-process transport.
	account, err := repo.SaveAccount(t.Context(), inventory.Account{ID: "atomic", Name: "Atomic", ClientID: "BUSINESSAPI.old", KeyID: "old", Enabled: true, BaseURL: "https://old.example", TokenURL: "https://old.example/token"}, oldKey, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	rotationError := make(chan error, 1)
	backend := &interleavedAccountBackend{Backend: repo.Backend, started: make(chan struct{}), done: make(chan struct{})}
	backend.replace = func() {
		replacement := account
		replacement.ClientID = "BUSINESSAPI.new"
		replacement.KeyID = "new"
		replacement.BaseURL = "https://new.example"
		replacement.TokenURL = "https://new.example/token"
		_, err := repo.SaveAccount(t.Context(), replacement, newKey, time.Now())
		rotationError <- err
	}
	isolated, err := inventory.New(backend)
	if err != nil {
		t.Fatal(err)
	}
	a := &App{Inventory: isolated}
	snapshot, key, err := a.accountSnapshot(t.Context(), account.ID)
	if err != nil {
		t.Fatal(err)
	}
	<-backend.done
	if err = <-rotationError; err != nil {
		t.Fatal(err)
	}
	if snapshot.ClientID != account.ClientID || snapshot.TokenURL != account.TokenURL || !bytes.Equal(key, oldKey) {
		t.Fatal("mixed identity/key snapshot", snapshot.ClientID, snapshot.TokenURL)
	}
	current, key, err := a.accountSnapshot(t.Context(), account.ID)
	if err != nil || current.ClientID != "BUSINESSAPI.new" || !bytes.Equal(key, newKey) {
		t.Fatal("replacement not preserved", current, err)
	}
}
