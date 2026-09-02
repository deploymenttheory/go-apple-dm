package service

import (
	"context"
	"crypto/md5" // #nosec G501 -- RFC 2617 Digest requires MD5
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/deploymenttheory/go-apple-mdm/event"
	"github.com/deploymenttheory/go-apple-mdm/internal/clock"
	"github.com/deploymenttheory/go-apple-mdm/mdm"
	"github.com/deploymenttheory/go-apple-mdm/plist"
	"github.com/deploymenttheory/go-apple-mdm/schema/checkin"
	"github.com/deploymenttheory/go-apple-mdm/storage"
)

// UserAuthenticate errors (decision record 0016).
var (
	// ErrUserNotManaged is returned by DigestUserAuth.Manage to answer 410
	// for the current login session.
	ErrUserNotManaged = errors.New("service: user not managed")
	// ErrNoChallenge is used internally when the second message arrives
	// without an outstanding challenge; it surfaces as an empty AuthToken.
	ErrNoChallenge = errors.New("service: no outstanding challenge")
	// ErrBadDigest reports a DigestResponse that could not be parsed or
	// that does not match the challenge. DigestUserAuth treats it as a
	// rejected login rather than a server failure.
	ErrBadDigest = errors.New("service: malformed digest response")

	errUserAuthConfig = errors.New("service: DigestUserAuth needs Store and Verifier")
)

// Defaults for DigestUserAuth.
const (
	DefaultUserAuthRealm        = "mdm"
	DefaultUserAuthChallengeTTL = 5 * time.Minute
)

// VerifyInput is what a UserVerifier needs to check one DigestResponse.
type VerifyInput struct {
	// UserID is the GUID from the UserAuthenticate message.
	UserID string
	// Realm is the realm the challenge was issued for.
	Realm string
	// Challenge is the full DigestChallenge string that was issued.
	Challenge string
	// DigestResponse is the client's Authorization-style parameter list.
	DigestResponse string
}

// UserVerifier checks a DigestResponse against the deployment's password
// store. It returns false for a wrong password or unknown user and an error
// only when the check itself could not run.
type UserVerifier interface {
	Verify(ctx context.Context, r *mdm.Request, in VerifyInput) (bool, error)
}

// UserVerifierFunc adapts a function to UserVerifier.
type UserVerifierFunc func(ctx context.Context, r *mdm.Request, in VerifyInput) (bool, error)

// Verify implements UserVerifier.
func (f UserVerifierFunc) Verify(ctx context.Context, r *mdm.Request, in VerifyInput) (bool, error) {
	return f(ctx, r, in)
}

// HA1Verifier implements RFC 2617 Digest (MD5, qop=auth) given the HA1
// value MD5(username:realm:password) from the deployment. The lookup
// returning ("", nil) means the user is unknown and the login is rejected;
// an error from the lookup is returned as such.
func HA1Verifier(ha1 func(ctx context.Context, username, realm string) (string, error)) UserVerifier {
	return UserVerifierFunc(func(ctx context.Context, _ *mdm.Request, in VerifyInput) (bool, error) {
		p, err := parseDigestParams(in.DigestResponse)
		if err != nil {
			return false, err
		}
		if err := checkDigestParams(p, in); err != nil {
			return false, err
		}
		h1, err := ha1(ctx, p["username"], in.Realm)
		if err != nil {
			return false, fmt.Errorf("service: ha1 lookup: %w", err)
		}
		if h1 == "" {
			return false, nil
		}
		expected := expectedDigest(h1, p)
		return subtle.ConstantTimeCompare([]byte(expected), []byte(strings.ToLower(p["response"]))) == 1, nil
	})
}

// checkDigestParams validates the parsed response against the challenge.
func checkDigestParams(p map[string]string, in VerifyInput) error {
	for _, k := range []string{"username", "realm", "nonce", "uri", "response"} {
		if p[k] == "" {
			return fmt.Errorf("%w: missing %s", ErrBadDigest, k)
		}
	}
	if p["realm"] != in.Realm {
		return fmt.Errorf("%w: realm %q is not %q", ErrBadDigest, p["realm"], in.Realm)
	}
	ch, err := parseDigestParams(in.Challenge)
	if err != nil {
		return err
	}
	if ch["nonce"] == "" || p["nonce"] != ch["nonce"] {
		return fmt.Errorf("%w: nonce does not match the challenge", ErrBadDigest)
	}
	if qop := p["qop"]; qop != "" {
		if qop != "auth" {
			return fmt.Errorf("%w: unsupported qop %q", ErrBadDigest, qop)
		}
		if p["nc"] == "" || p["cnonce"] == "" {
			return fmt.Errorf("%w: qop=auth requires nc and cnonce", ErrBadDigest)
		}
	}
	return nil
}

