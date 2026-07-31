//go:build windows

package ledgeranchor

import (
	"testing"

	"golang.org/x/sys/windows"
)

// On Windows the invariant is the DACL, not mode bits (Go reports 0666 for
// every writable file). enforceKeyPerm IS the check the daemon runs, so
// asserting it accepts a freshly created key is the meaningful assertion.
func assertKeyIsOwnerOnly(t *testing.T, path string) {
	t.Helper()
	if err := enforceKeyPerm(path, nil); err != nil {
		t.Errorf("freshly created key rejected by its own permission check: %v", err)
	}
}

// widenKeyPermissions grants Everyone full control, the Windows equivalent of
// chmod 0644, so the refusal path is exercised for real.
func widenKeyPermissions(t *testing.T, path string) {
	t.Helper()
	everyone, err := windows.CreateWellKnownSid(windows.WinWorldSid)
	if err != nil {
		t.Fatal(err)
	}
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
		AccessPermissions: windows.GENERIC_ALL,
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       windows.NO_INHERITANCE,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_WELL_KNOWN_GROUP,
			TrusteeValue: windows.TrusteeValueFromSID(everyone),
		},
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, acl, nil,
	); err != nil {
		t.Fatal(err)
	}
}
