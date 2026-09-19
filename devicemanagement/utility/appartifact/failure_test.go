package appartifact

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/deploymenttheory/go-apfs-v2/pkg/disk"
	"github.com/deploymenttheory/go-macos-pkg/pkg/cpio"
	"github.com/deploymenttheory/go-macos-pkg/pkg/flatpkg"
	"github.com/deploymenttheory/go-macos-pkg/pkg/xar"
)

func testInspector(t *testing.T) *inspector {
	t.Helper()
	return &inspector{ctx: t.Context(), temp: t.TempDir(), opts: Options{MaxBytes: 1 << 20, MaxExpandedBytes: 64 << 20, MaxEntries: 100, MaxApplications: 5, MaxDepth: 2}, report: Report{Complete: true}}
}

type failingFS struct {
	fs.FS
	name, operation string
	cancel          context.CancelFunc
}

func (f failingFS) Open(name string) (fs.File, error) {
	if name == f.name {
		if f.cancel != nil {
			f.cancel()
		}
		if f.operation == "open" {
			return nil, fs.ErrPermission
		}
	}
	r, err := f.FS.Open(name)
	if err == nil && name == f.name {
		return failingFile{File: r, operation: f.operation}, nil
	}
	return r, err
}

type failingFile struct {
	fs.File
	operation string
}

func (f failingFile) Read(b []byte) (int, error) {
	if f.operation == "read" {
		return 0, fs.ErrPermission
	}
	return f.File.Read(b)
}

func (f failingFile) Stat() (fs.FileInfo, error) {
	if f.operation == "stat" {
		return nil, fs.ErrPermission
	}
	return f.File.Stat()
}

func TestWalkFailuresAndStandaloneBinaries(t *testing.T) {
	files := fstest.MapFS{"bin": &fstest.MapFile{Data: executable(t)}}
	i := testInspector(t)
	if err := i.walk(files, "artifact", 0); err != nil || len(i.report.Applications) != 1 {
		t.Fatal(i.report, err)
	}
	for _, operation := range []string{"open", "read", "stat", "readerat"} {
		i := testInspector(t)
		if err := i.walk(failingFS{FS: files, name: "bin", operation: operation}, "artifact", 0); err == nil {
			t.Fatal(operation)
		}
	}
	for _, files := range []fstest.MapFS{
		{"bad": &fstest.MapFile{Data: []byte{0xfe, 0xed, 0xfa, 0xcf}}},
		{"A.app/x": &fstest.MapFile{}, "B.app/x": &fstest.MapFile{}},
	} {
		i := testInspector(t)
		i.opts.MaxApplications = 1
		if err := i.walk(files, "artifact", 0); err == nil {
			t.Fatal("accepted invalid candidates")
		}
	}
	for _, name := range []string{".", "Fixture.app/Contents/Info.plist"} {
		i := testInspector(t)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		i.ctx = ctx
		files := fstest.MapFS{"Fixture.app/Contents/Info.plist": &fstest.MapFile{Data: []byte(appPlist)}}
		if err := i.walk(failingFS{FS: files, name: name, operation: "open", cancel: cancel}, "artifact", 0); err == nil {
			t.Fatal("ignored filesystem failure")
		}
	}
	i = testInspector(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	i.ctx = ctx
	if err := i.walk(files, "artifact", 0); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	i = testInspector(t)
	i.opts.MaxEntries = 1
	if err := i.walk(files, "artifact", 0); !errors.Is(err, ErrLimit) {
		t.Fatal(err)
	}
	i = testInspector(t)
	if err := i.walk(fstest.MapFS{"link": &fstest.MapFile{Mode: fs.ModeSymlink, Data: []byte("/etc/passwd")}, "pipe": &fstest.MapFile{Mode: fs.ModeNamedPipe}}, "artifact", 0); err != nil || len(i.report.Applications) != 0 {
		t.Fatal(err)
	}
}

func TestNestedLimitsAndScratchFailures(t *testing.T) {
	files := fstest.MapFS{"nested.zip": &fstest.MapFile{Data: zipFixture(t)}}
	for _, mode := range []string{"depth", "scratch", "expanded"} {
		i := testInspector(t)
		switch mode {
		case "depth":
			i.opts.MaxDepth = 0
		case "scratch":
			i.temp = filepath.Join(i.temp, "missing")
		case "expanded":
			i.opts.MaxExpandedBytes = 3
		}
		if err := i.walk(files, "artifact", 0); err == nil {
			t.Fatal(mode)
		}
	}
	i := testInspector(t)
	if _, err := i.inspectFile("missing", "artifact", 0); err == nil {
		t.Fatal("missing accepted")
	}
	file := put(t, t.TempDir(), "macho", executable(t))
	i.opts.MaxBytes = 1
	if _, err := i.inspectFile(file, "artifact", 0); !errors.Is(err, ErrLimit) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	i.ctx = ctx
	if _, err := i.inspectFile(file, "artifact", 0); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	i = testInspector(t)
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	for _, name := range []string{".", "../escape"} {
		if err := i.writeFile(root, name, bytes.NewReader(nil)); !errors.Is(err, ErrInvalid) {
			t.Fatal(err)
		}
	}
	if err := i.writeFile(root, "one", bytes.NewReader(nil)); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"one", "one/child"} {
		if err := i.writeFile(root, name, bytes.NewReader(nil)); err == nil {
			t.Fatal("collision ignored", name)
		}
	}
}

