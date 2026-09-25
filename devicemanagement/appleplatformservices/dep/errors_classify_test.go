package dep_test

import (
	"errors"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/dep"
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
		"Invalid":         {dep.ErrInvalid, fault.InvalidArgument, fault.Client, "DM-ADE-INVALID"},
		"NotFound":        {dep.ErrNotFound, fault.NotFound, fault.Client, "DM-ADE-NOT-FOUND"},
		"Conflict":        {dep.ErrConflict, fault.Conflict, fault.Client, "DM-ADE-CONFLICT"},
		"BodyTooLarge":    {dep.ErrBodyTooLarge, fault.Internal, fault.Operator, ""},
		"ConsumerKey":     {dep.ErrConsumerKeyMismatch, fault.Conflict, fault.Client, "DM-ADE-CONSUMER-KEY-MISMATCH"},
		"Answer401":       {&dep.Error{Status: 401}, fault.Unavailable, fault.Operator, ""},
		"Answer404":       {&dep.Error{Status: 404}, fault.NotFound, fault.Operator, ""},
		"ProfileInvalid":  {&dep.ProfileError{Code: "X", Detail: "y"}, fault.InvalidArgument, fault.Client, "DM-ADE-PROFILE-INVALID"},
		"NoTokens":        {dep.ErrNoTokens, fault.Unavailable, fault.Operator, ""},
		"TokenExpired":    {dep.ErrTokenExpired, fault.Unavailable, fault.Operator, ""},
		"SameCursor":      {dep.ErrSameCursor, fault.Upstream, fault.Operator, ""},
		"Backoff":         {dep.ErrBackoff, fault.Unavailable, fault.Operator, ""},
		"Sync":            {dep.ErrSync, fault.Internal, fault.Operator, ""},
		"Config":          {dep.ErrConfig, fault.Internal, fault.Operator, ""},
		"Answer5xx":       {&dep.Error{Status: 500}, fault.Upstream, fault.Operator, ""},
		"AnswerThrottled": {&dep.Error{Status: 429}, fault.ResourceExhausted, fault.Operator, ""},
		"AnswerTimeout":   {&dep.Error{Status: 504}, fault.DeadlineExceeded, fault.Operator, ""},
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

// TestErrorExposesRetryDelay covers the Retry-After Apple sent.
func TestErrorExposesRetryDelay(t *testing.T) {
	err := &dep.Error{Status: 429, RetryAfter: 9 * time.Second}
	if fault.RetryAfterOf(err) != 9*time.Second {
		t.Fatal(fault.RetryAfterOf(err))
	}
}
