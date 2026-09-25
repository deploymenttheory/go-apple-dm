package push_test

import (
	"errors"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/push"
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
		"NoCertificate": {push.ErrNoCertificate, fault.Unavailable, fault.Operator, ""},
		"CertExpired":   {push.ErrCertExpired, fault.Unavailable, fault.Operator, ""},
		"InvalidToken":  {push.ErrInvalidToken, fault.Gone, fault.Operator, ""},
		"Rejected":      {push.ErrRejected, fault.Upstream, fault.Operator, ""},
		"RateLimited":   {push.ErrRateLimited, fault.ResourceExhausted, fault.Operator, ""},
		"Upstream":      {push.ErrUpstream, fault.Upstream, fault.Operator, ""},
		"Coalesced":     {push.ErrCoalesced, fault.Conflict, fault.Operator, ""},
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
