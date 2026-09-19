package appidentity

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"path"
	"strings"

	"github.com/deploymenttheory/go-macos-pkg/pkg/machoidentity"
	"howett.net/plist"
)

// CodeDirectory preserves one architecture's observed code-directory identity.
// CDHash is the twenty-byte hash used in DDM binary rules. FullHash retains the
// complete digest. HashType identifies Apple's algorithm, including truncation.
type CodeDirectory struct {
	HashType  uint8  `json:"hashType"`
	Algorithm string `json:"algorithm"`
	CDHash    string `json:"cdhash"`
	FullHash  string `json:"fullHash"`
	SigningID string `json:"signingID"`
	TeamID    string `json:"teamID,omitempty"`
	Flags     uint32 `json:"flags"`
	Version   uint32 `json:"version"`
}

// ReadExecutable reads a bounded Mach-O on any platform. Every architecture and
// supported code directory is returned, without signature verification. Signed
// metadata has NotChecked status and Unknown category. CDHash is populated only
// when an architecture has exactly one code directory; callers must select from
// CodeDirectories when multiple algorithms exist. Reader bytes must be immutable.
func ReadExecutable(ctx context.Context, r io.ReaderAt, size int64) (Identity, error) {
	arches, err := machoidentity.Read(ctx, r, size)
	if err != nil {
		return Identity{}, fmt.Errorf("%w: %w", ErrInspect, err)
	}
	id := Identity{}
	for _, source := range arches {
		a := Architecture{Name: source.Name, CPU: source.CPU, Subtype: source.Subtype, Signature: Signature{Status: NotChecked, Category: Unknown}}
		if len(source.CodeDirectories) == 0 {
			a.Signature.Status = Unsigned
		}
		for i, d := range source.CodeDirectories {
			if i == 0 {
				a.SigningID, a.TeamID = d.SigningID, d.TeamID
			}
			if d.SigningID != a.SigningID || d.TeamID != a.TeamID {
				return Identity{}, fmt.Errorf("%w: conflicting code-directory identities", ErrInput)
			}
			a.CodeDirectories = append(a.CodeDirectories, CodeDirectory(d))
		}
		if len(a.CodeDirectories) == 1 {
			a.CDHash = a.CodeDirectories[0].CDHash
		}
		id.Architectures = append(id.Architectures, a)
	}
	return id, nil
}

// ReadBundle reads a macOS application's Info.plist and main Mach-O executable
// from an fs.FS on any platform. bundle is relative to the filesystem root; "."
// selects that root. It does not inspect embedded helpers or execute the app.
// The filesystem must confine reads to the supplied source; archive adapters
// should reject symlinks escaping it. Metadata is bounded to one MiB.
func ReadBundle(ctx context.Context, files fs.FS, bundle string) (Identity, error) {
	if files == nil || !fs.ValidPath(bundle) {
		return Identity{}, ErrInput
	}
	if err := ctx.Err(); err != nil {
		return Identity{}, err
	}
	f, err := files.Open(path.Join(bundle, "Contents/Info.plist"))
	if err != nil {
		return Identity{}, fmt.Errorf("%w: %w", ErrInput, err)
	}
	data, readErr := io.ReadAll(io.LimitReader(f, maxMetadata+1))
	closeErr := f.Close()
	if readErr != nil {
		return Identity{}, fmt.Errorf("%w: %w", ErrInput, readErr)
	}
	if closeErr != nil {
		return Identity{}, fmt.Errorf("%w: %w", ErrInput, closeErr)
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
		return Identity{}, ErrInput
	}
	executable := path.Join(bundle, "Contents/MacOS", meta.Executable)
	f, err = files.Open(executable)
	if err != nil {
		return Identity{}, fmt.Errorf("%w: %w", ErrInput, err)
	}
	defer func() { _ = f.Close() }()
	stat, err := f.Stat()
	if err != nil {
		return Identity{}, fmt.Errorf("%w: %w", ErrInput, err)
	}
	if !stat.Mode().IsRegular() {
		return Identity{}, ErrInput
	}
	r, ok := f.(io.ReaderAt)
	if !ok {
		return Identity{}, fmt.Errorf("%w: executable must support random access", ErrInput)
	}
	id, err := ReadExecutable(ctx, r, stat.Size())
	if err != nil {
		return Identity{}, err
	}
	id.Path, id.Executable = bundle, executable
	id.BundleID, id.Name, id.Version = meta.BundleID, meta.Name, meta.Version
	return id, nil
}
