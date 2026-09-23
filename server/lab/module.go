package lab

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/deploymenttheory/go-apple-dm/server/lab/target"
)

// Lifecycle stages order modules for one target. Modules with equal stages keep
// catalogue order.
const (
	StagePreflight     = 0
	StageProvision     = 10
	StageEnroll        = 20
	StageReadiness     = 30
	StageInventory     = 40
	StageConfiguration = 50
	StageApps          = 60
	StageSecurity      = 70
	StageEvents        = 80
	StageReplacement   = 85
	StageDestructive   = 90
	StageUnenroll      = 99
)

// themeStages is the default lifecycle stage of each catalogue theme.
var themeStages = map[string]int{
	"admin":           StagePreflight,
	"enrollment":      StageEnroll,
	"acme":            StageEnroll,
	"ota":             StageEnroll,
	"attestation":     StageEnroll,
	"accountdriven":   StageEnroll,
	"ade":             StageEnroll,
	"dep":             StageEnroll,
	"abm":             StageEnroll,
	"user":            StageEnroll,
	"sharedipad":      StageEnroll,
	"mdm":             StageReadiness,
	"apns":            StageReadiness,
	"ddm":             StageConfiguration,
	"blueprints":      StageConfiguration,
	"apppush":         StageEvents,
	"replacement":     StageReplacement,
	"returntoservice": StageDestructive,
}

// Result statuses. Blocked and unsupported are distinct from failed: blocked means a
// prerequisite or an earlier gate was not met, unsupported means the module does not
// apply to the selected mode or target.
const (
	StatusPassed      = "passed"
	StatusFailed      = "failed"
	StatusBlocked     = "blocked"
	StatusUnsupported = "unsupported"
)

// TagDestructive marks modules that erase, lock or restart a device. They run only when
// Options.Destructive is set.
const TagDestructive = "destructive"

// Module is one maintained acceptance check. Implementations never depend on testing.T.
// Modes names the workspace modes whose server environment the module needs; Requires
// names target capabilities; Applies adds target-specific conditions. Regression names
// the detailed component test retained in server/e2e.
//
// Catalogue JSON uses the administration PascalCase convention.
type Module struct {
	Settings      map[string]string        `json:"Settings,omitempty"`
	ID            string                   `json:"ID"`
	Name          string                   `json:"Name"`
	Theme         string                   `json:"Theme"`
	Stage         int                      `json:"Stage"`
	Regression    string                   `json:"Regression"`
	Modes         []string                 `json:"Modes"`
	Prerequisites []string                 `json:"Prerequisites"`
	Requires      []string                 `json:"Requires,omitempty"`
	Tags          []string                 `json:"Tags,omitempty"`
	Gate          bool                     `json:"Gate,omitempty"`
	Applies       func(target.Info) string `json:"-"`
	Steps         []Step                   `json:"-"`
}

// Step is one named action of a module. A module stops at its first non-passing step.
type Step struct {
	Name string
	Run  func(context.Context, *Session) error
}

// Session is the context a step executes in. Evidence is the module's private evidence
// directory, or empty when the run keeps no evidence files.
type Session struct {
	Env      *Environment
	Target   target.Target
	Info     target.Info
	Evidence string
}

// Result distinguishes unavailable prerequisites from an assertion failure.
//
// Evidence JSON uses the administration PascalCase convention.
type Result struct {
	ID       string        `json:"ID"`
	Name     string        `json:"Name"`
	Theme    string        `json:"Theme"`
	Stage    int           `json:"Stage"`
	Mode     string        `json:"Mode"`
	Adapter  string        `json:"Adapter"`
	Revision string        `json:"Revision"`
	Status   string        `json:"Status"`
	Detail   string        `json:"Detail"`
	Target   target.Info   `json:"Target"`
	Steps    []StepResult  `json:"Steps,omitempty"`
	Evidence []string      `json:"Evidence,omitempty"`
	Started  time.Time     `json:"Started"`
	Duration time.Duration `json:"Duration"`
}

// StepResult records one executed or skipped step.
type StepResult struct {
	Name     string        `json:"Name"`
	Status   string        `json:"Status"`
	Detail   string        `json:"Detail,omitempty"`
	Duration time.Duration `json:"Duration"`
}

