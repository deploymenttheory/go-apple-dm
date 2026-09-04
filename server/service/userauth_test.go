package service_test

import (
	"context"
	"crypto/md5" // #nosec G501 -- RFC 2617 Digest requires MD5
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/deploymenttheory/go-apple-dm/v3/clock"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/event"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/plist"
	"github.com/deploymenttheory/go-apple-dm/v3/schema/checkin"
	"github.com/deploymenttheory/go-apple-dm/v3/server/service"
	"github.com/deploymenttheory/go-apple-dm/v3/server/storage"
	"github.com/deploymenttheory/go-apple-dm/v3/server/storage/inmem"
	"github.com/deploymenttheory/go-apple-dm/v3/simulator"
)

const (
	uaUser     = "alice"
	uaPassword = "secret"
	uaURI      = "/mdm/checkin"
)

var (
	errBoom = errors.New("boom")
	errRand = errors.New("entropy exhausted")
	uaDev   = mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "D1"}
	uaUID   = mdm.EnrollmentID{Channel: mdm.ChannelUser, ID: "D1:U1", ParentID: "D1"}
)

// seqReader is a deterministic random source: every byte is the next
// counter value, so successive reads never repeat.
type seqReader struct{ n byte }

func (r *seqReader) Read(b []byte) (int, error) {
	for i := range b {
		b[i] = r.n
		r.n++
	}
	return len(b), nil
}

// failingUserAuth wraps a UserAuthStore and fails the named methods.
type failingUserAuth struct {
	storage.UserAuthStore
	fail map[string]error
}

func (f *failingUserAuth) StoreUserAuthChallenge(ctx context.Context, id mdm.EnrollmentID, challenge string, raw []byte, at time.Time) error {
	if err := f.fail["StoreUserAuthChallenge"]; err != nil {
		return err
	}
	return f.UserAuthStore.StoreUserAuthChallenge(ctx, id, challenge, raw, at)
}

func (f *failingUserAuth) StoreUserAuthToken(ctx context.Context, id mdm.EnrollmentID, token string, raw []byte, at time.Time) error {
	if err := f.fail["StoreUserAuthToken"]; err != nil {
		return err
	}
	return f.UserAuthStore.StoreUserAuthToken(ctx, id, token, raw, at)
}

func (f *failingUserAuth) UserAuth(ctx context.Context, id mdm.EnrollmentID) (*storage.UserAuthState, error) {
	if err := f.fail["UserAuth"]; err != nil {
		return nil, err
	}
	return f.UserAuthStore.UserAuth(ctx, id)
}

func (f *failingUserAuth) ClearUserAuth(ctx context.Context, id mdm.EnrollmentID) error {
	if err := f.fail["ClearUserAuth"]; err != nil {
		return err
	}
	return f.UserAuthStore.ClearUserAuth(ctx, id)
}

// uaFixture is a DigestUserAuth over an inmem store seeded with device D1.
type uaFixture struct {
	d      *service.DigestUserAuth
	store  *inmem.Store
	clock  *clock.Fake
	events []event.Event
}

func ha1Lookup(_ context.Context, username, realm string) (string, error) {
	if username == uaUser {
		return simulator.HA1(uaUser, realm, uaPassword), nil
	}
	return "", nil
}

func newUAFixture(t *testing.T) *uaFixture {
	t.Helper()
	ctx := context.Background()
	f := &uaFixture{store: inmem.New(), clock: clock.NewFake(t0)}
	if err := f.store.UpsertAuthenticate(ctx, uaDev, &checkin.Authenticate{Topic: "com.apple.mgmt.t"}, []byte("<plist/>"), t0); err != nil {
		t.Fatal(err)
	}
	if err := f.store.StoreTokenUpdate(ctx, uaDev, mdm.Push{Topic: "com.apple.mgmt.t", Token: []byte{1}, Magic: "m"}, nil, nil, t0); err != nil {
		t.Fatal(err)
	}
	bus := event.New()
	bus.Subscribe(event.All, func(_ context.Context, e event.Event) error { f.events = append(f.events, e); return nil })
	f.d = &service.DigestUserAuth{Store: f.store, Verifier: service.HA1Verifier(ha1Lookup), Clock: f.clock, Bus: bus, Rand: &seqReader{}}
	return f
}

