package appidentity

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"howett.net/plist"
)

// Inspection errors distinguish host support, invalid input, and tool failures.
var (
	ErrUnsupported = errors.New("appidentity: native inspection requires macOS")
	ErrInput       = errors.New("appidentity: invalid bundle or Mach-O executable")
	ErrInspect     = errors.New("appidentity: inspection failed")
	ErrTooLarge    = errors.New("appidentity: metadata exceeds size limit")
)

// Status describes signature integrity, independently of signing category.
type Status string

const (
	Valid    Status = "valid"
	Invalid  Status = "invalid"
	Unsigned Status = "unsigned"
	// NotChecked means metadata was read without verifying signature integrity.
	NotChecked Status = "not-checked"
)

// Category is a verified signing requirement, or Unknown. Enterprise and
// TestFlight are deliberately not inferred from certificate display names.
type Category string

const (
	Unknown     Category = "unknown"
	Apple       Category = "Apple"
	DeveloperID Category = "DeveloperID"
	AppStore    Category = "AppStore"
)

// Signature records native verification or NotChecked for portable metadata.
// Detail is diagnostic text from codesign, unsuitable for policy decisions.
type Signature struct {
	Status   Status   `json:"status"`
	Category Category `json:"category"`
	Detail   string   `json:"detail,omitempty"`
}

// Architecture contains the identity of one independently signed Mach-O slice.
// TeamID remains empty when absent, including for Apple platform binaries.
type Architecture struct {
	Name                  string          `json:"name"`
	CPU                   uint32          `json:"cpu,omitempty"`
	Subtype               uint32          `json:"subtype,omitempty"`
	CodeDirectories       []CodeDirectory `json:"codeDirectories,omitempty"`
	CDHash                string          `json:"cdhash,omitempty"`
	SigningID             string          `json:"signingID,omitempty"`
	TeamID                string          `json:"teamID,omitempty"`
	DesignatedRequirement string          `json:"designatedRequirement,omitempty"`
	Signature             Signature       `json:"signature"`
}

// Identity describes a file observed at inspection time. Bundle metadata is
// absent for standalone binaries. Every architecture must be handled by callers.
type Identity struct {
	Path          string         `json:"path"`
	Executable    string         `json:"executable"`
	BundleID      string         `json:"bundleID,omitempty"`
	Name          string         `json:"name,omitempty"`
	Version       string         `json:"version,omitempty"`
	Architectures []Architecture `json:"architectures"`
}

const (
	maxMetadata       = 1 << 20
	inspectionTimeout = 30 * time.Second
)

type commandRunner func(context.Context, string, ...string) (string, error)

// Inspect reads a bundle or Mach-O binary using /usr/bin/codesign and /usr/bin/lipo. Calls are
// bounded by the caller's deadline and a 30-second inspection timeout. Invalid
// or unsigned signatures are returned as observations; I/O, malformed metadata,
// malformed architecture listings and tool failures return errors.
func Inspect(ctx context.Context, path string) (Identity, error) {
	if runtime.GOOS != "darwin" {
		return Identity{}, ErrUnsupported
	}
	ctx, cancel := context.WithTimeout(ctx, inspectionTimeout)
	defer cancel()
	return inspect(ctx, path, runTool)
}

// inspect collects bundle metadata and native signing observations without launching the
// application.
func inspect(ctx context.Context, path string, run commandRunner) (Identity, error) {
	if err := ctx.Err(); err != nil {
		return Identity{}, err
	}
	id, err := resolve(path)
	if err != nil {
		return Identity{}, err
	}
	names, err := architectures(ctx, id.Executable, run)
	if err != nil {
		return Identity{}, err
	}
	for _, name := range names {
		arch, err := inspectArchitecture(ctx, id.Path, name, run)
		if err != nil {
			return Identity{}, err
		}
		id.Architectures = append(id.Architectures, arch)
	}
	return id, nil
}

// resolve resolves an application bundle and its main executable for inspection.
func resolve(path string) (Identity, error) {
	if strings.TrimSpace(path) == "" {
		return Identity{}, ErrInput
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return Identity{}, fmt.Errorf("%w: %w", ErrInput, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return Identity{}, fmt.Errorf("%w: %w", ErrInput, err)
	}
	id := Identity{Path: abs, Executable: abs}
	if info.Mode().IsRegular() {
		return id, nil
	}
	if !info.IsDir() {
		return Identity{}, fmt.Errorf("%w: expected regular file or bundle", ErrInput)
	}
	f, err := os.Open(filepath.Join(abs, "Contents", "Info.plist")) // #nosec G304 -- the caller explicitly selects the local application bundle to inspect.
	if err != nil {
		return Identity{}, fmt.Errorf("%w: %w", ErrInput, err)
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, maxMetadata+1))
	if err != nil {
		return Identity{}, fmt.Errorf("%w: %w", ErrInput, err)
	}
	if len(data) > maxMetadata {
		return Identity{}, ErrTooLarge
	}
	var meta struct {
		BundleID   string `plist:"CFBundleIdentifier"`
		Executable string `plist:"CFBundleExecutable"`
		Name       string `plist:"CFBundleName"`
		Version    string `plist:"CFBundleShortVersionString"`
	}
	if _, err := plist.Unmarshal(data, &meta); err != nil {
		return Identity{}, fmt.Errorf("%w: Info.plist: %w", ErrInput, err)
	}
	if meta.BundleID == "" || meta.Executable == "" || meta.Executable == "." || meta.Executable == ".." || strings.ContainsAny(meta.Executable, "/\\") {
		return Identity{}, fmt.Errorf("%w: bundle identifier and executable name required", ErrInput)
	}
	id.BundleID, id.Name, id.Version = meta.BundleID, meta.Name, meta.Version
	id.Executable = filepath.Join(abs, "Contents", "MacOS", meta.Executable)
	return id, nil
}

