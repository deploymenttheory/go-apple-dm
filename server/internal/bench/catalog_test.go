package bench

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCatalogueAndReports(t *testing.T) {
	seen := map[string]bool{}
	for _, s := range Catalogue() {
		if seen[s.ID] || s.ID == "" {
			t.Fatal("duplicate or empty ID")
		}
		seen[s.ID] = true
		if s.Regression != "" && (s.Run == nil || len(s.Modes) == 0) {
			t.Fatalf("unmigrated scenario %s", s.ID)
		}
	}
	for _, id := range []string{"E2E-015", "E2E-022"} {
		if seen[id] {
			t.Fatalf("reserved ID %s advertised", id)
		}
	}
	if _, err := Select("typo"); err == nil {
		t.Fatal("unknown selector accepted")
	}
	s := Scenario{ID: "missing", Modes: []string{"live"}}
	r := Run(
		context.Background(),
		&Environment{Instance: Instance{Mode: "simulated"}},
		s,
		"process",
		"revision",
		"",
	)
	if r.Status != "unsupported" {
		t.Fatal("unsupported counted as pass")
	}
	dir := t.TempDir()
	if err := WriteReports(dir, []Result{r}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "junit.xml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "<skipped") {
		t.Fatal("JUnit lost non-pass")
	}
}

func TestInitPreservesIdentities(t *testing.T) {
	dir := t.TempDir()
	if err := Init(dir, "simulated", "sqlite", "all", "127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(dir, "mdm", "ca.key"))
	if err != nil {
		t.Fatal(err)
	}
	if err := Init(dir, "live", "sqlite", "all", "127.0.0.1:0"); err == nil {
		t.Fatal("existing workspace replaced")
	}
	after, err := os.ReadFile(filepath.Join(dir, "mdm", "ca.key"))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("identity changed")
	}
	w, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(w.Doctor())
	if strings.Contains(string(b), string(before)) {
		t.Fatal("doctor leaked key")
	}
}
