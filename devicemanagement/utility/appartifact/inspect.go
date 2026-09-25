package appartifact

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/deploymenttheory/go-macos-pkg/pkg/flatpkg"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/fault"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/utility/appidentity"
)

var (
	ErrInvalid     = fault.AppArtifactInvalid
	ErrUnsupported = fault.AppArtifactUnsupported
	ErrLimit       = fault.AppArtifactTooLarge
)

const DefaultMaxBytes int64 = 512 << 20

// Options bounds one inspection. Zero values select the documented defaults:
// 512 MiB per artifact/file, 2 GiB expanded data, 100,000 entries, 128 identities,
// four nested containers, and two minutes. TempDir selects private scratch storage.
type Options struct {
	MaxBytes         int64
	MaxExpandedBytes int64
	MaxEntries       int
	MaxApplications  int
	MaxDepth         int
	Timeout          time.Duration
	TempDir          string
}

// Application associates observed identity with its container-relative location.
type Application struct {
	Location string               `json:"location"`
	Identity appidentity.Identity `json:"identity"`
}

// Issue identifies a candidate that could not be inspected completely.
type Issue struct {
	Location string `json:"location"`
	Reason   string `json:"reason"`
}

// Report describes the exact artifact bytes. Complete means every discovered
// candidate was inspected, not that install-time scripts or downloads were run.
type Report struct {
	SHA256       string        `json:"sha256"`
	Size         int64         `json:"size"`
	Format       string        `json:"format"`
	Complete     bool          `json:"complete"`
	Applications []Application `json:"applications"`
	Issues       []Issue       `json:"issues,omitempty"`
}

type inspector struct {
	ctx      context.Context
	opts     Options
	temp     string
	expanded int64
	entries  int
	report   Report
}

