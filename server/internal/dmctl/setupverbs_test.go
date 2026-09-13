package dmctl_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/server/internal/app"
)

func TestSetupBootstrapResumesAndExportsOnlyPublicArtifacts(t *testing.T) {
	dir := t.TempDir()
	env := noConfig(t)
	call := func(args ...string) string {
		t.Helper()
		out, stderr, err := run(t, env, append([]string{"setup"}, args...)...)
		if err != nil {
			t.Fatalf("%v: %v; %s", args, err, stderr)
		}
		return out
	}
	call("init", "-dir", dir, "-role", "combined")
	setup := filepath.Join(dir, "setup.json")
	keyBefore, err := os.ReadFile(filepath.Join(dir, "secrets", "storage"))
	if err != nil {
		t.Fatal(err)
	}
	call("init", "-dir", dir, "-role", "combined")
	keyAfter, err := os.ReadFile(filepath.Join(dir, "secrets", "storage"))
	if err != nil || !bytes.Equal(keyBefore, keyAfter) {
		t.Fatal("bootstrap replaced storage key", err)
	}
	call("https", "lab", "-setup-file", setup, "-cn", "local lab", "-hosts", "localhost,127.0.0.1")
	call("https", "activate", "-setup-file", setup, "-revision", "1")
	call("issuer", "create", "-setup-file", setup, "-cn", "enrollment CA")
	call("issuer", "activate", "-setup-file", setup, "-revision", "1")
	call("issuer", "renew", "-setup-file", setup, "-force")
	call("issuer", "renew", "-setup-file", setup, "-force")
	call("vendor", "request", "-setup-file", setup, "-cn", "MDM Vendor")
	call("vendor", "request", "-setup-file", setup, "-cn", "MDM Vendor")
	csr := filepath.Join(dir, "vendor.certSigningRequest")
	call(
		"workflow",
		"export",
		"-setup-file",
		setup,
		"-id",
		"vendor",
		"-artifact",
		"csr",
		"-out",
		csr,
	)
	if _, _, err = run(
		t,
		env,
		"setup",
		"workflow",
		"export",
		"-setup-file",
		setup,
		"-id",
		"vendor",
		"-artifact",
		"key",
		"-out",
		filepath.Join(dir, "key.pem"),
	); err == nil {
		t.Fatal("exported private key")
	}
	status := call("status", "-setup-file", setup)
	var result app.SetupStatus
	if err = json.Unmarshal([]byte(status), &result); err != nil {
		t.Fatal(err)
	}
	for _, item := range result.Identities {
		if item.ID == "issuer" &&
			(len(item.Revisions) != 2 || item.Active != "1" || item.Pending != "2") {
			t.Fatal("explicit early renewal did not resume its pending revision")
		}
		if item.ID == "vendor" && len(item.Revisions) != 1 {
			t.Fatal("request did not resume")
		}
	}
	if strings.Contains(status, "PRIVATE KEY") {
		t.Fatal("status leaked private key")
	}
	for _, p := range []string{setup, csr, filepath.Join(dir, "secrets", "storage"), filepath.Join(dir, "secrets", "admin")} {
		info, err := os.Stat(p)
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatal("unprotected setup artifact", p, err)
		}
	}
}
