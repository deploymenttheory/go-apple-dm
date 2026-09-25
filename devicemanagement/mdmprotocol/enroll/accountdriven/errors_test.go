package accountdriven_test

import (
	"errors"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/fault"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/enroll/accountdriven"
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
		"TokenNotFound":       {accountdriven.ErrTokenNotFound, fault.NotFound, fault.Client, "DM-ENROLLMENT-TOKEN-NOT-FOUND"},
		"TokenExpired":        {accountdriven.ErrTokenExpired, fault.Gone, fault.Client, "DM-ENROLLMENT-TOKEN-EXPIRED"},
		"TokenUsed":           {accountdriven.ErrTokenUsed, fault.Conflict, fault.Client, "DM-ENROLLMENT-TOKEN-USED"},
		"Config":              {accountdriven.ErrConfig, fault.Internal, fault.Operator, ""},
		"ManagedAppleAccount": {accountdriven.ErrManagedAppleAccount, fault.PermissionDenied, fault.Device, ""},
		"Mode":                {accountdriven.ErrMode, fault.InvalidArgument, fault.Device, ""},
		"EnrollmentToken":     {accountdriven.ErrEnrollmentToken, fault.PermissionDenied, fault.Device, ""},
		"Association":         {accountdriven.ErrAssociation, fault.PermissionDenied, fault.Device, ""},
		"Challenge":           {accountdriven.ErrChallenge, fault.InvalidArgument, fault.Device, ""},
		"Reauthentication":    {&accountdriven.Reauthentication{}, fault.Unauthenticated, fault.Device, ""},
		"OAuth2Request":       {accountdriven.ErrOAuth2Request, fault.InvalidArgument, fault.Device, ""},
		"OAuth2Grant":         {accountdriven.ErrOAuth2Grant, fault.InvalidArgument, fault.Device, ""},
		"HTTP400":             {&accountdriven.HTTPError{Status: 400, Err: errors.New("x")}, fault.InvalidArgument, fault.Operator, ""},
		"HTTP401":             {&accountdriven.HTTPError{Status: 401, Err: errors.New("x")}, fault.Unauthenticated, fault.Operator, ""},
		"HTTP403":             {&accountdriven.HTTPError{Status: 403, Err: errors.New("x")}, fault.PermissionDenied, fault.Operator, ""},
		"HTTP404":             {&accountdriven.HTTPError{Status: 404, Err: errors.New("x")}, fault.NotFound, fault.Operator, ""},
		"HTTP500":             {&accountdriven.HTTPError{Status: 500, Err: errors.New("x")}, fault.Internal, fault.Operator, ""},
		"HTTPClassifiedCause": {&accountdriven.HTTPError{Status: 500, Err: accountdriven.ErrMode}, fault.InvalidArgument, fault.Device, ""},
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