// expectedDigest computes the RFC 2617 response for the check-in POST.
func expectedDigest(h1 string, p map[string]string) string {
	ha2 := md5hex("POST:" + p["uri"])
	if p["qop"] == "" {
		return md5hex(h1 + ":" + p["nonce"] + ":" + ha2)
	}
	return md5hex(strings.Join([]string{h1, p["nonce"], p["nc"], p["cnonce"], p["qop"], ha2}, ":"))
}

func md5hex(s string) string {
	sum := md5.Sum([]byte(s)) // #nosec G401 -- RFC 2617 Digest requires MD5
	return hex.EncodeToString(sum[:])
}

// parseDigestParams splits a Digest parameter list ("Digest k=v, k="v"")
// into lower case keys. The scheme prefix is optional.
func parseDigestParams(s string) (map[string]string, error) {
	s = strings.TrimSpace(s)
	if len(s) >= 7 && strings.EqualFold(s[:7], "Digest ") {
		s = strings.TrimSpace(s[7:])
	}
	out := map[string]string{}
	for s != "" {
		eq := strings.IndexByte(s, '=')
		if eq < 0 {
			return nil, fmt.Errorf("%w: expected key=value at %q", ErrBadDigest, s)
		}
		key := strings.ToLower(strings.TrimSpace(s[:eq]))
		if key == "" {
			return nil, fmt.Errorf("%w: empty parameter name", ErrBadDigest)
		}
		rest := strings.TrimLeft(s[eq+1:], " \t")
		var val string
		switch {
		case strings.HasPrefix(rest, `"`):
			end := strings.IndexByte(rest[1:], '"')
			if end < 0 {
				return nil, fmt.Errorf("%w: unterminated quote for %s", ErrBadDigest, key)
			}
			val = rest[1 : 1+end]
			rest = strings.TrimLeft(rest[end+2:], " \t")
			if rest != "" && rest[0] != ',' {
				return nil, fmt.Errorf("%w: unexpected %q after %s", ErrBadDigest, rest, key)
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

// DigestUserAuth implements the two-message UserAuthenticate handshake:
// the first message is answered with a one-shot DigestChallenge, the
// second is verified and answered with an AuthToken. A wrong or expired
// digest clears the challenge and answers an empty AuthToken, which is how
// Apple documents a rejected password.
type DigestUserAuth struct {
	Store    storage.UserAuthStore
	Verifier UserVerifier
	// Realm defaults to DefaultUserAuthRealm.
	Realm string
	// ChallengeTTL defaults to DefaultUserAuthChallengeTTL.
	ChallengeTTL time.Duration
	// Manage decides whether the user is managed at all. Returning
	// ErrUserNotManaged answers 410 (CodeGone) for this login session; any
	// other error is CodeInternal. Nil manages everyone.
	Manage func(ctx context.Context, r *mdm.Request, m *checkin.UserAuthenticate) error
	// Clock defaults to the real clock.
	Clock clock.Clock
	// Bus receives UserAuthenticated and UserAuthFailed; nil disables it.
	Bus *event.Bus
	// Rand defaults to crypto/rand.
	Rand io.Reader
}

// Handle satisfies UserAuthenticateHandler.
func (d *DigestUserAuth) Handle(ctx context.Context, r *mdm.Request, m *checkin.UserAuthenticate) (*mdm.UserAuthenticateResponse, error) {
	if d.Store == nil || d.Verifier == nil {
		return nil, wrapCode(CodeInternal, errUserAuthConfig)
	}
	if r == nil || m == nil {
		return nil, wrapCode(CodeBadRequest, fmt.Errorf("%w: nil request or message", ErrInvalidMessage))
	}
	if !r.ID.Channel.IsUser() {
		return nil, wrapCode(CodeBadRequest, fmt.Errorf("%w: UserAuthenticate on the device channel %s", ErrInvalidMessage, r.ID.ID))
	}
	raw, err := plist.Marshal(m)
	if err != nil {
		return nil, wrapCode(CodeInternal, err)
	}
	if m.DigestResponse == "" {
		return d.challenge(ctx, r, m, raw)
	}
	return d.verify(ctx, r, m, raw)
}

func (d *DigestUserAuth) challenge(ctx context.Context, r *mdm.Request, m *checkin.UserAuthenticate, raw []byte) (*mdm.UserAuthenticateResponse, error) {
	if d.Manage != nil {
		if err := d.Manage(ctx, r, m); err != nil {
			if errors.Is(err, ErrUserNotManaged) {
				return nil, wrapCode(CodeGone, err)
			}
			return nil, wrapCode(CodeInternal, err)
		}
	}
	nonce, err := d.random(16)
	if err != nil {
		return nil, wrapCode(CodeInternal, err)
	}
	challenge := `Digest realm="` + d.realm() + `", nonce="` + nonce + `", qop="auth", algorithm=MD5`
	if err := d.Store.StoreUserAuthChallenge(ctx, r.ID, challenge, raw, d.now()); err != nil {
		return nil, wrapCode(codeForStorage(err), err)
	}
	return &mdm.UserAuthenticateResponse{DigestChallenge: &challenge}, nil
}

func (d *DigestUserAuth) verify(ctx context.Context, r *mdm.Request, m *checkin.UserAuthenticate, raw []byte) (*mdm.UserAuthenticateResponse, error) {
	now := d.now()
	st, err := d.outstanding(ctx, r.ID)
	if errors.Is(err, ErrNoChallenge) {
		return &mdm.UserAuthenticateResponse{AuthToken: new("")}, nil
	}
	if err != nil {
		return nil, wrapCode(codeForStorage(err), err)
	}
	if now.Sub(st.ChallengeAt) > d.ttl() {
		return d.reject(ctx, r, m)
	}
	in := VerifyInput{UserID: m.UserID, Realm: d.realm(), Challenge: st.Challenge, DigestResponse: m.DigestResponse}
	ok, err := d.Verifier.Verify(ctx, r, in)
	if err != nil && !errors.Is(err, ErrBadDigest) {
		return nil, wrapCode(CodeInternal, err)
	}
	if !ok {
		return d.reject(ctx, r, m)
	}
	token, err := d.random(32)
	if err != nil {
		return nil, wrapCode(CodeInternal, err)
	}
	if err := d.Store.StoreUserAuthToken(ctx, r.ID, token, raw, now); err != nil {
		return nil, wrapCode(codeForStorage(err), err)
	}
	d.publish(ctx, event.UserAuthenticated, r.ID, m.UserID)
	return &mdm.UserAuthenticateResponse{AuthToken: &token}, nil
}

// outstanding loads the state and returns ErrNoChallenge when no challenge
// is waiting for an answer.
func (d *DigestUserAuth) outstanding(ctx context.Context, id mdm.EnrollmentID) (*storage.UserAuthState, error) {
	st, err := d.Store.UserAuth(ctx, id)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return nil, fmt.Errorf("%w: %w", ErrNoChallenge, err)
		}
		return nil, err
	}
	if st.Challenge == "" {
		return nil, fmt.Errorf("%w: %s", ErrNoChallenge, id.ID)
	}
	return st, nil
}

// reject clears the one-shot challenge, publishes UserAuthFailed, and
// answers the empty AuthToken Apple documents for a rejected password.
func (d *DigestUserAuth) reject(ctx context.Context, r *mdm.Request, m *checkin.UserAuthenticate) (*mdm.UserAuthenticateResponse, error) {
	if err := d.Store.ClearUserAuth(ctx, r.ID); err != nil {
		return nil, wrapCode(codeForStorage(err), err)
	}
	d.publish(ctx, event.UserAuthFailed, r.ID, m.UserID)
	return &mdm.UserAuthenticateResponse{AuthToken: new("")}, nil
}

func (d *DigestUserAuth) publish(ctx context.Context, t event.Type, id mdm.EnrollmentID, data any) {
	if d.Bus == nil {
		return
	}
	// Handler errors are the subscriber's concern; the handshake outcome
	// does not depend on them.
	_ = d.Bus.Publish(ctx, event.Event{Type: t, At: d.now(), Enrollment: id, Actor: "device", Data: data})
}

func (d *DigestUserAuth) random(n int) (string, error) {
	rd := d.Rand
	if rd == nil {
		rd = rand.Reader
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(rd, b); err != nil {
		return "", fmt.Errorf("service: random bytes: %w", err)
	}
	return hex.EncodeToString(b), nil
}

func (d *DigestUserAuth) realm() string {
	if d.Realm == "" {
		return DefaultUserAuthRealm
	}
	return d.Realm
}

func (d *DigestUserAuth) ttl() time.Duration {
	if d.ChallengeTTL <= 0 {
		return DefaultUserAuthChallengeTTL
	}
	return d.ChallengeTTL
}

func (d *DigestUserAuth) now() time.Time {
	if d.Clock == nil {
		return time.Now()
	}
	return d.Clock.Now()
}
