package app_test

import (
	"context"
	json "encoding/json/v2"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
	"github.com/deploymenttheory/go-apple-dm/server/blueprints"
	"github.com/deploymenttheory/go-apple-dm/server/internal/app"
)

type blueprintListFailure struct{ state.Store }

func (blueprintListFailure) List(context.Context, string, string, int) ([]state.Record, error) {
	return nil, errors.New("blueprint storage unavailable")
}

func TestBlueprintAdminInputAndReadContracts(t *testing.T) {
	a := build(t, app.Config{Storage: "inmem", BootstrapToken: "admin"})
	request := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequestWithContext(t.Context(), method, "https://mdm.example/admin/v1"+path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer admin")
		w := httptest.NewRecorder()
		a.Handler.ServeHTTP(w, r)
		return w
	}
	for _, test := range []struct {
		method, path, body string
		status             int
	}{
		{"POST", "/blueprints/validate", "{", http.StatusBadRequest},
		{"PUT", "/blueprints/apps", `{"Identifier":"apps","Unexpected":true}`, http.StatusBadRequest},
		{"POST", "/blueprints/validate", strings.Repeat("x", app.MaxAdminBody+1), http.StatusRequestEntityTooLarge},
		{"PUT", "/blueprints/apps", `{"Identifier":"other"}`, http.StatusBadRequest},
		{"GET", "/blueprints?limit=invalid", "", http.StatusBadRequest},
		{"GET", "/blueprints/missing", "", http.StatusNotFound},
		{"PUT", "/enrollments/invalid/device/blueprints/apps", "", http.StatusBadRequest},
	} {
		t.Run(test.method+" "+test.path, func(t *testing.T) {
			if w := request(test.method, test.path, test.body); w.Code != test.status {
				t.Fatalf("status=%d, want %d: %s", w.Code, test.status, w.Body.String())
			}
		})
	}
	if w := request("GET", "/blueprints", ""); w.Code != http.StatusOK || strings.Contains(w.Body.String(), `"Identifier":"apps"`) {
		t.Fatalf("rejected input persisted: %d %s", w.Code, w.Body.String())
	}
	published := request("PUT", "/blueprints/apps", `{"Identifier":"apps"}`)
	if published.Code != http.StatusOK {
		t.Fatal(published.Code, published.Body.String())
	}
	var want blueprints.Record
	if err := json.Unmarshal(published.Body.Bytes(), &want); err != nil {
		t.Fatal(err)
	}
	got := request("GET", "/blueprints/apps", "")
	var record blueprints.Record
	if err := json.Unmarshal(got.Body.Bytes(), &record); err != nil {
		t.Fatal(err)
	}
	if got.Code != http.StatusOK || record.Spec.Identifier != "apps" || record.Revision != want.Revision || got.Header().Get("ETag") != published.Header().Get("ETag") {
		t.Fatalf("read lost source or revision: %d %+v", got.Code, record)
	}
	manager, err := blueprints.New(blueprints.Config{Engine: a.Engine, State: blueprintListFailure{}})
	if err != nil {
		t.Fatal(err)
	}
	a.Blueprints = manager
	if w := request("GET", "/blueprints", ""); w.Code != http.StatusInternalServerError {
		t.Fatalf("storage failure returned success: %d %s", w.Code, w.Body.String())
	}
}