// architectures enumerates the executable architectures reported by lipo.
func architectures(ctx context.Context, path string, run commandRunner) ([]string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInput, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: executable must be a regular file", ErrInput)
	}
	out, err := run(ctx, "/usr/bin/lipo", "-archs", path)
	if err != nil {
		return nil, fmt.Errorf("%w: enumerate architectures: %w", ErrInspect, err)
	}
	names := strings.Fields(out)
	if len(names) == 0 {
		return nil, fmt.Errorf("%w: no architectures reported", ErrInspect)
	}
	seen := make(map[string]bool)
	for _, name := range names {
		if seen[name] || strings.IndexFunc(name, func(r rune) bool {
			return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '.')
		}) >= 0 {
			return nil, fmt.Errorf("%w: invalid or duplicate architecture %q", ErrInspect, name)
		}
		seen[name] = true
	}
	return names, nil
}

// inspectArchitecture collects and verifies signing metadata for one executable
// architecture.
func inspectArchitecture(ctx context.Context, path, name string, run commandRunner) (Architecture, error) {
	a := Architecture{Name: name, Signature: Signature{Category: Unknown}}
	out, err := run(ctx, "/usr/bin/codesign", "-d", "--verbose=4", "-r-", "--arch", name, path)
	if err != nil {
		if isExit(err) && strings.Contains(out, "code object is not signed at all") {
			a.Signature.Status = Unsigned
			return a, nil
		}
		return Architecture{}, fmt.Errorf("%w: display %s: %w", ErrInspect, name, err)
	}
	fields, err := parseMetadata(out)
	if err != nil {
		return Architecture{}, err
	}
	a.CDHash, a.SigningID, a.TeamID = fields["CDHash"], fields["Identifier"], fields["TeamIdentifier"]
	a.DesignatedRequirement = fields["designated"]
	if a.TeamID == "not set" {
		a.TeamID = ""
	}
	out, err = run(ctx, "/usr/bin/codesign", "--verify", "--strict", "--arch", name, path)
	if err != nil {
		if !isExit(err) {
			return Architecture{}, fmt.Errorf("%w: verify: %w", ErrInspect, err)
		}
		a.Signature.Status, a.Signature.Detail = Invalid, strings.TrimSpace(out)
		return a, nil
	}
	a.Signature.Status = Valid
	for _, category := range []struct {
		name        Category
		requirement string
	}{
		{Apple, "anchor apple"},
		{DeveloperID, "anchor apple generic and certificate 1[field.1.2.840.113635.100.6.2.6] exists and certificate leaf[field.1.2.840.113635.100.6.1.13] exists"},
		{AppStore, "anchor apple generic and certificate leaf[field.1.2.840.113635.100.6.1.9] exists"},
	} {
		_, err := run(ctx, "/usr/bin/codesign", "--verify", "--strict", "--arch", name, "-R", "="+category.requirement, path)
		if err == nil {
			a.Signature.Category = category.name
			break
		}
		if !isExit(err) {
			return Architecture{}, fmt.Errorf("%w: classify: %w", ErrInspect, err)
		}
	}
	return a, nil
}

// isExit reports whether the error chain contains an exec.ExitError.
func isExit(err error) bool {
	var exit *exec.ExitError
	return errors.As(err, &exit)
}

// parseMetadata extracts supported signing metadata from codesign output.
func parseMetadata(out string) (map[string]string, error) {
	fields := make(map[string]string)
	for line := range strings.SplitSeq(out, "\n") {
		line = strings.TrimSpace(line)
		key, value, ok := strings.Cut(line, "=")
		if dr, found := strings.CutPrefix(line, "designated => "); found {
			key, value, ok = "designated", dr, true
		}
		if !ok {
			continue
		}
		switch key {
		case "CDHash", "Identifier", "TeamIdentifier", "designated":
			if _, duplicate := fields[key]; duplicate {
				return nil, fmt.Errorf("%w: duplicate %s", ErrInspect, key)
			}
			fields[key] = value
		}
	}
	hash, err := hex.DecodeString(fields["CDHash"])
	if err != nil || len(hash) != 20 || fields["Identifier"] == "" {
		return nil, fmt.Errorf("%w: missing or malformed signing identity", ErrInspect)
	}
	return fields, nil
}

type boundedOutput struct {
	buffer   bytes.Buffer
	overflow bool
}

// Write retains subprocess output up to the configured bound and reports overflow.
func (b *boundedOutput) Write(p []byte) (int, error) {
	if len(p) > maxMetadata-b.buffer.Len() {
		b.overflow = true
		return 0, ErrTooLarge
	}
	return b.buffer.Write(p)
}

// String returns the captured subprocess output.
func (b *boundedOutput) String() string { return b.buffer.String() }

// runTool runs an inspection tool with the configured deadline and bounded output capture.
func runTool(ctx context.Context, tool string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, tool, args...) // #nosec G204 -- fixed tool; target is an absolute path and arguments never pass through a shell.
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	cmd.WaitDelay = time.Second
	var output boundedOutput
	cmd.Stdout, cmd.Stderr = &output, &output
	err := cmd.Run()
	if ctx.Err() != nil {
		return output.String(), ctx.Err()
	}
	if output.overflow {
		return output.String(), ErrTooLarge
	}
	return output.String(), err
}
