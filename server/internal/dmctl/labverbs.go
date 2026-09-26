package dmctl

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/deploymenttheory/go-apple-dm/server/lab"
	"github.com/deploymenttheory/go-apple-dm/server/lab/target"
)

// labFlags are the flags shared by the lab subcommands.
type labFlags struct {
	fs                                          *flag.FlagSet
	dir, mode, storage, listen, binary, modules *string
	adapter, hosts                              *string
	device, user, identity, destination, format *string
	revision, report, attachURL, run            *string
	destructive                                 *bool
}

// newLabFlags declares the lab flag set for the named subcommand.
func newLabFlags(e *env, name string) *labFlags {
	fs := e.verbFlags(name)
	f := &labFlags{fs: fs}
	f.dir = fs.String("workspace", "test-lab/local", "private workspace")
	f.mode = fs.String("mode", "simulated", "simulated or live (init)")
	f.storage = fs.String("storage", "sqlite", "sqlite, inmem, postgres, or mysql (init)")
	f.listen = fs.String("listen", "127.0.0.1:8443", "listener address (init)")
	f.adapter = fs.String("adapter", "process", "server adapter: process or docker (init)")
	f.hosts = fs.String("hosts", "", "comma-separated device-facing DNS names and IPs for the HTTPS leaf (init, tls)")
	f.binary = fs.String("dmserver", "test-lab/local/bin/dmserver", "built reference server executable (up)")
	f.modules = fs.String("modules", "all", "comma-separated module IDs, themes, or all (run)")
	f.device = fs.String("device-id", "", "enrolled device ID for a live run (attached target)")
	f.user = fs.String("user-id", "", "installing user's GeneratedUID for user-channel modules")
	f.identity = fs.String("identity", "", "acme or scep; empty uses server default")
	f.destination = fs.String("file", "", "new output path")
	f.format = fs.String("format", "json", "json or markdown (list)")
	f.revision = fs.String("revision", version(), "source revision recorded in evidence")
	f.report = fs.String("report-dir", "", "evidence directory (run)")
	f.attachURL = fs.String("attach-url", "", "existing HTTPS server origin for a live run")
	f.run = fs.String("run", "", "run evidence directory to render (report)")
	f.destructive = fs.Bool("destructive", false, "run modules tagged destructive (run)")
	return f
}

// parse parses args, reporting whether help was requested.
func (f *labFlags) parse(args []string) (bool, error) {
	if err := f.fs.Parse(reorder(f.fs, args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return true, nil
		}
		return false, wrapError(err)
	}
	if f.fs.NArg() != 0 {
		return false, fmt.Errorf("%w: unexpected arguments", ErrUsage)
	}
	return false, nil
}

// runLab dispatches the lab acceptance commands: workspace setup and supervision, module
// listing, runs that write JSON, JUnit and HTML evidence, and report rendering.
func runLab(ctx context.Context, e *env, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf(
			"%w: lab needs init, doctor, tls, preflight, trust, list, up, run, report, profile, replace, status, or down",
			ErrUsage,
		)
	}
	sub := args[0]
	f := newLabFlags(e, "lab "+sub)
	if help, err := f.parse(args[1:]); help || err != nil {
		return err
	}
	return labCommand(ctx, e, sub, f)
}

// labCommand executes a parsed lab subcommand.
func labCommand(ctx context.Context, e *env, sub string, f *labFlags) error {
	if *f.attachURL != "" && sub != "run" && sub != "profile" && sub != "replace" {
		return fmt.Errorf("%w: -attach-url supports run, profile, and replace", ErrUsage)
	}
	switch sub {
	case "init":
		return wrapError(lab.Init(*f.dir, *f.mode, *f.storage, *f.listen, *f.adapter, splitList(*f.hosts)))
	case "list":
		return labList(e, *f.format)
	case "report":
		if *f.run == "" {
			return fmt.Errorf("%w: -run is required", ErrUsage)
		}
		results, err := lab.ReadResults(*f.run)
		if err != nil {
			return wrapError(err)
		}
		if err = lab.WriteHTML(*f.run, results); err != nil {
			return wrapError(err)
		}
		_, err = fmt.Fprintln(e.stdout, filepath.Join(*f.run, lab.HTMLFile))
		return wrapError(err)
	case "doctor", "tls", "preflight", "trust", "up", "run", "profile", "replace", "status", "down":
	default:
		return fmt.Errorf("%w: unknown lab subcommand %q", ErrUsage, sub)
	}
	w, err := lab.Load(*f.dir)
	if err != nil {
		return wrapError(err)
	}
	switch sub {
	case "doctor":
		return wrapError(json.NewEncoder(e.stdout).Encode(w.Doctor()))
	case "tls":
		hosts := splitList(*f.hosts)
		if len(hosts) == 0 {
			return fmt.Errorf("%w: tls requires -hosts", ErrUsage)
		}
		if err = lab.ReissueTLS(w, hosts); err != nil {
			return wrapError(err)
		}
		_, err = fmt.Fprintf(e.stdout, "reissued %s for %s\n", w.Path("mdm", "tls.pem"), strings.Join(hosts, ", "))
		return wrapError(err)
	case "preflight", "trust":
		return labEnrollmentOffline(e, w, sub, *f.identity, *f.destination)
	case "up":
		return labUp(ctx, e, w, *f.binary)
	}
	var instance *lab.Environment
	if *f.attachURL != "" {
		instance, err = lab.AttachURL(w, *f.attachURL)
	} else {
		instance, err = lab.Attach(w)
	}
	if err != nil {
		return wrapError(err)
	}
	defer instance.Client.CloseIdleConnections()
	instance.InstallingUserID = *f.user
	switch sub {
	case "run":
		return labRun(ctx, e, w, instance, f)
	case "replace":
		return labReplace(ctx, e, instance, *f.device, *f.identity)
	case "profile":
		if *f.destination == "" {
			return fmt.Errorf("%w: -file is required", ErrUsage)
		}
		return wrapError(lab.ProfileWithIdentity(ctx, instance, *f.device, *f.identity, *f.destination))
	}
	method, path := "GET", "/status"
	if sub == "down" {
		method, path = "POST", "/stop"
	}
	b, err := instance.Control(ctx, method, path, nil)
	if err != nil {
		return wrapError(err)
	}
	if sub == "down" {
		return nil
	}
	var status map[string]any
	if err = json.Unmarshal(b, &status); err != nil {
		return wrapError(err)
	}
	delete(status, "ControlToken")
	return wrapError(json.NewEncoder(e.stdout).Encode(status))
}

