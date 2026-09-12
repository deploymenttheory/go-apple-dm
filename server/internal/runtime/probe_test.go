package runtime

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/secrets"
	"github.com/deploymenttheory/go-apple-dm/server/internal/app"
)

func probeFixture(
	t *testing.T,
	names []string,
	ips []net.IP,
	expired bool,
) (tls.Certificate, string) {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	cert := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(time.Hour),
		DNSNames:     names,
		IPAddresses:  ips,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	if expired {
		cert.NotAfter = now.Add(-time.Minute)
	}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, pub, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	pair, err := tls.X509KeyPair(
		certPEM,
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}),
	)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "server.crt")
	if err := os.WriteFile(path, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	return pair, path
}

func TestAutomaticProbeTLS(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		dns     []string
		ips     []net.IP
		expired bool
		fail    bool
	}{
		{name: "DNS only", dns: []string{"mdm.example.test"}},
		{name: "wildcard", dns: []string{"*.example.test"}},
		{name: "loopback IP", ips: []net.IP{net.ParseIP("127.0.0.1")}},
		{name: "other IP", ips: []net.IP{net.ParseIP("192.0.2.1")}},
		{name: "expired", dns: []string{"mdm.example.test"}, expired: true, fail: true},
		{name: "no SAN", fail: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pair, path := probeFixture(t, tc.dns, tc.ips, tc.expired)
			srv := httptest.NewUnstartedServer(
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path != "/healthz" {
						t.Error(r.URL.Path)
					}
					w.WriteHeader(200)
				}),
			)
			srv.TLS = &tls.Config{
				Certificates: []tls.Certificate{pair},
				MinVersion:   tls.VersionTLS12,
			}
			srv.StartTLS()
			defer srv.Close()
			_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
			err := Probe(
				t.Context(),
				ProbeConfig{
					URL:         "auto",
					Listen:      ":" + port,
					TLSCertFile: path,
					TLSKeyFile:  "unused-by-probe",
				},
			)
			if (err != nil) != tc.fail {
				t.Fatal(err)
			}
		})
	}
}

func TestProbePrivateCAAndMismatch(t *testing.T) {
	pair, path := probeFixture(t, nil, []net.IP{net.ParseIP("127.0.0.1")}, false)
	srv := httptest.NewUnstartedServer(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }),
	)
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12}
	srv.StartTLS()
	defer srv.Close()
	if err := Probe(t.Context(), ProbeConfig{URL: srv.URL, CAFile: path}); err != nil {
		t.Fatal(err)
	}
	if err := Probe(t.Context(), ProbeConfig{URL: srv.URL}); err == nil {
		t.Fatal("trusted private certificate without a root")
	}
	_, other := probeFixture(t, []string{"other.example.test"}, nil, false)
	if err := Probe(
		t.Context(),
		ProbeConfig{
			URL:         "auto",
			Listen:      srv.Listener.Addr().String(),
			TLSCertFile: other,
			TLSKeyFile:  "key",
		},
	); err == nil {
		t.Fatal("accepted wrong pin")
	}
	_, config, err := automaticProbe(
		ProbeConfig{Listen: srv.Listener.Addr().String(), TLSCertFile: path, TLSKeyFile: "key"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := config.VerifyConnection(tls.ConnectionState{}); err == nil {
		t.Fatal("accepted absent peer")
	}
	wrong, _ := probeCertificate(other)
	if err := config.VerifyConnection(
		tls.ConnectionState{PeerCertificates: []*x509.Certificate{wrong}},
	); err == nil {
		t.Fatal("accepted alternate peer")
	}
}

func TestAutomaticProbeAddresses(t *testing.T) {
	for _, tc := range []struct{ listen, want string }{{":1234", "127.0.0.1:1234"}, {"0.0.0.0:4321", "127.0.0.1:4321"}, {"[::]:1234", "[::1]:1234"}, {"[::1]:1234", "[::1]:1234"}} {
		url, _, err := automaticProbe(ProbeConfig{Listen: tc.listen})
		if err != nil || url != "http://"+tc.want+"/healthz" {
			t.Fatal(url, err)
		}
	}
	listener, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Skipf("IPv6 unavailable: %v", err)
	}
	srv := httptest.NewUnstartedServer(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }),
	)
	srv.Listener.Close()
	srv.Listener = listener
	srv.Start()
	defer srv.Close()
	if err := Probe(
		t.Context(),
		ProbeConfig{URL: "auto", Listen: listener.Addr().String()},
	); err != nil {
		t.Fatal(err)
	}
}

func TestProbeFailurePaths(t *testing.T) {
	t.Parallel()
	for _, cfg := range []ProbeConfig{
		{URL: "auto", Listen: "invalid"},
		{URL: "auto", Listen: "127.0.0.1:0"},
		{URL: "auto", Listen: "127.0.0.1:"},
		{URL: "auto", Listen: ":8080", TLSCertFile: "missing"},
		{URL: "auto", Listen: ":8080", TLSCertFile: "missing", TLSKeyFile: "key"},
		{URL: "http://127.0.0.1", CAFile: "missing"},
		{URL: "://invalid"},
	} {
		if err := Probe(t.Context(), cfg); !errors.Is(err, ErrProbe) {
			t.Fatal(cfg, err)
		}
	}
	for _, data := range []string{"invalid", string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("bad DER")})), string(pem.EncodeToMemory(&pem.Block{Type: "OTHER", Bytes: []byte("other")}))} {
		path := filepath.Join(t.TempDir(), "bad.pem")
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := probeCertificate(path); err == nil {
			t.Fatal("accepted malformed certificate")
		}
		if err := Probe(
			t.Context(),
			ProbeConfig{URL: "https://127.0.0.1", CAFile: path},
		); err == nil {
			t.Fatal("accepted malformed CA")
		}
	}
	for _, status := range []int{http.StatusServiceUnavailable, http.StatusTemporaryRedirect} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Location", "http://127.0.0.1:1")
			w.WriteHeader(status)
		}))
		err := Probe(t.Context(), ProbeConfig{URL: srv.URL})
		srv.Close()
		if err == nil || !strings.Contains(err.Error(), http.StatusText(status)) {
			t.Fatal(err)
		}
	}
	srv := httptest.NewServer(
		http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() }),
	)
	defer srv.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()
	if err := Probe(ctx, ProbeConfig{URL: srv.URL}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}

func TestProbeReflectsDatabaseFailure(t *testing.T) {
	a, err := app.Build(
		t.Context(),
		app.Config{
			Role:        app.RoleAll,
			Storage:     "sqlite",
			DSN:         filepath.Join(t.TempDir(), "health.db"),
			StorageKeys: []string{"test"},
			Secrets:     secrets.Static{"test": []byte("0123456789abcdef0123456789abcdef")},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	srv := httptest.NewServer(a.Handler)
	defer srv.Close()
	cfg := ProbeConfig{URL: "auto", Listen: srv.Listener.Addr().String()}
	if err := Probe(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	if err := Probe(t.Context(), cfg); err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatal(err)
	}
}
