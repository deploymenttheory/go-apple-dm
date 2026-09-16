package recovery

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"filippo.io/age"

	"github.com/deploymenttheory/go-apple-dm/server/internal/privatefile"
)

var (
	ErrInvalid  = errors.New("recovery: invalid checkpoint")
	ErrOccupied = errors.New("recovery: target already exists")
	ErrLimit    = errors.New("recovery: checkpoint exceeds limits")
)

// Metadata identifies the state being recovered. It must contain no secrets.
type Metadata struct {
	Backend   string    `json:"backend"`
	PublicURL string    `json:"publicUrl"`
	Revision  string    `json:"revision"`
	CreatedAt time.Time `json:"createdAt"`
}

type Entry struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type Manifest struct {
	Version  int      `json:"version"`
	Metadata Metadata `json:"metadata"`
	Entries  []Entry  `json:"entries"`
}

// Limits bound decrypted data, including an archive supplied by another party.
// Defaults permit 8 GiB and 10,000 files. Files stream through private staging.
type Limits struct {
	Bytes int64
	Files int
}

func (l Limits) defaults() Limits {
	if l.Bytes <= 0 {
		l.Bytes = 8 << 30
	}
	if l.Files <= 0 {
		l.Files = 10000
	}
	return l
}

func validName(name string) bool {
	if name == "" || len(name) > 1024 || path.Clean(name) != name || strings.HasPrefix(name, "/") {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
		for _, c := range part {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.') {
				return false
			}
		}
	}
	return true
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}

// Create seals the regular files beneath stage. The caller must first fence and
// drain writers and produce a consistent database snapshot. Symlinks, changing
// inputs and overwriting an archive are rejected. Publication uses a hard link
// so another process cannot have its output replaced by a racing creation.
func Create(
	ctx context.Context,
	destination, stage string,
	metadata Metadata,
	recipients []age.Recipient,
	limits Limits,
) (Manifest, error) {
	var manifest Manifest
	if len(recipients) == 0 || metadata.PublicURL == "" || metadata.Revision == "" {
		return manifest, ErrInvalid
	}
	if metadata.Backend != "sqlite" && metadata.Backend != "postgres" &&
		metadata.Backend != "mysql" {
		return manifest, ErrInvalid
	}
	limits = limits.defaults()
	entries, err := inventory(ctx, stage, limits)
	if err != nil {
		return manifest, err
	}
	if metadata.CreatedAt.IsZero() {
		metadata.CreatedAt = time.Now().UTC()
	}
	manifest = Manifest{Version: 1, Metadata: metadata, Entries: entries}
	raw, err := json.Marshal(manifest)
	if err != nil {
		return manifest, fmt.Errorf("recovery: manifest: %w", err)
	}
	if len(raw) > 4<<20 {
		return manifest, ErrLimit
	}
	out, err := privatefile.CreateTemp(filepath.Dir(destination), ".recovery-*.age")
	if err != nil {
		return manifest, fmt.Errorf("recovery: temporary archive: %w", err)
	}
	defer func() { _ = os.Remove(out.Name()) }()
	defer func(cleanup func() error) { _ = cleanup() }(out.Close)
	sealed, err := age.Encrypt(out, recipients...)
	if err != nil {
		return manifest, fmt.Errorf("recovery: encrypt: %w", err)
	}
	tarball := tar.NewWriter(sealed)
	if err = tarball.WriteHeader(
		&tar.Header{Name: "manifest.json", Mode: 0o600, Size: int64(len(raw))},
	); err == nil {
		_, err = tarball.Write(raw)
	}
	if err == nil {
		err = writeEntries(ctx, tarball, stage, entries)
	}
	if err == nil {
		err = tarball.Close()
	}
	if err == nil {
		err = sealed.Close()
	}
	if err == nil {
		err = out.Sync()
	}
	if err != nil {
		return manifest, fmt.Errorf("recovery: write archive: %w", err)
	}
	if err = out.Close(); err != nil {
		return manifest, fmt.Errorf("recovery: close archive: %w", err)
	}
	if err = os.Link(out.Name(), destination); errors.Is(err, fs.ErrExist) {
		return manifest, ErrOccupied
	}
	if err != nil {
		return manifest, fmt.Errorf("recovery: publish archive: %w", err)
	}
	return manifest, syncDirectory(filepath.Dir(destination))
}

