package ade_test

import (
	"context"
	"crypto"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/cms"
	"github.com/deploymenttheory/go-apple-dm/enroll"
	"github.com/deploymenttheory/go-apple-dm/enroll/ade"
	"github.com/deploymenttheory/go-apple-dm/enroll/adetest"
	"github.com/deploymenttheory/go-apple-dm/internal/testpki"
	"github.com/deploymenttheory/go-apple-dm/profile"
	schemaerrors "github.com/deploymenttheory/go-apple-dm/schema/errors"
)

const target = "https://mdm.example.com/enroll/ade"

var quietLogger = slog.New(slog.NewTextHandler(io.Discard, nil))

func okHook(_ context.Context, p *ade.Parsed, id ade.Identity) (*enroll.Profile, error) {
	prof := &enroll.Profile{
		Identifier: "com.example.mdm", Topic: "com.apple.mgmt.test", ServerURL: "https://mdm.example.com/mdm?ref=" + p.SERIAL,
		SCEP: &enroll.SCEP{URL: "https://mdm.example.com/scep", Challenge: "challenge-for-" + id.Serial},
	}
	if id.Subject != "" {
		prof.AssignedManagedAppleID = id.Subject
	}
	if rec, ok := id.DEP.(depRecord); ok {
		prof.AssignedManagedAppleID = rec.ManagedAppleAccount
	}
	return prof, nil
}

type depRecord struct {
	Serial, ManagedAppleAccount string
}

