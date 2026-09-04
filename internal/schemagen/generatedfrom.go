package schemagen

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// generatedFromFile is the name of the record inside the output directory.
const generatedFromFile = "GENERATED_FROM.json"

// upstreamRef is the branch .gitmodules tracks for the submodule.
const upstreamRef = "release"

// upstreamSource is the repository the schema is generated from.
const upstreamSource = "https://github.com/apple/device-management"

// generatorName identifies what produced the tree.
const generatorName = "admgen (cmd/admgen, internal/schemagen)"

// osFamilies is the order OS versions are reported in, which is Apple's own
// order in every supportedOS block.
var osFamilies = []string{"iOS", "macOS", "tvOS", "visionOS", "watchOS"}

// describe builds GENERATED_FROM.json for a checkout.
//
// Every field is a function of the checkout alone, so regenerating the same
// pin produces the same bytes and `make verify` can hold this file to the same
// standard as the generated Go. That is why the commit's own date is recorded
// rather than the time the generator ran: a wall clock would make the file
// differ on every run and the determinism check would have to skip it, which
// is how the previous hand-maintained version came to record a commit the tree
// had long since moved past.
func describe(schemaRoot string, t *Tree, commit string) ([]byte, error) {
	sum, err := yamlSHA256(schemaRoot)
	if err != nil {
		return nil, err
	}
	g := GeneratedFrom{
		Source:     upstreamSource,
		Ref:        upstreamRef,
		Commit:     commit,
		YAMLSHA256: sum,
		OSVersions: NewestOSVersion(t),
		CommitDate: gitCommitDate(schemaRoot),
		Generator:  generatorName,
	}
	data, err := json.MarshalIndent(g, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("schemagen: %w", err)
	}
	return append(data, '\n'), nil
}

// yamlSHA256 hashes every YAML file under root, path and content, in sorted
// path order, so the digest identifies the schema rather than the checkout.
func yamlSHA256(root string) (string, error) {
	var paths []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case d.IsDir() && d.Name() == ".git":
			return filepath.SkipDir
		case d.IsDir():
			return nil
		}
		if ext := filepath.Ext(path); ext == ".yaml" || ext == ".yml" {
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return relErr
			}
			paths = append(paths, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("schemagen: hashing %s: %w", root, err)
	}
	sort.Strings(paths)
	h := sha256.New()
	for _, rel := range paths {
		data, readErr := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if readErr != nil {
			return "", fmt.Errorf("schemagen: hashing %s: %w", rel, readErr)
		}
		_, _ = h.Write([]byte(rel))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write(data)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// NewestOSVersion returns the highest version any schema in the tree is
// introduced in, which is the OS release the pin covers up to.
func NewestOSVersion(t *Tree) string {
	newest := ""
	for _, v := range NewestIntroduced(t) {
		if compareVersions(v, newest) > 0 {
			newest = v
		}
	}
	return newest
}

// NewestIntroduced returns, per OS family, the highest version any schema in
// the tree is introduced in. A family Apple has not shipped the schema on at
// all is absent rather than empty, so a caller can tell "no support" from
// "supported since the beginning".
//
// This is what an update is reported in terms of: a pin moving from 26.4 to
// 26.5 on iOS says more about what changed than a commit hash does.
func NewestIntroduced(t *Tree) map[string]string {
	out := map[string]string{}
	if t == nil {
		return out
	}
	for _, s := range t.Schemas {
		for _, family := range osFamilies {
			os := s.Payload.SupportedOS.ByName(family)
			if os == nil {
				continue
			}
			v := strings.TrimSpace(os.Introduced)
			// Apple writes "n/a" for an OS a schema never shipped on.
			if v == "" || strings.EqualFold(v, "n/a") {
				continue
			}
			if compareVersions(v, out[family]) > 0 {
				out[family] = v
			}
		}
	}
	return out
}

// compareVersions orders dotted numeric versions. An unparsable component
// sorts below a numeric one, so a stray value cannot claim to be the newest.
func compareVersions(a, b string) int {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(pa) || i < len(pb); i++ {
		x, y := 0, 0
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		if x != y {
			if x > y {
				return 1
			}
			return -1
		}
	}
	return 0
}

// gitCommitDate returns the committer date of HEAD in the checkout, as
// YYYY-MM-DD, or "" when the directory is not a git checkout.
func gitCommitDate(schemaRoot string) string {
	cmd := exec.Command("git", "-C", schemaRoot, "show", "-s", "--format=%cs", "HEAD") // #nosec G204 -- operator-supplied checkout path
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// gitHEAD returns the commit checked out in schemaRoot, or "" when the
// directory is not a git checkout.
func gitHEAD(schemaRoot string) string {
	cmd := exec.Command("git", "-C", schemaRoot, "rev-parse", "HEAD") // #nosec G204 -- operator-supplied checkout path
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
