package lab

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/server/lab/target"
)

// reportFixture is one result per status across two targets and three stages.
func reportFixture() []Result {
	mac := target.Info{ID: "mac-26.6-vm", Driver: "guestweave", Kind: target.KindDevice, Platform: "macOS", OSVersion: "26.6", BuildVersion: "25G83", Model: "VirtualMac2,1", UDID: "UDID-1", Virtual: true}
	sim := target.Info{ID: "simulator", Driver: "simulator", Kind: target.KindSimulator}
	return []Result{
		{ID: "LAB-010", Name: "Provision", Theme: "provision", Stage: StageProvision, Mode: "live", Adapter: "docker", Revision: "abc123", Status: StatusPassed, Target: mac, Duration: 2 * time.Second},
		{
			ID: "LAB-040", Name: "Inventory", Theme: "inventory", Stage: StageInventory, Mode: "live", Adapter: "docker", Revision: "abc123", Status: StatusFailed, Detail: "OSVersion mismatch", Target: mac, Duration: time.Second,
			Steps:    []StepResult{{Name: "query", Status: StatusPassed}, {Name: "compare", Status: StatusFailed, Detail: "26.6 != 26.5"}},
			Evidence: []string{"evidence/LAB-040/response.plist"},
		},
		{ID: "LAB-020", Name: "Enroll", Theme: "enrollment", Stage: StageEnroll, Mode: "live", Adapter: "docker", Revision: "abc123", Status: StatusBlocked, Detail: "apns-readiness", Target: mac},
		{ID: "E2E-001", Name: "Idle", Theme: "mdm", Stage: StageReadiness, Mode: "simulated", Adapter: "inprocess", Revision: "abc123", Status: StatusUnsupported, Detail: "no adapter", Target: sim},
	}
}

// TestWriteReportsProducesEveryArtifact checks the JSON, JUnit and HTML outputs and that
// results survive a round trip.
func TestWriteReportsProducesEveryArtifact(t *testing.T) {
	dir := t.TempDir()
	results := reportFixture()
	if err := WriteReports(dir, results); err != nil {
		t.Fatal(err)
	}
	loaded, err := ReadResults(dir)
	if err != nil || len(loaded) != len(results) || loaded[1].Steps[1].Detail != "26.6 != 26.5" {
		t.Fatalf("round trip: %+v %v", loaded, err)
	}
	junit := readFile(t, filepath.Join(dir, JUnitFile))
	if !strings.Contains(junit, `failures="1"`) || !strings.Contains(junit, `skipped="2"`) {
		t.Fatalf("junit: %s", junit)
	}
	html := readFile(t, filepath.Join(dir, HTMLFile))
	for _, want := range []string{
		"Lab acceptance report", "mac-26.6-vm", "guestweave", "26.6", "VirtualMac2,1",
		"OSVersion mismatch", "evidence/LAB-040/response.plist", "26.6 != 26.5",
		`<span class="pill failed">`, "10 Provision", "20 Enrollment", "40 Inventory",
		"abc123", "simulator",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("report lacks %q", want)
		}
	}
	if strings.Contains(html, "http://") || strings.Contains(html, "https://") {
		t.Fatal("report is not self-contained")
	}
	// Each stage heading appears once per target, in ascending order.
	order := []string{"10 Provision", "20 Enrollment", "40 Inventory"}
	at := 0
	for _, stage := range order {
		i := strings.Index(html[at:], stage)
		if i < 0 || strings.Count(html, "<h3>"+stage+"</h3>") != 1 {
			t.Fatalf("stage %s is missing, repeated or out of order", stage)
		}
		at += i
	}
}

// readFile reads a report artifact.
func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path) // #nosec G304 -- test-controlled report path
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestReportHandlesEmptyAndUnwritableRuns checks an empty run and unreadable results.
func TestReportHandlesEmptyAndUnwritableRuns(t *testing.T) {
	dir := t.TempDir()
	if err := WriteReports(dir, nil); err != nil {
		t.Fatal(err)
	}
	if html := readFile(t, filepath.Join(dir, HTMLFile)); !strings.Contains(html, "<b>0</b>") {
		t.Fatal("empty run lost its summary")
	}
	if _, err := ReadResults(t.TempDir()); err == nil {
		t.Fatal("missing results accepted")
	}
	corrupt := t.TempDir()
	if err := os.WriteFile(filepath.Join(corrupt, ResultsFile), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadResults(corrupt); err == nil {
		t.Fatal("corrupt results accepted")
	}
	blocked := t.TempDir()
	if err := os.Mkdir(filepath.Join(blocked, HTMLFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := WriteHTML(blocked, reportFixture()); err == nil {
		t.Fatal("unwritable report accepted")
	}
}
