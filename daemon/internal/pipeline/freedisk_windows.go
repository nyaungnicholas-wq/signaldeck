//go:build windows

package pipeline

import (
	"syscall"
	"unsafe"
)

var procGetDiskFreeSpaceExW = syscall.NewLazyDLL("kernel32.dll").NewProc("GetDiskFreeSpaceExW")

// statfsFreeBytes returns the bytes available to the calling user on the volume
// holding dir. GetDiskFreeSpaceExW's freeBytesAvailableToCaller honours per-user
// quotas, matching the statfs Bavail semantics the bulk-sync guard expects.
func statfsFreeBytes(dir string) (uint64, error) {
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
	return freeToCaller, nil
}
