//go:build e2e

package e2e

import (
	"context"
	"crypto/x509"
	json "encoding/json/v2"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/dep"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/dep/deptest"
	"github.com/deploymenttheory/go-apple-dm/server/adminauth"
	"github.com/deploymenttheory/go-apple-dm/server/internal/app"
)

// depRegistrationCLI registers the reference server with the fake device
// enrollment service the way an operator registers it with Apple Business
// Manager: dmctl stages the keypair, the portal encrypts a server token to its
// certificate, and dmctl imports the .p7m, which the server validates with the
// service before synchronizing devices.
func depRegistrationCLI(t *testing.T) {
	t.Helper()
	fake := deptest.NewServer(deptest.Options{})
	t.Cleanup(fake.Close)
	h := newAdminHarnessWith(t, func(c *app.Config) {
		c.DEP = app.DEPConfig{BaseURL: fake.URL(), HTTPClient: fake.Client()}
	})
	root := h.mint(t, adminauth.Principal{Name: "ops", Root: true})
	if _, err := h.manager.PutPolicy(context.Background(), adminauth.Root, adminauth.Policy{
		Name:   "ops",
		Source: `permit (principal == MDM::Principal::"ops", action, resource);`,
	}); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	ctl := func(args ...string) string {
		t.Helper()
		out, err := h.ctl(t, root, "", args...)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}

	certFile := filepath.Join(dir, "public.pem")
	ctl("dep", "accounts", "keypair", "abm-lab", "-out", certFile)
	token := portalToken(t, fake, certFile)
	tokenFile := filepath.Join(dir, "token.p7m")
	if err := os.WriteFile(tokenFile, token, 0o600); err != nil {
		t.Fatal(err)
	}
	var detail dep.AccountDetail
	if err := json.Unmarshal([]byte(ctl("-output", "json", "dep", "accounts", "import-token", "abm-lab", "-file", tokenFile)), &detail); err != nil || detail.ServerUUID == "" {
		t.Fatalf("import detail = %+v, %v", detail, err)
	}

	var accounts struct {
		Items []struct {
			Name, ServerUUID string
			HasTokens        bool
		}
	}
	if err := json.Unmarshal([]byte(ctl("-output", "json", "dep", "accounts", "list")), &accounts); err != nil {
		t.Fatal(err)
	}
	if len(accounts.Items) != 1 || accounts.Items[0].Name != "abm-lab" || accounts.Items[0].ServerUUID != detail.ServerUUID || !accounts.Items[0].HasTokens {
		t.Fatalf("accounts = %+v, want abm-lab bound to %s", accounts.Items, detail.ServerUUID)
	}

	fake.AddDevices(dep.Device{SerialNumber: "C02DEPCLI01", Model: "MacBook Pro", DeviceFamily: "Mac", OS: "OSX"})
	var synced struct{ Sync dep.SyncResult }
	if err := json.Unmarshal([]byte(ctl("-output", "json", "dep", "accounts", "sync", "abm-lab")), &synced); err != nil || synced.Sync.Added != 1 {
		t.Fatalf("sync = %+v, %v", synced, err)
	}
	if out := ctl("dep", "accounts", "devices", "abm-lab"); !strings.Contains(out, "C02DEPCLI01") {
		t.Fatalf("devices = %q", out)
	}

	// Importing the same token again renews the same identity.
	ctl("dep", "accounts", "import-token", "abm-lab", "-file", tokenFile)
	// A newly staged keypair cannot decrypt a token issued for the earlier
	// certificate, which is why keypair must not be rerun before import.
	ctl("dep", "accounts", "keypair", "abm-lab", "-out", filepath.Join(dir, "replacement.pem"))
	if _, err := h.ctl(t, root, "", "dep", "accounts", "import-token", "abm-lab", "-file", tokenFile); err == nil {
		t.Fatal("a token for a replaced staged keypair was imported")
	}
}

// portalToken plays the portal: it encrypts the fake's server token to the
// certificate dmctl wrote.
func portalToken(t *testing.T, fake *deptest.Server, certFile string) []byte {
	t.Helper()
	// #nosec G304 -- The test owns the certificate path dmctl wrote.
	raw, err := os.ReadFile(certFile)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		t.Fatal("keypair output is not PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	p7m, err := fake.TokenP7M(cert)
	if err != nil {
		t.Fatal(err)
	}
	return p7m
}
