//go:build !windows

package ledgeranchor

import (
	"fmt"
	"os"
)

// enforceKeyPerm refuses a key file readable by group or other. It is NOT
// silently tightened: the file may already have been read, so the honest
// response is to stop and make the operator decide whether the key is still
// trustworthy.
func enforceKeyPerm(path string, fi os.FileInfo) error {
	if fi.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("%w (%s is %04o)", ErrKeyPermissions, path, fi.Mode().Perm())
	}
	return nil
}

// hardenNewKey is a no-op on Unix: O_EXCL|0600 already created the file
// owner-only, and the parent directory was created 0700.
func hardenNewKey(string) error { return nil }
