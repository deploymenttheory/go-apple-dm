package lab

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/server/lab/target"
)

// fakeTarget reports a configurable description and capability set.
type fakeTarget struct {
	target.Unsupported
	info  target.Info
	caps  []string
	fails error
}

// Describe returns the configured description or failure.
func (f fakeTarget) Describe(context.Context) (target.Info, error) {
	if f.fails != nil {
		return target.Info{}, f.fails
	}
	return f.info, nil
}

// Capabilities returns the configured capability set.
func (f fakeTarget) Capabilities() []string { return f.caps }

// simulatedEnv is an environment with no server, usable by modules that never call one.
func simulatedEnv(mode string) *Environment {
	return &Environment{Instance: Instance{Mode: mode}}
}

// step returns a named step that runs fn.
func step(name string, fn func(*Session) error) Step {
	return Step{Name: name, Run: func(_ context.Context, s *Session) error { return fn(s) }}
}

// TestModuleStagesAndCatalogue checks that every catalogue module is executable, uniquely
// identified and assigned a lifecycle stage.
func TestModuleStagesAndCatalogue(t *testing.T) {
	seen := map[string]bool{}
	for _, m := range Catalogue() {
		if seen[m.ID] || m.ID == "" || m.Theme == "" {
			t.Fatalf("duplicate, unnamed or unthemed module %q", m.ID)
		}
		seen[m.ID] = true
		if len(m.Steps) == 0 || len(m.Modes) == 0 {
			t.Fatalf("module %s has no steps or modes", m.ID)
		}
		if _, ok := themeStages[m.Theme]; !ok && m.Stage == 0 {
			t.Fatalf("theme %q of %s has no lifecycle stage", m.Theme, m.ID)
		}
	}
	if !strings.Contains(Markdown(), "| E2E-032 | enrollment |") {
		t.Fatal("catalogue markdown lost a module")
	}
	if stageName(StageEnroll) != "20 Enrollment" || stageName(42) != "42" {
		t.Fatal(stageName(StageEnroll), stageName(42))
	}
}

// TestSelectResolvesIDsThemesAndLists checks selector resolution and rejection.
func TestSelectResolvesIDsThemesAndLists(t *testing.T) {
	one, err := Select("E2E-001")
	if err != nil || len(one) != 1 {
		t.Fatal(one, err)
	}
	several, err := Select("E2E-001,blueprints")
	if err != nil || len(several) != 3 {
		t.Fatalf("%d modules: %v", len(several), err)
	}
	if _, err = Select("E2E-001,typo"); err == nil {
		t.Fatal("unknown term in a list accepted")
	}
	live, err := SelectMode("live", "all")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range live {
		if !slices.Contains(m.Modes, "live") {
			t.Fatalf("%s is not a live module", m.ID)
		}
	}
	explicit, err := SelectMode("live", "E2E-001")
	if err != nil || len(explicit) != 1 {
		t.Fatal("an explicit unsupported selection was silently dropped")
	}
}

// TestRunReportsUnsupportedReasons checks each applicability rule, without executing steps.
func TestRunReportsUnsupportedReasons(t *testing.T) {
	ran := false
	base := Module{
		ID: "TEST-001", Modes: []string{"simulated"},
		Steps: []Step{step("run", func(*Session) error { ran = true; return nil })},
	}
	sim := fakeTarget{info: target.Info{ID: "sim", Kind: target.KindSimulator}}
	for name, tc := range map[string]struct {
		module Module
		tgt    target.Target
		opts   Options
		want   string
	}{
		"mode":        {Module{ID: "x", Modes: []string{"live"}, Steps: base.Steps}, sim, Options{}, "no adapter for this mode"},
		"no steps":    {Module{ID: "x", Modes: []string{"simulated"}}, sim, Options{}, "no adapter for this mode"},
		"destructive": {Module{ID: "x", Modes: []string{"simulated"}, Tags: []string{TagDestructive}, Steps: base.Steps}, sim, Options{}, "destructive module not enabled"},
		"capability":  {Module{ID: "x", Modes: []string{"simulated"}, Requires: []string{target.CapExec}, Steps: base.Steps}, sim, Options{}, "lacks capability exec"},
		"applies":     {Module{ID: "x", Modes: []string{"simulated"}, Steps: base.Steps, Applies: func(target.Info) string { return "macOS 26 or later" }}, sim, Options{}, "macOS 26 or later"},
	} {
		t.Run(name, func(t *testing.T) {
			ran = false
			r := Run(t.Context(), simulatedEnv("simulated"), tc.tgt, tc.module, tc.opts)
			if r.Status != StatusUnsupported || !strings.Contains(r.Detail, tc.want) {
				t.Fatalf("status %s detail %q", r.Status, r.Detail)
			}
			if ran {
				t.Fatal("an inapplicable module executed")
			}
		})
	}
	enabled := base
	enabled.Tags = []string{TagDestructive}
	if r := Run(t.Context(), simulatedEnv("simulated"), sim, enabled, Options{Destructive: true}); r.Status != StatusPassed {
		t.Fatal(r.Status, r.Detail)
	}
	if !ran {
		t.Fatal("enabled destructive module did not run")
	}
	capable := base
	capable.Requires = []string{target.CapExec}
	tgt := fakeTarget{info: target.Info{ID: "device"}, caps: []string{target.CapExec}}
	if r := Run(t.Context(), simulatedEnv("simulated"), tgt, capable, Options{}); r.Status != StatusPassed {
		t.Fatal(r.Status, r.Detail)
	}
}

