package dmctl_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/server/internal/dmctl"
)

// TestLabOfflineCommands checks offline lab setup, identity preservation, trust export,
// prerequisites, and credential redaction.
func TestLabOfflineCommands(t *testing.T) {
	env := noConfig(t)
	dir := t.TempDir()
	if _, _, err := run(
		t,
		env,
		"lab",
		"init",
		"-workspace",
		dir,
		"-listen",
		"127.0.0.1:0",
	); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, env, "lab", "init", "-workspace", dir); err == nil {
		t.Fatal("existing workspace replaced")
	}

	if _, _, err := run(
		t,
		env,
		"lab",
		"preflight",
		"-workspace",
		dir,
		"-identity",
		"scep",
	); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(
		t,
		env,
		"lab",
		"preflight",
		"-workspace",
		dir,
		"-identity",
		"invalid",
	); err == nil {
		t.Fatal("invalid identity passed preflight")
	}
	if _, _, err := run(t, env, "lab", "trust", "-workspace", dir); err == nil {
		t.Fatal("missing trust destination accepted")
	}
	trust := filepath.Join(dir, "trust.mobileconfig")
	if _, _, err := run(t, env, "lab", "trust", "-workspace", dir, "-file", trust); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, env, "lab", "trust", "-workspace", dir, "-file", trust); err == nil {
		t.Fatal("trust profile overwritten")
	}
	out, _, err := run(t, env, "lab", "doctor", "-workspace", dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "LivePrerequisites") {
		t.Fatal("prerequisites omitted")
	}
	// #nosec G304 -- The test controls this fixture path within its private workspace.
	key, err := os.ReadFile(filepath.Join(dir, "mdm", "storage-key"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, string(key)) {
		t.Fatal("doctor leaked a credential")
	}
	out, _, err = run(t, env, "lab", "list", "-format", "markdown")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "APP-001") {
		t.Fatal("app scenario omitted")
	}
	for _, sub := range []string{"init", "doctor", "list", "up", "run", "profile", "status", "down"} {
		if _, _, err = run(t, env, "lab", sub, "-h"); err != nil {
			t.Errorf("%s help: %v", sub, err)
		}
	}
	for _, sub := range []string{"list", "put", "send"} {
		if _, _, err = run(t, env, "apppush", sub, "-h"); err != nil {
			t.Errorf("%s help: %v", sub, err)
		}
	}
}

// TestLabDirectAttachmentRejectsUnsupportedActions checks that lab direct attachment rejects
// unsupported actions.
func TestLabDirectAttachmentRejectsUnsupportedActions(t *testing.T) {
	for _, sub := range []string{"init", "list", "doctor", "preflight", "trust", "up", "status", "down"} {
		t.Run(sub, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "workspace")
			_, _, err := run(t, noConfig(t), "lab", sub, "-workspace", dir, "-attach-url", "https://localhost:8443")
			if err == nil || !strings.Contains(err.Error(), "-attach-url supports") {
				t.Fatalf("unsupported attachment: %v", err)
			}
			if _, err := os.Stat(dir); !os.IsNotExist(err) {
				t.Fatal("invalid attachment changed the workspace")
			}
		})
	}
}

// TestLabReportRendersExistingRun checks that report rerenders HTML from a run directory
// and rejects a missing or unreadable run.
func TestLabReportRendersExistingRun(t *testing.T) {
	env := noConfig(t)
	dir := t.TempDir()
	results := `[{"ID":"E2E-001","Name":"Idle","Theme":"mdm","Stage":30,"Mode":"simulated","Adapter":"inprocess","Status":"passed","Target":{"ID":"simulator","Driver":"simulator","Kind":"simulator"},"Duration":1000000}]`
	if err := os.WriteFile(filepath.Join(dir, "results.json"), []byte(results), 0o600); err != nil {
		t.Fatal(err)
	}
	out, _, err := run(t, env, "lab", "report", "-run", dir)
	if err != nil || !strings.Contains(out, "report.html") {
		t.Fatal(out, err)
	}
	// #nosec G304 -- The test controls this fixture path within its private workspace.
	html, err := os.ReadFile(filepath.Join(dir, "report.html"))
	if err != nil || !strings.Contains(string(html), "E2E-001") {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"report"}, {"report", "-run", filepath.Join(dir, "absent")}} {
		if _, _, err := run(t, env, append([]string{"lab"}, args...)...); err == nil {
			t.Fatal(args, "invalid report accepted")
		}
	}
}

// TestLabListPrintsCatalogue checks the JSON catalogue and the module metadata it carries.
func TestLabListPrintsCatalogue(t *testing.T) {
	out, _, err := run(t, noConfig(t), "lab", "list")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"ID":"E2E-032"`, `"Theme":"enrollment"`, `"Stage":`, `"Modes":`} {
		if !strings.Contains(out, want) {
			t.Fatalf("catalogue JSON lacks %s", want)
		}
	}
	if _, _, err = run(t, noConfig(t), "lab", "list", "-format", "yaml"); !errors.Is(err, dmctl.ErrUsage) {
		t.Fatal("unknown catalogue format accepted:", err)
	}
}

// TestLabContainerWorkspaceAndTLS checks container-adapter initialization, the recorded
// device-facing hosts and HTTPS leaf reissue.
func TestLabContainerWorkspaceAndTLS(t *testing.T) {
	env := noConfig(t)
	dir := filepath.Join(t.TempDir(), "workspace")
	if _, _, err := run(t, env, "lab", "init", "-workspace", dir, "-mode", "live",
		"-listen", "127.0.0.1:18443", "-adapter", "docker", "-hosts", "mdm.lab.test, 192.168.64.1"); err != nil {
		t.Fatal(err)
	}
	// #nosec G304 -- The test controls this fixture path within its private workspace.
	document, err := os.ReadFile(filepath.Join(dir, "lab.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"Adapter": "docker"`, `"mdm.lab.test"`, `"192.168.64.1"`} {
		if !strings.Contains(string(document), want) {
			t.Fatalf("workspace document lacks %s:\n%s", want, document)
		}
	}
	out, _, err := run(t, env, "lab", "doctor", "-workspace", dir)
	if err != nil || !strings.Contains(out, "Containers") || !strings.Contains(out, "PublicURL") {
		t.Fatal(out, err)
	}
	if out, _, err = run(t, env, "lab", "tls", "-workspace", dir, "-hosts", "mdm.lab.test"); err != nil ||
		!strings.Contains(out, "tls.pem") {
		t.Fatal(out, err)
	}
	if _, _, err = run(t, env, "lab", "tls", "-workspace", dir); !errors.Is(err, dmctl.ErrUsage) {
		t.Fatal("tls without hosts accepted:", err)
	}
	// A simulated workspace cannot serve container fixtures.
	if _, _, err = run(t, env, "lab", "init", "-workspace", filepath.Join(t.TempDir(), "sim"),
		"-adapter", "docker"); err == nil {
		t.Fatal("simulated container workspace accepted")
	}
}
