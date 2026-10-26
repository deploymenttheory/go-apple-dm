package privatefile

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// TestDirectoryCheckWindowsGrants proves that actual broad and inherited grants
// are rejected and the existing explicit private ACL passes the same checker.
func TestDirectoryCheckWindowsGrants(t *testing.T) {
	for _, inherited := range []bool{false, true} {
		name := "explicit"
		if inherited {
			name = "inherited"
		}
		t.Run(name, func(t *testing.T) {
			parent := filepath.Join(t.TempDir(), "parent")
			if err := os.Mkdir(parent, 0o700); err != nil {
				t.Fatal(err)
			}
			// These test-owned objects intentionally grant Everyone full access.
			text := "D:P(A;;FA;;;WD)"
			if inherited {
				text = "D:P(A;OICI;FA;;;WD)"
			}
			descriptor, err := windows.SecurityDescriptorFromString(text)
			if err != nil {
				t.Fatal(err)
			}
			acl, _, err := descriptor.DACL()
			if err != nil {
				t.Fatal(err)
			}
			if err := windows.SetNamedSecurityInfo(parent, windows.SE_FILE_OBJECT,
				windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
				nil, nil, acl, nil); err != nil {
				t.Fatal(err)
			}
			path := parent
			if inherited {
				path = filepath.Join(parent, "child")
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
				assertInheritedEveryoneGrant(t, path)
			}
			if err := CheckDirectory(path); !errors.Is(err, os.ErrPermission) {
				t.Fatal("broad Windows directory grant accepted", err)
			}
			if err := Mkdir(path); !errors.Is(err, os.ErrExist) {
				t.Fatal("directory collision was not refused", err)
			}
			if err := CheckDirectory(path); !errors.Is(err, os.ErrPermission) {
				t.Fatal("collision repaired an existing broad directory", err)
			}
			created := filepath.Join(parent, "private-created")
			if err := Mkdir(created); err != nil {
				t.Fatal(err)
			}
			if err := CheckDirectory(created); err != nil {
				t.Fatal("new directory retained broad parent grants", err)
			}
			if err := CheckDirectory(parent); !errors.Is(err, os.ErrPermission) {
				t.Fatal("private child creation changed parent grants", err)
			}
			if err := Protect(path); err != nil {
				t.Fatal(err)
			}
			if err := CheckDirectory(path); err != nil {
				t.Fatal("explicit private Windows directory rejected", err)
			}
		})
	}
}

// assertInheritedEveryoneGrant confirms the fixture actually inherited a broad
// effective ACE, rather than assuming Win32 creation inherited its parent DACL.
func assertInheritedEveryoneGrant(t *testing.T, path string) {
	t.Helper()
	descriptor, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	acl, _, err := descriptor.DACL()
	if err != nil || acl == nil {
		t.Fatal("inherited ACL fixture unavailable", err)
	}
	for i := uint32(0); i < uint32(acl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(acl, i, &ace); err != nil {
			t.Fatal(err)
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Header.AceFlags&windows.INHERITED_ACE == 0 || ace.Header.AceFlags&windows.INHERIT_ONLY_ACE != 0 || ace.Mask == 0 {
			continue
		}
		// #nosec G103 -- SID embedded in the OS-validated ACL returned by GetAce.
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if sid.IsWellKnown(windows.WinWorldSid) {
			return
		}
	}
	t.Fatal("fixture child did not inherit an effective Everyone grant")
}

// TestWindowsObjectPaths checks namespace and absolute-path preparation without
// accessing a UNC server or changing machine-wide long-path settings.
func TestWindowsObjectPaths(t *testing.T) {
	base := t.TempDir()
	t.Chdir(base)
	tail := strings.Repeat(`segment\`, 40) + "leaf"
	unc := `\\server\share\` + tail
	local := `C:\` + tail
	for _, tc := range []struct{ name, input, want string }{
		{"empty", "", ""},
		{"relative", `child`, filepath.Join(base, "child")},
		{"relative-dot", `parent\..\child`, filepath.Join(base, "child")},
		{"absolute", `C:/parent/./child`, `C:\parent\child`},
		{"rooted", `\child`, filepath.VolumeName(base) + `\child`},
		{"drive-relative", filepath.VolumeName(base) + `child`, filepath.Join(base, "child")},
		{"short-unc", `\\server\share\parent\..\child`, `\\server\share\child`},
		{"long-local", local, `\\?\` + local},
		{"long-unc", unc, `\\?\UNC\server\share\` + tail},
		{"long-relative", tail, `\\?\` + filepath.Join(base, tail)},
		{"qualified-local", `\\?\C:\parent\..\child`, `\\?\C:\parent\..\child`},
		{"qualified-unc", `\\?\UNC\server\share\child`, `\\?\UNC\server\share\child`},
		{"device", `\\.\pipe\example`, `\\.\pipe\example`},
		{"nt-namespace", `\??\C:\child`, `\??\C:\child`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := windowsObjectPath(tc.input)
			if err != nil || got != tc.want {
				t.Fatalf("path normalization %q: got %q want %q: %v", tc.input, got, tc.want, err)
			}
		})
	}
	if _, err := windowsObjectPath("invalid\x00path"); err == nil {
		t.Fatal("embedded NUL accepted")
	}
}

// TestPrivateLongWindowsDirectory creates actual local directories beyond
// MAX_PATH and checks their native DACLs without changing the parent descriptor.
func TestPrivateLongWindowsDirectory(t *testing.T) {
	parent := t.TempDir()
	before, err := windows.GetNamedSecurityInfo(parent, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	path := parent
	for range 9 {
		path = filepath.Join(path, strings.Repeat("long-segment-", 4))
		if err := Mkdir(path); err != nil {
			t.Fatal(err)
		}
		if err := CheckDirectory(path); err != nil {
			t.Fatal("long directory is not private", err)
		}
	}
	encoded, err := windows.UTF16FromString(path)
	if err != nil || len(encoded)-1 <= 260 {
		t.Fatal("fixture did not exceed MAX_PATH", err)
	}
	if err := Mkdir(path); !errors.Is(err, os.ErrExist) {
		t.Fatal("long-path collision was not refused", err)
	}
	after, err := windows.GetNamedSecurityInfo(parent, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil || before.String() != after.String() {
		t.Fatal("private long-directory creation changed parent access", err)
	}
	for _, invalid := range []string{"", "invalid\x00path"} {
		var pe *os.PathError
		if err := Mkdir(invalid); !errors.As(err, &pe) || pe.Op != "mkdir" || pe.Path != invalid {
			t.Fatal("directory failure lost its original path contract", err)
		}
	}
}
