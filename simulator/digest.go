package simulator

import (
	"crypto/md5" // #nosec G501 -- RFC 2617 Digest requires MD5
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
)

// ErrDigestChallenge is returned when a DigestChallenge cannot be parsed.
var ErrDigestChallenge = errors.New("simulator: malformed digest challenge")

// HA1 returns the RFC 2617 HA1 value MD5(username:realm:password) as lower
// case hex. Servers that use service.HA1Verifier store this per user.
func HA1(username, realm, password string) string {
	return md5hex(username + ":" + realm + ":" + password)
}

// DigestResponse answers a DigestChallenge the way a macOS client does for
// RFC 2617 Digest with qop=auth: it parses realm and nonce from the
// challenge, draws a client nonce from random (crypto/rand when nil), and
// returns the parameter list the client places in the second
// UserAuthenticate message's DigestResponse. The request method is POST
// because the check-in endpoint is always posted to.
func DigestResponse(challenge, username, password, uri string, random io.Reader) (string, error) {
	params, err := parseDigestChallenge(challenge)
	if err != nil {
		return "", err
	}
	realm, nonce := params["realm"], params["nonce"]
	if nonce == "" {
		return "", fmt.Errorf("%w: missing nonce", ErrDigestChallenge)
	}
	if random == nil {
		random = rand.Reader
	}
	buf := make([]byte, 8)
	if _, err := io.ReadFull(random, buf); err != nil {
		return "", fmt.Errorf("simulator: client nonce: %w", err)
	}
	cnonce := hex.EncodeToString(buf)
	const nc = "00000001"
	ha2 := md5hex("POST:" + uri)
	response := md5hex(strings.Join([]string{HA1(username, realm, password), nonce, nc, cnonce, "auth", ha2}, ":"))
	return fmt.Sprintf(`username=%q, realm=%q, nonce=%q, uri=%q, qop=auth, nc=%s, cnonce=%q, response=%q`,
		username, realm, nonce, uri, nc, cnonce, response), nil
}

func md5hex(s string) string {
	sum := md5.Sum([]byte(s)) // #nosec G401 -- RFC 2617 Digest requires MD5
	return hex.EncodeToString(sum[:])
}

// parseDigestChallenge splits a "Digest k=v, k="v"" list into lower case
// keys. It accepts the challenge with or without the scheme prefix.
func parseDigestChallenge(s string) (map[string]string, error) {
	s = strings.TrimSpace(s)
	if len(s) >= 7 && strings.EqualFold(s[:7], "Digest ") {
		s = strings.TrimSpace(s[7:])
	}
	out := map[string]string{}
	for s != "" {
		eq := strings.IndexByte(s, '=')
		if eq < 0 {
			return nil, fmt.Errorf("%w: expected key=value at %q", ErrDigestChallenge, s)
		}
		key := strings.ToLower(strings.TrimSpace(s[:eq]))
		if key == "" {
			return nil, fmt.Errorf("%w: empty parameter name", ErrDigestChallenge)
		}
		rest := strings.TrimLeft(s[eq+1:], " \t")
		var val string
		switch {
		case strings.HasPrefix(rest, `"`):
			end := strings.IndexByte(rest[1:], '"')
			if end < 0 {
				return nil, fmt.Errorf("%w: unterminated quote for %s", ErrDigestChallenge, key)
			}
			val = rest[1 : 1+end]
			rest = strings.TrimLeft(rest[end+2:], " \t")
			if rest != "" && rest[0] != ',' {
				return nil, fmt.Errorf("%w: unexpected %q after %s", ErrDigestChallenge, rest, key)
			}
		default:
			if c := strings.IndexByte(rest, ','); c >= 0 {
				val, rest = strings.TrimSpace(rest[:c]), rest[c:]
			} else {
				val, rest = strings.TrimSpace(rest), ""
			}
		}
		out[key] = val
		s = strings.TrimSpace(strings.TrimPrefix(rest, ","))
	}
	return out, nil
}
