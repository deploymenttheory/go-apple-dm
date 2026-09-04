package proxywire

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

// Wire constants.
const (
	// Path is the only route the ddm role serves for the mdm role.
	Path = "/v1/declarative-management"
	// ContentType is the request body type: the check-in plist as received.
	ContentType = "application/x-apple-aspen-mdm-checkin"
	// HeaderSignature carries base64(HMAC-SHA256(key, ...)): the body on a
	// request, and the status with the body on a response.
	HeaderSignature = "X-MDM-Signature"
	// DefaultMaxBody bounds request and response bodies (1 MiB).
	DefaultMaxBody = 1 << 20
)

// Errors.
var (
	ErrMissingSignature = errors.New("proxywire: missing signature")
	ErrBadSignature     = errors.New("proxywire: bad signature")
	ErrBodyTooLarge     = errors.New("proxywire: body too large")
	ErrContentType      = errors.New("proxywire: unexpected content type")
)

// Sign returns the header value for a request body under key. A request is
// the whole message, so the body is the whole of what needs covering.
func Sign(key, body []byte) string {
	return sign(key, nil, body)
}

// SignResponse returns the header value for a response carrying body under
// status.
//
// The status is covered because the receiving side's trust decision is a
// switch on it while every error path sends an empty body. A MAC over the body
// alone makes the signature of an empty response a constant, which an on-path
// attacker lifts from any error response and replays with a status of their
// choosing: a forged 404 tells every device it has no declarations.
//
// The content type is deliberately not covered. It carries no trust decision,
// and an intermediary that normalises the header would break verification for
// nothing.
func SignResponse(key []byte, status int, body []byte) string {
	return sign(key, preamble(status), body)
}

// Verify checks header against a request body under key. A missing header is
// ErrMissingSignature; anything that does not match is ErrBadSignature.
func Verify(key []byte, header string, body []byte) error {
	return verify(key, header, nil, body)
}

// VerifyResponse checks header against a response carrying body under status.
func VerifyResponse(key []byte, header string, status int, body []byte) error {
	return verify(key, header, preamble(status), body)
}

// preamble binds a response MAC to its status. The label separates the two
// domains so a request signature can never verify as a response, and the
// delimiters cannot appear in a decimal status, so no status and body pair
// can be rewritten as another.
func preamble(status int) []byte {
	return fmt.Appendf(nil, "ddm-response\nstatus=%d\n", status)
}

func sign(key, prefix, body []byte) string {
	mac := hmac.New(sha256.New, key)
	mac.Write(prefix)
	mac.Write(body)
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func verify(key []byte, header string, prefix, body []byte) error {
	if header == "" {
		return ErrMissingSignature
	}
	got, err := base64.StdEncoding.DecodeString(header)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrBadSignature, err)
	}
	mac := hmac.New(sha256.New, key)
	mac.Write(prefix)
	mac.Write(body)
	if !hmac.Equal(got, mac.Sum(nil)) {
		return ErrBadSignature
	}
	return nil
}

// ReadBody reads at most limit bytes from r; a longer body is
// ErrBodyTooLarge. A non-positive limit means DefaultMaxBody.
func ReadBody(r io.Reader, limit int64) ([]byte, error) {
	if limit <= 0 {
		limit = DefaultMaxBody
	}
	body, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, fmt.Errorf("proxywire: read body: %w", err)
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("%w: more than %d bytes", ErrBodyTooLarge, limit)
	}
	return body, nil
}
