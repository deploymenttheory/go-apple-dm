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
	// HeaderSignature carries base64(HMAC-SHA256(key, body)).
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

// Sign returns the header value for body under key.
func Sign(key, body []byte) string {
	mac := hmac.New(sha256.New, key)
	mac.Write(body)
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// Verify checks header against body under key. A missing header is
// ErrMissingSignature; anything that does not match is ErrBadSignature.
func Verify(key []byte, header string, body []byte) error {
	if header == "" {
		return ErrMissingSignature
	}
	got, err := base64.StdEncoding.DecodeString(header)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrBadSignature, err)
	}
	mac := hmac.New(sha256.New, key)
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
