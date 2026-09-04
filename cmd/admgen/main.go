package main

import (
	"flag"
	"fmt"
	"os"
	"sort"

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
		return fmt.Errorf("usage: admgen [-schema dir] [-out dir] generate|verify|identifiers|versions")
	}
	// Commit is left empty: schemagen reads it from the checkout. Reading it
	// from the output directory made a submodule bump stamp the old commit.
	opts := schemagen.Options{}
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
