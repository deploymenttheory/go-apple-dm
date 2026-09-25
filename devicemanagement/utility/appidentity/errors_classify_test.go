package appidentity_test

import (
	"errors"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/fault"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/utility/appidentity"
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
		"Unsupported": {appidentity.ErrUnsupported, fault.Unimplemented, fault.Operator, ""},
		"Input":       {appidentity.ErrInput, fault.InvalidArgument, fault.Client, "DM-APPIDENTITY-INVALID"},
		"Inspect":     {appidentity.ErrInspect, fault.Internal, fault.Operator, ""},
		"TooLarge":    {appidentity.ErrTooLarge, fault.PayloadTooLarge, fault.Client, "DM-APPIDENTITY-TOO-LARGE"},
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
