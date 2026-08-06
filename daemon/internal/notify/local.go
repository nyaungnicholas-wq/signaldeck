package notify

// LOCAL desktop notification — the popup on the machine the daemon runs on,
// as distinct from the remote transports in notify.go.
//
// WHY THIS FILE EXISTS. internal/health and internal/alerts each carried their
// own copy of an osascriptNotify that shelled out to `osascript`, unguarded by
// platform. That is a macOS-only binary. After the move to Windows every local
// alert this daemon raised died as
//
//	watchdog: notification failed err="exec: \"osascript\": executable file not
//	found in %PATH%"
//
// and internal/alerts discarded even that error (`_ = local(...)`). Measured
// 2026-08-05: data/health.json read {"ok":false,"staleWorkers":["signalbt-weekly",
// "weekly-report"]} while no remote transport was configured in daemon/.env, so
// the fleet had been unhealthy with NOBODY told. The SQLITE_BUSY storm and the
// SQLite OOM in the same log went unreported for the same reason. An alerting
// path that cannot fire on the platform it runs on is worse than none, because
// /api/notify-status reported it as configured.
//
// The rule here is the one offsiteBackupDir already states for backups: a
// destination that only LOOKS real is worse than an absent one. So Local
// dispatches per platform and, on a platform with no supported channel, returns
// ErrLocalUnsupported instead of pretending. LocalTransport/LocalSupported let
// the status endpoint report what is actually true of the running machine.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"time"
)

// localTimeout bounds one local-notification attempt. The helper shells out to
// a UI subsystem that can hang (a Windows session with no desktop, a macOS
// login window); the daemon must not.
const localTimeout = 10 * time.Second

// ErrLocalUnsupported is returned by Local on a platform with no implemented
// desktop channel. Callers log it; it is never fatal.
var ErrLocalUnsupported = errors.New("notify: no local desktop notification channel on this platform")

// Local transport names, reported by LocalTransport and surfaced verbatim by
// /api/notify-status.
const (
	TransportMacOS   = "macos"
	TransportWindows = "windows"
)

// LocalTransport names the local channel for the running platform, or "" when
// there is none. Callers MUST NOT hardcode this — it is the only honest answer
// to "will a desktop popup happen on this box".
func LocalTransport() string {
	switch runtime.GOOS {
	case "darwin":
		return TransportMacOS
	case "windows":
		return TransportWindows
	default:
		return ""
	}
}

// LocalSupported reports whether Local can attempt a delivery here.
func LocalSupported() bool { return LocalTransport() != "" }

// Local shows a desktop notification on the machine the daemon runs on.
//
// Best-effort by contract: the caller logs the error and continues. Delivery is
// NOT tracked — neither platform returns a receipt, so a successful return means
// "the request was accepted", not "a human saw it". That caveat travels with the
// status endpoint.
func Local(msg string) error {
	ctx, cancel := context.WithTimeout(context.Background(), localTimeout)
	defer cancel()
	switch runtime.GOOS {
	case "darwin":
		return osascriptNotify(ctx, msg)
	case "windows":
		return powershellToast(ctx, msg)
	default:
		return ErrLocalUnsupported
	}
}

// osascriptNotify pops a macOS notification via AppleScript.
func osascriptNotify(ctx context.Context, msg string) error {
	script := fmt.Sprintf("display notification %q with title %q", msg, "SignalDeck")
	return run(exec.CommandContext(ctx, "osascript", "-e", script))
}

// toastMsgEnv carries the notification text into the PowerShell child.
const toastMsgEnv = "SIGNALDECK_TOAST_MSG"

// powershellToast raises a Windows toast through the WinRT notification API.
//
// THE MESSAGE TRAVELS IN AN ENVIRONMENT VARIABLE, NOT IN THE COMMAND LINE.
// `powershell -Command <script> <arg>` does NOT bind $args — -Command joins
// every remaining argument into one string and EXECUTES the result. Measured
// while building this: a message of
//
//	2 stale worker(s): [signalbt-weekly weekly-report]
//
// came back as `The term 's' is not recognized as the name of a cmdlet`, because
// the apostrophe in "worker(s)" reopened a quote and the rest of the alert ran as
// code. Alert bodies carry worker names, DQ details and upstream error strings,
// so that is an injection sink, not merely a quoting bug. $env: is read as data
// and can never be parsed as script, which removes the class rather than escaping
// around it.
//
// The AppUserModelID is Windows PowerShell's own registered ID; a toast must be
// attributed to a registered AUMID or Show() is a silent no-op, and SignalDeck
// installs no Start Menu shortcut of its own to register one. `powershell` (5.1,
// always present) is used rather than `pwsh` (7+, optional) because the WinRT
// type accelerators this needs are not loaded in PowerShell 7 by default.
//
// A daemon running in a service session (session 0) has no desktop to draw on:
// Show() still returns success and no human sees anything. That is why delivery
// is documented as untracked rather than claimed.
func powershellToast(ctx context.Context, msg string) error {
	return run(toastCmd(ctx, msg))
}

// toastCmd builds the child process. Split from powershellToast so the
// injection invariant — the message reaches the child ONLY through the
// environment, never through argv — is assertable without popping a toast on
// every test run.
func toastCmd(ctx context.Context, msg string) *exec.Cmd {
	const script = `
$ErrorActionPreference = 'Stop'
[Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType=WindowsRuntime] > $null
[Windows.UI.Notifications.ToastNotification, Windows.UI.Notifications, ContentType=WindowsRuntime] > $null
$xml = [Windows.UI.Notifications.ToastNotificationManager]::GetTemplateContent(
    [Windows.UI.Notifications.ToastTemplateType]::ToastText02)
$t = $xml.GetElementsByTagName('text')
$t.Item(0).AppendChild($xml.CreateTextNode('SignalDeck')) > $null
$t.Item(1).AppendChild($xml.CreateTextNode($env:SIGNALDECK_TOAST_MSG)) > $null
$aumid = '{1AC14E77-02E7-4E5D-B744-2EB1AE5198B7}\WindowsPowerShell\v1.0\powershell.exe'
[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier($aumid).Show(
    [Windows.UI.Notifications.ToastNotification]::new($xml))
`
	cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive",
		"-ExecutionPolicy", "Bypass", "-Command", script)
	cmd.Env = append(os.Environ(), toastMsgEnv+"="+msg)
	return cmd
}

// run executes cmd and folds the child's stderr into the error, so a failure
// reads as the reason rather than as a bare "exit status 1".
func run(cmd *exec.Cmd) error {
	out, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	if len(out) == 0 {
		return err
	}
	return fmt.Errorf("%w: %s", err, trimForLog(string(out)))
}

// trimForLog caps child output so one failing notification cannot dump a
// multi-KB PowerShell stack trace into every log line under the cooldown.
func trimForLog(s string) string {
	const max = 300
	s = collapseSpace(s)
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}

// collapseSpace squeezes runs of whitespace (PowerShell errors are multi-line)
// into single spaces so the message stays one log line.
func collapseSpace(s string) string {
	out := make([]rune, 0, len(s))
	sp := false
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			sp = true
			continue
		}
		if sp && len(out) > 0 {
			out = append(out, ' ')
		}
		sp = false
		out = append(out, r)
	}
	return string(out)
}
