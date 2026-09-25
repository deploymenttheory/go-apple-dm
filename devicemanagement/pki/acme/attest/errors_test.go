package attest_test

import (
	"errors"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/fault"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/acme/attest"
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
		"Format":        {attest.ErrFormat, fault.InvalidArgument, fault.Device, ""},
		"NoAttestation": {attest.ErrNoAttestation, fault.PermissionDenied, fault.Device, ""},
		"Chain":         {attest.ErrChain, fault.PermissionDenied, fault.Device, ""},
		"Freshness":     {attest.ErrFreshness, fault.PermissionDenied, fault.Device, ""},
		"KeyMismatch":   {attest.ErrKeyMismatch, fault.PermissionDenied, fault.Device, ""},
		"Extension":     {attest.ErrExtension, fault.InvalidArgument, fault.Device, ""},
		"Options":       {attest.ErrOptions, fault.Internal, fault.Operator, ""},
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
