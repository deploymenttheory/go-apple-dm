package simulator_test

import (
	"context"
	"crypto/x509/pkix"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/cms"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/enroll"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/plist"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/profile"
	"github.com/deploymenttheory/go-apple-dm/v3/pki/ca"
	"github.com/deploymenttheory/go-apple-dm/v3/pki/scep"
	"github.com/deploymenttheory/go-apple-dm/v3/server/httpapi"
	"github.com/deploymenttheory/go-apple-dm/v3/server/service"
	"github.com/deploymenttheory/go-apple-dm/v3/server/storage/inmem"
	"github.com/deploymenttheory/go-apple-dm/v3/simulator"
	"github.com/deploymenttheory/go-apple-dm/v3/testpki"
)

// adeStub is a minimal ADE endpoint: verifies the MachineInfo, answers a
// software update 403 for old builds, and otherwise serves a signed
// profile; the web view lane redirects to a "sign-in" page then serves.
type adeStub struct {
	srv     *httptest.Server
	ca      *testpki.CA
	store   *inmem.Store
	profile []byte
	seen    map[string]any
	plist   bool
}

func newADEStub(t *testing.T) *adeStub {
	t.Helper()
	s := &adeStub{store: inmem.New()}
	var err error
	if s.ca, err = testpki.NewCA("ade test CA"); err != nil {
		t.Fatal(err)
	}
	signer, _ := s.ca.Issue("signer", time.Now().Add(-time.Minute))
	scepCert, scepKey, err := ca.NewSelfSigned(ca.SelfSignedOptions{Subject: pkix.Name{CommonName: "SCEP CA"}})
	if err != nil {
		t.Fatal(err)
	}
	local, _ := ca.NewLocal(scepCert, scepKey, ca.WithDepot(ca.NewMemoryDepot()))
	scepServer, _ := scep.NewServer(local, scepCert, scepKey, scep.WithChallenge(scep.StaticChallenge("secret")))
	core, _ := service.New(service.Config{Store: s.store, Pinning: service.PinOff})
	mux := http.NewServeMux()
	s.srv = httptest.NewTLSServer(mux)
	t.Cleanup(s.srv.Close)
	p := enroll.Profile{Identifier: "com.example.mdm", Topic: "com.apple.mgmt.External.simulator", ServerURL: s.srv.URL + "/mdm", CheckInURL: s.srv.URL + "/mdm", SCEP: &enroll.SCEP{URL: s.srv.URL + "/scep", Challenge: "secret"}}
	built, err := p.Build()
	if err != nil {
		t.Fatal(err)
	}
	if s.profile, err = built.Sign(signer.Cert, signer.Key); err != nil {
		t.Fatal(err)
	}
	verify := func(blob []byte) (map[string]any, error) {
		content, _, err := cms.VerifyAttached(blob, cms.VerifyOptions{Roots: s.ca.Pool()})
		if err != nil {
			return nil, err
		}
		var m map[string]any
		return m, plist.Unmarshal(content, &m)
	}
	serve := func(w http.ResponseWriter, info map[string]any) {
		s.seen = info
		if v, _ := info["OS_VERSION"].(string); v == "1.0" {
			if s.plist {
				w.Header().Set("Content-Type", "application/xml")
				w.WriteHeader(http.StatusForbidden)
				body, _ := plist.Marshal(map[string]any{"code": "com.apple.softwareupdate.required", "details": map[string]any{"OSVersion": "26.1", "BuildVersion": "26B1"}})
				_, _ = w.Write(body)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"code":"com.apple.softwareupdate.required","message":"update","details":{"OSVersion":"26.1"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/x-apple-aspen-config")
		_, _ = w.Write(s.profile)
	}
	mux.HandleFunc("POST /ade", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Type") != "application/pkcs7-signature" {
			http.Error(w, "content type", http.StatusUnsupportedMediaType)
			return
		}
		blob, _ := io.ReadAll(r.Body)
		info, err := verify(blob)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		serve(w, info)
	})
	mux.HandleFunc("GET /webview", func(w http.ResponseWriter, r *http.Request) {
		blob, err := base64.StdEncoding.DecodeString(r.Header.Get("x-apple-aspen-deviceinfo"))
		if err != nil {
			http.Error(w, "header", http.StatusBadRequest)
			return
		}
		if _, err := verify(blob); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		// The "sign-in" page: a redirect the web view follows.
		http.Redirect(w, r, "/signin", http.StatusFound)
	})
	mux.HandleFunc("GET /signin", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/x-apple-aspen-config")
		_, _ = w.Write(s.profile)
	})
	mux.HandleFunc("GET /plain", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not a profile"))
	})
	mux.Handle("/scep", scepServer.Handler())
	mux.Handle("/mdm", httpapi.Handler(httpapi.Config{Checkin: core, Connect: core}))
	return s
}

