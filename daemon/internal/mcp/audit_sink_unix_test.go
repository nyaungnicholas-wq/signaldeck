//go:build !windows

package mcp

import (
	"os"
	"testing"
)

// unwritableSink returns a path that opens for append but refuses the write:
// /dev/full answers every write with ENOSPC, i.e. a full disk.
func unwritableSink(t *testing.T) string {
	t.Helper()
	if _, err := os.Stat("/dev/full"); err != nil {
		t.Skip("no /dev/full on this platform")
	}
	return "/dev/full"
}
