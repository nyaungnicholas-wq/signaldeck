// TARGET FILE : signaldeck/daemon/internal/testharness/signal_other.go  (NEW FILE)
// HOW TO APPLY: copy into daemon/internal/testharness/. No existing file changes.

//go:build !windows

package testharness

import (
	"fmt"
	"os/exec"
	"syscall"
)

// newProcessGroup is a no-op off Windows: SIGTERM addresses the process
// directly, so the child needs no process group of its own.
func newProcessGroup(cmd *exec.Cmd) {}

// interrupt sends SIGTERM — the signal cmd/signaldeckd/main.go:67 listens for
// and the one ops/*.plist and systemd deliver on stop.
func interrupt(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return fmt.Errorf("testharness: interrupt: process not started")
	}
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("testharness: SIGTERM to pid %d: %w", cmd.Process.Pid, err)
	}
	return nil
}