// Options configure a run. Timeout bounds each module; zero selects DefaultModuleTimeout.
// Evidence is the run directory; each module writes beneath Evidence/evidence/<ID>.
type Options struct {
	Adapter, Revision string
	Timeout           time.Duration
	Destructive       bool
	Evidence          string
}

// DefaultModuleTimeout bounds one module when Options.Timeout is zero.
const DefaultModuleTimeout = 2 * time.Minute

var ErrBlocked = errors.New("scenario prerequisites unavailable")

// scenario adapts a single-function catalogue scenario to one module step. The device
// argument is the target's enrollment identifier.
func scenario(fn func(context.Context, *Environment, string) error) []Step {
	return []Step{{
		Name: "scenario",
		Run:  func(ctx context.Context, r *Session) error { return fn(ctx, r.Env, r.Info.UDID) },
	}}
}

// unsupportedReason reports why m cannot run in mode against info and t, or "".
func unsupportedReason(m Module, mode string, t target.Target, info target.Info, opts Options) string {
	switch {
	case len(m.Steps) == 0 || !slices.Contains(m.Modes, mode):
		return "scenario has no adapter for this mode; retained regression: " + m.Regression
	case slices.Contains(m.Tags, TagDestructive) && !opts.Destructive:
		return "destructive module not enabled"
	}
	if missing, ok := target.Has(t, m.Requires...); !ok {
		return "target " + info.ID + " lacks capability " + missing
	}
	if m.Applies != nil {
		return m.Applies(info)
	}
	return ""
}

