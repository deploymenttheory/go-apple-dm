package appidentity

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

const metadata = "Identifier=com.example.signed\nCDHash=0123456789abcdef0123456789abcdef01234567\nTeamIdentifier=EXAMPLETEAM\ndesignated => identifier \"com.example.signed\" and anchor apple generic\n"

func fixture(t *testing.T, path string, data []byte) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func bundle(t *testing.T, meta string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "Example.app")
	fixture(t, filepath.Join(root, "Contents", "Info.plist"), []byte(meta))
	fixture(t, filepath.Join(root, "Contents", "MacOS", "Example"), []byte("fixture executable"))
	return root
}

const bundlePlist = `<?xml version="1.0"?><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>com.example.bundle</string><key>CFBundleExecutable</key><string>Example</string><key>CFBundleName</key><string>Example</string><key>CFBundleShortVersionString</key><string>1.2</string></dict></plist>`

func goodRunner(_ context.Context, tool string, args ...string) (string, error) {
	if tool == "/usr/bin/lipo" {
		return "x86_64 arm64e.x1", nil
	}
	if args[0] == "-d" {
		return metadata, nil
	}
	if slices.Contains(args, "-R") {
		return "", &exec.ExitError{}
	}
	return "", nil
}

func TestInspectBundleAndArchitectures(t *testing.T) {
	root := bundle(t, bundlePlist)
	var displayed []string
	run := func(ctx context.Context, tool string, args ...string) (string, error) {
		if tool == "/usr/bin/codesign" {
			if args[len(args)-1] != root {
				t.Fatalf("target split or changed: %v", args)
			}
			if args[0] == "-d" {
				displayed = append(displayed, args[slices.Index(args, "--arch")+1])
			}
		}
		return goodRunner(ctx, tool, args...)
	}
	id, err := inspect(t.Context(), root, run)
	if err != nil || id.BundleID != "com.example.bundle" || id.Name != "Example" || id.Version != "1.2" || len(id.Architectures) != 2 {
		t.Fatalf("%+v %v", id, err)
	}
	if !slices.Equal(displayed, []string{"x86_64", "arm64e.x1"}) {
		t.Fatal(displayed)
	}
	for _, a := range id.Architectures {
		if a.SigningID == id.BundleID || a.SigningID != "com.example.signed" || a.TeamID != "EXAMPLETEAM" || a.Signature.Status != Valid || a.Signature.Category != Unknown || a.DesignatedRequirement == "" {
			t.Fatalf("%+v", a)
		}
	}
	file := fixture(t, filepath.Join(t.TempDir(), "-name with spaces"), []byte("fixture"))
	id, err = inspect(t.Context(), file, goodRunner)
	if err != nil || id.BundleID != "" || id.Path != file {
		t.Fatalf("%+v %v", id, err)
	}
}

