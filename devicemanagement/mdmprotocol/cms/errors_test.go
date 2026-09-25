package cms_test

import (
	"errors"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/fault"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/cms"
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
		"Header":          {cms.ErrHeader, fault.InvalidArgument, fault.Device, ""},
		"Parse":           {cms.ErrParse, fault.InvalidArgument, fault.Device, ""},
		"NoSigner":        {cms.ErrNoSigner, fault.PermissionDenied, fault.Device, ""},
		"MultipleSigners": {cms.ErrMultipleSigners, fault.PermissionDenied, fault.Device, ""},
		"Signature":       {cms.ErrSignature, fault.PermissionDenied, fault.Device, ""},
		"SigningTime":     {cms.ErrSigningTime, fault.PermissionDenied, fault.Device, ""},
		"Chain":           {cms.ErrChain, fault.PermissionDenied, fault.Device, ""},
		"Algorithm":       {cms.ErrAlgorithm, fault.InvalidArgument, fault.Device, ""},
		"Sign":            {cms.ErrSign, fault.Internal, fault.Operator, ""},
		"Recipient":       {cms.ErrRecipient, fault.Internal, fault.Operator, ""},
		"Decrypt":         {cms.ErrDecrypt, fault.Internal, fault.Operator, ""},
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
