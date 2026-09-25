package dep

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/fault"

	json "encoding/json/v2"
)

// Errors shared by the client, the token lifecycle, the syncer, the
// assigner, and every store backend.
var (
	ErrInvalid  = fault.ADEInvalid
	ErrNotFound = fault.ADENotFound
	ErrConflict = fault.ADEConflict
	// ErrNoTokens is returned when an account has no OAuth 1.0a tokens yet.
	ErrNoTokens = fault.NewOperator(fault.Unavailable, "the device enrollment account has no tokens")
	// ErrTokenExpired is returned before any HTTP call when the account's
	// access_token_expiry has passed.
	ErrTokenExpired = fault.NewOperator(fault.Unavailable, "the device enrollment access token has expired")
	// ErrTokenInvalid is returned when /session answers 401: the OAuth
	// tokens were rejected. The account state records TokenInvalid.
	ErrTokenInvalid = fault.NewOperator(fault.Unavailable, "Apple rejected the device enrollment tokens")
	// ErrTermsNotSigned is returned when /session answers 403
	// T_C_NOT_SIGNED: an administrator must accept the updated terms in
	// Apple Business Manager or Apple School Manager. The account state
	// records TermsExpired.
	ErrTermsNotSigned = fault.NewOperator(fault.Unavailable, "the Apple terms and conditions are not signed")
	// ErrSeedForITOff is returned by BetaEnrollmentTokens when the
	// organisation has AppleSeed for IT turned off
	// (403 APPLE_SEED_FOR_IT_TURNED_OFF).
	ErrSeedForITOff = fault.NewOperator(fault.Unavailable, "AppleSeed for IT is turned off for this account")
	// ErrSameCursor is returned when a fetch or sync page repeats the
	// cursor it was requested with while claiming more_to_follow; looping on
	// it would never terminate.
	ErrSameCursor = fault.NewOperator(fault.Upstream, "the device enrollment service repeated the cursor with more_to_follow")
	// ErrConsumerKeyMismatch is returned by ImportToken when the new token's
	// consumer_key differs from the stored one and Force is not set.
	ErrConsumerKeyMismatch = fault.ADEConsumerKeyMismatch
	// ErrBodyTooLarge is returned when a request body without GetBody
	// exceeds the replay buffer bound.
	ErrBodyTooLarge = fault.NewOperator(fault.Internal, "the request body exceeds the replay buffer")
	// ErrProfileInvalid wraps every local profile validation failure.
	ErrProfileInvalid = fault.ADEProfileInvalid
	// ErrConfig reports a missing required dependency.
	ErrConfig = fault.NewOperator(fault.Internal, "the Automated Device Enrollment configuration is incomplete")
	// ErrBackoff is returned by RunOnce when the account is backing off after
	// HTTP 429 and no call was made.
	ErrBackoff = fault.NewOperator(fault.Unavailable, "the device enrollment account is backing off")
)

// Error codes Apple's DEP service returns in response bodies, cited from the
// endpoint pages under
// https://developer.apple.com/documentation/devicemanagement/device-assignment.
const (
	CodeForbidden           = "FORBIDDEN"
	CodeExpiredToken        = "EXPIRED_TOKEN" // #nosec G101 -- an error code, not a credential
	CodeTermsNotSigned      = "T_C_NOT_SIGNED"
	CodeCursorRequired      = "CURSOR_REQUIRED"
	CodeInvalidCursor       = "INVALID_CURSOR"
	CodeExhaustedCursor     = "EXHAUSTED_CURSOR"
	CodeExpiredCursor       = "EXPIRED_CURSOR"
	CodeUserAgentInvalid    = "USER_AGENT_INVALID"
	CodeUserAgentMissing    = "USER_AGENT_MISSING"
	CodeDeviceIDRequired    = "DEVICE_ID_REQUIRED"
	CodeProfileUUIDRequired = "PROFILE_UUID_REQUIRED"
	CodeNotFound            = "NOT_FOUND"
	CodeSeedForITOff        = "APPLE_SEED_FOR_IT_TURNED_OFF"
	CodeMalformedBody       = "MALFORMED_REQUEST_BODY"
	CodeDiscoveryRequired   = "MDM_SERVICE_DISCOVERY_URL_REQUIRED"
	CodeDiscoveryInvalid    = "MDM_SERVICE_DISCOVERY_URL_NOT_VALID"
	CodeOrgNotSupported     = "ORG_NOT_SUPPORTED"

	// Profile validation codes from the Define a Profile page.
	CodeConfigNameInvalid   = "CONFIG_NAME_INVALID"
	CodeConfigNameRequired  = "CONFIG_NAME_REQUIRED"
	CodeConfigURLInvalid    = "CONFIG_URL_INVALID"
	CodeConfigURLRequired   = "CONFIG_URL_REQUIRED"
	CodeDepartmentInvalid   = "DEPARTMENT_INVALID"
	CodeFlagsInvalid        = "FLAGS_INVALID"
	CodeLocaleInvalid       = "LOCALE_INVALID"
	CodeMagicInvalid        = "MAGIC_INVALID"
	CodeSupportEmailInvalid = "SUPPORT_EMAIL_INVALID"
	CodeSupportPhoneInvalid = "SUPPORT_PHONE_INVALID"
	// CodeSkipKeyInvalid is the local validation code for an unknown
	// skip_setup_items entry. Apple's documentation does not specify a code for
	// this case.
	CodeSkipKeyInvalid = "SKIP_SETUP_ITEM_INVALID"
)

