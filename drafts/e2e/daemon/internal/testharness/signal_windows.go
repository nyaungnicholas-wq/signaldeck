// TARGET FILE : signaldeck/daemon/internal/testharness/signal_windows.go  (NEW FILE)
// HOW TO APPLY: copy into daemon/internal/testharness/. No existing file changes.

//go:build windows

package testharness

import (
	"fmt"
	"os/exec"
	"syscall"
)

// The Windows story, precisely.
//
// os.Process.Signal on Windows accepts ONLY os.Kill; anything else returns
// syscall.EWINDOWS ("not supported by windows") without touching the child.
// That is why daemon/e2e/e2e_test.go:218-225 skipped the whole suite: the test
// sent syscall.SIGTERM and it could not land.
//
// The skip's PREMISE was right and its CONCLUSION was too broad. Windows has a
// first-class graceful-shutdown mechanism — console control events — and Go
// supports both ends of it:
//
//   - CREATE_NEW_PROCESS_GROUP puts the child in its own console process group,
//     so an event can be addressed to the child alone and does not also hit the
//     test binary.
//   - GenerateConsoleCtrlEvent(CTRL_BREAK_EVENT, pid-of-group-leader) delivers
//     the event to that group.
//   - The Go runtime in the CHILD installs a console control handler that turns
//     CTRL_BREAK_EVENT into os.Interrupt for signal.Notify consumers.
//
// cmd/signaldeckd/main.go:67 already listens for os.Interrupt alongside
// syscall.SIGTERM, so the daemon needs no change: the graceful path under test
// on Windows is the SAME code path production takes on Unix. Nothing is faked
// and nothing is downgraded to a Kill.
//
// CREATE_NEW_PROCESS_GROUP disables CTRL_C for the group but leaves CTRL_BREAK
// working — CTRL_BREAK is exactly what is sent here.
var (
	kernel32                     = syscall.NewLazyDLL("kernel32.dll")
	procGenerateConsoleCtrlEvent = kernel32.NewProc("GenerateConsoleCtrlEvent")
)

const ctrlBreakEvent = 1 // CTRL_BREAK_EVENT

// winerror.h codes. package syscall exports neither of these on Windows
// (checked: go build fails on syscall.ERROR_INVALID_HANDLE), and pulling in
// golang.org/x/sys just for two integers is not worth a new dependency.
const (
	errInvalidHandle    = syscall.Errno(6)  // ERROR_INVALID_HANDLE
	errInvalidParameter = syscall.Errno(87) // ERROR_INVALID_PARAMETER
)

func newProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= syscall.CREATE_NEW_PROCESS_GROUP
}

func interrupt(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return fmt.Errorf("testharness: interrupt: process not started")
	}
	pid := cmd.Process.Pid
	r, _, errno := procGenerateConsoleCtrlEvent.Call(uintptr(ctrlBreakEvent), uintptr(pid))
	if r != 0 {
		return nil
	}
	// The one environmental failure worth distinguishing: the caller has no
	// console to generate the event on (a test binary launched by an IDE or a
	// service, rather than from a terminal or CI runner). That is a property of
	// where the test was started, not of the daemon.
	if errno == errInvalidHandle || errno == errInvalidParameter {
		return fmt.Errorf("%w: GenerateConsoleCtrlEvent(CTRL_BREAK_EVENT, %d): %v "+
			"(the test process has no attached console — run `go test` from a terminal or CI runner)",
			ErrSignalUnavailable, pid, errno)
	}
	return fmt.Errorf("testharness: GenerateConsoleCtrlEvent(CTRL_BREAK_EVENT, %d): %v", pid, errno)
}
