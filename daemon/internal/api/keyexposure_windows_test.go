//go:build windows

package api

import (
	"os"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// writeExposedKey writes a syntactically valid anchor key that other local
// users can read, so LoadOrCreateSigner refuses it.
//
// A 0644 mode is meaningless on Windows — the file just inherits the temp
// directory's owner-only ACL and is NOT exposed. Exposure has to be created
// explicitly by granting the Everyone SID access.
func writeExposedKey(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(strings.Repeat("ab", 32)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	everyone, err := windows.CreateWellKnownSid(windows.WinWorldSid)
	if err != nil {
		t.Fatal(err)
	}
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
		AccessPermissions: windows.GENERIC_READ,
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
