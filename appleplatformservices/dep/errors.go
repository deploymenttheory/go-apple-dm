package dep

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"time"

	json "encoding/json/v2"
)

// Errors shared by the client, the token lifecycle, the syncer, the
// assigner, and every store backend.
var (
	ErrInvalid  = errors.New("dep: invalid argument")
	ErrNotFound = errors.New("dep: not found")
	ErrConflict = errors.New("dep: conflict")
	// ErrNoTokens is returned when an account has no OAuth 1.0a tokens yet.
	ErrNoTokens = errors.New("dep: account has no tokens")
	// ErrTokenExpired is returned before any HTTP call when the account's
	// access_token_expiry has passed.
	ErrTokenExpired = errors.New("dep: access token expired")
	// ErrTokenInvalid is returned when /session answers 401: the OAuth
	// tokens were rejected. The account state records TokenInvalid.
	ErrTokenInvalid = errors.New("dep: tokens rejected by /session")
	// ErrTermsNotSigned is returned when /session answers 403
	// T_C_NOT_SIGNED: an administrator must accept the updated terms in
	// Apple Business Manager or Apple School Manager. The account state
	// records TermsExpired.
	ErrTermsNotSigned = errors.New("dep: terms and conditions not signed")
	// ErrSeedForITOff is returned by BetaEnrollmentTokens when the
	// organisation has AppleSeed for IT turned off
	// (403 APPLE_SEED_FOR_IT_TURNED_OFF).
	ErrSeedForITOff = errors.New("dep: AppleSeed for IT is turned off")
	// ErrSameCursor is returned when a fetch or sync page repeats the
	// cursor it was requested with while claiming more_to_follow; looping on
	// it would never terminate.
	ErrSameCursor = errors.New("dep: server repeated the cursor with more_to_follow")
	// ErrConsumerKeyMismatch is returned by ImportToken when the new token's
	// consumer_key differs from the stored one and Force is not set.
	ErrConsumerKeyMismatch = errors.New("dep: consumer key differs from the stored token")
	// ErrBodyTooLarge is returned when a request body without GetBody
	// exceeds the replay buffer bound.
	ErrBodyTooLarge = errors.New("dep: request body exceeds replay buffer")
	// ErrProfileInvalid wraps every local profile validation failure.
	ErrProfileInvalid = errors.New("dep: profile invalid")
	// ErrConfig reports a missing required dependency.
	ErrConfig = errors.New("dep: configuration")
	// ErrBackoff is returned by RunOnce when the account is backing off after
	// HTTP 429 and no call was made.
	ErrBackoff = errors.New("dep: account backing off")
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
	// CodeSkipKeyInvalid is ours: Apple's page does not name the code for
	// an unknown skip_setup_items entry, so the local validator uses this.
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
		return fmt.Sprintf("dep: HTTP %d %s", e.Status, e.Code)
	}
	return fmt.Sprintf("dep: HTTP %d", e.Status)
}

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
func (e *ProfileError) Error() string { return "dep: profile " + e.Code + ": " + e.Detail }

// Is makes errors.Is(err, ErrProfileInvalid) true.
func (e *ProfileError) Is(target error) bool { return target == ErrProfileInvalid }
