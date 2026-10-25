package privatefile

import (
	"errors"
	"os"
	"path/filepath"
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