func uaMsg(digest string) *checkin.UserAuthenticate {
	return &checkin.UserAuthenticate{MessageType: "UserAuthenticate", UDID: "D1", UserID: "U1", DigestResponse: digest}
}

func uaReq() *mdm.Request { return &mdm.Request{ID: uaUID, ReceivedAt: t0} }

// challenge runs the first message and returns the DigestChallenge.
func (f *uaFixture) challenge(t *testing.T) string {
	t.Helper()
	resp, err := f.d.Handle(context.Background(), uaReq(), uaMsg(""))
	if err != nil || resp == nil || resp.DigestChallenge == nil || resp.AuthToken != nil {
		t.Fatalf("challenge: %+v %v", resp, err)
	}
	return *resp.DigestChallenge
}

// answer runs the second message with a digest for the given password.
func (f *uaFixture) answer(t *testing.T, challenge, password string) (*mdm.UserAuthenticateResponse, error) {
	t.Helper()
	digest, err := simulator.DigestResponse(challenge, uaUser, password, uaURI, &seqReader{n: 100})
	if err != nil {
		t.Fatal(err)
	}
	return f.d.Handle(context.Background(), uaReq(), uaMsg(digest))
}

func (f *uaFixture) lastEvent(t *testing.T, want event.Type) event.Event {
	t.Helper()
	if len(f.events) == 0 {
		t.Fatalf("no events, want %s", want)
	}
	e := f.events[len(f.events)-1]
	if e.Type != want || e.Actor != "device" || e.Enrollment != uaUID || e.Data != "U1" || !e.At.Equal(f.clock.Now()) {
		t.Fatalf("event %+v, want %s", e, want)
	}
	return e
}

// must2 packs a response and error for emptyToken.
func must2(resp *mdm.UserAuthenticateResponse, err error) outcome { return outcome{resp, err} }

type outcome struct {
	resp *mdm.UserAuthenticateResponse
	err  error
}

// rejected answers the challenge and expects an empty AuthToken.
func (f *uaFixture) rejected(t *testing.T, challenge, password string) {
	t.Helper()
	emptyToken(t, must2(f.answer(t, challenge, password)))
}

func emptyToken(t *testing.T, o outcome) {
	resp, err := o.resp, o.err
	t.Helper()
	if err != nil || resp == nil || resp.AuthToken == nil || *resp.AuthToken != "" || resp.DigestChallenge != nil {
		t.Fatalf("want empty AuthToken, got %+v %v", resp, err)
	}
}

