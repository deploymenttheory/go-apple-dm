package appidentity

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPortableFixtureMatchesAppleTools(t *testing.T) {
	f, err := os.Open("testdata/fixture.macho")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	id, err := ReadExecutable(t.Context(), f, st.Size())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"arm64": "7fe0792159dba657a8dce283963273c74e35cba8", "x86_64": "c167705737e365fa79b9bff6927b7594bed5a190"}
	if len(id.Architectures) != len(want) {
		t.Fatal(id)
	}
	for _, a := range id.Architectures {
		if a.CDHash != want[a.Name] || a.SigningID != "com.deploymenttheory.identity.fixture" || a.TeamID != "" || a.Signature.Status != NotChecked || a.Signature.Category != Unknown || len(a.CodeDirectories) != 1 {
			t.Fatal(a)
		}
		if a.CodeDirectories[0].Algorithm != "sha256" || len(a.CodeDirectories[0].FullHash) != 64 {
			t.Fatal(a)
		}
	}
	root := bundle(t, bundlePlist)
	b, err := os.ReadFile("testdata/fixture.macho")
	if err != nil {
		t.Fatal(err)
	}
	fixture(t, filepath.Join(root, "Contents/MacOS/Example"), b)
	id, err = ReadBundle(t.Context(), os.DirFS(root), ".")
	if err != nil || id.BundleID != "com.example.bundle" || id.Name != "Example" || id.Version != "1.2" || id.Executable != "Contents/MacOS/Example" {
		t.Fatal(id, err)
	}
}

func portableImage(ids ...string) []byte {
	var dirs [][]byte
	for _, id := range ids {
		b := make([]byte, 96)
		be := binary.BigEndian
		be.PutUint32(b, 0xfade0c02)
		be.PutUint32(b[4:], 96)
		be.PutUint32(b[8:], 0x20200)
		be.PutUint32(b[16:], 96)
		be.PutUint32(b[20:], 52)
		b[36] = 32
		b[37] = 2
		copy(b[52:], id)
		dirs = append(dirs, b)
	}
	b := make([]byte, 32)
	le := binary.LittleEndian
	le.PutUint32(b, 0xfeedfacf)
	le.PutUint32(b[4:], 0x100000c)
	if len(dirs) == 0 {
		return b
	}
	le.PutUint32(b[16:], 1)
	le.PutUint32(b[20:], 16)
	cmd := make([]byte, 16)
	le.PutUint32(cmd, 0x1d)
	le.PutUint32(cmd[4:], 16)
	le.PutUint32(cmd[8:], 48)
	super := make([]byte, 12+len(dirs)*104)
	be := binary.BigEndian
	be.PutUint32(super, 0xfade0cc0)
	be.PutUint32(super[4:], uint32(len(super))) // #nosec G115 -- small test-owned code directories.
	be.PutUint32(super[8:], uint32(len(dirs)))  // #nosec G115 -- small test-owned code directories.
	off := 12 + len(dirs)*8
	for n, d := range dirs {
		kind := uint32(0)
		if n > 0 {
			kind = 0x1000 + uint32(n) - 1
		}
		be.PutUint32(super[12+n*8:], kind)
		be.PutUint32(super[16+n*8:], uint32(off)) // #nosec G115 -- offset within the small test-owned buffer.
		copy(super[off:], d)
		off += len(d)
	}
	le.PutUint32(cmd[12:], uint32(len(super))) // #nosec G115 -- small test-owned signature buffer.
	return bytes.Join([][]byte{b, cmd, super}, nil)
}

func TestPortableUnsignedMultipleDirectoriesAndErrors(t *testing.T) {
	for _, ids := range [][]string{nil, {"com.example.same", "com.example.same"}} {
		b := portableImage(ids...)
		id, err := ReadExecutable(t.Context(), bytes.NewReader(b), int64(len(b)))
		if err != nil || len(id.Architectures) != 1 || id.Architectures[0].CDHash != "" {
			t.Fatal(id, err)
		}
		if ids == nil && id.Architectures[0].Signature.Status != Unsigned {
			t.Fatal(id)
		}
	}
	b := portableImage("com.example.one", "com.example.two")
	if _, err := ReadExecutable(t.Context(), bytes.NewReader(b), int64(len(b))); !errors.Is(err, ErrInput) {
		t.Fatal(err)
	}
	if _, err := ReadExecutable(t.Context(), bytes.NewReader(nil), 0); !errors.Is(err, ErrInspect) {
		t.Fatal(err)
	}
}

type portableFS struct{ open func(string) (fs.File, error) }

func (f portableFS) Open(name string) (fs.File, error) { return f.open(name) }

type portableFile struct {
	fs.File
	readErr, statErr, closeErr error
}

func (f portableFile) Read(p []byte) (int, error) {
	if f.readErr != nil {
		return 0, f.readErr
	}
	return f.File.Read(p)
}

func (f portableFile) Stat() (fs.FileInfo, error) {
	if f.statErr != nil {
		return nil, f.statErr
	}
	return f.File.Stat()
}
func (f portableFile) Close() error { _ = f.File.Close(); return f.closeErr }

func TestPortableBundleFailures(t *testing.T) {
	if _, err := ReadBundle(t.Context(), nil, "."); !errors.Is(err, ErrInput) {
		t.Fatal(err)
	}
	if _, err := ReadBundle(t.Context(), os.DirFS(t.TempDir()), "../escape"); !errors.Is(err, ErrInput) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := ReadBundle(ctx, os.DirFS(t.TempDir()), "."); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	for _, meta := range []string{"", "invalid", strings.Repeat("x", maxMetadata+1), strings.ReplaceAll(bundlePlist, "Example</string>", "../Example</string>"), strings.ReplaceAll(bundlePlist, "com.example.bundle", "")} {
		root := bundle(t, meta)
		if _, err := ReadBundle(t.Context(), os.DirFS(root), "."); err == nil {
			t.Fatal("accepted invalid metadata")
		}
	}
	root := bundle(t, bundlePlist)
	files := os.DirFS(root)
	for _, target := range []string{"Contents/Info.plist", "Contents/MacOS/Example"} {
		for _, fault := range []string{"open", "read", "close", "stat", "reader-at"} {
			if target == "Contents/Info.plist" && (fault == "stat" || fault == "reader-at") {
				continue
			}
			if target != "Contents/Info.plist" && (fault == "read" || fault == "close") {
				continue
			}
			t.Run(target+"/"+fault, func(t *testing.T) {
				broken := portableFS{open: func(name string) (fs.File, error) {
					if name == target && fault == "open" {
						return nil, os.ErrPermission
					}
					f, err := files.Open(name)
					if err != nil {
						return nil, err
					}
					if name != target {
						return f, nil
					}
					v := portableFile{File: f}
					switch fault {
					case "read":
						v.readErr = io.ErrUnexpectedEOF
					case "close":
						v.closeErr = os.ErrPermission
					case "stat":
						v.statErr = os.ErrPermission
					}
					return v, nil
				}}
				if _, err := ReadBundle(t.Context(), broken, "."); err == nil {
					t.Fatal("ignored file failure")
				}
			})
		}
	}
	if _, err := ReadBundle(t.Context(), files, "."); err == nil {
		t.Fatal("invalid executable accepted")
	}
	if err := os.Remove(filepath.Join(root, "Contents/MacOS/Example")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "Contents/MacOS/Example"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadBundle(t.Context(), files, "."); !errors.Is(err, ErrInput) {
		t.Fatal(err)
	}
}
