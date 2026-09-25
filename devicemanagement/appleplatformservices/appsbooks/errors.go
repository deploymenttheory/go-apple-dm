package appsbooks

import (
	"encoding/json/jsontext"
	"fmt"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/fault"
)

// Sentinel errors never contain token or request data.
var (
	ErrConfig           = fault.NewOperator(fault.Internal, "the Apps and Books configuration is not valid")
	ErrInput            = fault.AppsBooksInvalid
	ErrExpired          = fault.NewOperator(fault.Unavailable, "the Apps and Books content token has expired")
	ErrOwnership        = fault.NewOperator(fault.Unavailable, "the location is owned by another server or unclaimed")
	ErrLocation         = fault.NewOperator(fault.Upstream, "the response belongs to another location")
	ErrProtocol         = fault.NewOperator(fault.Upstream, "the Apps and Books response is malformed or unsupported")
	ErrLimit            = fault.AppsBooksLimitExceeded
	ErrNotificationAuth = fault.NewOperator(fault.Unauthenticated, "the notification authentication failed")
)

// Failure preserves Apple's error number, message and evolving request-specific
// errorInfo. Message and Info may contain private data; Error omits them.
type Failure struct {
	Number  int            `json:"errorNumber"`
	Message string         `json:"errorMessage"`
	Info    jsontext.Value `json:"errorInfo,omitempty"`
}

// APIError describes a rejected HTTP/API request. RetryAfter is a minimum wait,
// never a guarantee that replaying a mutation is safe.
type APIError struct {
	Failure
	HTTPStatus int
	RetryAfter time.Duration
}

// Error reports the HTTP status and Apple service error number.
func (e *APIError) Error() string {
	return fmt.Sprintf("Apps and Books answered HTTP %d, Apple error %d", e.HTTPStatus, e.Number)
}

// Kind classifies the answer by the rule every client of an Apple service shares
// (see fault.KindForUpstreamStatus).
func (e *APIError) Kind() *fault.Kind { return fault.KindForUpstreamStatus(e.HTTPStatus) }

// Is matches the error's kind.
func (e *APIError) Is(target error) bool { return target == e.Kind() }

// RetryDelay returns the minimum wait Apple asked for, for fault.RetryAfterOf.
func (e *APIError) RetryDelay() time.Duration { return e.RetryAfter }

// TransportError hides potentially credential-bearing URL/error diagnostics.
// Unwrap supports context cancellation/deadline checks. A failed mutation may
// already have reached Apple: reconcile before explicitly resubmitting it.
type TransportError struct{ cause error }

// Error returns a transport-failure message without disclosing the request URL.
func (*TransportError) Error() string { return "the Apps and Books request transport failed" }

// Unwrap exposes the wrapped cause for errors.Is and errors.As.
func (e *TransportError) Unwrap() error { return e.cause }

// Kind classifies a transport failure as Upstream.
func (*TransportError) Kind() *fault.Kind { return fault.Upstream }
