package ade_test

import (
	"errors"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/fault"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/enroll/ade"
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
		"Store":         {ade.ErrStore, fault.Unavailable, fault.Operator, ""},
		"Rejected":      {ade.ErrRejected, fault.PermissionDenied, fault.Device, ""},
		"Gate":          {ade.ErrGate, fault.Internal, fault.Operator, ""},
		"NoMachineInfo": {ade.ErrNoMachineInfo, fault.InvalidArgument, fault.Device, ""},
		"TooLarge":      {ade.ErrTooLarge, fault.InvalidArgument, fault.Device, ""},
		"Malformed":     {ade.ErrMalformed, fault.InvalidArgument, fault.Device, ""},
		"Unverified":    {ade.ErrUnverified, fault.PermissionDenied, fault.Device, ""},
		"UnknownSigner": {ade.ErrUnknownSigner, fault.PermissionDenied, fault.Device, ""},
		"Presence":      {ade.ErrPresence, fault.InvalidArgument, fault.Device, ""},
		"PasswordHash":  {ade.ErrPasswordHash, fault.Internal, fault.Operator, ""},
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
