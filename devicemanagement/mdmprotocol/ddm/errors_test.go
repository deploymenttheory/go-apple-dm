package ddm_test

import (
	"errors"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/fault"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm"
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
		"NotFound":           {ddm.ErrNotFound, fault.NotFound, fault.Client, "DM-DDM-NOT-FOUND"},
		"Conflict":           {ddm.ErrConflict, fault.Conflict, fault.Client, "DM-DDM-CONFLICT"},
		"Invalid":            {ddm.ErrInvalid, fault.InvalidArgument, fault.Client, "DM-DDM-INVALID"},
		"UnknownType":        {ddm.ErrUnknownType, fault.InvalidArgument, fault.Client, "DM-DDM-UNKNOWN-TYPE"},
		"InvalidDeclaration": {ddm.ErrInvalidDeclaration, fault.InvalidArgument, fault.Client, "DM-DDM-DECLARATION-INVALID"},
		"BadEndpoint":        {ddm.ErrBadEndpoint, fault.InvalidArgument, fault.Device, ""},
		"StatusTooLarge":     {ddm.ErrStatusTooLarge, fault.PayloadTooLarge, fault.Device, ""},
		"StatusMalformed":    {ddm.ErrStatusMalformed, fault.InvalidArgument, fault.Device, ""},
		"Resolver":           {ddm.ErrResolver, fault.Internal, fault.Operator, ""},
		"Expander":           {ddm.ErrExpander, fault.Internal, fault.Operator, ""},
		"Notifier":           {ddm.ErrNotifier, fault.Unavailable, fault.Operator, ""},
		"NoStore":            {ddm.ErrNoStore, fault.Internal, fault.Operator, ""},
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
