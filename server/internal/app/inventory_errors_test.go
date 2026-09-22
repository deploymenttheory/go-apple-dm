package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/inventory"
)

var errInventoryStorage = errors.New("inventory storage unavailable")

type inventoryFailureBackend struct {
	inventory.Backend
	prefix         string
	reads, updates bool
}

// Get injects a document read failure while retaining the real inventory store.
func (b inventoryFailureBackend) Get(ctx context.Context, k string) (json.RawMessage, error) {
	if b.reads && strings.HasPrefix(k, b.prefix) {
		return nil, errInventoryStorage
	}
	return b.Backend.Get(ctx, k)
}

// Scan injects a page read failure at the selected namespace.
func (b inventoryFailureBackend) Scan(ctx context.Context, p, a string, n int) ([]inventory.Entry, error) {
	if b.reads && strings.HasPrefix(p, b.prefix) {
		return nil, errInventoryStorage
	}
	return b.Backend.Scan(ctx, p, a, n)
}

// Update rejects mutations before any underlying state can change.
func (b inventoryFailureBackend) Update(ctx context.Context, fn func(inventory.Tx) error) error {
	if b.updates {
		return errInventoryStorage
	}
	return b.Backend.Update(ctx, fn)
}

// inventoryTestMux mounts real handlers without authorization to isolate their input contracts.
func inventoryTestMux(a *App) *http.ServeMux {
	mux := http.NewServeMux()
	for _, route := range a.inventoryRoutes() {
		mux.Handle(route.Pattern, route.Handler)
	}
	return mux
}

// TestInventoryHTTPValidation checks malformed requests and errors before response streaming starts.
func TestInventoryHTTPValidation(t *testing.T) {
	r, err := inventory.New(inventory.NewMemory())
	if err != nil {
		t.Fatal(err)
	}
	a := &App{Inventory: r}
	mux := inventoryTestMux(a)
	private := url.QueryEscape(`[{"field":"axm.private","operator":"eq","value":1}]`)
	cases := []struct {
		method, path, body string
		want               int
	}{
		{"POST", "/axm/accounts", "{", 400},
		{"POST", "/axm/accounts", `{"account":{},"private_key_pem":"bad"}`, 400},
		{"POST", "/axm/accounts", `{"account":{"dep_accounts":["missing"]}}`, 400},
		{"PUT", "/axm/accounts/missing", `{"account":{}}`, 404},
		{"DELETE", "/axm/accounts/missing?revision=bad", "", 400},
		{"DELETE", "/axm/accounts/missing?revision=1", "", 404},
		{"POST", "/axm/accounts/missing/verify", "", 404},
		{"POST", "/axm/accounts/missing/sync", "", 404},
		{"GET", "/inventory/jobs?limit=bad", "", 400},
		{"GET", "/inventory/jobs?limit=-1", "", 400},
		{"GET", "/inventory/jobs/missing", "", 404},
		{"POST", "/inventory/jobs/missing/pause", "", 404},
		{"PUT", "/inventory/schedules/a", "{", 400},
		{"PUT", "/inventory/schedules/a", `{"expression":"bad"}`, 400},
		{"PUT", "/inventory/presets/a", "{", 400},
		{"PUT", "/inventory/presets/a", `{}`, 400},
		{"POST", "/devices/missing/collect", "", 404},
		{"GET", "/devices/missing", "", 404},
		{"GET", "/inventory/exports?format=bad", "", 400},
		{"GET", "/inventory/exports?columns=axm.private", "", 400},
	}
	for _, path := range []string{"/devices", "/devices/fields", "/inventory/raw/devices", "/inventory/raw/devices/fields", "/inventory/reports", "/inventory/exports", "/inventory/raw/exports"} {
		for _, query := range []string{"limit=bad", "as_of=bad", "where=%7B", "focus=bad"} {
			cases = append(cases, struct {
				method, path, body string
				want               int
			}{"GET", path + "?" + query, "", 400})
		}
		if !strings.Contains(path, "/raw/") {
			cases = append(cases, struct {
				method, path, body string
				want               int
			}{"GET", path + "?where=" + private, "", 400})
		}
	}
	for _, tc := range cases {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			req := httptest.NewRequestWithContext(t.Context(), tc.method, tc.path, strings.NewReader(tc.body))
			mux.ServeHTTP(w, req)
			if w.Code != tc.want {
				t.Fatalf("got %d: %s", w.Code, w.Body.String())
			}
		})
	}
	for _, raw := range []bool{false, true} {
		prefix := "/devices"
		if raw {
			prefix = "/inventory/raw/devices"
		}
		req := httptest.NewRequestWithContext(t.Context(), "GET", prefix+"?limit=1&as_of=2026-09-21T00:00:00Z&where=[]", nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	for _, err := range []error{inventory.ErrConflict, inventory.ErrLease, errInventoryStorage} {
		w := httptest.NewRecorder()
		inventoryError(w, err)
		want := 409
		if errors.Is(err, errInventoryStorage) {
			want = 500
		}
		if w.Code != want {
			t.Fatal(w.Code)
		}
	}
	req := httptest.NewRequestWithContext(t.Context(), "POST", "/", strings.NewReader(strings.Repeat("x", MaxAdminBody+1)))
	if err := inventoryBody(req, new(any)); !errors.Is(err, inventory.ErrInvalid) {
		t.Fatal(err)
	}
	req.Body = io.NopCloser(inventoryBrokenReader{})
	if err := inventoryBody(req, new(any)); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatal(err)
	}
}

