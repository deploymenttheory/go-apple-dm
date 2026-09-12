package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/deploymenttheory/go-apple-dm/internal/schemagen"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "schemagen:", err)
		os.Exit(1)
	}
}

func run(args []string, out *os.File) error {
	fs := flag.NewFlagSet("schemagen", flag.ContinueOnError)
	schemaRoot := fs.String(
		"schema",
		"third_party/device-management",
		"path to apple/device-management checkout",
	)
	outDir := fs.String("out", "devicemanagement/schema", "output directory")
	baseline := fs.String("baseline", "", "baseline schema directory for audit or api-diff")
	history := fs.String("history", "", "older Apple checkout to retain for mixed-OS fleets")
	ref := fs.String("ref", "", "upstream branch (defaults to the configured submodule branch)")
	reportDir := fs.String("report", "", "write audit.json and audit.md to this directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf(
			"usage: schemagen [-schema dir] [-out dir] [-ref branch] [-baseline dir] [-report dir] generate|verify|identifiers|versions|audit|api-diff|boundaries",
		)
	}
	historyExplicit := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "history" {
			historyExplicit = true
		}
	})
	if !historyExplicit {
		if data, err := exec.CommandContext(context.Background(), "git", "config", "--file", ".gitmodules", "--get", "submodule.third_party/device-management-history.path").
			Output(); err == nil {
			*history = strings.TrimSpace(string(data))
		}
	}
	// Commit is left empty: schemagen reads it from the checkout. Reading it
	// from the output directory made a submodule bump stamp the old commit.
	if *ref == "" {
		data, err := exec.CommandContext(context.Background(), "git", "config", "--file", ".gitmodules", "--get", "submodule.third_party/device-management.branch").
			Output()
		if err != nil {
			return fmt.Errorf("read configured schema branch (or provide -ref): %w", err)
		}
		*ref = strings.TrimSpace(string(data))
	}
	opts := schemagen.Options{Ref: *ref, History: *history}
	switch fs.Arg(0) {
	case "boundaries":
		if *baseline == "" {
			return fmt.Errorf("boundaries requires -baseline")
		}
		cases, err := schemagen.BoundaryProbes(*baseline, *schemaRoot, *history)
		if err != nil {
			return err
		}
		if err := json.NewEncoder(out).Encode(cases); err != nil {
			return fmt.Errorf("encode boundaries: %w", err)
		}
		return nil
	case "audit":
		if *baseline == "" {
			return fmt.Errorf("audit requires -baseline")
		}
		report, err := schemagen.Audit(*baseline, *schemaRoot, *ref)
		if err != nil {
			return err
		}
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return fmt.Errorf("encode audit: %w", err)
		}
		if *reportDir != "" {
			if err := os.MkdirAll(*reportDir, 0o750); err != nil {
				return fmt.Errorf("create audit directory: %w", err)
			}
			for name, body := range map[string][]byte{"audit.json": append(data, '\n'), "audit.md": []byte(report.Markdown())} {
				if err := os.WriteFile(filepath.Join(*reportDir, name), body, 0o600); err != nil {
					return fmt.Errorf("write audit: %w", err)
				}
			}
		}
		_, err = fmt.Fprintln(out, string(data))
		return err
	case "api-diff":
		if *baseline == "" {
			return fmt.Errorf("api-diff requires -baseline")
		}
		report, err := schemagen.CompareAPI(*baseline, *schemaRoot)
		if err != nil {
			return err
		}
		if err := json.NewEncoder(out).Encode(report); err != nil {
			return fmt.Errorf("encode API audit: %w", err)
		}
		return nil
	case "generate":
		files, err := schemagen.Run(*schemaRoot, opts)
		if err != nil {
			return err
		}
		if err := schemagen.Write(*outDir, files); err != nil {
			return err
		}
		fmt.Fprintf(out, "generated %d files into %s\n", len(files), *outDir)
		return nil
	case "verify":
		if err := schemagen.Verify(*schemaRoot, *outDir, opts); err != nil {
			return err
		}
		fmt.Fprintln(out, "verify: ok")
		return nil
	case "versions":
		tree, err := schemagen.Load(*schemaRoot)
		if err != nil {
			return err
		}
		newest := schemagen.NewestIntroduced(tree)
		families := make([]string, 0, len(newest))
		for f := range newest {
			families = append(families, f)
		}
		sort.Strings(families)
		for _, f := range families {
			fmt.Fprintf(out, "%s\t%s\n", f, newest[f])
		}
		return nil
	case "identifiers":
		files, err := schemagen.Run(*schemaRoot, opts)
		if err != nil {
			return err
		}
		_, err = out.Write(files["EXPORTED_IDENTIFIERS.lock"])
		return err
	}
	return fmt.Errorf("unknown command %q", fs.Arg(0))
}
