package ade_test

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/enroll/ade"
	"github.com/deploymenttheory/go-apple-dm/enroll/adetest"
	"github.com/deploymenttheory/go-apple-dm/schema/support"
)

func parse(t *testing.T, req *http.Request, o ade.ParseOptions) (*ade.Parsed, error) {
	t.Helper()
	var (
		p   *ade.Parsed
		err error
	)
	http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { p, err = ade.ParseMachineInfo(r, o) }).ServeHTTP(httptest.NewRecorder(), req)
	return p, err
}

func getWithHeader(t *testing.T, value string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "https://mdm.example.com/enroll", http.NoBody)
	req.Header.Set(ade.HeaderName, value)
	return req
}

func TestParseMachineInfo(t *testing.T) {
	t.Parallel()
	chain := adetest.NewChain(t)
	opts := chain.Options()
	info := adetest.Info("C02PARSE")
	blob := adetest.Sign(t, chain, info, adetest.SignOptions{SignedAttributes: true})

	t.Run("Header", func(t *testing.T) {
		t.Parallel()
		for _, attrs := range []bool{true, false} {
			b := adetest.Sign(t, chain, info, adetest.SignOptions{SignedAttributes: attrs})
			p, err := parse(t, adetest.Request(t, "https://mdm.example.com/enroll", b, adetest.LaneHeader), opts)
			if err != nil {
				t.Fatalf("attrs=%v: %v", attrs, err)
			}
			if p.Origin != ade.OriginHeader || p.SERIAL != "C02PARSE" || p.PRODUCT != "iPhone15,2" || !p.Verified || !p.Signer.Equal(chain.Leaf.Cert) || !bytes.Equal(p.Raw, b) || p.Platform != ade.PlatformIPhone {
				t.Fatalf("%+v", p)
			}
			if *p.LANGUAGE != "en" || !*p.MDMCANREQUESTSOFTWAREUPDATE || p.Target() != (support.Target{OS: support.IOS, Version: support.V(17, 5, 1)}) {
				t.Fatalf("%+v", p.MachineInfo)
			}
		}
		// Whitespace and missing padding are tolerated.
		padded := adetest.Header(blob)
		loose := strings.TrimRight(padded, "=")
		loose = loose[:10] + "\r\n " + loose[10:]
		if _, err := parse(t, getWithHeader(t, loose), opts); err != nil {
			t.Fatalf("loose: %v", err)
		}
	})
	t.Run("HeaderURLBase64", func(t *testing.T) {
		t.Parallel()
		// Find a blob whose base64 differs between alphabets.
		for i := range 20 {
			b := adetest.Sign(t, chain, adetest.Info("C02URL"+string(rune('A'+i))), adetest.SignOptions{SignedAttributes: true})
			enc := base64.URLEncoding.EncodeToString(b)
			if !strings.ContainsAny(enc, "-_") {
				continue
			}
			for _, v := range []string{enc, base64.RawURLEncoding.EncodeToString(b)} {
				p, err := parse(t, getWithHeader(t, v), opts)
				if err != nil || p.Origin != ade.OriginHeader {
					t.Fatalf("%v", err)
				}
			}
			return
		}
		t.Skip("no blob with URL-specific characters")
	})
	t.Run("QueryParam", func(t *testing.T) {
		t.Parallel()
		p, err := parse(t, adetest.Request(t, "https://mdm.example.com/enroll", blob, adetest.LaneQuery), opts)
		if err != nil || p.Origin != ade.OriginQuery || p.SERIAL != "C02PARSE" {
			t.Fatalf("%+v %v", p, err)
		}
		// Standard alphabet in a query: '+' arrives as a space after decoding.
		raw := "https://mdm.example.com/enroll?deviceinfo=" + strings.ReplaceAll(base64.StdEncoding.EncodeToString(blob), "+", "%20")
		p, err = parse(t, httptest.NewRequest(http.MethodGet, raw, http.NoBody), opts)
		if err != nil || p.Origin != ade.OriginQuery {
			t.Fatalf("std alphabet in query: %v", err)
		}
		// The header wins when both are present.
		req := adetest.Request(t, "https://mdm.example.com/enroll", blob, adetest.LaneQuery)
		req.Header.Set(ade.HeaderName, adetest.Header(blob))
		if p, _ := parse(t, req, opts); p.Origin != ade.OriginHeader {
			t.Fatalf("precedence: %s", p.Origin)
		}
	})
	t.Run("Body", func(t *testing.T) {
		t.Parallel()
		p, err := parse(t, adetest.Request(t, "https://mdm.example.com/enroll", blob, adetest.LaneBody), opts)
		if err != nil || p.Origin != ade.OriginBody || p.SERIAL != "C02PARSE" {
			t.Fatalf("%+v %v", p, err)
		}
		put := httptest.NewRequest(http.MethodPut, "https://mdm.example.com/enroll", bytes.NewReader(blob))
		if p, err := parse(t, put, opts); err != nil || p.Origin != ade.OriginBody {
			t.Fatalf("put: %v", err)
		}
		// A GET without header or query never reads a body.
		get := httptest.NewRequest(http.MethodGet, "https://mdm.example.com/enroll", bytes.NewReader(blob))
		if _, err := parse(t, get, opts); !errors.Is(err, ade.ErrNoMachineInfo) {
			t.Fatalf("get with body: %v", err)
		}
		empty := httptest.NewRequest(http.MethodPost, "https://mdm.example.com/enroll", http.NoBody)
		if _, err := parse(t, empty, opts); !errors.Is(err, ade.ErrNoMachineInfo) {
			t.Fatalf("empty post: %v", err)
		}
		text := httptest.NewRequest(http.MethodPost, "https://mdm.example.com/enroll", strings.NewReader("<plist/>"))
		if _, err := parse(t, text, opts); !errors.Is(err, ade.ErrMalformed) {
			t.Fatalf("plain body: %v", err)
		}
		broken := httptest.NewRequest(http.MethodPost, "https://mdm.example.com/enroll", failingReader{})
		if _, err := parse(t, broken, opts); !errors.Is(err, ade.ErrMalformed) {
			t.Fatalf("read failure: %v", err)
		}
	})
	t.Run("TooLarge", func(t *testing.T) {
		t.Parallel()
		small := ade.ParseOptions{Anchors: chain.Anchors(), MaxBytes: 256}
		for _, lane := range []adetest.Lane{adetest.LaneHeader, adetest.LaneQuery, adetest.LaneBody} {
			if _, err := parse(t, adetest.Request(t, "https://mdm.example.com/enroll", blob, lane), small); !errors.Is(err, ade.ErrTooLarge) {
				t.Fatalf("lane %d: %v", lane, err)
			}
		}
		// A header whose encoded length is fine but decodes over the limit.
		exact := ade.ParseOptions{Anchors: chain.Anchors(), MaxBytes: int64(len(blob)) - 1}
		if _, err := parse(t, getWithHeader(t, adetest.Header(blob)), exact); !errors.Is(err, ade.ErrTooLarge) {
			t.Fatalf("decoded: %v", err)
		}
		// The plist inside is also bounded.
		big := adetest.Sign(t, chain, info, adetest.SignOptions{Content: []byte("<plist><dict><key>PRODUCT</key><string>" + strings.Repeat("x", 3000) + "</string></dict></plist>")})
		if _, err := parse(t, adetest.Request(t, "https://mdm.example.com/enroll", big, adetest.LaneBody), ade.ParseOptions{Anchors: chain.Anchors(), MaxBytes: 2900}); !errors.Is(err, ade.ErrTooLarge) {
			t.Fatalf("body over: %v", err)
		}
	})
	t.Run("Malformed", func(t *testing.T) {
		t.Parallel()
		if _, err := parse(t, getWithHeader(t, "!!!not base64!!!"), opts); !errors.Is(err, ade.ErrMalformed) {
			t.Fatalf("base64: %v", err)
		}
		if _, err := parse(t, getWithHeader(t, base64.StdEncoding.EncodeToString([]byte("hello"))), opts); !errors.Is(err, ade.ErrMalformed) {
			t.Fatalf("not DER: %v", err)
		}
		if _, err := parse(t, getWithHeader(t, base64.StdEncoding.EncodeToString([]byte{0x30, 0x03, 0x02, 0x01, 0x01})), opts); !errors.Is(err, ade.ErrMalformed) {
			t.Fatalf("DER but not CMS: %v", err)
		}
		notPlist := adetest.Sign(t, chain, info, adetest.SignOptions{Content: []byte("not a plist")})
		if _, err := parse(t, getWithHeader(t, adetest.Header(notPlist)), opts); !errors.Is(err, ade.ErrMalformed) {
			t.Fatalf("plist: %v", err)
		}
		if _, err := parse(t, httptest.NewRequest(http.MethodGet, "https://mdm.example.com/enroll", http.NoBody), opts); !errors.Is(err, ade.ErrNoMachineInfo) {
			t.Fatalf("nothing: %v", err)
		}
	})
	t.Run("PresenceRules", func(t *testing.T) {
		t.Parallel()
		p, err := parse(t, adetest.Request(t, "https://mdm.example.com/enroll", blob, adetest.LaneHeader), opts)
		if err != nil {
			t.Fatal(err)
		}
		if err := p.Validate(false); err != nil {
			t.Fatalf("device enrollment: %v", err)
		}
		// Under user enrollment UDID and SERIAL are forbidden.
		err = p.Validate(true)
		if !errors.Is(err, ade.ErrPresence) || !strings.Contains(err.Error(), "SERIAL") || !strings.Contains(err.Error(), "UDID") {
			t.Fatalf("user enrollment: %v", err)
		}
		// The account-driven body: LANGUAGE, PRODUCT, VERSION only.
		ue := ade.MachineInfo{PRODUCT: "iPhone17,2", VERSION: "23A300", LANGUAGE: new("en-US")}
		if err := ade.Validate(&ue, true); err != nil {
			t.Fatalf("user enrollment body: %v", err)
		}
		if err := ade.Validate(&ue, false); !errors.Is(err, ade.ErrPresence) || !strings.Contains(err.Error(), "missing [SERIAL UDID]") {
			t.Fatalf("device enrollment needs identifiers: %v", err)
		}
		ue.IMEI, ue.MEID = new("i"), new("m")
		if err := ade.Validate(&ue, true); err == nil || !strings.Contains(err.Error(), "forbidden [IMEI MEID]") {
			t.Fatalf("imei/meid forbidden: %v", err)
		}
		// PRODUCT and VERSION are always required.
		if err := ade.Validate(&ade.MachineInfo{}, true); err == nil || !strings.Contains(err.Error(), "missing [PRODUCT VERSION]") {
			t.Fatalf("empty: %v", err)
		}
		// OS_VERSION is required once a key introduced with it is present.
		old := ade.MachineInfo{UDID: "u", SERIAL: "s", PRODUCT: "iPhone9,1", VERSION: "20H115"}
		if err := ade.Validate(&old, false); err != nil {
			t.Fatalf("iOS 16 device: %v", err)
		}
		for _, set := range []func(m *ade.MachineInfo){
			func(m *ade.MachineInfo) { m.MDMCANREQUESTSOFTWAREUPDATE = new(false) },
			func(m *ade.MachineInfo) { m.SOFTWAREUPDATEDEVICEID = new("x") },
			func(m *ade.MachineInfo) { m.SUPPLEMENTALBUILDVERSION = new("x") },
			func(m *ade.MachineInfo) { m.SUPPLEMENTALOSVERSIONEXTRA = new("x") },
			func(m *ade.MachineInfo) { m.MDMCANREQUESTPSSOCONFIG = new(true) },
			func(m *ade.MachineInfo) { m.MANDATORYSOFTWAREUPDATEREQUIRED = new(true) },
		} {
			m := old
			set(&m)
			if err := ade.Validate(&m, false); err == nil || !strings.Contains(err.Error(), "OS_VERSION") {
				t.Fatalf("OS_VERSION required: %v", err)
			}
			m.OSVERSION = "17.0"
			if err := ade.Validate(&m, false); err != nil {
				t.Fatalf("with OS_VERSION: %v", err)
			}
		}
	})
	t.Run("PairingTokenBytes", func(t *testing.T) {
		t.Parallel()
		token := []byte{0x00, 0x01, 0xfe, 0xff, 'p', 'a', 'i', 'r'}
		w := adetest.Info("C02WATCH")
		w.PRODUCT, w.PAIRINGTOKEN, w.SOFTWAREUPDATEDEVICEID = "Watch7,1", token, nil
		b := adetest.Sign(t, chain, w, adetest.SignOptions{SignedAttributes: true})
		p, err := parse(t, adetest.Request(t, "https://mdm.example.com/enroll", b, adetest.LaneBody), opts)
		if err != nil || !bytes.Equal(p.PAIRINGTOKEN, token) || p.Platform != ade.PlatformWatch {
			t.Fatalf("%+v %v", p, err)
		}
		if p.Target().OS != support.WatchOS {
			t.Fatalf("target %+v", p.Target())
		}
	})
	t.Run("Audit", func(t *testing.T) {
		t.Parallel()
		stranger := adetest.NewChain(t)
		foreign := adetest.Sign(t, stranger, info, adetest.SignOptions{SignedAttributes: true})
		var log bytes.Buffer
		audit := ade.ParseOptions{Anchors: chain.Anchors(), Audit: true, Logger: slog.New(slog.NewTextHandler(&log, nil))}
		p, err := parse(t, getWithHeader(t, adetest.Header(foreign)), audit)
		if err != nil || p.Verified || p.Signer != nil || p.SERIAL != "C02PARSE" {
			t.Fatalf("%+v %v", p, err)
		}
		if !strings.Contains(log.String(), "audit mode") {
			t.Fatalf("log: %s", log.String())
		}
		// Audit does not excuse a malformed blob.
		if _, err := parse(t, getWithHeader(t, base64.StdEncoding.EncodeToString([]byte{0x30, 0x03, 0x02, 0x01, 0x01})), audit); !errors.Is(err, ade.ErrMalformed) {
			t.Fatalf("malformed under audit: %v", err)
		}
		// Audit without a logger falls back to the default logger.
		if p, err := parse(t, getWithHeader(t, adetest.Header(foreign)), ade.ParseOptions{Anchors: chain.Anchors(), Audit: true}); err != nil || p.Verified {
			t.Fatalf("default logger: %v", err)
		}
		// A verified blob under audit is still reported verified.
		if p, err := parse(t, getWithHeader(t, adetest.Header(blob)), audit); err != nil || !p.Verified {
			t.Fatalf("verified under audit: %v", err)
		}
	})
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestVerify(t *testing.T) {
	t.Parallel()
	chain := adetest.NewChain(t)
	info := adetest.Info("C02VERIFY")
	blob := adetest.Sign(t, chain, info, adetest.SignOptions{SignedAttributes: true})

	t.Run("ChainIgnoresValidity", func(t *testing.T) {
		t.Parallel()
		// The intermediate expired in 2014 and is SHA-1 signed: accepted by default.
		for _, anchors := range [][]*x509.Certificate{chain.Anchors(), {chain.Root.Cert}, {chain.Intermediate.Cert}} {
			if _, signer, err := ade.Verify(blob, ade.ParseOptions{Anchors: anchors}); err != nil || !signer.Equal(chain.Leaf.Cert) {
				t.Fatalf("%v", err)
			}
		}
		// Enforcing validity rejects the same chain.
		if _, _, err := ade.Verify(blob, ade.ParseOptions{Anchors: chain.Anchors(), EnforceValidity: true, Now: time.Now}); !errors.Is(err, ade.ErrUnknownSigner) {
			t.Fatalf("enforced: %v", err)
		}
		// Enforcing validity at a time inside every window would pass a well-formed chain; here the leaf is not valid in 2010.
		if _, _, err := ade.Verify(blob, ade.ParseOptions{Anchors: chain.Anchors(), EnforceValidity: true, Now: func() time.Time { return time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC) }}); !errors.Is(err, ade.ErrUnknownSigner) {
			t.Fatalf("enforced 2010: %v", err)
		}
	})
	t.Run("UnknownRootRejected", func(t *testing.T) {
		t.Parallel()
		stranger := adetest.NewChain(t)
		if _, _, err := ade.Verify(blob, stranger.Options()); !errors.Is(err, ade.ErrUnknownSigner) {
			t.Fatalf("%v", err)
		}
		// The default anchors are Apple's; a test chain never reaches them.
		if _, _, err := ade.Verify(blob, ade.ParseOptions{}); !errors.Is(err, ade.ErrUnknownSigner) {
			t.Fatalf("apple anchors: %v", err)
		}
		// A tampered blob is unverified, not unknown.
		bad := append([]byte(nil), blob...)
		bad[bytes.Index(bad, []byte("C02VERIFY"))] = 'X'
		if _, _, err := ade.Verify(bad, chain.Options()); !errors.Is(err, ade.ErrUnverified) {
			t.Fatalf("tampered: %v", err)
		}
		if _, _, err := ade.Verify([]byte("junk"), chain.Options()); !errors.Is(err, ade.ErrMalformed) {
			t.Fatalf("junk: %v", err)
		}
	})
	t.Run("AuditModePerHandler", func(t *testing.T) {
		t.Parallel()
		stranger := adetest.NewChain(t)
		foreign := adetest.Sign(t, stranger, info, adetest.SignOptions{SignedAttributes: true})
		quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
		strict := ade.New(ade.Config{Parse: chain.Options(), Profile: okHook, Logger: quiet})
		audit := ade.New(ade.Config{Parse: ade.ParseOptions{Anchors: chain.Anchors(), Audit: true}, Profile: okHook, Logger: quiet})
		// The same request: one handler refuses, the other admits and logs.
		if rec := serve(t, strict, adetest.Request(t, "https://mdm.example.com/enroll", foreign, adetest.LaneBody)); rec.Code != http.StatusUnauthorized {
			t.Fatalf("strict: %d", rec.Code)
		}
		if rec := serve(t, audit, adetest.Request(t, "https://mdm.example.com/enroll", foreign, adetest.LaneBody)); rec.Code != http.StatusOK {
			t.Fatalf("audit: %d %s", rec.Code, rec.Body.String())
		}
		// And the strict handler is unaffected by the audit one existing.
		if rec := serve(t, strict, adetest.Request(t, "https://mdm.example.com/enroll", foreign, adetest.LaneBody)); rec.Code != http.StatusUnauthorized {
			t.Fatalf("strict again: %d", rec.Code)
		}
	})
}