// Per-device outcome values shared by the profile, disown, and details
// responses.
const (
	StatusSuccess       = "SUCCESS"
	StatusFailed        = "FAILED"
	StatusNotAccessible = "NOT_ACCESSIBLE"
	StatusThrottled     = "THROTTLED"
)

// Error is a non-2xx answer from the DEP service. Code is the error code
// Apple puts in the body, parsed from its bare (EXPIRED_CURSOR) and quoted
// ("EXPIRED_CURSOR") forms and from a JSON object with a "code" or "error"
// member; callers compare Code, never the body text. RetryAfter is the
// Retry-After header when present.
type Error struct {
	Status     int
	Code       string
	Body       []byte
	RetryAfter time.Duration
}

// Error implements error.
func (e *Error) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("the device enrollment service answered HTTP %d %s", e.Status, e.Code)
	}
	return fmt.Sprintf("the device enrollment service answered HTTP %d", e.Status)
}

// Kind classifies the answer for a caller that does not read DEP status codes, by the
// rule every client of an Apple service shares (see fault.AXMInvalid): the caller's
// request for a 400, 404 or 409, a rate limit for a 429, a deadline for a timeout, the
// deployment's credential for a 401 or 403, and Upstream for anything else.
func (e *Error) Kind() *fault.Kind {
	return fault.KindForUpstreamStatus(e.Status)
}

// Is matches the error's kind, so errors.Is(err, fault.Upstream) holds.
func (e *Error) Is(target error) bool { return target == e.Kind() }

// RetryDelay returns the Retry-After Apple sent, for fault.RetryAfterOf.
func (e *Error) RetryDelay() time.Duration { return e.RetryAfter }

// newError builds an Error from a response body and Retry-After header.
func newError(status int, body []byte, retryAfter string, now time.Time) *Error {
	return &Error{Status: status, Code: ParseCode(body), Body: body, RetryAfter: parseRetryAfter(retryAfter, now)}
}

// ParseCode extracts Apple's error code from a response body: the bare
// token, the same token in double quotes, or the "code" or "error" member
// of a JSON object. Anything else yields "".
func ParseCode(body []byte) string {
	b := bytes.TrimSpace(body)
	if len(b) == 0 {
		return ""
	}
	if b[0] == '{' {
		var obj struct {
			Code  string `json:"code"`
			Error string `json:"error"`
		}
		if err := json.Unmarshal(b, &obj); err != nil {
			return ""
		}
		if isCode(obj.Code) {
			return obj.Code
		}
		if isCode(obj.Error) {
			return obj.Error
		}
		return ""
	}
	if len(b) >= 2 && b[0] == '"' && b[len(b)-1] == '"' {
		b = b[1 : len(b)-1]
	}
	if s := string(b); isCode(s) {
		return s
	}
	return ""
}

// isCode reports whether s looks like an Apple error code: upper-case
// letters, digits, and underscores.
func isCode(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_':
		default:
			return false
		}
	}
	return true
}

// parseRetryAfter reads a Retry-After header as delay seconds or an HTTP
// date; unknown forms yield 0.
func parseRetryAfter(h string, now time.Time) time.Duration {
	if h == "" {
		return 0
	}
	if secs, err := strconv.Atoi(h); err == nil {
		if secs < 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	if at, err := time.Parse(time.RFC1123, h); err == nil {
		if d := at.Sub(now); d > 0 {
			return d
		}
	}
	return 0
}

// codeIs reports whether err is a *Error carrying code.
func codeIs(err error, code string) bool {
	var e *Error
	return errors.As(err, &e) && e.Code == code
}

// statusIs reports whether err is a *Error with the HTTP status.
func statusIs(err error, status int) bool {
	var e *Error
	return errors.As(err, &e) && e.Status == status
}

// ProfileError is one local validation failure with the code Apple would
// return for the same input.
type ProfileError struct {
	Code   string
	Detail string
}

// Error implements error.
func (e *ProfileError) Error() string { return "profile " + e.Code + ": " + e.Detail }

// Unwrap makes errors.Is(err, ErrProfileInvalid) true and lets the catalogued
// condition classify the failure.
func (e *ProfileError) Unwrap() error { return ErrProfileInvalid }