// Inspect reads an immutable local artifact. It returns no report on container,
// I/O, cancellation or limit failures. Per-application parsing failures instead
// appear in Issues, allowing the author to review other candidates explicitly.
func Inspect(ctx context.Context, filename string, opts Options) (Report, error) {
	if opts.MaxBytes == 0 {
		opts.MaxBytes = DefaultMaxBytes
	}
	if opts.MaxExpandedBytes == 0 {
		opts.MaxExpandedBytes = 2 << 30
	}
	if opts.MaxEntries == 0 {
		opts.MaxEntries = 100000
	}
	if opts.MaxApplications == 0 {
		opts.MaxApplications = 128
	}
	if opts.MaxDepth == 0 {
		opts.MaxDepth = 4
	}
	if opts.Timeout == 0 {
		opts.Timeout = 2 * time.Minute
	}
	if opts.MaxBytes < 1 || opts.MaxBytes > 1<<40 || opts.MaxExpandedBytes < 1 || opts.MaxExpandedBytes > 1<<40 || opts.MaxEntries < 1 || opts.MaxApplications < 1 || opts.MaxDepth < 1 || opts.Timeout < 0 {
		return Report{}, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()
	f, err := os.Open(filename) // #nosec G304 -- the caller selects the immutable artifact; HTTP callers supply a private uploaded file.
	if err != nil {
		return Report{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		return Report{}, err
	}
	if !st.Mode().IsRegular() {
		return Report{}, ErrInvalid
	}
	if st.Size() > opts.MaxBytes {
		return Report{}, ErrLimit
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, contextReader{ctx, f}); err != nil {
		return Report{}, err
	}
	temp, err := os.MkdirTemp(opts.TempDir, "dm-app-identity-")
	if err != nil {
		return Report{}, err
	}
	defer func() { _ = os.RemoveAll(temp) }()
	i := inspector{ctx: ctx, opts: opts, temp: temp, report: Report{SHA256: hex.EncodeToString(hash.Sum(nil)), Size: st.Size(), Complete: true, Applications: []Application{}}}
	format, err := i.inspectFile(filename, "artifact", 0)
	if err != nil {
		return Report{}, err
	}
	i.report.Format = format
	return i.report, nil
}

// inspectFile identifies the local artifact format and invokes the corresponding bounded
// reader.
func (i *inspector) inspectFile(filename, location string, depth int) (string, error) {
	if err := i.ctx.Err(); err != nil {
		return "", err
	}
	if depth > i.opts.MaxDepth {
		return "", ErrLimit
	}
	f, err := os.Open(filename) // #nosec G304 -- recursive paths are created inside private scratch storage.
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		return "", err
	}
	if st.Size() > i.opts.MaxBytes {
		return "", ErrLimit
	}
	head := make([]byte, 4)
	if _, err := f.ReadAt(head, 0); err != nil {
		return "", fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	switch {
	case string(head) == "xar!":
		return "pkg", i.pkg(filename, location, depth)
	case string(head) == "PK\x03\x04" || string(head) == "PK\x05\x06":
		return "zip", i.zip(filename, location, depth)
	case isMachO(head):
		id, err := appidentity.ReadExecutable(i.ctx, f, st.Size())
		if err != nil {
			return "", err
		}
		return "macho", i.add(location, id)
	default:
		if st.Size() >= 512 {
			if _, err := f.ReadAt(head, st.Size()-512); err == nil && string(head) == "koly" {
				return "dmg", i.dmg(filename, location, depth)
			}
		}
	}
	return "", ErrUnsupported
}

// isMachO recognizes supported thin and universal Mach-O magic values.
func isMachO(head []byte) bool {
	if len(head) < 4 {
		return false
	}
	switch binary.BigEndian.Uint32(head) {
	case 0xfeedface, 0xfeedfacf, 0xcefaedfe, 0xcffaedfe, 0xcafebabe, 0xcafebabf, 0xbebafeca, 0xbfbafeca:
		return true
	}
	return false
}

// add records a discovered application identity and its artifact-relative provenance.
func (i *inspector) add(location string, id appidentity.Identity) error {
	if len(i.report.Applications) >= i.opts.MaxApplications {
		return ErrLimit
	}
	i.report.Applications = append(i.report.Applications, Application{Location: location, Identity: id})
	return nil
}

// walk visits artifact entries while enforcing depth, entry-count, and expanded-byte
// limits.
func (i *inspector) walk(files fs.FS, location string, depth int) error {
	return fs.WalkDir(files, ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := i.ctx.Err(); err != nil {
			return err
		}
		i.entries++
		if i.entries > i.opts.MaxEntries {
			return ErrLimit
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		if entry.IsDir() {
			if !strings.HasSuffix(strings.ToLower(name), ".app") {
				return nil
			}
			id, err := appidentity.ReadBundle(i.ctx, files, name)
			if err != nil {
				if i.ctx.Err() != nil {
					return i.ctx.Err()
				}
				i.report.Complete = false
				i.report.Issues = append(i.report.Issues, Issue{Location: location + "!" + name, Reason: "application identity could not be read"})
				if len(i.report.Issues) > i.opts.MaxApplications {
					return ErrLimit
				}
			} else if err := i.add(location+"!"+name, id); err != nil {
				return err
			}
			return fs.SkipDir
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		f, err := files.Open(name)
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		head := make([]byte, 4)
		_, err = io.ReadFull(f, head)
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return nil
		}
		if err != nil {
			return err
		}
		info, err := f.Stat()
		if err != nil {
			return err
		}
		if isMachO(head) {
			r, ok := f.(io.ReaderAt)
			if !ok {
				return ErrUnsupported
			}
			id, err := appidentity.ReadExecutable(i.ctx, r, info.Size())
			if err != nil {
				return err
			}
			id.Path, id.Executable = name, name
			return i.add(location+"!"+name, id)
		}
		if string(head) != "xar!" && !strings.HasSuffix(strings.ToLower(name), ".dmg") && !strings.HasSuffix(strings.ToLower(name), ".zip") {
			return nil
		}
		if depth >= i.opts.MaxDepth {
			return ErrLimit
		}
		nested, err := os.CreateTemp(i.temp, "nested-")
		if err != nil {
			return err
		}
		err = i.copy(nested, io.MultiReader(bytes.NewReader(head), f))
		closeErr := nested.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		_, err = i.inspectFile(nested.Name(), location+"!"+name, depth+1)
		return err
	})
}

// copy copies artifact data into owned temporary storage while enforcing size and
// cancellation limits.
func (i *inspector) copy(w io.Writer, r io.Reader) error {
	remaining := i.opts.MaxExpandedBytes - i.expanded
	limit := min(i.opts.MaxBytes, remaining)
	if limit < 0 {
		return ErrLimit
	}
	n, err := io.Copy(w, io.LimitReader(contextReader{i.ctx, r}, limit+1))
	i.expanded += n
	if n > limit {
		return ErrLimit
	}
	return err
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

// Read checks context cancellation before reading from the wrapped artifact stream.
func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

// safeName rejects archive entry names that could escape their extraction root.
func safeName(name string) (string, error) {
	if name == "./" {
		return ".", nil
	}
	name = strings.TrimPrefix(name, "./")
	if name == "." {
		return name, nil
	}
	if !fs.ValidPath(name) || strings.ContainsAny(name, "\\:") {
		return "", ErrInvalid
	}
	return name, nil
}

// writeFile creates an owned extraction file for a validated artifact entry.
func (i *inspector) writeFile(root *os.Root, name string, r io.Reader) error {
	clean, err := safeName(name)
	if err != nil || clean == "." {
		return ErrInvalid
	}
	if err := root.MkdirAll(filepath.FromSlash(path.Dir(clean)), 0o700); err != nil {
		return err
	}
	f, err := root.OpenFile(filepath.FromSlash(clean), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	err = i.copy(f, r)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	return closeErr
}

// pkg inspects package payloads without executing installer scripts.
func (i *inspector) pkg(filename, location string, depth int) error {
	p, err := flatpkg.Open(filename)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	defer func() { _ = p.Close() }()
	for _, component := range p.Components {
		i.entries++
		if i.entries > i.opts.MaxEntries {
			return ErrLimit
		}
		if component.HasScripts() {
			i.report.Complete = false
			i.report.Issues = append(i.report.Issues, Issue{Location: location + "!" + component.Name, Reason: "installer scripts were not executed; results describe embedded payloads"})
			if len(i.report.Issues) > i.opts.MaxApplications {
				return ErrLimit
			}
		}
		if !component.HasPayload() {
			continue
		}
		if err := i.component(component, location, depth); err != nil {
			return err
		}
	}
	return nil
}

// component inspects a package component and accumulates its discovered application
// identities.
func (i *inspector) component(component *flatpkg.Component, location string, depth int) error {
	r, err := component.OpenPayload()
	if err != nil {
		return err
	}
	defer func() { _ = r.Close() }()
	cr, _, err := flatpkg.OpenCPIO(contextReader{i.ctx, r})
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp(i.temp, "payload-")
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	for {
		if err := i.ctx.Err(); err != nil {
			return err
		}
		h, err := cr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		i.entries++
		if i.entries > i.opts.MaxEntries {
			return ErrLimit
		}
		if _, err := safeName(h.Name); err != nil {
			return err
		}
		// AppleDouble sidecars carry filesystem metadata, including a directory's
		// link count. They are not application executables or bundle plists.
		if h.Mode&0o170000 != 0o100000 || strings.HasPrefix(path.Base(h.Name), "._") {
			if err := i.copy(io.Discard, cr); err != nil {
				return err
			}
			continue
		}
		if h.NLink > 1 {
			return ErrUnsupported
		}
		if err := i.writeFile(root, h.Name, cr); err != nil {
			return err
		}
	}
	return i.walk(root.FS(), location+"!"+component.Name, depth)
}

// zip walks ZIP members while enforcing extraction and nested-container limits.
func (i *inspector) zip(filename, location string, depth int) error {
	z, err := zip.OpenReader(filename)
	if err != nil {
		return err
	}
	defer func() { _ = z.Close() }()
	dir, err := os.MkdirTemp(i.temp, "zip-")
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	for _, file := range z.File {
		i.entries++
		if i.entries > i.opts.MaxEntries {
			return ErrLimit
		}
		if _, err := safeName(strings.TrimSuffix(file.Name, "/")); err != nil {
			return err
		}
		if !file.Mode().IsRegular() {
			continue
		}
		if file.UncompressedSize64 > uint64(i.opts.MaxBytes) { // #nosec G115 -- Inspect validates a positive MaxBytes at or below one TiB.
			return ErrLimit
		}
		r, err := file.Open()
		if err != nil {
			return err
		}
		err = i.writeFile(root, file.Name, r)
		closeErr := r.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return i.walk(root.FS(), location, depth)
}
