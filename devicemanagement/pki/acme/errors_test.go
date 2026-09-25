package acme_test

import (
	"errors"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/fault"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/acme"
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
		"NotFound":      {acme.ErrNotFound, fault.NotFound, fault.Client, "DM-ACME-NOT-FOUND"},
		"Conflict":      {acme.ErrConflict, fault.Conflict, fault.Operator, ""},
		"Invalid":       {acme.ErrInvalid, fault.Internal, fault.Operator, ""},
		"Problem400":    {acme.NewProblem(acme.ProblemMalformed, ""), fault.InvalidArgument, fault.Operator, ""},
		"Problem401":    {acme.NewProblem(acme.ProblemUnauthorized, ""), fault.Unauthenticated, fault.Operator, ""},
		"Problem403":    {acme.NewProblem(acme.ProblemOrderNotReady, ""), fault.PermissionDenied, fault.Operator, ""},
		"Problem404":    {&acme.Problem{Type: acme.ProblemAccountDoesNotExist, Status: 404}, fault.NotFound, fault.Operator, ""},
		"Problem409":    {&acme.Problem{Type: acme.ProblemMalformed, Status: 409}, fault.Conflict, fault.Operator, ""},
		"Problem429":    {acme.NewProblem(acme.ProblemRateLimited, ""), fault.ResourceExhausted, fault.Operator, ""},
		"Problem500":    {acme.NewProblem(acme.ProblemServerInternal, ""), fault.Internal, fault.Operator, ""},
		"Config":        {acme.ErrConfig, fault.Internal, fault.Operator, ""},
		"IdentifierKey": {acme.ErrIdentifierKey, fault.Internal, fault.Operator, ""},
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