func TestInspectionFailures(t *testing.T) {
	file := fixture(t, filepath.Join(t.TempDir(), "binary"), nil)
	for _, tc := range []struct {
		output string
		err    error
	}{
		{"", nil}, {"arm64 arm64", nil}, {"arm64 --bad", nil}, {"", os.ErrPermission},
	} {
		_, err := inspect(t.Context(), file, func(context.Context, string, ...string) (string, error) { return tc.output, tc.err })
		if !errors.Is(err, ErrInspect) {
			t.Fatalf("%+v: %v", tc, err)
		}
	}
	for _, p := range []string{"", filepath.Join(t.TempDir(), "missing"), t.TempDir()} {
		if _, err := inspect(t.Context(), p, goodRunner); !errors.Is(err, ErrInput) {
			t.Fatalf("%s: %v", p, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := inspect(ctx, file, goodRunner); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := architectures(t.Context(), t.TempDir(), goodRunner); !errors.Is(err, ErrInput) {
		t.Fatal(err)
	}
	if _, err := architectures(t.Context(), file+"missing", goodRunner); !errors.Is(err, ErrInput) {
		t.Fatal(err)
	}
	_, err := inspect(t.Context(), file, func(ctx context.Context, tool string, args ...string) (string, error) {
		if tool == "/usr/bin/lipo" {
			return "arm64", nil
		}
		return "", os.ErrPermission
	})
	if !errors.Is(err, ErrInspect) {
		t.Fatal(err)
	}
}

func TestBundleFailures(t *testing.T) {
	for _, data := range []string{"not plist", strings.Repeat("a", maxMetadata+1), strings.ReplaceAll(bundlePlist, "<string>Example</string>", "<string>../escape</string>"), strings.ReplaceAll(bundlePlist, "com.example.bundle", "")} {
		_, err := resolve(bundle(t, data))
		if err == nil {
			t.Fatal("accepted malformed bundle")
		}
	}
	root := bundle(t, bundlePlist)
	if err := os.Remove(filepath.Join(root, "Contents", "Info.plist")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "Contents", "Info.plist"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := resolve(root); err == nil {
		t.Fatal("accepted directory as plist")
	}
	if runtime.GOOS != "windows" {
		if _, err := resolve("/dev/null"); !errors.Is(err, ErrInput) {
			t.Fatal(err)
		}
	}
}

func TestSignatureObservations(t *testing.T) {
	for _, tc := range []struct {
		name                              string
		status                            Status
		category                          Category
		displayErr, verifyErr, errorClass error
		display                           string
		successRequirement                int
	}{
		{name: "unsigned", status: Unsigned, category: Unknown, displayErr: &exec.ExitError{}, display: "code object is not signed at all"},
		{name: "invalid", status: Invalid, category: Unknown, verifyErr: &exec.ExitError{}},
		{name: "ad hoc", status: Valid, category: Unknown},
		{name: "Apple", status: Valid, category: Apple, successRequirement: 1},
		{name: "Developer ID", status: Valid, category: DeveloperID, successRequirement: 2},
		{name: "App Store", status: Valid, category: AppStore, successRequirement: 3},
		{name: "display failure", displayErr: os.ErrPermission, errorClass: ErrInspect},
		{name: "display exit", displayErr: &exec.ExitError{}, errorClass: ErrInspect},
		{name: "verify failure", verifyErr: context.Canceled, errorClass: ErrInspect},
		{name: "bad metadata", display: "CDHash=bad", errorClass: ErrInspect},
	} {
		t.Run(tc.name, func(t *testing.T) {
			count := 0
			run := func(_ context.Context, _ string, args ...string) (string, error) {
				if args[0] == "-d" {
					out := tc.display
					if out == "" {
						out = strings.ReplaceAll(metadata, "EXAMPLETEAM", "not set")
					}
					return out, tc.displayErr
				}
				if slices.Contains(args, "-R") {
					count++
					if count == tc.successRequirement {
						return "", nil
					}
					return "", &exec.ExitError{}
				}
				return "invalid signature", tc.verifyErr
			}
			a, err := inspectArchitecture(t.Context(), "/binary", "arm64", run)
			if tc.errorClass != nil {
				if !errors.Is(err, tc.errorClass) {
					t.Fatal(err)
				}
				return
			}
			if err != nil || a.Signature.Status != tc.status || a.Signature.Category != tc.category || a.TeamID != "" {
				t.Fatalf("%+v %v", a, err)
			}
		})
	}
	_, err := inspectArchitecture(t.Context(), "/binary", "arm64", func(ctx context.Context, tool string, args ...string) (string, error) {
		if slices.Contains(args, "-R") {
			return "", context.Canceled
		}
		return goodRunner(ctx, tool, args...)
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestMetadataAndOutputLimits(t *testing.T) {
	for _, s := range []string{metadata + "Identifier=other\n", strings.ReplaceAll(metadata, "0123456789abcdef0123456789abcdef01234567", "xx"), "Identifier=x\n", strings.ReplaceAll(metadata, "Identifier=com.example.signed\n", "")} {
		if _, err := parseMetadata(s); !errors.Is(err, ErrInspect) {
			t.Fatalf("%q: %v", s, err)
		}
	}
	if _, err := parseMetadata("ignored line\nAuthority=ignored\n" + metadata); err != nil {
		t.Fatal(err)
	}
	var b boundedOutput
	if _, err := b.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Write(make([]byte, maxMetadata)); !errors.Is(err, ErrTooLarge) {
		t.Fatal(err)
	}
}

func TestNativeInspect(t *testing.T) {
	if runtime.GOOS != "darwin" {
		if _, err := Inspect(t.Context(), "ignored"); !errors.Is(err, ErrUnsupported) {
			t.Fatal(err)
		}
		return
	}
	id, err := Inspect(t.Context(), "/usr/bin/true")
	if err != nil {
		t.Fatal(err)
	}
	if len(id.Architectures) == 0 {
		t.Fatal("no architecture observations")
	}
	for _, a := range id.Architectures {
		if a.Signature.Status != Valid || a.Signature.Category != Apple || len(a.CDHash) != 40 {
			t.Fatalf("%+v", a)
		}
	}
}

func TestToolRunner(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GO_APPIDENTITY_TEST_HELPER", "1")
	out, err := runTool(t.Context(), exe, "-test.run=^TestToolHelper$")
	if err != nil || !strings.Contains(out, "stdout C") || !strings.Contains(out, "stderr") {
		t.Fatalf("%q %v", out, err)
	}
	t.Setenv("GO_APPIDENTITY_TEST_HELPER", "overflow")
	if out, err := runTool(t.Context(), exe, "-test.run=^TestToolHelper$"); !errors.Is(err, ErrTooLarge) || len(out) > maxMetadata {
		t.Fatalf("tool output was not bounded: %d bytes, %v", len(out), err)
	}
	if _, err := runTool(t.Context(), filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("missing tool succeeded")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := runTool(ctx, exe); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestToolHelper(t *testing.T) {
	if os.Getenv("GO_APPIDENTITY_TEST_HELPER") == "" {
		return
	}
	if os.Getenv("GO_APPIDENTITY_TEST_HELPER") == "overflow" {
		if _, err := fmt.Fprint(os.Stdout, strings.Repeat("x", maxMetadata+1)); err != nil {
			t.Log(err)
		}
		return
	}
	if _, err := fmt.Fprintln(os.Stdout, "stdout", os.Getenv("LC_ALL")); err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintln(os.Stderr, "stderr"); err != nil {
		t.Fatal(err)
	}
}
