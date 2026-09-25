package storage_test

import (
	"errors"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/fault"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
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
		"NotFound":            {storage.ErrNotFound, fault.NotFound, fault.Client, "DM-ENROLLMENT-NOT-FOUND"},
		"Disabled":            {storage.ErrDisabled, fault.Conflict, fault.Client, "DM-ENROLLMENT-DISABLED"},
		"Conflict":            {storage.ErrConflict, fault.Conflict, fault.Client, "DM-ENROLLMENT-CONFLICT"},
		"Invalid":             {storage.ErrInvalid, fault.InvalidArgument, fault.Client, "DM-ENROLLMENT-INVALID"},
		"UserChannelRequired": {storage.ErrUserChannelRequired, fault.InvalidArgument, fault.Client, "DM-ENROLLMENT-USER-CHANNEL-REQUIRED"},
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
