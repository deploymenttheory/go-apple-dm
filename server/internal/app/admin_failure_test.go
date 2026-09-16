package app

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/ddm/ddmtest"
	ddminmem "github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/ddm/inmem"
)

func TestDDMHandlersReportFailedStoreReadsAndInvalidChannels(t *testing.T) {
	failure := errors.New("private database connection details")
	for _, tc := range []struct {
		method, path, fail string
		status             int
	}{
		{"PUT", "/sets/test/declarations/test", "PutSet", 500},
		{"GET", "/enrollments/device/test/declarations", "StaticDeclarations", 500},
		{"GET", "/enrollments/device/test/tokens", "Snapshot", 500},
		{"GET", "/enrollments/device/test/status", "DeclarationStatus", 500},
		{"PUT", "/enrollments/invalid/test/sets/test", "", 400},
		{"DELETE", "/enrollments/invalid/test/sets/test", "", 400},
		{"GET", "/enrollments/invalid/test/status", "", 400},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			st := &ddmtest.Failing{Store: ddminmem.New(), Fail: map[string]error{tc.fail: failure}}
			e, err := ddm.New(ddm.Config{Store: st})
			if err != nil {
				t.Fatal(err)
			}
			a := &App{Engine: e}
			mux := http.NewServeMux()
			for _, route := range a.ddmAdminRoutes() {
				mux.Handle(route.Pattern, route.Handler)
			}
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), tc.method, tc.path, nil))
			if w.Code != tc.status || strings.Contains(w.Body.String(), failure.Error()) {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
}
