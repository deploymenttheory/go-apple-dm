package axm_test

import (
	"errors"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/axm"
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
		"Config":       {axm.ErrConfig, fault.Internal, fault.Operator, ""},
		"Key":          {axm.ErrKey, fault.Internal, fault.Operator, ""},
		"KeyType":      {axm.ErrKeyType, fault.Internal, fault.Operator, ""},
		"Decode":       {axm.ErrDecode, fault.Upstream, fault.Operator, ""},
		"Transport":    {axm.ErrTransport, fault.Upstream, fault.Operator, ""},
		"Limit":        {axm.ErrLimit, fault.InvalidArgument, fault.Client, "DM-AXM-LIMIT-INVALID"},
		"PageCap":      {axm.ErrPageCap, fault.ResourceExhausted, fault.Operator, ""},
		"NextLink":     {axm.ErrNextLink, fault.Upstream, fault.Operator, ""},
		"Argument":     {axm.ErrArgument, fault.InvalidArgument, fault.Client, "DM-AXM-INVALID"},
		"ActivityRule": {axm.ErrActivityRule, fault.InvalidArgument, fault.Client, "DM-AXM-ACTIVITY-INVALID"},
		"WaitTimeout":  {axm.ErrWaitTimeout, fault.DeadlineExceeded, fault.Operator, ""},
		"ForeignHost":  {axm.ErrForeignHost, fault.Upstream, fault.Operator, ""},
		"NoEventData":  {axm.ErrNoEventData, fault.Upstream, fault.Operator, ""},
		"Store":        {axm.ErrStore, fault.Unavailable, fault.Operator, ""},
		"Answer400":    {&axm.Error{Status: 400}, fault.InvalidArgument, fault.Operator, ""},
		"Answer404":    {&axm.Error{Status: 404}, fault.NotFound, fault.Operator, ""},
		"Answer409":    {&axm.Error{Status: 409}, fault.Conflict, fault.Operator, ""},
		"Answer429":    {&axm.Error{Status: 429}, fault.ResourceExhausted, fault.Operator, ""},
		"Answer504":    {&axm.Error{Status: 504}, fault.DeadlineExceeded, fault.Operator, ""},
		"Answer500":    {&axm.Error{Status: 500}, fault.Upstream, fault.Operator, ""},
		"Auth":         {&axm.AuthError{Status: 401}, fault.Unavailable, fault.Operator, ""},
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
	err := &axm.Error{Status: 429, RetryAfter: 3 * time.Second}
	if fault.RetryAfterOf(err) != 3*time.Second {
		t.Fatal(fault.RetryAfterOf(err))
	}
}