type inventoryBrokenReader struct{}

// Read fails before a JSON request can be decoded.
func (inventoryBrokenReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

// TestInventoryHTTPStorageFailures checks error mapping for reads, mutations and incomplete exports.
func TestInventoryHTTPStorageFailures(t *testing.T) {
	r, err := inventory.New(inventory.NewMemory())
	if err != nil {
		t.Fatal(err)
	}
	base := r.Backend
	a := &App{Inventory: r}
	mux := inventoryTestMux(a)
	for _, path := range []string{"/axm/accounts", "/axm/accounts/a", "/inventory/jobs", "/inventory/jobs/a", "/inventory/schedules", "/inventory/presets", "/devices", "/devices/fields", "/devices/a", "/inventory/reports", "/inventory/diagnostics", "/inventory/exports?format=ndjson"} {
		r.Backend = inventoryFailureBackend{Backend: base, reads: true}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), "GET", path, nil))
		if w.Code != 500 {
			t.Fatal(path, w.Code, w.Body.String())
		}
	}
	for _, path := range []string{"/inventory/sync", "/axm/accounts/a/verify", "/devices/a/collect"} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), "POST", path, nil))
		if w.Code != 500 {
			t.Fatal(path, w.Code, w.Body.String())
		}
	}
	r.Backend = inventoryFailureBackend{Backend: base, updates: true}
	for _, path := range []string{"/inventory/jobs/cancel-all", "/inventory/jobs/a/cancel", "/axm/accounts/a/sync"} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), "POST", path, nil))
		if w.Code != 500 {
			t.Fatal(path, w.Code, w.Body.String())
		}
	}
	// JSON writes its opening delimiter before a later repository read can fail.
	r.Backend = inventoryFailureBackend{Backend: base, reads: true}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), "GET", "/inventory/raw/exports", nil))
	if w.Code != 200 || w.Body.String() != "[" || w.Header().Get("X-Inventory-Error") != "export_failed" {
		t.Fatal(w.Code, w.Body.String(), w.Header())
	}
	r.Backend = base
	if err := base.Update(t.Context(), func(tx inventory.Tx) error {
		return tx.Put(t.Context(), "account/a", json.RawMessage(`{"id":"a","enabled":true,"revision":1}`))
	}); err != nil {
		t.Fatal(err)
	}
	r.Backend = inventoryFailureBackend{Backend: base, prefix: "credential/", reads: true}
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), "POST", "/axm/accounts/a/verify", nil))
	if w.Code != 500 {
		t.Fatal(w.Code)
	}
	r.Backend = inventoryFailureBackend{Backend: base, updates: true}
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), "POST", "/inventory/sync", nil))
	if w.Code != 500 {
		t.Fatal(w.Code)
	}
	if err := a.initialInventorySync(t.Context(), inventory.Account{ID: "a"}); !errors.Is(err, errInventoryStorage) {
		t.Fatal(err)
	}
	r.Backend = base
	if _, err := r.SaveSchedule(t.Context(), inventory.Schedule{AccountID: "a", Expression: "* * * * *"}, time.Now()); err != nil {
		t.Fatal(err)
	}
}
