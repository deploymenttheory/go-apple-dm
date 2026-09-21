package appartifact

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apfs-v2/pkg/apfswrite"
	"github.com/deploymenttheory/go-apfs-v2/pkg/disk"
	"github.com/deploymenttheory/go-apfs-v2/pkg/hfsplus"
	"github.com/deploymenttheory/go-macos-pkg/pkg/flatpkg"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/utility/appidentity"
)

const appPlist = `<?xml version="1.0"?><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>com.deploymenttheory.identity.fixture</string><key>CFBundleExecutable</key><string>Fixture</string><key>CFBundleName</key><string>Fixture</string><key>CFBundleShortVersionString</key><string>1.0</string></dict></plist>`

// put creates parent directories and writes a fixture file, returning its path.
func put(t *testing.T, root, name string, b []byte) string {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// executable loads the signed Mach-O fixture used by artifact inspection tests.
func executable(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("../appidentity/testdata/fixture.macho")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// tree creates a temporary application bundle containing metadata and the Mach-O fixture.
func tree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	put(t, root, "Fixture.app/Contents/Info.plist", []byte(appPlist))
	put(t, root, "Fixture.app/Contents/MacOS/Fixture", executable(t))
	return root
}

// makeZIP encodes the supplied files as a ZIP archive.
func makeZIP(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var out bytes.Buffer
	z := zip.NewWriter(&out)
	for name, b := range files {
		w, err := z.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(b); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// zipFixture builds a ZIP containing the application metadata and Mach-O fixture.
func zipFixture(t *testing.T) []byte {
	t.Helper()
	return makeZIP(t, map[string][]byte{"Fixture.app/Contents/Info.plist": []byte(appPlist), "Fixture.app/Contents/MacOS/Fixture": executable(t)})
}

// packageFixture builds a component package with the requested compression and optional
// postinstall script.
func packageFixture(t *testing.T, root string, scripts bool, compression flatpkg.Compression) []byte {
	t.Helper()
	opts := flatpkg.ComponentOptions{Root: root, Identifier: "com.example.package", Version: "1.0", InstallLocation: "/Applications", TempDir: t.TempDir(), Compression: compression}
	if scripts {
		opts.Scripts = t.TempDir()
		put(t, opts.Scripts, "postinstall", []byte("#!/bin/sh\nexit 99\n"))
	}
	var out bytes.Buffer
	if _, err := flatpkg.BuildComponent(opts, &out); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// imageFixture creates an APFS or HFS+ image from the fixture directory and wraps it as a DMG.
func imageFixture(t *testing.T, root, kind string) string {
	t.Helper()
	raw, err := os.Create(filepath.Join(t.TempDir(), "raw"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw.Close() }()
	size := int64(32 << 20)
	if kind == "APFS" {
		err = apfswrite.CreateContainerFromDir(raw, size, root, &apfswrite.CreateOptions{VolumeName: "Fixture"})
	} else {
		err = hfsplus.CreateImageFromDir(raw, size, "Fixture", root, nil)
	}
	if err != nil {
		t.Fatal(err)
	}
	dmg := filepath.Join(t.TempDir(), "fixture.dmg")
	if err := disk.WrapRawImageDMGFrom(dmg, raw, size, "Apple_"+kind, nil); err != nil {
		t.Fatal(err)
	}
	return dmg
}

// checkReport checks artifact metadata, per-architecture identity extraction, and
// scratch-directory cleanup.
func checkReport(t *testing.T, file, format string) Report {
	t.Helper()
	temp := t.TempDir()
	report, err := Inspect(t.Context(), file, Options{TempDir: temp})
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(file) // #nosec G304 -- reads a test-owned artifact in t.TempDir.
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(b)
	if report.Format != format || report.SHA256 != hex.EncodeToString(hash[:]) || report.Size != int64(len(b)) || len(report.Applications) != 1 {
		t.Fatalf("%+v", report)
	}
	a := report.Applications[0]
	if len(a.Identity.Architectures) != 2 {
		t.Fatal(a)
	}
	for _, arch := range a.Identity.Architectures {
		if arch.Signature.Status != appidentity.NotChecked || arch.SigningID != "com.deploymenttheory.identity.fixture" || len(arch.CDHash) != 40 {
			t.Fatal(arch)
		}
	}
	left, err := os.ReadDir(temp)
	if err != nil || len(left) != 0 {
		t.Fatal("scratch leak", left, err)
	}
	return report
}

// TestDistributionFormats checks inspection of scripted and nested-package distribution formats.
func TestDistributionFormats(t *testing.T) {
	root := tree(t)
	for _, test := range []struct {
		name string
		data []byte
	}{{"macho", executable(t)}, {"zip", zipFixture(t)}, {"pkg", packageFixture(t, root, false, flatpkg.CompressionGzip)}} {
		t.Run(test.name, func(t *testing.T) {
			file := put(t, t.TempDir(), "artifact", test.data)
			r := checkReport(t, file, test.name)
			if !r.Complete {
				t.Fatal(r)
			}
		})
	}
	for _, kind := range []string{"APFS", "HFSX"} {
		t.Run(kind, func(t *testing.T) {
			r := checkReport(t, imageFixture(t, root, kind), "dmg")
			if r.Applications[0].Identity.BundleID != "com.deploymenttheory.identity.fixture" {
				t.Fatal(r)
			}
		})
	}
	t.Run("scripts", func(t *testing.T) {
		file := put(t, t.TempDir(), "script.pkg", packageFixture(t, root, true, flatpkg.CompressionGzip))
		r := checkReport(t, file, "pkg")
		if r.Complete || len(r.Issues) != 1 || !strings.Contains(r.Issues[0].Reason, "scripts") {
			t.Fatal(r)
		}
	})
	t.Run("nested-pkg", func(t *testing.T) {
		container := t.TempDir()
		put(t, container, "nested.pkg", packageFixture(t, root, false, flatpkg.CompressionGzip))
		r := checkReport(t, imageFixture(t, container, "HFSX"), "dmg")
		if !strings.Contains(r.Applications[0].Location, "nested.pkg!") {
			t.Fatal(r)
		}
	})
}

// TestAmbiguityAndCandidateFailure checks ambiguity and candidate failure.
func TestAmbiguityAndCandidateFailure(t *testing.T) {
	files := map[string][]byte{"First.app/Contents/Info.plist": []byte(appPlist), "First.app/Contents/MacOS/Fixture": executable(t), "Second.app/Contents/Info.plist": []byte(appPlist), "Second.app/Contents/MacOS/Fixture": executable(t), "Broken.app/Contents/Info.plist": []byte("broken"), "readme": []byte("documentation"), "empty": nil}
	file := put(t, t.TempDir(), "apps.zip", makeZIP(t, files))
	r, err := Inspect(t.Context(), file, Options{})
	if err != nil || r.Complete || len(r.Applications) != 2 || len(r.Issues) != 1 {
		t.Fatal(r, err)
	}
	for _, opts := range []Options{{MaxApplications: 1}, {MaxEntries: 1}, {MaxBytes: 10}, {MaxExpandedBytes: 10}} {
		if _, err := Inspect(t.Context(), file, opts); err == nil {
			t.Fatal("limit ignored", opts)
		}
	}
	if _, err := Inspect(t.Context(), file, Options{MaxDepth: -1}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := Inspect(t.Context(), file, Options{TempDir: filepath.Join(t.TempDir(), "missing")}); err == nil {
		t.Fatal("scratch failure ignored")
	}
}

// TestInspectionDeadline checks cancellation when artifact inspection exceeds its deadline.
func TestInspectionDeadline(t *testing.T) {
	file := put(t, t.TempDir(), "artifact", zipFixture(t))
	// An expired deadline is deterministic across platforms and timer resolutions.
	ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer cancel()
	report, err := Inspect(ctx, file, Options{Timeout: time.Minute})
	if !errors.Is(err, context.DeadlineExceeded) || report.SHA256 != "" || len(report.Applications) != 0 {
		t.Fatal(report, err)
	}
}

// TestRejectedInputAndTraversal checks rejected input and traversal.
func TestRejectedInputAndTraversal(t *testing.T) {
	for _, data := range [][]byte{nil, []byte("not an artifact"), []byte("xar!invalid"), []byte("PK\x03\x04invalid"), {0xfe, 0xed, 0xfa, 0xcf}} {
		if _, err := Inspect(t.Context(), put(t, t.TempDir(), "input", data), Options{}); err == nil {
			t.Fatal("malformed input accepted")
		}
	}
	for _, name := range []string{"../escape", "/absolute", "a/../../escape", "C:/escape", "a\\escape"} {
		file := put(t, t.TempDir(), "unsafe.zip", makeZIP(t, map[string][]byte{name: []byte("unsafe")}))
		if _, err := Inspect(t.Context(), file, Options{}); err == nil {
			t.Fatalf("accepted %s", name)
		}
	}
	if _, err := Inspect(t.Context(), t.TempDir(), Options{}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := Inspect(t.Context(), filepath.Join(t.TempDir(), "missing"), Options{}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := Inspect(ctx, put(t, t.TempDir(), "input", executable(t)), Options{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if isMachO(nil) {
		t.Fatal("empty Mach-O")
	}
	if _, err := safeName("."); err != nil {
		t.Fatal(err)
	}
	i := inspector{ctx: t.Context(), opts: Options{MaxDepth: 1}}
	if _, err := i.inspectFile("missing", "artifact", 2); !errors.Is(err, ErrLimit) {
		t.Fatal(err)
	}
	i.expanded = 2
	i.opts.MaxExpandedBytes = 1
	if err := i.copy(io.Discard, strings.NewReader("x")); !errors.Is(err, ErrLimit) {
		t.Fatal(err)
	}
}