func TestDigestUserAuthFlow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("ChallengeDiffersPerCall", func(t *testing.T) {
		t.Parallel()
		f := newUAFixture(t)
		c1 := f.challenge(t)
		c2 := f.challenge(t)
		if c1 == c2 {
			t.Fatalf("nonce reused: %s", c1)
		}
		for _, part := range []string{`Digest realm="mdm"`, `nonce="`, `qop="auth"`, `algorithm=MD5`} {
			if !strings.Contains(c2, part) {
				t.Fatalf("challenge %q lacks %q", c2, part)
			}
		}
		st, err := f.store.UserAuth(ctx, uaUID)
		if err != nil || st.Challenge != c2 || !st.ChallengeAt.Equal(t0) || st.AuthToken != "" {
			t.Fatalf("state %+v %v", st, err)
		}
		if !strings.Contains(string(st.AuthenticateRaw), "<key>UserID</key>") {
			t.Fatalf("raw plist not stored: %s", st.AuthenticateRaw)
		}
		if len(f.events) != 0 {
			t.Fatalf("challenge published %v", f.events)
		}
	})

	t.Run("WrongDigest", func(t *testing.T) {
		t.Parallel()
		f := newUAFixture(t)
		c := f.challenge(t)
		f.rejected(t, c, "wrong")
		if _, err := f.store.UserAuth(ctx, uaUID); !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("challenge not cleared: %v", err)
		}
		f.lastEvent(t, event.UserAuthFailed)
		// The challenge is one-shot: the right password no longer helps.
		f.rejected(t, c, uaPassword)
		if len(f.events) != 1 {
			t.Fatalf("replay published %v", f.events)
		}
	})

	t.Run("MalformedDigest", func(t *testing.T) {
		t.Parallel()
		f := newUAFixture(t)
		f.challenge(t)
		emptyToken(t, must2(f.d.Handle(ctx, uaReq(), uaMsg("not a digest"))))
		f.lastEvent(t, event.UserAuthFailed)
	})

	t.Run("RightDigest", func(t *testing.T) {
		t.Parallel()
		f := newUAFixture(t)
		c := f.challenge(t)
		f.clock.Advance(time.Minute)
		resp, err := f.answer(t, c, uaPassword)
		if err != nil || resp == nil || resp.AuthToken == nil || len(*resp.AuthToken) != 64 || resp.DigestChallenge != nil {
			t.Fatalf("token: %+v %v", resp, err)
		}
		if _, err := hex.DecodeString(*resp.AuthToken); err != nil {
			t.Fatalf("token is not hex: %v", err)
		}
		st, err := f.store.UserAuth(ctx, uaUID)
		if err != nil || st.AuthToken != *resp.AuthToken || st.Challenge != "" || !st.TokenAt.Equal(f.clock.Now()) {
			t.Fatalf("state %+v %v", st, err)
		}
		if !strings.Contains(string(st.DigestRaw), "<key>DigestResponse</key>") {
			t.Fatalf("digest plist not stored: %s", st.DigestRaw)
		}
		f.lastEvent(t, event.UserAuthenticated)
		// Replaying the digest after success finds no challenge.
		f.rejected(t, c, uaPassword)
		if len(f.events) != 1 {
			t.Fatalf("replay published %v", f.events)
		}
		// A new login session issues a new challenge and drops the token.
		f.challenge(t)
		if st, _ := f.store.UserAuth(ctx, uaUID); st.AuthToken != "" || st.Challenge == "" {
			t.Fatalf("new challenge kept token: %+v", st)
		}
	})

	t.Run("Expired", func(t *testing.T) {
		t.Parallel()
		f := newUAFixture(t)
		f.d.ChallengeTTL = time.Minute
		c := f.challenge(t)
		f.clock.Advance(time.Minute)
		// Exactly the TTL is still accepted.
		if resp, err := f.answer(t, c, uaPassword); err != nil || *resp.AuthToken == "" {
			t.Fatalf("at TTL: %+v %v", resp, err)
		}
		c = f.challenge(t)
		f.clock.Advance(time.Minute + time.Second)
		f.rejected(t, c, uaPassword)
		f.lastEvent(t, event.UserAuthFailed)
		if _, err := f.store.UserAuth(ctx, uaUID); !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("expired challenge not cleared: %v", err)
		}
	})

	t.Run("NoChallenge", func(t *testing.T) {
		t.Parallel()
		f := newUAFixture(t)
		emptyToken(t, must2(f.d.Handle(ctx, uaReq(), uaMsg("x"))))
		// Unknown parent on the second message is also "no challenge".
		emptyToken(t, must2(f.d.Handle(ctx, &mdm.Request{ID: mdm.EnrollmentID{Channel: mdm.ChannelUser, ID: "D9:U1", ParentID: "D9"}}, uaMsg("x"))))
		if len(f.events) != 0 {
			t.Fatalf("published %v", f.events)
		}
	})

	t.Run("VerifierError", func(t *testing.T) {
		t.Parallel()
		f := newUAFixture(t)
		f.d.Verifier = service.UserVerifierFunc(func(context.Context, *mdm.Request, service.VerifyInput) (bool, error) { return false, errBoom })
		c := f.challenge(t)
		if _, err := f.answer(t, c, uaPassword); service.CodeOf(err) != service.CodeInternal || !errors.Is(err, errBoom) {
			t.Fatalf("verifier error: %v", err)
		}
		// The challenge survives a server-side failure.
		if st, err := f.store.UserAuth(ctx, uaUID); err != nil || st.Challenge != c {
			t.Fatalf("state %+v %v", st, err)
		}
		// The verifier sees the issued challenge and the message.
		var got service.VerifyInput
		f.d.Verifier = service.UserVerifierFunc(func(_ context.Context, _ *mdm.Request, in service.VerifyInput) (bool, error) {
			got = in
			return true, nil
		})
		if _, err := f.d.Handle(ctx, uaReq(), uaMsg("d")); err != nil {
			t.Fatal(err)
		}
		if got != (service.VerifyInput{UserID: "U1", Realm: "mdm", Challenge: c, DigestResponse: "d"}) {
			t.Fatalf("verify input %+v", got)
		}
	})

	t.Run("Manage", func(t *testing.T) {
		t.Parallel()
		f := newUAFixture(t)
		f.d.Manage = func(_ context.Context, _ *mdm.Request, m *checkin.UserAuthenticate) error {
			if m.UserID == "U1" {
				return service.ErrUserNotManaged
			}
			return errBoom
		}
		if _, err := f.d.Handle(ctx, uaReq(), uaMsg("")); service.CodeOf(err) != service.CodeGone || !errors.Is(err, service.ErrUserNotManaged) {
			t.Fatalf("not managed: %v", err)
		}
		m := uaMsg("")
		m.UserID = "U2"
		if _, err := f.d.Handle(ctx, uaReq(), m); service.CodeOf(err) != service.CodeInternal || !errors.Is(err, errBoom) {
			t.Fatalf("manage error: %v", err)
		}
		if _, err := f.store.UserAuth(ctx, uaUID); !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("challenge stored despite refusal: %v", err)
		}
	})

	t.Run("BadRequests", func(t *testing.T) {
		t.Parallel()
		f := newUAFixture(t)
		if _, err := f.d.Handle(ctx, &mdm.Request{ID: uaDev}, uaMsg("")); service.CodeOf(err) != service.CodeBadRequest || !errors.Is(err, service.ErrInvalidMessage) {
			t.Fatalf("device channel: %v", err)
		}
		if _, err := f.d.Handle(ctx, nil, uaMsg("")); service.CodeOf(err) != service.CodeBadRequest {
			t.Fatalf("nil request: %v", err)
		}
		if _, err := f.d.Handle(ctx, uaReq(), nil); service.CodeOf(err) != service.CodeBadRequest {
			t.Fatalf("nil message: %v", err)
		}
		// An invalid id that reaches the store is CodeBadRequest too.
		if _, err := f.d.Handle(ctx, &mdm.Request{ID: mdm.EnrollmentID{Channel: mdm.ChannelUser, ID: "D1:U1"}}, uaMsg("")); service.CodeOf(err) != service.CodeBadRequest {
			t.Fatalf("user channel without parent: %v", err)
		}
		if _, err := f.d.Handle(ctx, &mdm.Request{ID: mdm.EnrollmentID{Channel: mdm.ChannelUser, ID: "D1:U1"}}, uaMsg("x")); service.CodeOf(err) != service.CodeBadRequest {
			t.Fatalf("user channel without parent, second message: %v", err)
		}
		for _, d := range []*service.DigestUserAuth{{}, {Store: f.store}, {Verifier: f.d.Verifier}} {
			if _, err := d.Handle(ctx, uaReq(), uaMsg("")); service.CodeOf(err) != service.CodeInternal {
				t.Fatalf("unconfigured %+v: %v", d, err)
			}
		}
	})

	t.Run("UnknownParent", func(t *testing.T) {
		t.Parallel()
		f := newUAFixture(t)
		r := &mdm.Request{ID: mdm.EnrollmentID{Channel: mdm.ChannelUser, ID: "D9:U1", ParentID: "D9"}}
		if _, err := f.d.Handle(ctx, r, uaMsg("")); service.CodeOf(err) != service.CodeUnknownEnrollment {
			t.Fatalf("unknown parent: %v", err)
		}
	})

	t.Run("RandFailure", func(t *testing.T) {
		t.Parallel()
		f := newUAFixture(t)
		f.d.Rand = iotest.ErrReader(errRand)
		if _, err := f.d.Handle(ctx, uaReq(), uaMsg("")); service.CodeOf(err) != service.CodeInternal || !errors.Is(err, errRand) {
			t.Fatalf("nonce: %v", err)
		}
		// Enough entropy for the nonce but not for the token.
		f.d.Rand = io.MultiReader(io.LimitReader(&seqReader{}, 16), iotest.ErrReader(errRand))
		c := f.challenge(t)
		if _, err := f.answer(t, c, uaPassword); service.CodeOf(err) != service.CodeInternal || !errors.Is(err, errRand) {
			t.Fatalf("token: %v", err)
		}
	})

	t.Run("StoreFailures", func(t *testing.T) {
		t.Parallel()
		f := newUAFixture(t)
		fs := &failingUserAuth{UserAuthStore: f.store, fail: map[string]error{}}
		f.d.Store = fs
		check := func(method string, run func() error) {
			t.Helper()
			fs.fail[method] = errBoom
			defer delete(fs.fail, method)
			if err := run(); service.CodeOf(err) != service.CodeInternal || !errors.Is(err, errBoom) {
				t.Fatalf("%s: %v", method, err)
			}
		}
		check("StoreUserAuthChallenge", func() error { _, err := f.d.Handle(ctx, uaReq(), uaMsg("")); return err })
		c := f.challenge(t)
		check("UserAuth", func() error { _, err := f.answer(t, c, uaPassword); return err })
		check("StoreUserAuthToken", func() error { _, err := f.answer(t, c, uaPassword); return err })
		check("ClearUserAuth", func() error { _, err := f.answer(t, c, "wrong"); return err })
		if len(f.events) != 0 {
			t.Fatalf("failures published %v", f.events)
		}
		// StoreUserAuthToken racing a clear reports the missing challenge.
		fs.fail["StoreUserAuthToken"] = storage.ErrNotFound
		if _, err := f.answer(t, c, uaPassword); service.CodeOf(err) != service.CodeUnknownEnrollment {
			t.Fatalf("token without challenge: %v", err)
		}
	})

	t.Run("Defaults", func(t *testing.T) {
		t.Parallel()
		f := newUAFixture(t)
		d := &service.DigestUserAuth{Store: f.store, Verifier: service.HA1Verifier(ha1Lookup), Realm: "corp"}
		resp, err := d.Handle(ctx, uaReq(), uaMsg(""))
		if err != nil || !strings.HasPrefix(*resp.DigestChallenge, `Digest realm="corp", nonce="`) {
			t.Fatalf("challenge: %+v %v", resp, err)
		}
		digest, err := simulator.DigestResponse(*resp.DigestChallenge, uaUser, uaPassword, uaURI, nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err = d.Handle(ctx, uaReq(), uaMsg(digest))
		if err != nil || len(*resp.AuthToken) != 64 {
			t.Fatalf("token: %+v %v", resp, err)
		}
		st, err := f.store.UserAuth(ctx, uaUID)
		if err != nil || time.Since(st.TokenAt) > time.Minute {
			t.Fatalf("real clock: %+v %v", st, err)
		}
	})
}