// TestRunRecordsStepsAndEvidence checks step results, the first failure's detail, skipped
// steps and the evidence listing.
func TestRunRecordsStepsAndEvidence(t *testing.T) {
	dir := t.TempDir()
	third := false
	m := Module{
		ID: "TEST-002", Modes: []string{"simulated"},
		Steps: []Step{
			step("first", func(s *Session) error {
				if err := os.MkdirAll(s.Evidence, 0o700); err != nil {
					return err
				}
				return os.WriteFile(filepath.Join(s.Evidence, "capture.json"), []byte("{}"), 0o600)
			}),
			step("second", func(*Session) error { return errors.New("device refused") }),
			step("third", func(*Session) error { third = true; return nil }),
		},
	}
	r := Run(t.Context(), simulatedEnv("simulated"), fakeTarget{info: target.Info{ID: "sim"}}, m, Options{Evidence: dir})
	if r.Status != StatusFailed || r.Detail != "device refused" || third {
		t.Fatalf("%+v third=%v", r, third)
	}
	if len(r.Steps) != 3 || r.Steps[0].Status != StatusPassed ||
		r.Steps[1].Status != StatusFailed || r.Steps[2].Status != StatusBlocked {
		t.Fatalf("steps: %+v", r.Steps)
	}
	if len(r.Evidence) != 1 || r.Evidence[0] != "evidence/TEST-002/capture.json" {
		t.Fatalf("evidence: %v", r.Evidence)
	}
	blocked := Module{
		ID: "TEST-003", Modes: []string{"simulated"},
		Steps: []Step{step("prereq", func(*Session) error { return ErrBlocked })},
	}
	if r = Run(t.Context(), simulatedEnv("simulated"), fakeTarget{}, blocked, Options{}); r.Status != StatusBlocked {
		t.Fatal(r.Status, r.Detail)
	}
}

// TestRunReportsUnavailableTarget checks that a target that cannot describe itself blocks
// its module instead of failing it.
func TestRunReportsUnavailableTarget(t *testing.T) {
	m := Module{ID: "TEST-004", Modes: []string{"simulated"}, Steps: []Step{step("run", func(*Session) error { return nil })}}
	r := Run(t.Context(), simulatedEnv("simulated"), fakeTarget{fails: errors.New("vm not running")}, m, Options{})
	if r.Status != StatusBlocked || !strings.Contains(r.Detail, "vm not running") {
		t.Fatal(r.Status, r.Detail)
	}
}

// TestRunAllOrdersStagesAndHonoursGates checks lifecycle ordering, gate blocking and the
// observer callback.
func TestRunAllOrdersStagesAndHonoursGates(t *testing.T) {
	executed := []string{}
	run := func(name string, err error) []Step {
		return []Step{step(name, func(*Session) error { executed = append(executed, name); return err })}
	}
	modules := []Module{
		{ID: "LATE", Stage: StageInventory, Modes: []string{"simulated"}, Steps: run("late", nil)},
		{ID: "GATE", Stage: StageReadiness, Modes: []string{"simulated"}, Gate: true, Steps: run("gate", ErrBlocked)},
		{ID: "EARLY", Stage: StageEnroll, Modes: []string{"simulated"}, Steps: run("early", nil)},
	}
	var observed []string
	results := RunAll(t.Context(), simulatedEnv("simulated"), fakeTarget{info: target.Info{ID: "sim"}}, modules,
		Options{}, func(r Result) { observed = append(observed, r.ID) })
	if strings.Join(observed, ",") != "EARLY,GATE,LATE" {
		t.Fatalf("stage order: %v", observed)
	}
	if strings.Join(executed, ",") != "early,gate" {
		t.Fatalf("a module ran after a failed gate: %v", executed)
	}
	if results[2].Status != StatusBlocked || !strings.Contains(results[2].Detail, "blocked by gate GATE") {
		t.Fatalf("gate did not block: %+v", results[2])
	}
	// An unsupported gate does not block the modules after it.
	modules[1].Modes = []string{"live"}
	executed = nil
	results = RunAll(t.Context(), simulatedEnv("simulated"), fakeTarget{}, modules, Options{}, nil)
	if results[1].Status != StatusUnsupported || results[2].Status != StatusPassed {
		t.Fatalf("unsupported gate blocked later modules: %+v", results)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if got := RunAll(ctx, simulatedEnv("simulated"), fakeTarget{}, modules, Options{}, nil); len(got) != 0 {
		t.Fatalf("cancelled run executed %d modules", len(got))
	}
}

// TestRunAppliesUserChannelAndTimeout checks that the target's user identifier reaches the
// environment and that a module cannot exceed its timeout.
func TestRunAppliesUserChannelAndTimeout(t *testing.T) {
	e := simulatedEnv("simulated")
	tgt := fakeTarget{info: target.Info{ID: "device", UDID: "UDID", UserID: "GENERATED-UID"}}
	seen := ""
	m := Module{
		ID: "TEST-005", Modes: []string{"simulated"},
		Steps: []Step{step("run", func(s *Session) error { seen = s.Info.UDID; return nil })},
	}
	if r := Run(t.Context(), e, tgt, m, Options{}); r.Status != StatusPassed || seen != "UDID" {
		t.Fatal(r.Status, seen)
	}
	if e.InstallingUserID != "GENERATED-UID" {
		t.Fatalf("installing user = %q", e.InstallingUserID)
	}
	slow := Module{
		ID: "TEST-006", Modes: []string{"simulated"},
		Steps: []Step{step("wait", func(*Session) error { time.Sleep(time.Second); return context.Canceled })},
	}
	r := Run(t.Context(), e, tgt, slow, Options{Timeout: 10 * time.Millisecond})
	if r.Status != StatusFailed {
		t.Fatalf("timed-out module: %+v", r)
	}
}