func serve(t *testing.T, h http.Handler, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func parseProfile(t *testing.T, rec *httptest.ResponseRecorder) *enroll.Profile {
	t.Helper()
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != ade.ContentTypeProfile || rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("%d %v %s", rec.Code, rec.Header(), rec.Body.String())
	}
	p, err := enroll.Parse(rec.Body.Bytes(), profile.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

type webAuthSpy struct {
	bound []ade.Bound
}

func (s *webAuthSpy) Begin(w http.ResponseWriter, _ *http.Request, b ade.Bound) {
	s.bound = append(s.bound, b)
	http.Redirect(w, &http.Request{}, "https://idp.example.com/authorize?state=x", http.StatusFound)
}

type failingStore struct{ ade.MachineInfoStore }

var errStore = errors.New("db down")

func (failingStore) Put(context.Context, *ade.Record) error { return errStore }

func (failingStore) Get(context.Context, string) (*ade.Record, bool, error) {
	return nil, false, errStore
}

func TestHandler(t *testing.T) {
	t.Parallel()
	chain := adetest.NewChain(t)
	info := adetest.Info("C02HANDLER")
	blob := adetest.Sign(t, chain, info, adetest.SignOptions{SignedAttributes: true})
	post := func(t *testing.T) *http.Request {
		t.Helper()
		return adetest.Request(t, target, blob, adetest.LaneBody)
	}

	t.Run("ProfileHookPersonalises", func(t *testing.T) {
		t.Parallel()
		var seen ade.Identity
		hook := func(ctx context.Context, p *ade.Parsed, id ade.Identity) (*enroll.Profile, error) {
			seen = id
			return okHook(ctx, p, id)
		}
		h := ade.New(ade.Config{Parse: chain.Options(), Profile: hook, Logger: quietLogger})
		prof := parseProfile(t, serve(t, h, post(t)))
		if prof.ServerURL != "https://mdm.example.com/mdm?ref=C02HANDLER" || prof.SCEP.Challenge != "challenge-for-C02HANDLER" {
			t.Fatalf("%+v", prof)
		}
		if seen.Serial != "C02HANDLER" || seen.UDID != info.UDID || seen.Platform != ade.PlatformIPhone || !seen.Verified || seen.DEP != nil || seen.Subject != "" {
			t.Fatalf("identity %+v", seen)
		}
	})
	t.Run("PersistedPerSerial", func(t *testing.T) {
		t.Parallel()
		store := ade.NewMemStore()
		now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
		h := ade.New(ade.Config{Parse: chain.Options(), Store: store, Profile: okHook, Logger: quietLogger, Now: func() time.Time { return now }})
		if rec := serve(t, h, post(t)); rec.Code != http.StatusOK {
			t.Fatalf("%d", rec.Code)
		}
		other := adetest.Sign(t, chain, adetest.Info("C02OTHER"), adetest.SignOptions{})
		if rec := serve(t, h, adetest.Request(t, target, other, adetest.LaneHeader)); rec.Code != http.StatusOK {
			t.Fatalf("%d", rec.Code)
		}
		rec, ok, err := store.Get(context.Background(), "C02HANDLER")
		if err != nil || !ok || rec.Parsed.Origin != ade.OriginBody || !rec.ReceivedAt.Equal(now) || rec.DEP != nil || rec.Parsed.PRODUCT != "iPhone15,2" {
			t.Fatalf("%+v %v %v", rec, ok, err)
		}
		if rec, _, _ := store.Get(context.Background(), "C02OTHER"); rec.Parsed.Origin != ade.OriginHeader {
			t.Fatalf("%+v", rec)
		}
		if store.Len() != 2 {
			t.Fatalf("len %d", store.Len())
		}
		// A repeat replaces rather than duplicates.
		serve(t, h, post(t))
		if store.Len() != 2 {
			t.Fatalf("len after repeat %d", store.Len())
		}
		// The default store is in-memory and reachable through Resume.
		def := ade.New(ade.Config{Parse: chain.Options(), Profile: okHook, Logger: quietLogger})
		serve(t, def, post(t))
		if p, id, err := def.Resume(context.Background(), "C02HANDLER"); err != nil || p.SERIAL != "C02HANDLER" || id.Serial != "C02HANDLER" {
			t.Fatalf("%+v %+v %v", p, id, err)
		}
	})
	t.Run("JoinedToDEPRecord", func(t *testing.T) {
		t.Parallel()
		store := ade.NewMemStore()
		dep := ade.DEPLookupFunc(func(_ context.Context, serial string) (any, bool, error) {
			if serial == "C02HANDLER" {
				return depRecord{Serial: serial, ManagedAppleAccount: "user@example.com"}, true, nil
			}
			return nil, false, nil
		})
		h := ade.New(ade.Config{Parse: chain.Options(), Store: store, DEP: dep, Profile: okHook, Logger: quietLogger})
		prof := parseProfile(t, serve(t, h, post(t)))
		if prof.AssignedManagedAppleID != "user@example.com" {
			t.Fatalf("%+v", prof)
		}
		rec, _, _ := store.Get(context.Background(), "C02HANDLER")
		if r, ok := rec.DEP.(depRecord); !ok || r.ManagedAppleAccount != "user@example.com" {
			t.Fatalf("%+v", rec)
		}
		// Not in DEP: still served, not joined.
		unknown := adetest.Sign(t, chain, adetest.Info("C02NODEP"), adetest.SignOptions{SignedAttributes: true})
		if prof := parseProfile(t, serve(t, h, adetest.Request(t, target, unknown, adetest.LaneBody))); prof.AssignedManagedAppleID != "" {
			t.Fatalf("%+v", prof)
		}
		if rec, _, _ := store.Get(context.Background(), "C02NODEP"); rec.DEP != nil {
			t.Fatal("joined")
		}
		// Resume carries the join.
		if _, id, err := h.Resume(context.Background(), "C02HANDLER"); err != nil || id.DEP == nil {
			t.Fatalf("%+v %v", id, err)
		}
		// A lookup failure is a server error.
		broken := ade.New(ade.Config{Parse: chain.Options(), DEP: ade.DEPLookupFunc(func(context.Context, string) (any, bool, error) { return nil, false, errStore }), Profile: okHook, Logger: quietLogger})
		if rec := serve(t, broken, post(t)); rec.Code != http.StatusInternalServerError {
			t.Fatalf("%d", rec.Code)
		}
	})
	t.Run("UnknownSignerUnrecognizedDevice", func(t *testing.T) {
		t.Parallel()
		stranger := adetest.NewChain(t)
		foreign := adetest.Sign(t, stranger, info, adetest.SignOptions{SignedAttributes: true})
		h := ade.New(ade.Config{Parse: chain.Options(), Profile: okHook, UnrecognizedDevice: true, Logger: quietLogger})
		rec := serve(t, h, adetest.Request(t, target, foreign, adetest.LaneBody))
		if rec.Code != http.StatusForbidden || rec.Header().Get("Content-Type") != ade.ContentTypeJSON {
			t.Fatalf("%d %v", rec.Code, rec.Header())
		}
		if !strings.Contains(rec.Body.String(), schemaerrors.ErrorCodeUnrecognizedDevice) {
			t.Fatalf("%s", rec.Body.String())
		}
		req := adetest.Request(t, target, foreign, adetest.LaneBody)
		req.Header.Set("Accept", "application/xml")
		if rec := serve(t, h, req); rec.Code != http.StatusForbidden || rec.Header().Get("Content-Type") != ade.ContentTypePlist || !strings.Contains(rec.Body.String(), "<string>com.apple.unrecognized.device</string>") {
			t.Fatalf("%d %s", rec.Code, rec.Body.String())
		}
		// Without the option an unknown signer is 401.
		plain := ade.New(ade.Config{Parse: chain.Options(), Profile: okHook, Logger: quietLogger})
		if rec := serve(t, plain, adetest.Request(t, target, foreign, adetest.LaneBody)); rec.Code != http.StatusUnauthorized {
			t.Fatalf("%d", rec.Code)
		}
		// A bad signature is 401 either way.
		bad := append([]byte(nil), blob...)
		bad[strings.Index(string(bad), "C02HANDLER")] = 'X'
		if rec := serve(t, h, adetest.Request(t, target, bad, adetest.LaneBody)); rec.Code != http.StatusUnauthorized {
			t.Fatalf("tampered: %d", rec.Code)
		}
	})
	t.Run("MalformedIs400", func(t *testing.T) {
		t.Parallel()
		h := ade.New(ade.Config{Parse: chain.Options(), Profile: okHook, Logger: quietLogger})
		cases := map[string]*http.Request{
			"garbage body":   httptest.NewRequest(http.MethodPost, target, strings.NewReader("garbage")),
			"bad base64":     getWithHeader(t, "%%%"),
			"DER not CMS":    getWithHeader(t, adetest.Header([]byte{0x30, 0x03, 0x02, 0x01, 0x01})),
			"not a plist":    adetest.Request(t, target, adetest.Sign(t, chain, info, adetest.SignOptions{Content: []byte("nope")}), adetest.LaneBody),
			"presence rules": adetest.Request(t, target, adetest.Sign(t, chain, ade.MachineInfo{PRODUCT: "iPhone15,2", VERSION: "21F90"}, adetest.SignOptions{}), adetest.LaneBody),
		}
		for name, req := range cases {
			if rec := serve(t, h, req); rec.Code != http.StatusBadRequest {
				t.Fatalf("%s: %d", name, rec.Code)
			}
		}
		small := ade.New(ade.Config{Parse: ade.ParseOptions{Anchors: chain.Anchors(), MaxBytes: 100}, Profile: okHook, Logger: quietLogger})
		if rec := serve(t, small, post(t)); rec.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("too large: %d", rec.Code)
		}
		if rec := serve(t, h, httptest.NewRequest(http.MethodDelete, target, http.NoBody)); rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != "GET, POST" {
			t.Fatalf("method: %d", rec.Code)
		}
		// The default logger and store are used when none are configured.
		if rec := serve(t, ade.New(ade.Config{}), httptest.NewRequest(http.MethodDelete, target, http.NoBody)); rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("defaults: %d", rec.Code)
		}
	})
	t.Run("SignedProfileContentType", func(t *testing.T) {
		t.Parallel()
		ca, err := testpki.NewCA("Profile Signing CA")
		if err != nil {
			t.Fatal(err)
		}
		id, err := ca.Issue("mdm.example.com", time.Now().Add(-time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		h := ade.New(ade.Config{Parse: chain.Options(), Profile: okHook, Signer: ade.Signer{Cert: id.Cert, Key: id.Key}, Logger: quietLogger})
		rec := serve(t, h, post(t))
		if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/x-apple-aspen-config" || rec.Header().Get("Content-Length") != itoa(rec.Body.Len()) {
			t.Fatalf("%d %v", rec.Code, rec.Header())
		}
		if !cms.IsSigned(rec.Body.Bytes()) {
			t.Fatal("not signed")
		}
		content, signer, err := cms.VerifyAttached(rec.Body.Bytes(), cms.VerifyOptions{Roots: ca.Pool()})
		if err != nil || !signer.Equal(id.Cert) {
			t.Fatalf("%v", err)
		}
		if p, err := enroll.Parse(content, profile.ParseOptions{}); err != nil || p.Topic != "com.apple.mgmt.test" {
			t.Fatalf("%+v %v", p, err)
		}
		// The parser also accepts the signed form directly.
		if _, err := enroll.Parse(rec.Body.Bytes(), profile.ParseOptions{Verify: cms.VerifyOptions{Roots: ca.Pool()}}); err != nil {
			t.Fatal(err)
		}
		// Unsigned when no signer is configured.
		plain := ade.New(ade.Config{Parse: chain.Options(), Profile: okHook, Logger: quietLogger})
		if rec := serve(t, plain, post(t)); cms.IsSigned(rec.Body.Bytes()) {
			t.Fatal("signed without a signer")
		}
	})
	t.Run("NoProfileWithoutMachineInfo", func(t *testing.T) {
		t.Parallel()
		h := ade.New(ade.Config{Parse: chain.Options(), Profile: okHook, Logger: quietLogger})
		for _, req := range []*http.Request{
			httptest.NewRequest(http.MethodGet, target, http.NoBody),
			httptest.NewRequest(http.MethodPost, target, http.NoBody),
			httptest.NewRequest(http.MethodGet, target+"?deviceinfo=", http.NoBody),
		} {
			rec := serve(t, h, req)
			if rec.Code != http.StatusBadRequest || rec.Header().Get("Content-Type") == ade.ContentTypeProfile {
				t.Fatalf("%s: %d %v", req.Method, rec.Code, rec.Header())
			}
		}
	})
	t.Run("WebViewLane", func(t *testing.T) {
		t.Parallel()
		spy := &webAuthSpy{}
		store := ade.NewMemStore()
		h := ade.New(ade.Config{Parse: chain.Options(), Store: store, Profile: okHook, WebAuth: spy, Logger: quietLogger})
		// The web view GET with the header starts authentication instead of serving the profile.
		rec := serve(t, h, adetest.Request(t, target, blob, adetest.LaneHeader))
		if rec.Code != http.StatusFound || len(spy.bound) != 1 || spy.bound[0] != (ade.Bound{Serial: "C02HANDLER", UDID: info.UDID, Product: "iPhone15,2", OSVersion: "17.5.1"}) {
			t.Fatalf("%d %+v", rec.Code, spy.bound)
		}
		if _, ok, _ := store.Get(context.Background(), "C02HANDLER"); !ok {
			t.Fatal("not persisted before Begin")
		}
		// The POST lane is unaffected by WebAuth.
		if rec := serve(t, h, post(t)); rec.Code != http.StatusOK || len(spy.bound) != 1 {
			t.Fatalf("%d", rec.Code)
		}
		// After authentication the authenticator resumes and finishes.
		p, id, err := h.Resume(context.Background(), "C02HANDLER")
		if err != nil {
			t.Fatal(err)
		}
		id.Subject, id.Claims = "jane@example.com", map[string]any{"sub": "jane@example.com"}
		fin := httptest.NewRecorder()
		h.Finish(fin, httptest.NewRequest(http.MethodGet, target+"/callback", http.NoBody), p, id)
		if prof := parseProfile(t, fin); prof.AssignedManagedAppleID != "jane@example.com" {
			t.Fatalf("%+v", prof)
		}
		if _, _, err := h.Resume(context.Background(), "nope"); !errors.Is(err, ade.ErrNoMachineInfo) {
			t.Fatalf("unknown serial: %v", err)
		}
		// A function adapts to the interface.
		var got ade.Bound
		fn := ade.New(ade.Config{Parse: chain.Options(), Profile: okHook, Logger: quietLogger, WebAuth: ade.WebAuthFunc(func(w http.ResponseWriter, _ *http.Request, b ade.Bound) {
			got = b
			w.WriteHeader(http.StatusSeeOther)
		})})
		if rec := serve(t, fn, adetest.Request(t, target, blob, adetest.LaneHeader)); rec.Code != http.StatusSeeOther || got.Serial != "C02HANDLER" {
			t.Fatalf("%d %+v", rec.Code, got)
		}
		// Without WebAuth the GET serves the profile directly.
		direct := ade.New(ade.Config{Parse: chain.Options(), Profile: okHook, Logger: quietLogger})
		parseProfile(t, serve(t, direct, adetest.Request(t, target, blob, adetest.LaneHeader)))
	})
	t.Run("GateThroughHandler", func(t *testing.T) {
		t.Parallel()
		h := ade.New(ade.Config{Parse: chain.Options(), Profile: okHook, SoftwareUpdate: requireVersion("18.0"), Logger: quietLogger})
		rec := serve(t, h, post(t))
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "com.apple.softwareupdate.required") {
			t.Fatalf("%d %s", rec.Code, rec.Body.String())
		}
		// The gate runs on the web view lane too.
		spy := &webAuthSpy{}
		wv := ade.New(ade.Config{Parse: chain.Options(), Profile: okHook, SoftwareUpdate: requireVersion("18.0"), WebAuth: spy, Logger: quietLogger})
		if rec := serve(t, wv, adetest.Request(t, target, blob, adetest.LaneHeader)); rec.Code != http.StatusForbidden || len(spy.bound) != 0 {
			t.Fatalf("%d", rec.Code)
		}
		// Gate errors are 500.
		broken := ade.New(ade.Config{Parse: chain.Options(), Profile: okHook, SoftwareUpdate: ade.PolicyFunc(func(context.Context, *ade.Parsed) (ade.Target, bool, error) { return ade.Target{}, false, errPolicy }), Logger: quietLogger})
		if rec := serve(t, broken, post(t)); rec.Code != http.StatusInternalServerError {
			t.Fatalf("%d", rec.Code)
		}
		// A gate that cannot write its body is 500.
		invalid := ade.New(ade.Config{Parse: chain.Options(), Profile: okHook, SoftwareUpdate: ade.PolicyFunc(func(context.Context, *ade.Parsed) (ade.Target, bool, error) {
			return ade.Target{OSVersion: "18.0", RequireBetaProgram: &ade.BetaProgram{}}, true, nil
		}), Logger: quietLogger})
		if rec := serve(t, invalid, post(t)); rec.Code != http.StatusInternalServerError {
			t.Fatalf("%d", rec.Code)
		}
	})
	t.Run("HookAndStoreFailures", func(t *testing.T) {
		t.Parallel()
		rejecting := ade.New(ade.Config{Parse: chain.Options(), Logger: quietLogger, Profile: func(context.Context, *ade.Parsed, ade.Identity) (*enroll.Profile, error) {
			return nil, ade.ErrRejected
		}})
		if rec := serve(t, rejecting, post(t)); rec.Code != http.StatusForbidden {
			t.Fatalf("rejected: %d", rec.Code)
		}
		failing := ade.New(ade.Config{Parse: chain.Options(), Logger: quietLogger, Profile: func(context.Context, *ade.Parsed, ade.Identity) (*enroll.Profile, error) {
			return nil, errPolicy
		}})
		if rec := serve(t, failing, post(t)); rec.Code != http.StatusInternalServerError {
			t.Fatalf("hook error: %d", rec.Code)
		}
		unbuildable := ade.New(ade.Config{Parse: chain.Options(), Logger: quietLogger, Profile: func(context.Context, *ade.Parsed, ade.Identity) (*enroll.Profile, error) {
			return &enroll.Profile{}, nil
		}})
		if rec := serve(t, unbuildable, post(t)); rec.Code != http.StatusInternalServerError {
			t.Fatalf("build error: %d", rec.Code)
		}
		noHook := ade.New(ade.Config{Parse: chain.Options(), Logger: quietLogger})
		if rec := serve(t, noHook, post(t)); rec.Code != http.StatusNotImplemented {
			t.Fatalf("no hook: %d", rec.Code)
		}
		badStore := ade.New(ade.Config{Parse: chain.Options(), Store: failingStore{}, Profile: okHook, Logger: quietLogger})
		if rec := serve(t, badStore, post(t)); rec.Code != http.StatusInternalServerError {
			t.Fatalf("store: %d", rec.Code)
		}
		if _, _, err := badStore.Resume(context.Background(), "x"); !errors.Is(err, ade.ErrStore) {
			t.Fatalf("resume: %v", err)
		}
		// A signer that cannot sign is 500.
		ca, _ := testpki.NewCA("x")
		unsignable := ade.New(ade.Config{Parse: chain.Options(), Profile: okHook, Signer: ade.Signer{Cert: ca.Cert, Key: brokenSigner{ca.Key}}, Logger: quietLogger})
		if rec := serve(t, unsignable, post(t)); rec.Code != http.StatusInternalServerError {
			t.Fatalf("signer: %d", rec.Code)
		}
	})
}

type brokenSigner struct{ key crypto.Signer }

func (b brokenSigner) Public() crypto.PublicKey { return b.key.Public() }

func (brokenSigner) Sign(io.Reader, []byte, crypto.SignerOpts) ([]byte, error) {
	return nil, errPolicy
}

func itoa(n int) string { return strconv.Itoa(n) }
