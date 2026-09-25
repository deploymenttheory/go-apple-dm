package scep_test

import (
	"errors"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/fault"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/scep"
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
		"Client":    {scep.ErrClient, fault.Upstream, fault.Operator, ""},
		"Rejected":  {scep.ErrRejected, fault.Upstream, fault.Operator, ""},
		"CSR":       {scep.ErrCSR, fault.InvalidArgument, fault.Device, ""},
		"Operation": {scep.ErrOperation, fault.InvalidArgument, fault.Device, ""},
		"RA":        {scep.ErrRA, fault.Internal, fault.Operator, ""},
		"Issue":     {scep.ErrIssue, fault.Internal, fault.Operator, ""},
		"Challenge": {scep.ErrChallenge, fault.PermissionDenied, fault.Device, ""},
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
