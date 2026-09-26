package dmctl

import (
	"context"
	"crypto/x509"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/plist"
	"github.com/deploymenttheory/go-apple-dm/server/internal/dmctl/explain"
	"github.com/deploymenttheory/go-apple-dm/server/internal/dmctl/profilelint"
)

var errProfileLint = errors.New("profile validation failed")

// runProfile parses and executes the profile subcommand, reporting argument and operation
// failures to the CLI caller.
func runProfile(_ context.Context, e *env, args []string) error {
	if len(args) == 0 || args[0] != "lint" {
		return fmt.Errorf("%w: profile needs lint", ErrUsage)
	}
	fs := e.verbFlags("profile lint")
	file := fs.String("file", "", "profile path, or - for stdin")
	target := fs.String("target", "", "required target, e.g. macos:26,channel=device,supervised")
	rootsFile := fs.String("trust-roots", "", "PEM roots for profile signer trust")
	require := fs.Bool("require-signature", false, "reject unsigned profiles")
	rest, err := e.parseVerb(fs, args[1:])
	if err != nil {
		return err
	}
	if len(rest) != 0 || *file == "" || *target == "" {
		return fmt.Errorf("%w: profile lint requires -file and -target", ErrUsage)
	}
	tgt, err := explain.ParseTarget(*target)
	if err != nil || tgt.Version.IsZero() {
		return fmt.Errorf("%w: profile lint target must include a valid OS version", ErrUsage)
	}
	o := profilelint.Options{Target: tgt, RequireSignature: *require}
	if *rootsFile != "" {
		pem, readErr := os.ReadFile(*rootsFile)
		if readErr != nil {
			return fmt.Errorf("read trust roots: %w", readErr)
		}
		o.Roots = x509.NewCertPool()
		if !o.Roots.AppendCertsFromPEM(pem) {
			return fmt.Errorf("%w: no certificates in trust roots", ErrUsage)
		}
	}
	reader := e.stdin
	if *file != "-" {
		f, openErr := os.Open(*file)
		if openErr != nil {
			return fmt.Errorf("open profile: %w", openErr)
		}
		defer func(cleanup func() error) { _ = cleanup() }(f.Close)
		reader = f
	}
	data, err := io.ReadAll(io.LimitReader(reader, plist.DefaultMaxBytes+1))
	if err != nil {
		return fmt.Errorf("read profile: %w", err)
	}
	report := profilelint.Inspect(data, o)
	if err := emitLint(e, *file, report); err != nil {
		return err
	}
	partial := false
	for _, issue := range report.Issues {
		if issue.Severity == "error" {
			return errProfileLint
		}
		partial = partial || issue.Severity == "unvalidated"
	}
	if partial {
		return fmt.Errorf("%w: some profile content was not validated", ErrPartial)
	}
	return nil
}

// emitLint writes profile-inspection diagnostics in the requested output format.
func emitLint(e *env, file string, report profilelint.Report) error {
	if e.opts.output == outputJSON || e.opts.output == outputNDJSON {
		if err := json.MarshalWrite(e.stdout, report); err != nil {
			return fmt.Errorf("write lint report: %w", err)
		}
		_, err := fmt.Fprintln(e.stdout)
		if err != nil {
			return fmt.Errorf("write lint report: %w", err)
		}
		return nil
	}
	if _, err := fmt.Fprintf(
		e.stdout,
		"%s: signature=%s trust=%s\n",
		file,
		report.Signature,
		report.Trust,
	); err != nil {
		return fmt.Errorf("write lint report: %w", err)
	}
	for _, issue := range report.Issues {
		if _, err := fmt.Fprintf(
			e.stdout,
			"%s: %s: %s (%s)\n",
			issue.Severity,
			issue.Path,
			issue.Message,
			issue.Rule,
		); err != nil {
			return fmt.Errorf("write lint report: %w", err)
		}
	}
	return nil
}
