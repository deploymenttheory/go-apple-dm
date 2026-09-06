package axm

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Sentinel errors.
var (
	// ErrConfig is returned by New for an unusable Config.
	ErrConfig = errors.New("axm: invalid config")
	// ErrKey is returned when a private key cannot be read.
	ErrKey = errors.New("axm: invalid private key")
	// ErrKeyType is returned for a key that is not an ECDSA P-256 key.
	ErrKeyType = errors.New("axm: private key must be ECDSA P-256")
	// ErrDecode wraps a response body that does not decode.
	ErrDecode = errors.New("axm: cannot decode response")
	// ErrTransport wraps a request that produced no HTTP response.
	ErrTransport = errors.New("axm: transport failure")
	// ErrLimit is returned for a limit outside 1 to MaxLimit.
	ErrLimit = errors.New("axm: limit must be between 1 and 1000")
	// ErrPageCap is returned by the iterators when the page cap stops them
	// before links.next runs out.
	ErrPageCap = errors.New("axm: page cap reached")
	// ErrNextLink is returned when links.next cannot be followed.
	ErrNextLink = errors.New("axm: unusable links.next")
	// ErrArgument is returned for a missing or malformed argument.
	ErrArgument = errors.New("axm: invalid argument")
	// ErrActivityRule is returned when an activity request breaks one of
	// Apple's documented rules before it is sent.
	ErrActivityRule = errors.New("axm: activity rule violated")
	// ErrWaitTimeout is returned by the polling helpers when the timeout
	// elapses first.
	ErrWaitTimeout = errors.New("axm: wait timed out")
	// ErrForeignHost is returned by FetchActivityLog for a URL that is not
	// on the API host.
	ErrForeignHost = errors.New("axm: download URL is not on the API host")
	// ErrNoEventData is returned by AuditEventAttributes.Data when the
	// event carries no event data.
	ErrNoEventData = errors.New("axm: audit event has no event data")
	// ErrStore is returned by the credential store.
	ErrStore = errors.New("axm: credential store")
)

// ErrorSource says where in the request an error originated: Pointer is
// a JSON pointer into the request entity, Parameter a query parameter.
// Apple's JSON:API flat form ({"pointer": ...} or {"parameter": ...}) and
// the nested form some clients emit ({"jsonPointer": {"pointer": ...}},
// {"parameter": {"parameter": ...}}) both decode.
type ErrorSource struct {
	Pointer   string `json:"pointer,omitempty"`
	Parameter string `json:"parameter,omitempty"`
}

