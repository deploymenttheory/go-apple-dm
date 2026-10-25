package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/axm"
)

// TestAxMStatus checks AXM error-to-HTTP status mapping.
func TestAxMStatus(t *testing.T) {
	cases := map[error]int{
		&axm.Error{Status: http.StatusNotFound}:                          http.StatusNotFound,
		&axm.Error{Status: http.StatusConflict}:                          http.StatusConflict,
		&axm.Error{Status: http.StatusTooManyRequests}:                   http.StatusTooManyRequests,
		fmt.Errorf("x: %w", axm.ErrArgument):                             http.StatusBadRequest,
		fmt.Errorf("x: %w", axm.ErrActivityRule):                         http.StatusBadRequest,
		fmt.Errorf("x: %w", axm.ErrLimit):                                http.StatusBadRequest,
		fmt.Errorf("x: %w", axm.ErrWaitTimeout):                          http.StatusGatewayTimeout,
		fmt.Errorf("%w: %w", axm.ErrTransport, context.DeadlineExceeded): http.StatusGatewayTimeout,
		errors.New("transport"):                                          http.StatusBadGateway,
	}
	for err, want := range cases {
		if got := axmStatus(err); got != want {
			t.Errorf("%v: %d, want %d", err, got, want)
		}
	}
}

// TestListOptions checks AXM listing query option parsing.
func TestListOptions(t *testing.T) {
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/x?limit=abc&cursor=c1", nil)
	if o := listOptions(r); o.Limit != 0 || o.Cursor != "c1" {
		t.Fatalf("%+v", o)
	}
	r = httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/x?limit=7", nil)
	if o := listOptions(r); o.Limit != 7 {
		t.Fatalf("%+v", o)
	}
}

// TestAssignmentRawAuthority requires a distinct explicit grant before retained log access.
func TestAssignmentRawAuthority(t *testing.T) {
	a, e := Build(t.Context(), Config{Storage: "inmem", BootstrapToken: "bootstrap-secret"})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = a.Close() })
	root := bootstrapRBAC(t, a)
	// Use the actual registered action rather than infer raw authority from ordinary ABM reads.
	body, _ := json.Marshal(map[string]string{"Source": `permit(principal, action == MDM::Action::"` + ActionReadBusinessMgr + `", resource);`})
	if w := rbacRequest(a, "PUT", "/policies/assignment", root, string(body)); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := rbacRequest(a, "GET", "/axm/accounts/named/assignments/request", root, ""); w.Code != 404 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := rbacRequest(a, "GET", "/axm/accounts/named/assignments/request/log", root, ""); w.Code != 403 {
		t.Fatal(w.Code, w.Body.String())
	}
	body, _ = json.Marshal(map[string]string{"Source": `permit(principal, action == MDM::Action::"` + ActionReadRawInventory + `", resource);`})
	if w := rbacRequest(a, "PUT", "/policies/assignment-raw", root, string(body)); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := rbacRequest(a, "GET", "/axm/accounts/named/assignments/request/log", root, ""); w.Code != 404 {
		t.Fatal(w.Code, w.Body.String())
	}
}
