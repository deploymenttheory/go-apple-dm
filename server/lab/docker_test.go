package lab

import (
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// dockerWorkspace initializes a live workspace that uses the container adapter.
func dockerWorkspace(t *testing.T, hosts []string) *Workspace {
	t.Helper()
	dir := t.TempDir()
	if err := Init(dir, "live", "sqlite", "127.0.0.1:18443", AdapterDocker, hosts); err != nil {
		t.Fatal(err)
	}
	w, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

// TestContainerAdapterRequiresLiveWorkspace checks adapter validation at init.
func TestContainerAdapterRequiresLiveWorkspace(t *testing.T) {
	if err := Init(t.TempDir(), "simulated", "sqlite", "127.0.0.1:0", AdapterDocker, nil); err == nil {
		t.Fatal("simulated fixtures cannot serve a container")
	}
	if err := Init(t.TempDir(), "live", "sqlite", "127.0.0.1:0", "podman", nil); err == nil {
		t.Fatal("unknown adapter accepted")
	}
	w := dockerWorkspace(t, nil)
	if w.Adapter != AdapterDocker {
		t.Fatal(w.Adapter)
	}
	plain := t.TempDir()
	if err := Init(plain, "simulated", "inmem", "127.0.0.1:0", "", nil); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(plain)
	if err != nil || loaded.Adapter != AdapterProcess {
		t.Fatalf("default adapter: %+v %v", loaded, err)
	}
}

// TestContainerEnvironmentUsesContainerPaths checks that workspace paths, the listener and
// the device-facing URL are translated for the container.
func TestContainerEnvironmentUsesContainerPaths(t *testing.T) {
	w := dockerWorkspace(t, []string{"mdm.lab.test", "192.168.64.1"})
	env := w.containerEnv(map[string]string{
		"DM_LISTEN":        "127.0.0.1:18443",
		"DM_TLS_CERT_FILE": w.path("mdm", "tls.pem"),
		"DM_DSN":           w.path("mdm", "mdm.sqlite"),
		"DM_PUBLIC_URL":    "https://mdm.lab.test:18443",
		"DM_OUTSIDE":       "/etc/elsewhere/policy.json",
	})
	if env["DM_LISTEN"] != containerListen {
		t.Fatalf("listener = %q", env["DM_LISTEN"])
	}
	if env["DM_TLS_CERT_FILE"] != "/data/tls.pem" || env["DM_DSN"] != "/data/mdm.sqlite" {
		t.Fatalf("paths = %q %q", env["DM_TLS_CERT_FILE"], env["DM_DSN"])
	}
	if env["DM_PUBLIC_URL"] != "https://mdm.lab.test:18443" || env["DM_OUTSIDE"] != "/etc/elsewhere/policy.json" {
		t.Fatalf("unrelated values changed: %v", env)
	}
	if err := w.writeEnvFile(env); err != nil {
		t.Fatal(err)
	}
	// #nosec G304 -- test-controlled workspace path
	b, err := os.ReadFile(w.path("mdm", EnvFile))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if !slices.IsSorted(lines) || !slices.Contains(lines, "DM_DSN=/data/mdm.sqlite") {
		t.Fatalf("env file: %q", lines)
	}
	info, err := os.Stat(w.path("mdm", EnvFile))
	if err != nil {
		t.Fatal(err)
	}
	// Windows does not apply POSIX permissions, so the private mode is checked where it
	// is enforced.
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("env file mode: %v", info.Mode())
	}
}

// TestPublicBaseAndPorts checks the device-facing origin and published ports.
func TestPublicBaseAndPorts(t *testing.T) {
	w := dockerWorkspace(t, []string{"mdm.lab.test"})
	if base := w.PublicBase(); base != "https://mdm.lab.test:18443" {
		t.Fatal(base)
	}
	if w.port() != "18443" || w.fixturesPort() != "9443" || w.bind() != "127.0.0.1" {
		t.Fatal(w.port(), w.fixturesPort(), w.bind())
	}
	w.Bind, w.Settings = "192.168.64.1", map[string]string{"LAB_FIXTURES_PORT": "9999"}
	if w.bind() != "192.168.64.1" || w.fixturesPort() != "9999" {
		t.Fatal(w.bind(), w.fixturesPort())
	}
	hostless := dockerWorkspace(t, nil)
	if hostless.PublicBase() != "" {
		t.Fatal("a workspace without hosts has no device-facing origin")
	}
	hostless.Listen = "broken"
	if hostless.port() != "8443" || hostless.PublicBase() != "" {
		t.Fatal(hostless.port(), hostless.PublicBase())
	}
}

// TestReissueTLSCoversHostsAndKeepsCA checks that new names are added to the leaf, the CA
// is unchanged and the workspace records the hosts.
func TestReissueTLSCoversHostsAndKeepsCA(t *testing.T) {
	w := dockerWorkspace(t, nil)
	before := readCert(t, w.path("mdm", "ca.pem"))
	if err := ReissueTLS(w, []string{"mdm.lab.test", "192.168.64.1"}); err != nil {
		t.Fatal(err)
	}
	leaf := readCert(t, w.path("mdm", "tls.pem"))
	if !slices.Contains(leaf.DNSNames, "mdm.lab.test") || !slices.Contains(leaf.DNSNames, "localhost") {
		t.Fatalf("names = %v", leaf.DNSNames)
	}
	var ips []string
	for _, ip := range leaf.IPAddresses {
		ips = append(ips, ip.String())
	}
	if !slices.Contains(ips, "192.168.64.1") || !slices.Contains(ips, "127.0.0.1") {
		t.Fatalf("addresses = %v", ips)
	}
	if err := leaf.CheckSignatureFrom(before); err != nil {
		t.Fatal("the reissued leaf is not signed by the retained CA:", err)
	}
	reloaded, err := Load(w.Directory)
	if err != nil || len(reloaded.Hosts) != 2 {
		t.Fatalf("hosts were not recorded: %+v %v", reloaded, err)
	}
	if err := os.Remove(w.path("mdm", "ca.key")); err != nil {
		t.Fatal(err)
	}
	if err := ReissueTLS(w, []string{"other.lab.test"}); err == nil {
		t.Fatal("reissue without the CA key succeeded")
	}
	if err := os.WriteFile(w.path("mdm", "ca.key"), []byte("not pem"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ReissueTLS(w, []string{"other.lab.test"}); err == nil {
		t.Fatal("reissue with a malformed CA key succeeded")
	}
}

// readCert loads a PEM certificate from the workspace.
func readCert(t *testing.T, path string) *x509.Certificate {
	t.Helper()
	b, err := os.ReadFile(path) // #nosec G304 -- test-controlled workspace path
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(b)
	if block == nil {
		t.Fatalf("%s is not PEM", path)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}

// TestDoctorReportsContainerPrerequisites checks the container section and the notes that
// explain why a live workspace is not yet serving enrollment.
func TestDoctorReportsContainerPrerequisites(t *testing.T) {
	w := dockerWorkspace(t, nil)
	report := w.Doctor()
	containers, ok := report["Containers"].(map[string]any)
	if !ok {
		t.Fatalf("report = %v", report)
	}
	if containers["Port"] != "18443" || containers["Bind"] != "127.0.0.1" {
		t.Fatalf("containers = %v", containers)
	}
	notes, _ := report["Notes"].([]string)
	if len(notes) != 2 || !strings.Contains(notes[0], "push.pem") || !strings.Contains(notes[1], "lab tls") {
		t.Fatalf("notes = %v", notes)
	}
	w.Settings = map[string]string{"LAB_REPO_ROOT": t.TempDir()}
	blocked, _ := w.containerPrerequisites()["Blocked"].([]string)
	if !slices.ContainsFunc(blocked, func(s string) bool { return strings.Contains(s, ComposeFile) }) {
		t.Fatalf("a missing compose file is not reported: %v", blocked)
	}
	process := dockerWorkspace(t, []string{"mdm.lab.test"})
	process.Adapter, process.Mode = AdapterProcess, "simulated"
	if _, ok := process.Doctor()["Containers"]; ok {
		t.Fatal("the process adapter reported container prerequisites")
	}
}

// TestRepoRootPrefersConfiguredCheckout checks compose-file resolution.
func TestRepoRootPrefersConfiguredCheckout(t *testing.T) {
	w := dockerWorkspace(t, nil)
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "deploy", "lab"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(ComposeFile)), []byte("name: test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	w.Settings = map[string]string{"LAB_REPO_ROOT": root}
	if got := w.repoRoot(); got != root {
		t.Fatalf("root = %s", got)
	}
	w.Settings = nil
	// Without a configured root the workspace walks up, then falls back to the working
	// directory of the test, which is inside this checkout.
	if _, err := os.Stat(filepath.Join(w.repoRoot(), filepath.FromSlash(ComposeFile))); err != nil {
		t.Fatalf("compose file was not located from %s: %v", w.repoRoot(), err)
	}
}
