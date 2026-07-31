//go:build windows

package ledgeranchor

import (
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Windows has no Unix mode bits: Go reports 0666 for every writable file, so
// the portable `perm&0o077 != 0` check is meaningless here (it would reject
// every key, including one the daemon just created). The real equivalent is
// the file's DACL, so that is what we set and what we verify.
//
// "Owner-only" is read as: the file's owner, plus the two principals that can
// take ownership anyway (LocalSystem and BUILTIN\Administrators). Granting to
// anyone else — Everyone, Users, Authenticated Users, another account — means
// the key is readable and is refused, matching the Unix branch's stance.

// hardenNewKey replaces the inherited ACL with a protected one granting full
// access to the file's owner, LocalSystem and Administrators, and nobody else.
func hardenNewKey(path string) error {
	owner, err := fileOwner(path)
	if err != nil {
		return err
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return err
	}
	admins, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return err
	}
	entries := make([]windows.EXPLICIT_ACCESS, 0, 3)
	for _, sid := range []*windows.SID{owner, system, admins} {
		entries = append(entries, windows.EXPLICIT_ACCESS{
			AccessPermissions: windows.GENERIC_ALL,
			AccessMode:        windows.GRANT_ACCESS,
			Inheritance:       windows.NO_INHERITANCE,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeType:  windows.TRUSTEE_IS_UNKNOWN,
				TrusteeValue: windows.TrusteeValueFromSID(sid),
			},
		})
	}
	acl, err := windows.ACLFromEntries(entries, nil)
	if err != nil {
		return err
	}
	// PROTECTED_DACL_SECURITY_INFORMATION severs inheritance, so a permissive
	// ACL on the parent directory cannot re-open the key.
	return windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, acl, nil,
	)
}

// enforceKeyPerm refuses a key whose DACL grants access to any principal other
// than its owner, LocalSystem or Administrators. As on Unix, a too-permissive
// key is reported, never silently tightened.
func enforceKeyPerm(path string, _ os.FileInfo) error {
	sd, err := windows.GetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.OWNER_SECURITY_INFORMATION,
	)
	if err != nil {
		// Fail closed: an unverifiable ACL is not permission to sign.
		return fmt.Errorf("%w (%s: reading ACL: %v)", ErrKeyPermissions, path, err)
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return fmt.Errorf("%w (%s: reading owner: %v)", ErrKeyPermissions, path, err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return fmt.Errorf("%w (%s: reading DACL: %v)", ErrKeyPermissions, path, err)
	}
	if dacl == nil {
		// A NULL DACL grants everyone full control.
		return fmt.Errorf("%w (%s has a NULL DACL — world-writable)", ErrKeyPermissions, path)
	}
	allowed, err := ownerOnlySIDs(owner)
	if err != nil {
		return err
	}
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			return fmt.Errorf("%w (%s: reading ACE %d: %v)", ErrKeyPermissions, path, i, err)
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			continue // deny ACEs only narrow access
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if !sidIn(sid, allowed) {
			return fmt.Errorf("%w (%s grants access to %s)", ErrKeyPermissions, path, sid.String())
		}
	}
	return nil
}

func fileOwner(path string) (*windows.SID, error) {
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return nil, err
	}
	owner, _, err := sd.Owner()
	return owner, err
}

func ownerOnlySIDs(owner *windows.SID) ([]*windows.SID, error) {
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return nil, err
	}
	admins, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return nil, err
	}
	return []*windows.SID{owner, system, admins}, nil
}

func sidIn(sid *windows.SID, set []*windows.SID) bool {
	for _, s := range set {
		if s != nil && sid.Equals(s) {
			return true
		}
	}
	return false
}
