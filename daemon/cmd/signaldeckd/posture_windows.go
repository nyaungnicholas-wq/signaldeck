//go:build windows

package main

import (
	"errors"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// cloudflaredRunning reports whether a cloudflared process runs on this host,
// from a toolhelp snapshot: no WMI (tasklist is a WMI client and can hang when
// winmgmt is wedged; 2026-10-05 review), cheap enough to repeat every minute.
// known is false when the snapshot cannot be read.
func cloudflaredRunning() (running, known bool) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return false, false
	}
	defer windows.CloseHandle(snap) //nolint:errcheck
	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	for err = windows.Process32First(snap, &pe); err == nil; err = windows.Process32Next(snap, &pe) {
		if strings.EqualFold(windows.UTF16ToString(pe.ExeFile[:]), "cloudflared.exe") {
			return true, true
		}
	}
	return false, errors.Is(err, windows.ERROR_NO_MORE_FILES)
}
