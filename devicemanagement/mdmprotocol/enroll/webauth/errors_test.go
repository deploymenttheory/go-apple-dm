package webauth_test

import (
	"errors"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/fault"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/enroll/webauth"
)

// TestErrorsClassify pins each sentinel to its kind, audience and code, which is what
// a transport reads instead of the sentinel.
func TestErrorsClassify(t *testing.T) {
	for name, tc := range map[string]struct {
		err      error
		kind     *fault.Kind
		audience fault.Audience
		code     fault.Code
	}{
		"Provider":       {webauth.ErrProvider, fault.Upstream, fault.Operator, ""},
		"NotHTTPS":       {webauth.ErrNotHTTPS, fault.Internal, fault.Operator, ""},
		"IDToken":        {webauth.ErrIDToken, fault.PermissionDenied, fault.Device, ""},
		"JWK":            {webauth.ErrJWK, fault.Upstream, fault.Operator, ""},
		"Config":         {webauth.ErrConfig, fault.Internal, fault.Operator, ""},
		"Denied":         {webauth.ErrDenied, fault.PermissionDenied, fault.Device, ""},
		"Callback":       {webauth.ErrCallback, fault.InvalidArgument, fault.Device, ""},
		"AccessDenied":   {webauth.ErrAccessDenied, fault.PermissionDenied, fault.Device, ""},
		"StateExpired":   {webauth.ErrStateExpired, fault.PermissionDenied, fault.Device, ""},
		"BrowserBinding": {webauth.ErrBrowserBinding, fault.PermissionDenied, fault.Device, ""},
		"StateNotFound":  {webauth.ErrStateNotFound, fault.PermissionDenied, fault.Device, ""},
		"StateExists":    {webauth.ErrStateExists, fault.Conflict, fault.Operator, ""},
		"StoreFull":      {webauth.ErrStoreFull, fault.ResourceExhausted, fault.Operator, ""},
		"StateKey":       {webauth.ErrStateKey, fault.Internal, fault.Operator, ""},
	} {
		t.Run(name, func(t *testing.T) {
			if !errors.Is(tc.err, tc.kind) || fault.KindOf(tc.err) != tc.kind {
				t.Fatalf("kind = %v, want %v", fault.KindOf(tc.err), tc.kind)
			}
			if fault.AudienceOf(tc.err) != tc.audience || fault.CodeOf(tc.err) != tc.code {
				t.Fatalf("audience %v code %q, want %v %q", fault.AudienceOf(tc.err), fault.CodeOf(tc.err), tc.audience, tc.code)
			}
		})
	}
}
