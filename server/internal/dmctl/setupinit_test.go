package dmctl_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSetupInitReportsWhatEachSecretIsFor checks that init names the key material it
// generated, says what each piece protects, and singles out the one that cannot be
// replaced. The file names are identifiers rather than descriptions, so an operator who
// is not reading the server source depends on this output to know what to preserve.
func TestSetupInitReportsWhatEachSecretIsFor(t *testing.T) {
	dir := t.TempDir()
	out, _, err := run(t, noConfig(t), "setup", "init", "-dir", dir, "-role", "customer")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"storage", "database encryption key",
		"admin", "DM_BOOTSTRAP_TOKEN", "bootstrap administrator credential",
		"issuance", "DM_SCEP_HMAC_KEY", "SCEP enrollment challenge key",
		"UNRECOVERABLE", "secrets manager",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("init output did not mention %q\n%s", want, out)
		}
	}
	// The recoverable secrets must not be presented as unrecoverable, or the notice
	// stops distinguishing the one piece that has to leave the machine.
	if strings.Count(out, "UNRECOVERABLE") != 1 {
		t.Fatalf("expected exactly one unrecoverable secret\n%s", out)
	}
}

// TestSetupInitDescribesACustomStorageKeyName checks that the report follows the storage
// key name actually configured, because that name is both the file name and the key
// identifier recorded in every sealed value.
func TestSetupInitDescribesACustomStorageKeyName(t *testing.T) {
	dir := t.TempDir()
	out, _, err := run(t, noConfig(t),
		"setup", "init", "-dir", dir, "-role", "customer", "-storage-key-name", "vault",
		"-output", "json",
	)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		SecretsDir string `json:"secretsDir"`
		Secrets    []struct {
			Name     string `json:"name"`
			Path     string `json:"path"`
			Variable string `json:"variable"`
			Purpose  string `json:"purpose"`
			Custody  string `json:"custody"`
		} `json:"secrets"`
		NextAction string `json:"nextAction"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err, out)
	}
	if result.NextAction == "" || result.SecretsDir != filepath.Join(dir, "secrets") {
		t.Fatal("missing next action or secrets directory", out)
	}
	custody := map[string]string{}
	for _, s := range result.Secrets {
		custody[s.Name] = s.Custody
		if s.Path == "" || s.Purpose == "" {
			t.Fatal("secret reported without a path or purpose", out)
		}
		if _, err := os.Stat(s.Path); err != nil {
			t.Fatal("reported secret does not exist", s.Path, err)
		}
	}
	if custody["vault"] != "preserve" {
		t.Fatal("custom storage key was not reported as the preserved key", out)
	}
	if custody["storage"] != "" {
		t.Fatal("reported a default storage key that was never generated", out)
	}
	if custody["admin"] != "regenerable" || custody["issuance"] != "regenerable" {
		t.Fatal("referenced secrets were not reported as regenerable", out)
	}
}

// TestSetupInitReportsAnImportedACMEKey checks that an ACME key supplied at init is
// described too, rather than appearing as an unexplained fourth file.
func TestSetupInitReportsAnImportedACMEKey(t *testing.T) {
	dir, material := t.TempDir(), filepath.Join(t.TempDir(), "acme.key")
	if err := os.WriteFile(material, []byte("0123456789abcdef0123456789abcdef"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, _, err := run(t, noConfig(t),
		"setup", "init", "-dir", dir, "-role", "customer", "-acme-key-file", material,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "DM_ACME_HMAC_KEY") ||
		!strings.Contains(out, "ACME external account binding key") {
		t.Fatalf("imported ACME key was not described\n%s", out)
	}
}
