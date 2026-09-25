package gdmf_test

import (
	"errors"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/gdmf"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/fault"
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
		"Request":  {gdmf.ErrRequest, fault.Upstream, fault.Operator, ""},
		"Status":   {gdmf.ErrStatus, fault.Upstream, fault.Operator, ""},
		"Decode":   {gdmf.ErrDecode, fault.Upstream, fault.Operator, ""},
		"TooLarge": {gdmf.ErrTooLarge, fault.Upstream, fault.Operator, ""},
		"NotFound": {gdmf.ErrNotFound, fault.NotFound, fault.Operator, ""},
		"Status429": {&gdmf.StatusError{Status: 429}, fault.ResourceExhausted, fault.Operator, ""},
		"Status500": {&gdmf.StatusError{Status: 500}, fault.Upstream, fault.Operator, ""},
		"Status403": {&gdmf.StatusError{Status: 403}, fault.Unavailable, fault.Operator, ""},
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

// TestStatusErrorMatchesItsSentinel checks that an unexpected status still matches
// ErrStatus and renders the status without a package label.
func TestStatusErrorMatchesItsSentinel(t *testing.T) {
	err := &gdmf.StatusError{Status: 502}
	if !errors.Is(err, gdmf.ErrStatus) || err.Error() != "the software update catalog answered HTTP 502" {
		t.Fatalf("status error: %v", err)
	}
}
