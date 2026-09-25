package publicappstoreidentity_test

import (
	"errors"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/fault"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/utility/publicappstoreidentity"
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
		"Invalid":  {publicappstoreidentity.ErrInvalid, fault.InvalidArgument, fault.Client, "DM-APPSTORE-QUERY-INVALID"},
		"Request":  {publicappstoreidentity.ErrRequest, fault.Upstream, fault.Operator, ""},
		"Status":   {publicappstoreidentity.ErrStatus, fault.Upstream, fault.Operator, ""},
		"Decode":   {publicappstoreidentity.ErrDecode, fault.Upstream, fault.Operator, ""},
		"TooLarge": {publicappstoreidentity.ErrTooLarge, fault.Upstream, fault.Operator, ""},
		"NotFound": {publicappstoreidentity.ErrNotFound, fault.NotFound, fault.Client, "DM-APPSTORE-LISTING-NOT-FOUND"},
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