func rawPackage(t *testing.T, payload []byte, scripts bool) string {
	t.Helper()
	var out bytes.Buffer
	w, err := xar.NewWriter(&out, xar.WriterOptions{})
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{"PackageInfo": []byte(`<pkg-info identifier="com.example.test" version="1" install-location="/Applications"/>`)}
	if payload != nil {
		files["Payload"] = payload
	}
	if scripts {
		files["Scripts"] = []byte("not executed")
	}
	for name, b := range files {
		if err := w.AddFile(name, xar.FileHeader{}, "application/octet-stream", bytes.NewReader(b)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return put(t, t.TempDir(), "input.pkg", out.Bytes())
}

func cpioPayload(t *testing.T, name string, mode uint32, data []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	w := cpio.NewWriter(&out)
	if err := w.WriteHeader(&cpio.Header{Name: name, Mode: mode, Size: int64(len(data))}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestPackagePayloadFailures(t *testing.T) {
	if _, err := Inspect(t.Context(), rawPackage(t, cpioPayload(t, "./", 0o40755, nil), false), Options{}); err != nil {
		t.Fatal("valid root directory rejected", err)
	}
	for _, payload := range [][]byte{[]byte("invalid payload"), []byte("070707invalid"), cpioPayload(t, "../escape", 0o100644, []byte("bad")), cpioPayload(t, ".", 0o100644, nil)} {
		if _, err := Inspect(t.Context(), rawPackage(t, payload, false), Options{}); err == nil {
			t.Fatal("bad payload accepted")
		}
	}
	if r, err := Inspect(t.Context(), rawPackage(t, nil, true), Options{}); err != nil || r.Complete || len(r.Applications) != 0 {
		t.Fatal(r, err)
	}
	payload := cpioPayload(t, "link", 0o120777, bytes.Repeat([]byte("x"), 100))
	if _, err := Inspect(t.Context(), rawPackage(t, payload, false), Options{MaxExpandedBytes: 1}); !errors.Is(err, ErrLimit) {
		t.Fatal(err)
	}
	file := put(t, t.TempDir(), "fixture.pkg", packageFixture(t, tree(t), false, flatpkg.CompressionGzip))
	for _, opts := range []Options{{MaxEntries: 1}, {MaxEntries: 2}, {MaxExpandedBytes: 1}} {
		if _, err := Inspect(t.Context(), file, opts); !errors.Is(err, ErrLimit) {
			t.Fatal(opts, err)
		}
	}
	i := testInspector(t)
	if err := i.component(&flatpkg.Component{}, "artifact", 0); err == nil {
		t.Fatal("missing payload accepted")
	}
	i.temp = filepath.Join(i.temp, "missing")
	if err := i.pkg(file, "artifact", 0); err == nil {
		t.Fatal("scratch failure ignored")
	}
	i = testInspector(t)
	i.entries = i.opts.MaxEntries
	if err := i.pkg(file, "artifact", 1); !errors.Is(err, ErrLimit) {
		t.Fatal("nested package reset entry budget", err)
	}
	i = testInspector(t)
	i.report.Issues = make([]Issue, i.opts.MaxApplications)
	if err := i.pkg(rawPackage(t, nil, true), "artifact", 1); !errors.Is(err, ErrLimit) {
		t.Fatal("nested package reset issue budget", err)
	}
	var linked bytes.Buffer
	w := cpio.NewWriter(&linked)
	if err := w.WriteHeader(&cpio.Header{Name: "link", Mode: 0o100644, NLink: 2}); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Inspect(t.Context(), rawPackage(t, linked.Bytes(), false), Options{}); !errors.Is(err, ErrUnsupported) {
		t.Fatal("hard-link semantics silently discarded", err)
	}
}

func TestZIPSpecialEntriesAndFailure(t *testing.T) {
	var out bytes.Buffer
	z := zip.NewWriter(&out)
	for _, mode := range []fs.FileMode{fs.ModeDir | 0o700, fs.ModeSymlink | 0o700} {
		h := &zip.FileHeader{Name: mode.String()}
		h.SetMode(mode)
		if _, err := z.CreateHeader(h); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Inspect(t.Context(), put(t, t.TempDir(), "special.zip", out.Bytes()), Options{}); err != nil {
		t.Fatal(err)
	}
	b := makeZIP(t, map[string][]byte{"big": bytes.Repeat([]byte("a"), 4096)})
	file := put(t, t.TempDir(), "big.zip", b)
	if _, err := Inspect(t.Context(), file, Options{MaxBytes: 1024}); !errors.Is(err, ErrLimit) {
		t.Fatal(err)
	}
	i := testInspector(t)
	i.temp = filepath.Join(i.temp, "missing")
	if err := i.zip(file, "artifact", 0); err == nil {
		t.Fatal("scratch failure ignored")
	}
	// The central directory advertises an unsupported compression algorithm.
	for offset := 0; offset+12 < len(b); offset++ {
		if bytes.Equal(b[offset:offset+4], []byte("PK\x01\x02")) {
			b[offset+10] = 255
			b[offset+11] = 255
		}
	}
	if _, err := Inspect(t.Context(), put(t, t.TempDir(), "unsupported.zip", b), Options{}); err == nil {
		t.Fatal("unsupported compression accepted")
	}
}

func TestMalformedDMGFilesystems(t *testing.T) {
	for _, kind := range []string{"unknown", "apfs", "hfs", "short"} {
		b := make([]byte, 4096)
		switch kind {
		case "apfs":
			copy(b[32:], "NXSB")
		case "hfs":
			copy(b[1024:], "H+")
		case "short":
			b = b[:512]
		}
		file := filepath.Join(t.TempDir(), "bad.dmg")
		if err := disk.WrapRawImageDMGFrom(file, bytes.NewReader(b), int64(len(b)), "Apple_APFS", nil); err != nil {
			t.Fatal(err)
		}
		if _, err := Inspect(t.Context(), file, Options{}); err == nil {
			t.Fatal(kind)
		}
	}
	if _, err := Inspect(t.Context(), put(t, t.TempDir(), "bad.dmg", append([]byte("koly"), make([]byte, 508)...)), Options{}); err == nil {
		t.Fatal("bad footer accepted")
	}
	file := imageFixture(t, tree(t), "APFS")
	if _, err := Inspect(t.Context(), file, Options{MaxEntries: 1}); !errors.Is(err, ErrLimit) {
		t.Fatal(err)
	}
	if _, err := Inspect(t.Context(), file, Options{MaxExpandedBytes: 1024}); err == nil {
		t.Fatal("image limit ignored")
	}
}

var _ io.Reader = failingFile{}
