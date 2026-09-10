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
	"syscall"
	"time"

	"github.com/deploymenttheory/go-apple-dm/server/internal/bench"
)

func runBench(ctx context.Context, e *env, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf(
			"%w: bench needs init, doctor, enrollment-preflight, trust, list, up, run, profile, replace, status, or down",
			ErrUsage,
		)
	}
	sub := args[0]
	fs := e.verbFlags("bench " + sub)
	dir := fs.String("workspace", "test-lab/local", "private workspace")
	mode := fs.String("mode", "simulated", "simulated or live (init)")
	storage := fs.String("storage", "sqlite", "sqlite, inmem, postgres, or mysql (init)")
	topology := fs.String("topology", "all", "all or split (init)")
	listen := fs.String("listen", "127.0.0.1:8443", "loopback listener (init)")
	binary := fs.String(
		"dmserver",
		"test-lab/local/bin/dmserver",
		"built reference server executable (up)",
	)
	scenario := fs.String("scenario", "all", "stable scenario ID, family, or all (run)")
	device := fs.String("device-id", "", "target device ID (live run/profile)")
	identity := fs.String("identity", "", "acme or scep; empty uses server default")
	destination := fs.String("file", "", "new profile output path (profile)")
	format := fs.String("format", "json", "json or markdown (list)")
	revision := fs.String("revision", version(), "source revision recorded in evidence")
	report := fs.String("report-dir", "", "evidence directory (run)")
	if err := fs.Parse(reorder(fs, args[1:])); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return wrapError(err)
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("%w: unexpected arguments", ErrUsage)
	}
	if sub == "init" {
		return wrapError(bench.Init(*dir, *mode, *storage, *topology, *listen))
	}
	if sub == "list" {
		return benchList(e, *format)
	}

	w, err := bench.Load(*dir)
	if err != nil {
		return wrapError(err)
	}
	if sub == "doctor" {
		return wrapError(json.NewEncoder(e.stdout).Encode(w.Doctor()))
	}
	if sub == "enrollment-preflight" || sub == "trust" {
		return benchEnrollmentOffline(e, w, sub, *identity, *destination)
	}
	if sub == "up" {
		return benchUp(ctx, e, w, *binary)
	}

	instance, err := bench.Attach(w)
	if err != nil {
		return wrapError(err)
	}
	defer instance.Client.CloseIdleConnections()
	switch sub {
	case "replace":
		return benchReplace(ctx, e, instance, *device, *identity)
	case "status", "down":
		method, path := "GET", "/status"
		if sub == "down" {
			method, path = "POST", "/stop"
		}
		b, err := instance.Control(ctx, method, path, nil)
		if err != nil {
			return wrapError(err)
		}
		if sub == "status" {
			var status map[string]any
			if err = json.Unmarshal(b, &status); err != nil {
				return wrapError(err)
			}
			delete(status, "ControlToken")
			return wrapError(json.NewEncoder(e.stdout).Encode(status))
		}
		return nil
	case "profile":
		if *destination == "" {
			return fmt.Errorf("%w: -file is required", ErrUsage)
		}
		return wrapError(bench.ProfileWithIdentity(ctx, instance, *device, *identity, *destination))
	case "run":
		selected, err := bench.SelectMode(instance.Mode, *scenario)
		if err != nil {
			return wrapError(err)
		}
		results := make([]bench.Result, 0, len(selected))
		failed := false
		for _, s := range selected {
			fmt.Fprintln(e.stderr, "bench:", s.ID, s.Name)
			runCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			r := bench.Run(runCtx, instance, s, "process", *revision, *device)
			cancel()
			results = append(results, r)
			if r.Status != "passed" {
				failed = true
			}
			if err = json.NewEncoder(e.stdout).Encode(r); err != nil {
				return wrapError(err)
			}
			if ctx.Err() != nil {
				break
			}
		}
		output := *report
		if output == "" {
			output = filepath.Join(
				w.Directory,
				"evidence",
				time.Now().UTC().Format("20060102T150405.000000000"),
			)
		}
		if err = bench.WriteReports(output, results); err != nil {
			return wrapError(err)
		}
		if failed {
			return errBenchSelection
		}
		return nil
	default:
		return fmt.Errorf("%w: unknown bench subcommand %q", ErrUsage, sub)
	}
}

var errBenchSelection = errors.New(
	"bench: selection contains failed, blocked, or unsupported scenarios; inspect evidence",
)

func benchEnrollmentOffline(e *env, w *bench.Workspace, sub, identity, destination string) error {
	if sub == "trust" {
		if destination == "" {
			return fmt.Errorf("%w: -file is required", ErrUsage)
		}
		return wrapError(bench.ExportTrust(w, destination))
	}
	result := bench.EnrollmentPreflight(w, identity)
	if err := json.NewEncoder(e.stdout).Encode(result); err != nil {
		return wrapError(err)
	}
	if ready, _ := result["Ready"].(bool); !ready {
		return bench.ErrBlocked
	}
	return nil
}

func benchReplace(
	ctx context.Context,
	e *env,
	instance *bench.Environment,
	device, identity string,
) error {
	result, err := bench.Replace(ctx, instance, device, identity)
	if result != nil {
		if outputErr := json.NewEncoder(e.stdout).Encode(result); outputErr != nil {
			return wrapError(outputErr)
		}
	}
	return wrapError(err)
}

func benchList(e *env, format string) error {
	if format == "markdown" {
		_, err := fmt.Fprint(e.stdout, bench.Markdown())
		return wrapError(err)
	}
	return wrapError(json.NewEncoder(e.stdout).Encode(bench.Catalogue()))
}

func benchUp(ctx context.Context, e *env, w *bench.Workspace, binary string) error {
	ctx, cancel := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer cancel()
	b, err := filepath.Abs(binary)
	if err != nil {
		return wrapError(err)
	}
	return wrapError(bench.Up(ctx, w, b, e.stdout))
}
