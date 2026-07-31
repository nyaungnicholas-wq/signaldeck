//go:build windows

package maintain

import (
	"syscall"
	"unsafe"
)

var procGetDiskFreeSpaceExW = syscall.NewLazyDLL("kernel32.dll").NewProc("GetDiskFreeSpaceExW")

// diskFree returns the available bytes on the volume holding dir.
//
// GetDiskFreeSpaceExW's first out-param is freeBytesAvailableToCaller, which
// honours per-user quotas — the Windows analogue of statfs Bavail, and the
// right number for the fail-closed VACUUM headroom guard. An API failure is
// returned as an error so the caller skips the rewrite rather than proceeding
// on an unverified free-space figure.
func diskFree(dir string) (int64, error) {
	p, err := syscall.UTF16PtrFromString(dir)
	if err != nil {
		return 0, err
	}
	var freeToCaller, totalBytes, totalFree uint64
	r, _, e := procGetDiskFreeSpaceExW.Call(
		uintptr(unsafe.Pointer(p)),
		uintptr(unsafe.Pointer(&freeToCaller)),
		uintptr(unsafe.Pointer(&totalBytes)),
		uintptr(unsafe.Pointer(&totalFree)),
	)
	if r == 0 {
		return 0, e
	}
	return int64(freeToCaller), nil
}