// Run executes one module against t and returns its status, steps, duration and
// evidence. It never returns an error: every outcome is a Result.
func Run(ctx context.Context, e *Environment, t target.Target, m Module, opts Options) Result {
	info, err := t.Describe(ctx)
	r := Result{
		ID: m.ID, Name: m.Name, Theme: m.Theme, Stage: m.Stage, Mode: e.Mode,
		Adapter: opts.Adapter, Revision: opts.Revision, Target: info,
		Started: time.Now().UTC(), Status: StatusPassed,
	}
	if err != nil {
		r.Status, r.Detail = StatusBlocked, "target unavailable: "+err.Error()
		return r
	}
	if reason := unsupportedReason(m, e.Mode, t, info, opts); reason != "" {
		r.Status, r.Detail = StatusUnsupported, reason
		return r
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultModuleTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	run := &Session{Env: e, Target: t, Info: info}
	if opts.Evidence != "" {
		run.Evidence = filepath.Join(opts.Evidence, "evidence", m.ID)
	}
	if info.UserID != "" {
		e.InstallingUserID = info.UserID
	}
	if len(m.Settings) > 0 {
		nested, closeNested, nerr := isolated(ctx, e, m.Settings)
		if nerr != nil {
			r.Status, r.Detail = StatusFailed, nerr.Error()
			r.Duration = time.Since(r.Started)
			return r
		}
		defer closeNested()
		run.Env = nested
	}
	r.Steps = runSteps(ctx, run, m.Steps)
	for _, step := range r.Steps {
		if step.Status != StatusPassed {
			r.Status, r.Detail = step.Status, step.Detail
			break
		}
	}
	r.Duration = time.Since(r.Started)
	r.Evidence = evidenceFiles(opts.Evidence, run.Evidence)
	return r
}

// Execute runs the module's steps against e and t without applicability checks or
// isolated settings, returning the first step error. Run is the reporting entry point.
func (m Module) Execute(ctx context.Context, e *Environment, t target.Target) error {
	info, err := t.Describe(ctx)
	if err != nil {
		return wrapError(err)
	}
	session := &Session{Env: e, Target: t, Info: info}
	for _, step := range m.Steps {
		if err := step.Run(ctx, session); err != nil {
			return err
		}
	}
	return nil
}

// runSteps executes steps in order, recording skipped steps after the first non-pass.
func runSteps(ctx context.Context, run *Session, steps []Step) []StepResult {
	out := make([]StepResult, 0, len(steps))
	stopped := ""
	for _, step := range steps {
		if stopped != "" {
			out = append(out, StepResult{Name: step.Name, Status: StatusBlocked, Detail: "not run after " + stopped})
			continue
		}
		start := time.Now()
		err := step.Run(ctx, run)
		res := StepResult{Name: step.Name, Status: StatusPassed, Duration: time.Since(start)}
		if err != nil {
			res.Status, res.Detail = StatusFailed, err.Error()
			if errors.Is(err, ErrBlocked) {
				res.Status = StatusBlocked
			}
			stopped = step.Name
		}
		out = append(out, res)
	}
	return out
}

// isolated starts a temporary simulated workspace with the module's settings, using the
// same server binary as e.
func isolated(ctx context.Context, e *Environment, settings map[string]string) (*Environment, func(), error) {
	dir, err := os.MkdirTemp("", "dm-lab-module-")
	if err != nil {
		return nil, nil, wrapError(err)
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	if err = Init(dir, "simulated", "sqlite", "127.0.0.1:0", AdapterProcess, nil); err != nil {
		cleanup()
		return nil, nil, err
	}
	w, err := Load(dir)
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	w.Settings = settings
	nested, err := Start(ctx, w, e.Binary, io.Discard)
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	return nested, func() { nested.Close(); cleanup() }, nil
}

// evidenceFiles lists regular files under dir relative to root, in lexical order.
func evidenceFiles(root, dir string) []string {
	if dir == "" {
		return nil
	}
	var out []string
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil //nolint:nilerr // A missing or unreadable evidence tree lists nothing.
		}
		if rel, rerr := filepath.Rel(root, path); rerr == nil {
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	return out
}

// RunAll runs modules against one target in lifecycle order. A gate module that does not
// pass blocks every later module. observe, when non-nil, receives each result as it
// completes. Run cancellation stops before the next module.
func RunAll(
	ctx context.Context,
	e *Environment,
	t target.Target,
	modules []Module,
	opts Options,
	observe func(Result),
) []Result {
	ordered := slices.Clone(modules)
	slices.SortStableFunc(ordered, func(a, b Module) int { return a.Stage - b.Stage })
	results := make([]Result, 0, len(ordered))
	gate := ""
	for _, m := range ordered {
		if ctx.Err() != nil {
			break
		}
		var r Result
		if gate != "" {
			info, _ := t.Describe(ctx)
			r = Result{
				ID: m.ID, Name: m.Name, Theme: m.Theme, Stage: m.Stage, Mode: e.Mode,
				Adapter: opts.Adapter, Revision: opts.Revision, Target: info,
				Started: time.Now().UTC(), Status: StatusBlocked, Detail: gate,
			}
		} else {
			r = Run(ctx, e, t, m, opts)
			if m.Gate && r.Status != StatusPassed && r.Status != StatusUnsupported {
				gate = fmt.Sprintf("blocked by gate %s (%s)", m.ID, r.Status)
			}
		}
		results = append(results, r)
		if observe != nil {
			observe(r)
		}
	}
	return results
}

// Select resolves a comma-separated selector of module IDs, themes or all against the
// catalogue and rejects unknown selections.
func Select(selector string) ([]Module, error) {
	return selectFrom(Catalogue(), selector)
}

// selectFrom resolves selector against modules, preserving catalogue order.
func selectFrom(modules []Module, selector string) ([]Module, error) {
	terms := strings.Split(selector, ",")
	for i := range terms {
		terms[i] = strings.TrimSpace(terms[i])
	}
	var out []Module
	matched := map[string]bool{}
	for _, m := range modules {
		for _, term := range terms {
			if term == "all" || term == m.ID || term == m.Theme {
				out = append(out, m)
				matched[term] = true
				break
			}
		}
	}
	for _, term := range terms {
		if !matched[term] {
			return nil, fmt.Errorf("%w: unknown module or theme %q", errOperation, term)
		}
	}
	return out, nil
}

// SelectMode makes all select the modules applicable to mode; explicit IDs and themes
// still report unsupported modes rather than being silently ignored.
func SelectMode(mode, selector string) ([]Module, error) {
	all, err := Select(selector)
	if err != nil || selector != "all" {
		return all, wrapError(err)
	}
	var out []Module
	for _, m := range all {
		if slices.Contains(m.Modes, mode) {
			out = append(out, m)
		}
	}
	return out, nil
}