func TestHA1Verifier(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	const challenge = `Digest realm="mdm", nonce="abc123", qop="auth", algorithm=MD5`
	in := func(digest string) service.VerifyInput {
		return service.VerifyInput{UserID: "U1", Realm: "mdm", Challenge: challenge, DigestResponse: digest}
	}
	v := service.HA1Verifier(ha1Lookup)
	good, err := simulator.DigestResponse(challenge, uaUser, uaPassword, uaURI, nil)
	if err != nil {
		t.Fatal(err)
	}
	ok, err := v.Verify(ctx, nil, in(good))
	if err != nil || !ok {
		t.Fatalf("correct with qop: %v %v", ok, err)
	}
	i := strings.Index(good, `response="`) + len(`response="`)
	ok, err = v.Verify(ctx, nil, in("Digest "+good[:i]+strings.ToUpper(good[i:])))
	if err != nil || !ok {
		t.Fatalf("scheme prefix and upper case hex: %v %v", ok, err)
	}
	// Without qop the legacy RFC 2069 formula applies.
	md5s := func(s string) string {
		sum := md5.Sum([]byte(s)) // #nosec G401 -- RFC 2617 Digest requires MD5
		return hex.EncodeToString(sum[:])
	}
	legacy := md5s(simulator.HA1(uaUser, "mdm", uaPassword) + ":abc123:" + md5s("POST:"+uaURI))
	noQop := `username="alice", realm="mdm", nonce="abc123", uri="` + uaURI + `", response="` + legacy + `"`
	ok, err = v.Verify(ctx, nil, in(noQop))
	if err != nil || !ok {
		t.Fatalf("correct without qop: %v %v", ok, err)
	}
	// Wrong password and unknown user are plain rejections.
	wrong, _ := simulator.DigestResponse(challenge, uaUser, "wrong", uaURI, nil)
	if ok, err := v.Verify(ctx, nil, in(wrong)); err != nil || ok {
		t.Fatalf("wrong password: %v %v", ok, err)
	}
	unknown, _ := simulator.DigestResponse(challenge, "mallory", uaPassword, uaURI, nil)
	if ok, err := v.Verify(ctx, nil, in(unknown)); err != nil || ok {
		t.Fatalf("unknown user: %v %v", ok, err)
	}
	// A lookup failure is reported as such.
	failing := service.HA1Verifier(func(context.Context, string, string) (string, error) { return "", errBoom })
	if ok, err := failing.Verify(ctx, nil, in(good)); ok || !errors.Is(err, errBoom) || errors.Is(err, service.ErrBadDigest) {
		t.Fatalf("lookup error: %v %v", ok, err)
	}
	// Everything malformed is ErrBadDigest.
	bad := map[string]service.VerifyInput{
		"unterminated quote":      in(`username="alice`),
		"missing equals":          in(`username`),
		"empty name":              in(`="alice"`),
		"junk after quote":        in(`username="alice"x, realm="mdm"`),
		"missing response":        in(strings.Replace(good, "response=", "resp=", 1)),
		"missing username":        in(strings.Replace(good, "username=", "user=", 1)),
		"wrong realm":             in(strings.Replace(good, `realm="mdm"`, `realm="other"`, 1)),
		"wrong nonce":             in(strings.Replace(good, `nonce="abc123"`, `nonce="zzz"`, 1)),
		"qop auth-int":            in(strings.Replace(good, "qop=auth", "qop=auth-int", 1)),
		"qop without nc":          in(strings.Replace(good, "nc=00000001", "count=1", 1)),
		"malformed challenge":     {Realm: "mdm", Challenge: `Digest realm="mdm`, DigestResponse: good},
		"challenge without nonce": {Realm: "mdm", Challenge: `Digest realm="mdm"`, DigestResponse: good},
	}
	for name, input := range bad {
		if ok, err := v.Verify(ctx, nil, input); ok || !errors.Is(err, service.ErrBadDigest) {
			t.Errorf("%s: %v %v", name, ok, err)
		}
	}
}

