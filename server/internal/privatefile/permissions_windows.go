package privatefile

import (
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Protect removes inherited grants before any credential data is written.
// LocalSystem and administrators retain the access they have to Unix root-owned files.
func Protect(path string) error {
	acl, err := privateACL()
	if err != nil {
		return err
	}
	return wrap(windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, acl, nil))
}

var reopenFile = windows.NewLazySystemDLL("kernel32.dll").NewProc("ReOpenFile")

func protectFile(file *os.File) error {
	acl, err := privateACL()
	if err != nil {
		return err
	}
	// SetSecurityInfo reads the current descriptor before replacing the DACL,
	// so the handle needs READ_CONTROL as well as WRITE_DAC. Reopen the same
	// file to preserve os.Root's boundary if its path changes after creation.
	handle, _, callErr := reopenFile.Call(file.Fd(), windows.READ_CONTROL|windows.WRITE_DAC,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, 0)
	if windows.Handle(handle) == windows.InvalidHandle {
		return fmt.Errorf("private file: reopen for access control: %w", callErr)
	}
	defer func() { _ = windows.CloseHandle(windows.Handle(handle)) }()
	if err := windows.SetSecurityInfo(windows.Handle(handle), windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, acl, nil); err != nil {
		return fmt.Errorf("private file: set access control: %w", err)
	}
	return nil
}

func privateACL() (*windows.ACL, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, wrap(err)
	}
	descriptor, err := windows.SecurityDescriptorFromString(
		"D:P(A;;FA;;;SY)(A;;FA;;;BA)(A;;FA;;;" + user.User.Sid.String() + ")",
	)
	if err != nil {
		return nil, wrap(err)
	}
	acl, _, err := descriptor.DACL()
	return acl, wrap(err)
}

// Check examines the DACL; Go's synthesized Windows mode bits do not represent access.
func Check(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return wrap(err)
	}
	if !info.Mode().IsRegular() {
		return wrap(os.ErrPermission)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return wrap(err)
	}
	descriptor, err := windows.GetNamedSecurityInfo(
		path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		return wrap(err)
	}
	acl, _, err := descriptor.DACL()
	if err != nil {
		return wrap(err)
	}
	if acl == nil { // A null DACL grants everyone access.
		return wrap(os.ErrPermission)
	}
	for i := uint32(0); i < uint32(acl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(acl, i, &ace); err != nil {
			return wrap(err)
		}
		if ace.Header.AceFlags&windows.INHERIT_ONLY_ACE != 0 ||
			ace.Header.AceType == windows.ACCESS_DENIED_ACE_TYPE {
			continue
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			return wrap(os.ErrPermission)
		}
		// The Windows ACCESS_ALLOWED_ACE structure embeds the SID at SidStart.
		// #nosec G103 -- pointer into the OS-validated ACL returned by GetAce.
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if !sid.Equals(user.User.Sid) && !sid.IsWellKnown(windows.WinLocalSystemSid) &&
			!sid.IsWellKnown(windows.WinBuiltinAdministratorsSid) {
			return wrap(os.ErrPermission)
		}
	}
	return nil
}

// SyncDirectory validates the handle. Windows does not support FlushFileBuffers
// on directory handles; callers separately sync file contents before publication.
func SyncDirectory(file *os.File) error {
	_, err := file.Stat()
	return wrap(err)
}