// labTarget selects the device side of a run: the simulator for simulated workspaces and
// the operator-enrolled device identified by -device-id for live workspaces.
func labTarget(mode, device, user string) target.Target {
	if mode == "live" {
		return target.Attached{UDID: device, UserID: user}
	}
	return target.Simulator{}
}

// labRun executes the selected modules, streams results as JSON lines and writes the run's
// JSON, JUnit and HTML evidence. Any non-pass makes the command fail.
func labRun(ctx context.Context, e *env, w *lab.Workspace, instance *lab.Environment, f *labFlags) error {
	selected, err := lab.SelectMode(instance.Mode, *f.modules)
	if err != nil {
		return wrapError(err)
	}
	output := *f.report
	if output == "" {
		output = filepath.Join(w.Directory, "evidence", time.Now().UTC().Format("20060102T150405.000000000"))
	}
	opts := lab.Options{
		Adapter: "process", Revision: *f.revision, Destructive: *f.destructive, Evidence: output,
	}
	failed := false
	var writeErr error
	results := lab.RunAll(ctx, instance, labTarget(instance.Mode, *f.device, *f.user), selected, opts,
		func(r lab.Result) {
			_, _ = fmt.Fprintln(e.stderr, "lab:", r.ID, r.Status, r.Name)
			if r.Status != lab.StatusPassed {
				failed = true
			}
			if err := json.NewEncoder(e.stdout).Encode(r); err != nil && writeErr == nil {
				writeErr = err
			}
		})
	if writeErr != nil {
		return wrapError(writeErr)
	}
	if err = lab.WriteReports(output, results); err != nil {
		return wrapError(err)
	}
	_, _ = fmt.Fprintln(e.stderr, "report", filepath.Join(output, lab.HTMLFile))
	if failed || len(results) < len(selected) {
		return errLabSelection
	}
	return nil
}

var errLabSelection = errors.New(
	"selection contains failed, blocked, or unsupported modules; inspect evidence",
)

// labList prints the module catalogue and its execution requirements.
func labList(e *env, format string) error {
	switch format {
	case "markdown":
		_, err := fmt.Fprint(e.stdout, lab.Markdown())
		return wrapError(err)
	case "json":
		return wrapError(json.NewEncoder(e.stdout).Encode(lab.Catalogue()))
	default:
		return fmt.Errorf("%w: list -format must be json or markdown", ErrUsage)
	}
}

// labUp runs the workspace supervisor with cancellation on interrupt or termination signals.
func labUp(ctx context.Context, e *env, w *lab.Workspace, binary string) error {
	ctx, cancel := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer cancel()
	b, err := filepath.Abs(binary)
	if err != nil {
		return wrapError(err)
	}
	return wrapError(lab.Up(ctx, w, b, e.stdout))
}

// labEnrollmentOffline exports the workspace trust profile or reports enrollment preflight
// readiness. Preflight changes no device state and reports missing material as blocked.
func labEnrollmentOffline(e *env, w *lab.Workspace, sub, identity, destination string) error {
	if sub == "trust" {
		if destination == "" {
			return fmt.Errorf("%w: -file is required", ErrUsage)
		}
		return wrapError(lab.ExportTrust(w, destination))
	}
	result := lab.EnrollmentPreflight(w, identity)
	if err := json.NewEncoder(e.stdout).Encode(result); err != nil {
		return wrapError(err)
	}
	if ready, _ := result["Ready"].(bool); !ready {
		return lab.ErrBlocked
	}
	return nil
}

// labReplace runs the controlled identity-replacement workflow and prints the attempt even
// when the wake fails.
func labReplace(ctx context.Context, e *env, instance *lab.Environment, device, identity string) error {
	result, err := lab.Replace(ctx, instance, device, identity)
	if result != nil {
		if outputErr := json.NewEncoder(e.stdout).Encode(result); outputErr != nil {
			return wrapError(outputErr)
		}
	}
	return wrapError(err)
}

// splitList parses a comma-separated flag value, discarding empty entries.
func splitList(value string) []string {
	var out []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}