func TestPlatformFromProduct(t *testing.T) {
	t.Parallel()
	t.Run("Table", func(t *testing.T) {
		t.Parallel()
		cases := map[string]ade.Platform{
			"iPhone15,2": ade.PlatformIPhone, "iPhone17,1": ade.PlatformIPhone,
			"iPad13,16": ade.PlatformIPad, "iPad14,1": ade.PlatformIPad,
			"iPod9,1": ade.PlatformIPod,
			"Mac14,7": ade.PlatformMac, "MacBookPro18,1": ade.PlatformMac, "MacBookAir10,1": ade.PlatformMac, "Macmini9,1": ade.PlatformMac, "MacPro7,1": ade.PlatformMac, "iMac21,1": ade.PlatformMac, "VirtualMac2,1": ade.PlatformMac,
			"AppleTV14,1":       ade.PlatformAppleTV,
			"RealityDevice14,1": ade.PlatformRealityDevice,
			"Watch7,1":          ade.PlatformWatch,
			"":                  ade.PlatformUnknown, "Vision1,1": ade.PlatformUnknown, "xiPhone1,1": ade.PlatformUnknown, "TV1,1": ade.PlatformUnknown, "J413AP": ade.PlatformUnknown,
		}
		for product, want := range cases {
			if got := ade.PlatformFromProduct(product); got != want {
				t.Errorf("%q: %q want %q", product, got, want)
			}
		}
		os := map[ade.Platform]support.OS{
			ade.PlatformIPhone: support.IOS, ade.PlatformIPad: support.IOS, ade.PlatformIPod: support.IOS, ade.PlatformMac: support.MacOS,
			ade.PlatformAppleTV: support.TvOS, ade.PlatformRealityDevice: support.VisionOS, ade.PlatformWatch: support.WatchOS, ade.PlatformUnknown: "",
		}
		for p, want := range os {
			if p.OS() != want {
				t.Errorf("%s OS %q", p, p.OS())
			}
		}
		if ade.PlatformUnknown.String() != "Unknown" || ade.PlatformMac.String() != "Mac" {
			t.Fatal("String")
		}
	})
}

