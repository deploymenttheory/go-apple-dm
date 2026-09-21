package app

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm"
	ddminmem "github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/ddm/inmem"
	"github.com/deploymenttheory/go-apple-dm/server/configurationprofile"
)

func TestConfigurationProfileHandlersRejectInvalidRequestsAndStoreFailures(t *testing.T) {
	engine, err := ddm.New(ddm.Config{Store: ddminmem.New()})
	if err != nil {
		t.Fatal(err)
	}
	manager, err := configurationprofile.New(configurationprofile.Config{State: unavailableAppState{failure: io.ErrUnexpectedEOF}, Engine: engine})
	if err != nil {
		t.Fatal(err)
	}
	a := &App{ConfigurationProfiles: manager}
	mux := http.NewServeMux()
	for _, route := range a.configurationProfileAdminRoutes() {
		mux.Handle(route.Pattern, route.Handler)
	}
	for _, tc := range []struct {
		method, path, body string
		status             int
	}{
		{"GET", "/configuration-profiles?limit=invalid", "", 400},
		{"GET", "/configuration-profiles", "", 500},
		{"GET", "/configuration-profiles/invalid", "", 400},
		{"GET", "/configuration-profiles/invalid/content", "", 400},
		{"POST", "/configuration-profiles", "invalid plist", 400},
		{"POST", "/configuration-profiles", strings.Repeat("x", configurationprofile.MaxBytes+1), 413},
	} {
		t.Run(tc.method+tc.path+http.StatusText(tc.status), func(t *testing.T) {
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), tc.method, tc.path, strings.NewReader(tc.body)))
			if w.Code != tc.status || strings.Contains(w.Body.String(), io.ErrUnexpectedEOF.Error()) {
				t.Fatalf("HTTP %d: %s", w.Code, w.Body.String())
			}
		})
	}
}
