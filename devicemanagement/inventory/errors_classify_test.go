package inventory_test

import (
	"errors"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/fault"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/inventory"
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
		"NotFound": {inventory.ErrNotFound, fault.NotFound, fault.Client, "DM-INVENTORY-NOT-FOUND"},
		"Invalid":  {inventory.ErrInvalid, fault.InvalidArgument, fault.Client, "DM-INVENTORY-INVALID"},
		"Conflict": {inventory.ErrConflict, fault.Conflict, fault.Client, "DM-INVENTORY-CONFLICT"},
		"Stopped":  {inventory.ErrStopped, fault.Conflict, fault.Client, "DM-INVENTORY-JOB-STOPPED"},
		"Lease":    {inventory.ErrLease, fault.Conflict, fault.Operator, ""},
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
