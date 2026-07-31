//go:build !windows

package maintain

import "syscall"

// diskFree returns the available bytes on the filesystem holding dir.
// Bavail (not Bfree) is deliberate: it excludes root-reserved blocks, so the
// VACUUM headroom guard measures what an unprivileged writer can actually use.
func diskFree(dir string) (int64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, err
	}
	return int64(st.Bavail) * int64(st.Bsize), nil
}
