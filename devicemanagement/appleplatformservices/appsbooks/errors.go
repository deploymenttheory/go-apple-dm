package appsbooks

import (
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"time"
)

// Sentinel errors never contain token or request data.
var (
	ErrConfig           = errors.New("appsbooks: invalid configuration")
	ErrInput            = errors.New("appsbooks: invalid request")
	ErrExpired          = errors.New("appsbooks: content token expired")
	ErrOwnership        = errors.New("appsbooks: location ownership conflict or unclaimed location")
	ErrLocation         = errors.New("appsbooks: response belongs to another location")
	ErrProtocol         = errors.New("appsbooks: malformed or unsupported response")
	ErrLimit            = errors.New("appsbooks: request exceeds current service limits")
	ErrNotificationAuth = errors.New("appsbooks: notification authentication failed")
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

func (e *APIError) Error() string {
	return fmt.Sprintf("appsbooks: HTTP %d, Apple error %d", e.HTTPStatus, e.Number)
}

// TransportError hides potentially credential-bearing URL/error diagnostics.
// Unwrap supports context cancellation/deadline checks. A failed mutation may
// already have reached Apple: reconcile before explicitly resubmitting it.
type TransportError struct{ cause error }

func (*TransportError) Error() string   { return "appsbooks: request transport failed" }
func (e *TransportError) Unwrap() error { return e.cause }
