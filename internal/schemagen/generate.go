package schemagen

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Provenance mirrors schema/PROVENANCE.json.
//
//nolint:tagliatelle // keys match the PROVENANCE.json file format
type Provenance struct {
	Source     string `json:"source"`
	Ref        string `json:"ref"`
	Commit     string `json:"commit"`
	YAMLSHA256 string `json:"yaml_sha256"`
	OSVersions string `json:"os_versions"`
	Fetched    string `json:"fetched"`
	Generator  string `json:"generator"`
}

// ReadProvenance loads schema/PROVENANCE.json.
func ReadProvenance(path string) (*Provenance, error) {
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("provenance: %w", err)
	}
	var p Provenance
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("provenance: %w", err)
	}
	return &p, nil
}

// Run loads the tree, builds packages, and generates files.
func Run(schemaRoot string, opts Options) (Files, error) {
	tree, err := Load(schemaRoot)
	if err != nil {
		return nil, err
	}
	pkgs, err := Build(tree)
	if err != nil {
		return nil, err
	}
	return Generate(pkgs, opts)
}

// Write stores files under outDir, creating package directories and
// removing stale generated files that are no longer produced. All paths are
// resolved inside outDir through os.Root.
func Write(outDir string, files Files) error {
	if err := os.MkdirAll(outDir, 0o750); err != nil {
		return fmt.Errorf("schemagen: %w", err)
	}
	root, err := os.OpenRoot(outDir)
	if err != nil {
		return fmt.Errorf("schemagen: %w", err)
	}
	defer root.Close()
	written := map[string]bool{}
	if generated, ok := files["NAMES.lock"]; ok {
		existing, _ := root.ReadFile("NAMES.lock")
		merged, _ := mergeLock(existing, generated, renames(outDir))
		files = cloneFiles(files)
		files["NAMES.lock"] = merged
	}
	for rel, data := range files {
		rel = filepath.FromSlash(rel)
		if dir := filepath.Dir(rel); dir != "." {
			if err := root.MkdirAll(dir, 0o750); err != nil {
				return fmt.Errorf("schemagen: %w", err)
			}
		}
		if err := root.WriteFile(rel, data, 0o600); err != nil {
			return fmt.Errorf("schemagen: %w", err)
		}
		written[filepath.ToSlash(rel)] = true
	}
	return removeStale(root, ".", written)
}

// removeStale deletes generated files under dir that were not just written.
func removeStale(root *os.Root, dir string, written map[string]bool) error {
	entries, err := fs.ReadDir(root.FS(), dir)
	if err != nil {
		return fmt.Errorf("schemagen: %w", err)
	}
	for _, d := range entries {
		rel := d.Name()
		if dir != "." {
			rel = dir + "/" + d.Name()
		}
		if d.IsDir() {
			if err := removeStale(root, rel, written); err != nil {
				return err
			}
			continue
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".gen.go") && name != "conformance_gen_test.go" {
			continue
		}
		if !written[rel] {
			if err := root.Remove(filepath.FromSlash(rel)); err != nil {
				return fmt.Errorf("schemagen: %w", err)
			}
		}
	}
	return nil
}

// ErrVerify is returned by Verify when the tree is out of date.
var ErrVerify = errors.New("schemagen: verify failed")

func cloneFiles(f Files) Files {
	out := make(Files, len(f))
	for k, v := range f {
		out[k] = v
	}
	return out
}

// mergeLock computes the NAMES.lock content: every name ever generated
// stays in the lock until RENAMES.md allows its removal. It returns the
// merged lock and the names that are neither generated any more nor allowed
// to go, which is the rename-guard violation list.
func mergeLock(
	existing, generated []byte,
	allowed map[string]bool,
) (merged []byte, stale []string) {
	gen := map[string]bool{}
	for _, n := range strings.Split(strings.TrimSpace(string(generated)), "\n") {
		if n != "" {
			gen[n] = true
		}
	}
	all := map[string]bool{}
	for n := range gen {
		all[n] = true
	}
	for _, n := range strings.Split(strings.TrimSpace(string(existing)), "\n") {
		if n == "" || gen[n] {
			continue
		}
		if allowed[n] {
			continue // dropped deliberately
		}
		all[n] = true
		stale = append(stale, n)
	}
	names := make([]string, 0, len(all))
	for n := range all {
		names = append(names, n)
	}
	sort.Strings(names)
	sort.Strings(stale)
	return []byte(strings.Join(names, "\n") + "\n"), stale
}

// Verify regenerates in memory and compares with outDir: every generated
// file must be byte-identical, and every name in NAMES.lock must still be
// generated unless listed in RENAMES.md.
func Verify(schemaRoot, outDir string, opts Options) error {
	files, err := Run(schemaRoot, opts)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(outDir)
	if err != nil {
		return fmt.Errorf("schemagen: %w", err)
	}
	defer root.Close()
	var problems []string
	onDisk, _ := root.ReadFile("NAMES.lock")
	merged, stale := mergeLock(onDisk, files["NAMES.lock"], renames(outDir))
	for _, n := range stale {
		problems = append(
			problems,
			"NAMES.lock: "+n+" is no longer generated; add it to RENAMES.md to allow its removal",
		)
	}
	files = cloneFiles(files)
	files["NAMES.lock"] = merged
	for rel, want := range files {
		got, err := root.ReadFile(filepath.FromSlash(rel))
		if err != nil {
			problems = append(problems, rel+": missing")
			continue
		}
		if !bytes.Equal(got, want) {
			problems = append(problems, rel+": differs from regenerated output")
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return fmt.Errorf("%w:\n  %s", ErrVerify, strings.Join(problems, "\n  "))
	}
	return nil
}

// renames reads RENAMES.md and returns the identifiers allowed to disappear:
// every line of the form "- `package/Name`" (with optional explanation).
func renames(outDir string) map[string]bool {
	out := map[string]bool{}
	data, err := os.ReadFile(filepath.Join(filepath.Clean(outDir), "RENAMES.md"))
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "- `") {
			continue
		}
		rest := line[3:]
		if i := strings.Index(rest, "`"); i > 0 {
			out[rest[:i]] = true
		}
	}
	return out
}
