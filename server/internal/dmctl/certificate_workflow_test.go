package dmctl_test

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/pushcert"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/testpki"
)

func privateFixture(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func certificatePEM(der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func TestCLICompletesVendorSigningAndMDMImport(t *testing.T) {
	t.Parallel()
	env := noConfig(t)
	dir := t.TempDir()
	now := time.Now()
	root, err := testpki.NewCA("vendor root")
	if err != nil {
		t.Fatal(err)
	}
	intermediate, err := testpki.NewCA("vendor intermediate")
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.CreateCertificate(
		rand.Reader,
		intermediate.Cert,
		root.Cert,
		intermediate.Key.Public(),
		root.Key,
	)
	if err != nil {
		t.Fatal(err)
	}
	intermediate.Cert, err = x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	vendorKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	leaf := &x509.Certificate{
		SerialNumber: big.NewInt(5),
		Subject:      pkix.Name{CommonName: "vendor signer"},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	der, err = x509.CreateCertificate(
		rand.Reader,
		leaf,
		intermediate.Cert,
		vendorKey.Public(),
		intermediate.Key,
	)
	if err != nil {
		t.Fatal(err)
	}
	chain := append(
		append(certificatePEM(der), certificatePEM(intermediate.Cert.Raw)...),
		certificatePEM(root.Cert.Raw)...)
	_, csr, err := pushcert.GenerateCSR(pkix.Name{CommonName: "customer"})
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"-csr":   privateFixture(t, dir, "customer.csr", csr),
		"-chain": privateFixture(t, dir, "vendor.pem", chain),
		"-key": privateFixture(
			t,
			dir,
			"vendor.key",
			pem.EncodeToMemory(
				&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(vendorKey)},
			),
		),
		"-roots": privateFixture(t, dir, "root.pem", certificatePEM(root.Cert.Raw)),
		"-out":   filepath.Join(dir, "portal.request"),
	}
	sign := func(overrides map[string]string) error {
		t.Helper()
		args := []string{"pushcerts", "sign"}
		for _, flag := range []string{"-csr", "-chain", "-key", "-roots", "-out"} {
			v := files[flag]
			if x, ok := overrides[flag]; ok {
				v = x
			}
			args = append(args, flag, v)
		}
		_, _, err := run(t, env, args...)
		return err
	}
	if err := sign(nil); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(files["-out"])
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := base64.StdEncoding.DecodeString(string(data))
	if err != nil || !strings.Contains(string(envelope), "PushCertSignature") {
		t.Fatalf("portal envelope: %v", err)
	}
	if err := sign(nil); err == nil {
		t.Fatal("portal request overwritten")
	}
	bad := privateFixture(t, dir, "bad", []byte("broken"))
	for _, flag := range []string{"-csr", "-chain", "-key", "-roots"} {
		for _, path := range []string{bad, filepath.Join(dir, "absent")} {
			if err := sign(map[string]string{flag: path}); err == nil {
				t.Fatalf("bad signing input accepted: %s", flag)
			}
		}
	}
	mdm, err := root.IssuePush("com.apple.mgmt.External.customer", now.Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	_, key, err := mdm.PEM()
	if err != nil {
		t.Fatal(err)
	}
	certPath := privateFixture(t, dir, "mdm.der", mdm.Cert.Raw)
	keyPath := privateFixture(t, dir, "mdm.key", key)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		pair, err := pushcert.Parse([]byte(body["CertPEM"]), []byte(body["KeyPEM"]))
		if err != nil || pair.Topic != "com.apple.mgmt.External.customer" ||
			body["Topic"] != "com.apple.mgmt.External.customer" {
			t.Errorf("import: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{}`)
	}))
	defer srv.Close()
	base := []string{"-server", srv.URL, "-token", "operator", "pushcerts", "put"}
	if _, _, err := run(t, env, append(base, "-cert", certPath, "-key", keyPath)...); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"-cert", certPath}, {"-cert", certPath, "-key", keyPath, "-file", bad}, {"-cert", bad, "-key", keyPath}, {"-cert", certPath, "-key", bad}, {"-cert", certPath, "-key", keyPath, "-topic", "com.other"}, {"-cert", "absent", "-key", keyPath}, {"-cert", certPath, "-key", "absent"}} {
		if _, _, err := run(t, env, append(base, args...)...); err == nil {
			t.Fatalf("invalid import accepted: %v", args)
		}
	}
}

func TestOfflineCertificateCommandFailures(t *testing.T) {
	t.Parallel()
	env := noConfig(t)
	dir := t.TempDir()
	ca, err := testpki.NewCA("app issuer")
	if err != nil {
		t.Fatal(err)
	}
	id, err := ca.IssueApp("com.example.app", time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	cert, key, err := id.PEM()
	if err != nil {
		t.Fatal(err)
	}
	c := privateFixture(t, dir, "app.pem", cert)
	k := privateFixture(t, dir, "app.key", key)
	bad := privateFixture(t, dir, "bad", []byte("{"))
	payload := privateFixture(t, dir, "invalid-payload.json", []byte(`{"aps":{}}`))
	token := privateFixture(t, dir, "token", []byte("aabb"))
	for _, args := range [][]string{{"apns"}, {"apns", "unknown"}, {"apns", "inspect", "-bad"}, {"apns", "inspect"}, {"apns", "inspect", "-cert", "absent"}, {"apns", "inspect", "-cert", bad}, {"apns", "check", "-cert", c, "-key", "absent"}, {"apns", "send", "-cert", c, "-key", k}, {"pushcerts", "csr", "-bad"}, {"pushcerts", "csr"}, {"pushcerts", "sign", "-bad"}, {"pushcerts", "sign"}, {"bench"}, {"bench", "list"}, {"bench", "status", "-workspace", "absent"}} {
		_, _, err := run(t, env, args...)
		if args[0] == "bench" && len(args) > 1 && args[1] == "list" {
			if err != nil {
				t.Fatal(err)
			}
		} else if err == nil {
			t.Fatalf("invalid invocation accepted: %v", args)
		}
	}
	for _, environment := range []string{"development", "production"} {
		base := []string{"apns", "send", "-cert", c, "-key", k, "-environment", environment}
		for _, files := range [][2]string{{token, payload}, {"absent", payload}, {bad, payload}, {token, "absent"}} {
			out, _, err := run(
				t,
				env,
				append(base, "-token-file", files[0], "-payload-file", files[1])...)
			if err == nil {
				t.Fatal("invalid send accepted")
			}
			if strings.Contains(out, "PRIVATE KEY") {
				t.Fatal("credential leaked")
			}
		}
	}
	if _, _, err := run(
		t,
		env,
		"pushcerts",
		"csr",
		"-cn",
		"customer",
		"-key-out",
		filepath.Join(dir, "new.key"),
		"-csr-out",
		bad,
	); err == nil ||
		!strings.Contains(err.Error(), "key was saved") {
		t.Fatalf("partial CSR write omitted preservation guidance: %v", err)
	}
}

func TestAppPushCommandRejectsUnreadableFiles(t *testing.T) {
	t.Parallel()
	env := noConfig(t)
	for _, args := range [][]string{
		{"apppush", "list", "-unknown"},
		{"apppush", "put", "-cert", "absent", "-key", "absent"},
		{"apppush", "send", "-topic", "com.example.app", "-environment", "production", "-token-file", "absent", "-payload-file", "absent"},
	} {
		args = append([]string{"-server", "http://127.0.0.1:1", "-token", "operator"}, args...)
		if _, _, err := run(t, env, args...); err == nil {
			t.Fatalf("invalid command accepted: %v", args)
		}
	}
}
