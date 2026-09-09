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
			"%w: bench needs init, doctor, list, up, run, profile, status, or down",
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
		if *format == "markdown" {
			_, err := fmt.Fprint(e.stdout, bench.Markdown())
			return wrapError(err)
		}
		return wrapError(json.NewEncoder(e.stdout).Encode(bench.Catalogue()))
	}
	w, err := bench.Load(*dir)
	if err != nil {
		return wrapError(err)
	}
	if sub == "doctor" {
		return wrapError(json.NewEncoder(e.stdout).Encode(w.Doctor()))
	}
	if sub == "up" {
		ctx, cancel := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
		defer cancel()
		b, err := filepath.Abs(*binary)
		if err != nil {
			return wrapError(err)
		}
		return wrapError(bench.Up(ctx, w, b, e.stdout))
	}
	instance, err := bench.Attach(w)
	if err != nil {
		return wrapError(err)
	}
	defer instance.Client.CloseIdleConnections()
	switch sub {
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
		return wrapError(bench.Profile(ctx, instance, *device, *destination))
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