func inventory(ctx context.Context, stage string, limits Limits) ([]Entry, error) {
	root, err := os.OpenRoot(stage)
	if err != nil {
		return nil, fmt.Errorf("recovery: stage: %w", err)
	}
	defer func(cleanup func() error) { _ = cleanup() }(root.Close)
	entries := []Entry{}
	var total int64
	err = fs.WalkDir(root.FS(), ".", func(name string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if name == "." {
			return nil
		}
		if !validName(name) || name == "manifest.json" || d.Type()&os.ModeSymlink != 0 {
			return ErrInvalid
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return ErrInvalid
		}
		total += info.Size()
		if total > limits.Bytes || len(entries) >= limits.Files {
			return ErrLimit
		}
		f, err := root.Open(name)
		if err != nil {
			return err
		}
		h := sha256.New()
		n, copyErr := io.Copy(h, contextReader{ctx, io.LimitReader(f, info.Size()+1)})
		closeErr := f.Close()
		if copyErr != nil || closeErr != nil {
			return errors.Join(copyErr, closeErr)
		}
		if n != info.Size() {
			return ErrInvalid
		}
		entries = append(
			entries,
			Entry{Name: name, Size: n, SHA256: hex.EncodeToString(h.Sum(nil))},
		)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("recovery: inventory: %w", err)
	}
	if len(entries) == 0 {
		return nil, ErrInvalid
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	return entries, nil
}

func writeEntries(ctx context.Context, archive *tar.Writer, stage string, entries []Entry) error {
	root, err := os.OpenRoot(stage)
	if err != nil {
		return fmt.Errorf("recovery: stage: %w", err)
	}
	defer func(cleanup func() error) { _ = cleanup() }(root.Close)
	for _, entry := range entries {
		info, err := root.Lstat(entry.Name)
		if err != nil {
			return fmt.Errorf("recovery: source: %w", err)
		}
		if !info.Mode().IsRegular() || info.Size() != entry.Size {
			return ErrInvalid
		}
		if err = archive.WriteHeader(
			&tar.Header{Name: entry.Name, Mode: 0o600, Size: entry.Size},
		); err != nil {
			return fmt.Errorf("recovery: entry: %w", err)
		}
		f, err := root.Open(entry.Name)
		if err != nil {
			return fmt.Errorf("recovery: source: %w", err)
		}
		h := sha256.New()
		n, copyErr := io.Copy(
			io.MultiWriter(archive, h),
			contextReader{ctx, io.LimitReader(f, entry.Size+1)},
		)
		closeErr := f.Close()
		if copyErr != nil || closeErr != nil {
			return fmt.Errorf("recovery: copy: %w", errors.Join(copyErr, closeErr))
		}
		if n != entry.Size || hex.EncodeToString(h.Sum(nil)) != entry.SHA256 {
			return ErrInvalid
		}
	}
	return nil
}

// Verified owns an isolated, authenticated copy. Close removes the private
// staging directory. Callers can validate database state and keys using Directory
// before publishing it with RestoreFiles. Keep the directory private throughout.
type Verified struct {
	Manifest  Manifest
	directory string
}

func (v *Verified) Directory() string { return v.directory }

func (v *Verified) Close() error {
	if v.directory == "" {
		return nil
	}
	err := os.RemoveAll(v.directory)
	if err == nil {
		v.directory = ""
	}
	return wrap(err)
}

// Verify authenticates the complete age stream, validates the manifest and every
// entry, and only then returns access to the isolated files. A failed operation
// removes its staging directory. The caller chooses parent to control where
// temporary private material is stored.
func Verify(
	ctx context.Context,
	source, parent string,
	identities []age.Identity,
	limits Limits,
) (_ *Verified, err error) {
	if len(identities) == 0 {
		return nil, ErrInvalid
	}
	limits = limits.defaults()
	f, err := openLocal(source, os.O_RDONLY, 0)
	if err != nil {
		return nil, wrap(err)
	}
	defer func(cleanup func() error) { _ = cleanup() }(f.Close)
	plaintext, err := age.Decrypt(contextReader{ctx, f}, identities...)
	if err != nil {
		return nil, fmt.Errorf("recovery: decrypt: %w", err)
	}
	dir, err := os.MkdirTemp(parent, ".recovery-verify-")
	if err != nil {
		return nil, wrap(err)
	}
	v := &Verified{directory: dir}
	defer func() {
		if err != nil {
			_ = v.Close()
		}
	}()
	archive := tar.NewReader(plaintext)
	first, err := archive.Next()
	if err != nil {
		return nil, wrap(err)
	}
	if first.Name != "manifest.json" || first.Typeflag != tar.TypeReg || first.Size < 0 ||
		first.Size > 4<<20 {
		return nil, ErrInvalid
	}
	decoder := json.NewDecoder(archive)
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&v.Manifest); err != nil {
		return nil, wrap(err)
	}
	if err = decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return nil, ErrInvalid
	}
	if err = validateManifest(v.Manifest, limits); err != nil {
		return nil, err
	}
	if err = extract(ctx, archive, v); err != nil {
		return nil, err
	}
	// tar EOF is not age EOF. Authentication of the final age chunk is required
	// even when a truncated/tampered archive already yielded every tar member.
	trailing, err := io.ReadAll(io.LimitReader(plaintext, 1))
	if err != nil {
		return nil, wrap(err)
	}
	if len(trailing) != 0 {
		return nil, ErrInvalid
	}
	return v, nil
}

