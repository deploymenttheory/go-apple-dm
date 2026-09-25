package crypt_test

import (
	"errors"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/fault"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/crypt"
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
		"NoKeyring":  {crypt.ErrNoKeyring, fault.Unavailable, fault.Operator, ""},
		"UnknownKey": {crypt.ErrUnknownKey, fault.Unavailable, fault.Operator, ""},
		"Tampered":   {crypt.ErrTampered, fault.Internal, fault.Operator, ""},
		"Unsealed":   {crypt.ErrUnsealed, fault.Internal, fault.Operator, ""},
		"WeakKey":    {crypt.ErrWeakKey, fault.Internal, fault.Operator, ""},
		"BadFormat":  {crypt.ErrBadFormat, fault.Internal, fault.Operator, ""},
		"NoActive":   {crypt.ErrNoActive, fault.Unavailable, fault.Operator, ""},
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
