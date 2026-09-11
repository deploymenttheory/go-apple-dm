package app

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/mdmprotocol/enroll"
	"github.com/deploymenttheory/go-apple-dm/pki/acme"
)

func hardeningCSR(t *testing.T, cn string) *x509.CertificateRequest {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.CreateCertificateRequest(
		rand.Reader,
		&x509.CertificateRequest{Subject: pkix.Name{CommonName: cn}},
		key,
	)
	if err != nil {
		t.Fatal(err)
	}
	csr, err := x509.ParseCertificateRequest(der)
	if err != nil {
		t.Fatal(err)
	}
	return csr
}

func TestOutboundPrivateTrust(t *testing.T) {
	srv := httptest.NewTLSServer(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }),
	)
	defer srv.Close()
	file := filepath.Join(t.TempDir(), "root.pem")
	if err := os.WriteFile(
		file,
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	c, err := outboundClient(nil, file)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseIdleConnections()
	r, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL, http.NoBody)
	resp, err := c.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 204 {
		t.Fatal(resp.StatusCode)
	}
	if _, err := outboundClient(c, file); err == nil {
		t.Fatal("ambiguous trust accepted")
	}
	if _, err := outboundClient(nil, file+"missing"); err == nil {
		t.Fatal("missing roots accepted")
	}
	if same, err := outboundClient(c, ""); err != nil || same != c {
		t.Fatal("explicit client not preserved")
	}
	if err := os.WriteFile(file, []byte("not PEM"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := outboundClient(nil, file); err == nil {
		t.Fatal("invalid roots accepted")
	}
}

func TestKnownMacProfileContextAndAdmission(t *testing.T) {
	a, _ := replacementSecurityApp(t)
	e := a.enroll
	b := acme.Binding{Serial: "serial", CommonName: "device"}
	for _, hardware := range []enroll.MacHardware{enroll.MacAppleSilicon, enroll.MacT2, enroll.MacIntel} {
		for _, version := range []string{"13.1", "14"} {
			for _, allow := range []bool{false, true} {
				e.acme.cfg.AllowUnattested = allow
				p, err := e.profileForDevice(
					t.Context(),
					b,
					IdentityACME,
					"Mac16,1",
					version,
					hardware,
				)
				attest := version == "14" && hardware == enroll.MacAppleSilicon
				if !allow && !attest {
					if err == nil {
						t.Fatal("unusable initial profile returned")
					}
					continue
				}
				if err != nil {
					t.Fatal(err)
				}
				if p.ACME.Attest != attest || p.MacHardware != hardware ||
					p.Target.Version.Major == 0 {
					t.Fatal("context lost", p)
				}
			}
		}
	}
	if _, err := e.profileForDevice(
		t.Context(),
		b,
		IdentityACME,
		"Mac16,1",
		"bad",
		enroll.MacHardwareUnknown,
	); err == nil {
		t.Fatal("invalid OS version")
	}
	boom := errors.New("inventory unavailable")
	e.acme.cfg.MacHardware = func(context.Context, acme.Binding) (enroll.MacHardware, error) { return "", boom }
	if _, err := e.profileForDevice(
		t.Context(),
		b,
		IdentityACME,
		"Mac16,1",
		"14",
		enroll.MacHardwareUnknown,
	); !errors.Is(
		err,
		boom,
	) {
		t.Fatal("inventory error ignored", err)
	}
}
