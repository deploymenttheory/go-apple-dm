package appsbooks_test

import (
	"errors"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/appsbooks"
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
		"Config":           {appsbooks.ErrConfig, fault.Internal, fault.Operator, ""},
		"Input":            {appsbooks.ErrInput, fault.InvalidArgument, fault.Client, "DM-APPSBOOKS-INVALID"},
		"Expired":          {appsbooks.ErrExpired, fault.Unavailable, fault.Operator, ""},
		"Ownership":        {appsbooks.ErrOwnership, fault.Unavailable, fault.Operator, ""},
		"Location":         {appsbooks.ErrLocation, fault.Upstream, fault.Operator, ""},
		"Protocol":         {appsbooks.ErrProtocol, fault.Upstream, fault.Operator, ""},
		"Limit":            {appsbooks.ErrLimit, fault.ResourceExhausted, fault.Client, "DM-APPSBOOKS-LIMIT-EXCEEDED"},
		"NotificationAuth": {appsbooks.ErrNotificationAuth, fault.Unauthenticated, fault.Operator, ""},
		"Answer500":        {&appsbooks.APIError{HTTPStatus: 500}, fault.Upstream, fault.Operator, ""},
		"Answer429":        {&appsbooks.APIError{HTTPStatus: 429}, fault.ResourceExhausted, fault.Operator, ""},
		"Answer504":        {&appsbooks.APIError{HTTPStatus: 504}, fault.DeadlineExceeded, fault.Operator, ""},
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

// TestAPIErrorExposesRetryDelayAndTransportKind covers the retry delay Apple sent and the
// transport error's classification.
func TestAPIErrorExposesRetryDelayAndTransportKind(t *testing.T) {
	err := &appsbooks.APIError{HTTPStatus: 429, RetryAfter: 7 * time.Second}
	if fault.RetryAfterOf(err) != 7*time.Second || err.RetryDelay() != 7*time.Second {
		t.Fatal("retry delay", fault.RetryAfterOf(err))
	}
	if fault.KindOf(err) != fault.ResourceExhausted {
		t.Fatal(fault.KindOf(err))
	}
}