func (s *adeStub) device(t *testing.T, udid string) *simulator.Device {
	t.Helper()
	id, err := s.ca.Issue(udid, time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	d := simulator.New(udid, simulator.WithClient(s.srv.Client()), simulator.WithIdentity(&simulator.Identity{Cert: id.Cert, Key: id.Key}))
	d.SerialNumber = "SER" + udid
	return d
}

func TestADEEnroll(t *testing.T) {
	ctx := context.Background()
	t.Run("Profile", func(t *testing.T) {
		s := newADEStub(t)
		d := s.device(t, "UDID-ADE-1")
		if err := d.ADEEnroll(ctx, s.srv.URL+"/ade", simulator.ADEOptions{CanRequestSoftwareUpdate: true}); err != nil {
			t.Fatalf("ADE enroll: %v", err)
		}
		if s.seen["UDID"] != "UDID-ADE-1" || s.seen["SERIAL"] != "SERUDID-ADE-1" || s.seen["PRODUCT"] != d.ProductName || s.seen["MDM_CAN_REQUEST_SOFTWARE_UPDATE"] != true || s.seen["LANGUAGE"] != "en" {
			t.Fatalf("machine info = %v", s.seen)
		}
		if got, err := d.Connect(ctx); err != nil || len(got) != 0 {
			t.Fatalf("connect after ADE: %v %v", got, err)
		}
	})
	t.Run("SoftwareUpdateRequired", func(t *testing.T) {
		s := newADEStub(t)
		d := s.device(t, "UDID-ADE-2")
		d.OSVersion = "1.0"
		var sur *simulator.SoftwareUpdateRequired
		if err := d.ADEEnroll(ctx, s.srv.URL+"/ade", simulator.ADEOptions{}); !errors.As(err, &sur) || sur.OSVersion != "26.1" || sur.Message != "update" {
			t.Fatalf("json 403 = %v", err)
		}
		s.plist = true
		if err := d.ADEEnroll(ctx, s.srv.URL+"/ade", simulator.ADEOptions{}); !errors.As(err, &sur) || sur.BuildVersion != "26B1" {
			t.Fatalf("plist 403 = %v", err)
		}
	})
	t.Run("WebView", func(t *testing.T) {
		s := newADEStub(t)
		d := s.device(t, "UDID-ADE-3")
		hops := 0
		err := d.ADEEnroll(ctx, s.srv.URL+"/webview", simulator.ADEOptions{WebView: func(ctx context.Context, first *http.Response) (*http.Response, error) {
			// The web view follows the sign-in redirect chain.
			resp := first
			for resp.StatusCode == http.StatusFound {
				hops++
				loc := resp.Header.Get("Location")
				resp.Body.Close()
				req, _ := http.NewRequestWithContext(ctx, http.MethodGet, s.srv.URL+loc, nil)
				var err error
				if resp, err = s.srv.Client().Do(req); err != nil {
					return nil, err
				}
			}
			return resp, nil
		}})
		// The HTTP client already followed the sign-in redirect, so the
		// callback saw the final profile response.
		if err != nil || hops != 0 {
			t.Fatalf("web view lane: %v (hops %d)", err, hops)
		}
	})
	t.Run("Errors", func(t *testing.T) {
		s := newADEStub(t)
		d := s.device(t, "UDID-ADE-4")
		var herr *simulator.HTTPError
		if err := d.ADEEnroll(ctx, s.srv.URL+"/nowhere", simulator.ADEOptions{}); !errors.As(err, &herr) {
			t.Fatalf("404 = %v", err)
		}
		if err := d.ADEEnroll(ctx, s.srv.URL+"/plain", simulator.ADEOptions{WebView: func(_ context.Context, first *http.Response) (*http.Response, error) { return first, nil }}); !errors.Is(err, simulator.ErrADE) {
			t.Fatalf("wrong content type = %v", err)
		}
		if err := d.ADEEnroll(ctx, s.srv.URL+"/webview", simulator.ADEOptions{WebView: func(context.Context, *http.Response) (*http.Response, error) { return nil, errors.New("closed") }}); !errors.Is(err, simulator.ErrADE) {
			t.Fatalf("web view error = %v", err)
		}
		bare := simulator.New("UDID-ADE-5", simulator.WithClient(s.srv.Client()))
		if err := bare.ADEEnroll(ctx, s.srv.URL+"/ade", simulator.ADEOptions{}); !errors.Is(err, simulator.ErrADE) {
			t.Fatalf("no identity = %v", err)
		}
		stranger, _ := testpki.NewCA("other")
		other, _ := stranger.Issue("x", time.Now().Add(-time.Minute))
		if err := d.ADEEnroll(ctx, s.srv.URL+"/ade", simulator.ADEOptions{Signer: &simulator.Identity{Cert: other.Cert, Key: other.Key}}); !errors.As(err, &herr) || herr.Status != http.StatusBadRequest {
			t.Fatalf("unknown signer = %v", err)
		}
		if _, err := d.SignedMachineInfo(simulator.ADEOptions{Language: "fr", SoftwareUpdateDeviceID: "X1"}); err != nil {
			t.Fatal(err)
		}
		if err := d.ADEEnroll(ctx, "::bad", simulator.ADEOptions{}); !errors.Is(err, simulator.ErrADE) {
			t.Fatalf("bad url = %v", err)
		}
		_ = profile.ParseOptions{}
	})
}