// UnmarshalJSON implements json.Unmarshaler.
func (s *ErrorSource) UnmarshalJSON(b []byte) error {
	var raw struct {
		Pointer     json.RawMessage `json:"pointer"`
		Parameter   json.RawMessage `json:"parameter"`
		JSONPointer json.RawMessage `json:"jsonPointer"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return fmt.Errorf("%w: error source: %w", ErrDecode, err)
	}
	s.Pointer = stringOrNested(raw.Pointer, "pointer")
	if s.Pointer == "" {
		s.Pointer = stringOrNested(raw.JSONPointer, "pointer")
	}
	s.Parameter = stringOrNested(raw.Parameter, "parameter")
	return nil
}

// stringOrNested reads raw as a string, or as an object holding key.
func stringOrNested(raw json.RawMessage, key string) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return ""
	}
	if raw[0] == '"' {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			return s
		}
		return ""
	}
	var m map[string]string
	if json.Unmarshal(raw, &m) == nil {
		return m[key]
	}
	return ""
}

// ErrorLinks are ErrorResponse.Errors.links.
type ErrorLinks struct {
	About      string          `json:"about,omitempty"`
	Associated json.RawMessage `json:"associated,omitempty"`
}

// ErrorItem is one entry of Apple's ErrorResponse.errors.
type ErrorItem struct {
	ID     string          `json:"id,omitempty"`
	Status string          `json:"status"`
	Code   string          `json:"code"`
	Title  string          `json:"title"`
	Detail string          `json:"detail"`
	Source *ErrorSource    `json:"source,omitempty"`
	Links  *ErrorLinks     `json:"links,omitempty"`
	Meta   json.RawMessage `json:"meta,omitempty"`
}

// ErrorResponse is Apple's error document.
type ErrorResponse struct {
	Errors []ErrorItem `json:"errors"`
}

// Error is an API response with a 4xx or 5xx status. Errors holds every
// entry of Apple's error document; for a body that is not that document
// Errors is empty and Body holds the raw bytes.
type Error struct {
	Status     int
	Method     string
	URL        string
	Errors     []ErrorItem
	Body       []byte
	RetryAfter time.Duration
	RequestID  string
}

// Error implements error.
func (e *Error) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "axm: %s %s: %d", e.Method, e.URL, e.Status)
	for _, item := range e.Errors {
		b.WriteString(": ")
		b.WriteString(item.Code)
		if item.Detail != "" {
			b.WriteString(" (" + item.Detail + ")")
		}
		if item.Source != nil {
			switch {
			case item.Source.Parameter != "":
				b.WriteString(" parameter " + item.Source.Parameter)
			case item.Source.Pointer != "":
				b.WriteString(" at " + item.Source.Pointer)
			}
		}
	}
	if len(e.Errors) == 0 && len(e.Body) > 0 {
		body := string(e.Body)
		if len(body) > 200 {
			body = body[:200] + "..."
		}
		b.WriteString(": " + body)
	}
	return b.String()
}

// Code returns the code of the first error, or "".
func (e *Error) Code() string {
	if len(e.Errors) == 0 {
		return ""
	}
	return e.Errors[0].Code
}

// AuthError is an authentication failure: the token endpoint rejected the
// client assertion, or the API answered 401 to a request that was already
// replayed with a fresh token.
type AuthError struct {
	// Status is the HTTP status of the failing response.
	Status int
	// Code and Description are the OAuth error and error_description when
	// the token endpoint answered with them.
	Code        string
	Description string
	// Body is the raw response body.
	Body []byte
	// Err is the underlying error, if any.
	Err error
}

// Error implements error.
func (e *AuthError) Error() string {
	var b strings.Builder
	b.WriteString("axm: authentication failed")
	if e.Status != 0 {
		fmt.Fprintf(&b, ": status %d", e.Status)
	}
	if e.Code != "" {
		b.WriteString(": " + e.Code)
	}
	if e.Description != "" {
		b.WriteString(" (" + e.Description + ")")
	}
	if e.Err != nil {
		b.WriteString(": " + e.Err.Error())
	}
	return b.String()
}

// Unwrap returns Err.
func (e *AuthError) Unwrap() error { return e.Err }

// IsNotFound reports whether err is a 404 from the API.
func IsNotFound(err error) bool { return hasStatus(err, http.StatusNotFound) }

// IsConflict reports whether err is a 409 from the API.
func IsConflict(err error) bool { return hasStatus(err, http.StatusConflict) }

// IsRateLimited reports whether err is a 429 from the API.
func IsRateLimited(err error) bool { return hasStatus(err, http.StatusTooManyRequests) }

// IsUnauthorized reports whether err is an *AuthError or a 401 from the API.
func IsUnauthorized(err error) bool {
	var ae *AuthError
	if errors.As(err, &ae) {
		return true
	}
	return hasStatus(err, http.StatusUnauthorized)
}

func hasStatus(err error, status int) bool {
	var e *Error
	return errors.As(err, &e) && e.Status == status
}

// newError builds an *Error from a failed response body.
func newError(resp *http.Response, body []byte) *Error {
	e := &Error{
		Status:    resp.StatusCode,
		RequestID: resp.Header.Get("X-Request-Id"),
	}
	if resp.Request != nil {
		e.Method = resp.Request.Method
		if resp.Request.URL != nil {
			e.URL = resp.Request.URL.String()
		}
	}
	var doc ErrorResponse
	if json.Unmarshal(body, &doc) == nil && len(doc.Errors) > 0 {
		e.Errors = doc.Errors
	} else {
		e.Body = body
	}
	e.RetryAfter = parseRetryAfter(resp.Header.Get("Retry-After"), time.Time{})
	return e
}

// parseRetryAfter reads a Retry-After header as seconds or an HTTP date;
// now is used for dates (zero means the wall clock). Unparsable values
// give 0.
func parseRetryAfter(v string, now time.Time) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs < 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	at, err := http.ParseTime(v)
	if err != nil {
		return 0
	}
	if now.IsZero() {
		now = time.Now()
	}
	d := at.Sub(now)
	if d < 0 {
		return 0
	}
	return d
}