func validateManifest(m Manifest, limits Limits) error {
	if m.Version != 1 || m.Metadata.PublicURL == "" || m.Metadata.Revision == "" ||
		m.Metadata.CreatedAt.IsZero() {
		return ErrInvalid
	}
	if m.Metadata.Backend != "sqlite" && m.Metadata.Backend != "postgres" &&
		m.Metadata.Backend != "mysql" {
		return ErrInvalid
	}
	if len(m.Entries) == 0 || len(m.Entries) > limits.Files {
		return ErrLimit
	}
	seen := map[string]bool{}
	var total int64
	for _, entry := range m.Entries {
		if !validName(entry.Name) || entry.Name == "manifest.json" || seen[entry.Name] ||
			entry.Size < 0 {
			return ErrInvalid
		}
		decoded, err := hex.DecodeString(entry.SHA256)
		if err != nil || len(decoded) != sha256.Size {
			return ErrInvalid
		}
		if entry.Size > limits.Bytes-total {
			return ErrLimit
		}
		total += entry.Size
		seen[entry.Name] = true
	}
	return nil
}

func extract(ctx context.Context, archive *tar.Reader, v *Verified) error {
	for _, entry := range v.Manifest.Entries {
		header, err := archive.Next()
		if err != nil {
			return wrap(err)
		}
		if header.Name != entry.Name || header.Size != entry.Size ||
			header.Typeflag != tar.TypeReg {
			return ErrInvalid
		}
		name := filepath.Join(v.directory, filepath.FromSlash(entry.Name))
		if err = os.MkdirAll(filepath.Dir(name), 0o700); err != nil {
			return wrap(err)
		}
		out, err := openLocal(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return wrap(err)
		}
		h := sha256.New()
		n, copyErr := io.Copy(io.MultiWriter(out, h), contextReader{ctx, archive})
		closeErr := out.Close()
		if copyErr != nil || closeErr != nil {
			return wrap(errors.Join(copyErr, closeErr))
		}
		if n != entry.Size || hex.EncodeToString(h.Sum(nil)) != entry.SHA256 {
			return ErrInvalid
		}
	}
	if _, err := archive.Next(); !errors.Is(err, io.EOF) {
		return ErrInvalid
	}
	return nil
}

// RestoreFiles publishes verified files into a previously absent directory.
// It never replaces another deployment. Database adapters additionally require
// an empty database before applying the checkpoint's rows.
func (v *Verified) RestoreFiles(destination string) error {
	if v.directory == "" {
		return ErrInvalid
	}
	if _, err := os.Lstat(destination); err == nil {
		return ErrOccupied
	} else if !errors.Is(err, fs.ErrNotExist) {
		return wrap(err)
	}
	// Reserving the destination prevents rename from replacing a concurrent
	// directory. Copy only authenticated, regular entries into that reservation.
	if err := os.Mkdir(destination, 0o700); errors.Is(err, fs.ErrExist) {
		return ErrOccupied
	} else if err != nil {
		return wrap(err)
	}
	for _, entry := range v.Manifest.Entries {
		source := filepath.Join(v.directory, filepath.FromSlash(entry.Name))
		target := filepath.Join(destination, filepath.FromSlash(entry.Name))
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return wrap(err)
		}
		if err := copyVerified(source, target, entry); err != nil {
			return err
		}
	}
	return syncDirectory(destination)
}

func copyVerified(source, destination string, entry Entry) error {
	f, err := openLocal(source, os.O_RDONLY, 0)
	if err != nil {
		return wrap(err)
	}
	defer func(cleanup func() error) { _ = cleanup() }(f.Close)
	out, err := openLocal(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return wrap(err)
	}
	defer func(cleanup func() error) { _ = cleanup() }(out.Close)
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(out, h), io.LimitReader(f, entry.Size+1))
	if err != nil {
		return wrap(err)
	}
	if n != entry.Size || hex.EncodeToString(h.Sum(nil)) != entry.SHA256 {
		return ErrInvalid
	}
	return wrap(out.Sync())
}

func syncDirectory(directory string) error {
	// #nosec G304 -- Operator-selected publication directory; sync does not read its contents.
	f, err := os.Open(directory)
	if err != nil {
		return wrap(err)
	}
	defer func(cleanup func() error) { _ = cleanup() }(f.Close)
	return wrap(privatefile.SyncDirectory(f))
}

func wrap(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("recovery: %w", err)
}
