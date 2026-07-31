//go:build !windows

package pipeline

import "syscall"

// statfsFreeBytes returns the bytes available to an unprivileged writer on the
// filesystem holding dir. Bavail (not Bfree) excludes root-reserved blocks.
func statfsFreeBytes(dir string) (uint64, error) {
	var fs syscall.Statfs_t
	if err := syscall.Statfs(dir, &fs); err != nil {
		return 0, err
	}
	return uint64(fs.Bavail) * uint64(fs.Bsize), nil
}