func TestAppleAnchors(t *testing.T) {
	t.Parallel()
	a := ade.AppleAnchors()
	if len(a) != 2 || a[0].Subject.CommonName != "Apple iPhone Device CA" || a[1].Subject.CommonName != "Apple Root CA" {
		t.Fatalf("%v", a)
	}
	if !a[0].IsCA || !a[1].IsCA || a[0].NotAfter.Year() != 2014 {
		t.Fatal("shape")
	}
	a[0] = nil
	if ade.AppleAnchors()[0] == nil {
		t.Fatal("shared slice")
	}
}

func TestMemStore(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := ade.NewMemStore()
	if err := s.Put(ctx, nil); !errors.Is(err, ade.ErrStore) {
		t.Fatalf("nil: %v", err)
	}
	if err := s.Put(ctx, &ade.Record{Parsed: &ade.Parsed{}}); !errors.Is(err, ade.ErrStore) {
		t.Fatalf("no serial: %v", err)
	}
	p := &ade.Parsed{MachineInfo: adetest.Info("C02STORE")}
	if err := s.Put(ctx, &ade.Record{Parsed: p, DEP: "dep"}); err != nil {
		t.Fatal(err)
	}
	rec, ok, err := s.Get(ctx, "C02STORE")
	if err != nil || !ok || rec.Parsed != p || rec.DEP != "dep" || s.Len() != 1 {
		t.Fatalf("%+v %v %v", rec, ok, err)
	}
	if _, ok, _ := s.Get(ctx, "nope"); ok {
		t.Fatal("found")
	}
	// Put replaces.
	if err := s.Put(ctx, &ade.Record{Parsed: p, DEP: "dep2"}); err != nil {
		t.Fatal(err)
	}
	if rec, _, _ := s.Get(ctx, "C02STORE"); rec.DEP != "dep2" || s.Len() != 1 {
		t.Fatal("replace")
	}
	var _ ade.MachineInfoStore = s
}
