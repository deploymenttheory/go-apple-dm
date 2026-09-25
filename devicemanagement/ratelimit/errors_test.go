package ratelimit_test

import (
	"errors"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/fault"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/ratelimit"
)

// TestErrorsClassify pins each sentinel to its kind and audience.
func TestErrorsClassify(t *testing.T) {
	for name, tc := range map[string]struct {
		err  error
		kind *fault.Kind
	}{
		"Invalid":     {ratelimit.ErrInvalid, fault.Internal},
		"Unavailable": {ratelimit.ErrUnavailable, fault.Unavailable},
		"Capacity":    {ratelimit.ErrCapacity, fault.ResourceExhausted},
	} {
		t.Run(name, func(t *testing.T) {
			if !errors.Is(tc.err, tc.kind) || fault.KindOf(tc.err) != tc.kind {
				t.Fatalf("kind = %v, want %v", fault.KindOf(tc.err), tc.kind)
			}
			if fault.AudienceOf(tc.err) != fault.Operator || fault.CodeOf(tc.err) != "" {
				t.Fatal("an operator condition carried a code or another audience")
			}
		})
	}
}
