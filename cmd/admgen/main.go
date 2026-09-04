package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/deploymenttheory/go-apple-dm/v3/internal/schemagen"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "admgen:", err)
		os.Exit(1)
	}
}

func run(args []string, out *os.File) error {
	fs := flag.NewFlagSet("admgen", flag.ContinueOnError)
	schemaRoot := fs.String("schema", "third_party/device-management", "path to apple/device-management checkout")
	outDir := fs.String("out", "schema", "output directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: admgen [-schema dir] [-out dir] generate|verify|names")
	}
	opts := schemagen.Options{Commit: commitOf(*schemaRoot, *outDir)}
	switch fs.Arg(0) {
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
	case "names":
		files, err := schemagen.Run(*schemaRoot, opts)
		if err != nil {
			return err
		}
		_, err = out.Write(files["NAMES.lock"])
		return err
	}
	return fmt.Errorf("unknown command %q", fs.Arg(0))
}

// commitOf returns the pinned commit from PROVENANCE.json, falling back to
// the checkout's git HEAD.
func commitOf(schemaRoot, outDir string) string {
	if p, err := schemagen.ReadProvenance(outDir + "/PROVENANCE.json"); err == nil && p.Commit != "" {
		return p.Commit
	}
	cmd := exec.Command("git", "-C", schemaRoot, "rev-parse", "HEAD") // #nosec G204 -- operator-supplied checkout path
	if b, err := cmd.Output(); err == nil {
		return strings.TrimSpace(string(b))
	}
	return "unknown"
}
