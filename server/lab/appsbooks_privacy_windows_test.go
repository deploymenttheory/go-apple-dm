package lab

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"

	"github.com/deploymenttheory/go-apple-dm/server/internal/privatefile"
)

// assertAppsBooksPrivateFile checks real Windows grants for retained evidence.
func assertAppsBooksPrivateFile(t *testing.T, path string) {
	t.Helper()
	if err := privatefile.Check(path); err != nil {
		t.Fatal("evidence file grants access outside its private identities", path, err)
	}
}

// assertAppsBooksPrivateDirectory checks real Windows directory grants.
func assertAppsBooksPrivateDirectory(t *testing.T, path string) {
	t.Helper()
	if err := privatefile.CheckDirectory(path); err != nil {
		t.Fatal("evidence directory grants access outside its private identities", path, err)
	}
}

// appsBooksBroadParent deliberately grants inheritable Everyone access only to
// an owned temporary fixture directory. A child must override these grants.
func appsBooksBroadParent(t *testing.T) string {
	t.Helper()
	parent := filepath.Join(t.TempDir(), "broad-parent")
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	descriptor, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;FA;;;" + user.User.Sid.String() + ")(A;OICI;GR;;;WD)")
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
	if err := privatefile.CheckDirectory(parent); !errors.Is(err, os.ErrPermission) {
		t.Fatal("broad parent fixture was not rejected", err)
	}
	return parent
}

// TestAppsBooksNativePrivacyUnderInheritedAccess exercises the actual lab file
// and evidence-directory creators beneath a known broad Windows parent DACL.
// Private controls prove that privacy is achievable without relying on mode bits.
func TestAppsBooksNativePrivacyUnderInheritedAccess(t *testing.T) {
	for _, kind := range []string{"file", "scope", "phase"} {
		t.Run(kind, func(t *testing.T) {
			parent := appsBooksBroadParent(t)
			control := filepath.Join(parent, "private-control")
			if kind == "file" {
				f, err := privatefile.OpenFile(control, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
				if err != nil {
					t.Fatal(err)
				}
				if err := f.Close(); err != nil {
					t.Fatal(err)
				}
				assertAppsBooksPrivateFile(t, control)
				path := filepath.Join(parent, "actual-lab-file")
				if err := privateFile(path, []byte("controlled private evidence")); err != nil {
					t.Fatal(err)
				}
				assertAppsBooksPrivateFile(t, path)
				return
			}
			if err := os.Mkdir(control, 0o700); err != nil {
				t.Fatal(err)
			}
			// Existing Protect supports Windows file objects, including directories.
			if err := privatefile.Protect(control); err != nil {
				t.Fatal(err)
			}
			assertAppsBooksPrivateDirectory(t, control)
			s := &Session{Evidence: parent}
			var created *Session
			var err error
			if kind == "scope" {
				created, _, err = appsScope(s, "privacy")
			} else {
				created, err = appsPhase(s, "privacy")
			}
			if err != nil {
				t.Fatal(err)
			}
			assertAppsBooksPrivateDirectory(t, created.Evidence)
		})
	}
}