// TestDigestUserAuthCore wires DigestUserAuth into Core as the
// UserAuthenticate handler and runs both messages through Checkin.
func TestDigestUserAuthCore(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := &service.DigestUserAuth{Verifier: service.HA1Verifier(ha1Lookup), Rand: &seqReader{}}
	h := newHarness(t, service.Config{UserAuthenticate: d.Handle})
	d.Store, d.Clock = h.store, h.clock
	enroll(t, h, "D1")
	first := func(digest string) (*mdm.UserAuthenticateResponse, error) {
		got, err := h.core.Checkin(ctx, req(h.cert), checkinPlist(t, map[string]any{"MessageType": "UserAuthenticate", "UDID": "D1", "UserID": "U1", "DigestResponse": digest}))
		if err != nil {
			return nil, err
		}
		var resp mdm.UserAuthenticateResponse
		if err := plist.Unmarshal(got.Body, &resp); err != nil {
			t.Fatalf("decode %s: %v", got.Body, err)
		}
		return &resp, nil
	}
	resp, err := first("")
	if err != nil || resp.DigestChallenge == nil || !strings.HasPrefix(*resp.DigestChallenge, "Digest realm=") {
		t.Fatalf("challenge through Core: %+v %v", resp, err)
	}
	digest, err := simulator.DigestResponse(*resp.DigestChallenge, uaUser, uaPassword, uaURI, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err = first(digest)
	if err != nil || resp.AuthToken == nil || *resp.AuthToken == "" {
		t.Fatalf("token through Core: %+v %v", resp, err)
	}
	st, err := h.store.UserAuth(ctx, uaUID)
	if err != nil || st.AuthToken != *resp.AuthToken {
		t.Fatalf("stored token %+v %v", st, err)
	}
	// Errors keep their code through Core.
	d.Manage = func(context.Context, *mdm.Request, *checkin.UserAuthenticate) error { return service.ErrUserNotManaged }
	if _, err := first(""); service.CodeOf(err) != service.CodeGone {
		t.Fatalf("gone through Core: %v", err)
	}
}
