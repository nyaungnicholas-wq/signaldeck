package notify

import (
	"context"
	"runtime"
	"strings"
	"testing"
)

// The local channel must describe the platform it is actually running on.
// Before this, two packages hardcoded an osascript call and the status endpoint
// hardcoded a "macos, configured" row, so on Windows every alert failed while
// the dashboard reported the channel as working.
func TestLocalTransportMatchesPlatform(t *testing.T) {
	got := LocalTransport()
	want := map[string]string{"darwin": TransportMacOS, "windows": TransportWindows}[runtime.GOOS]
	if got != want {
		t.Errorf("LocalTransport() = %q on GOOS=%s, want %q", got, runtime.GOOS, want)
	}
	if LocalSupported() != (got != "") {
		t.Errorf("LocalSupported() = %v but transport = %q — they must agree",
			LocalSupported(), got)
	}
}

// A platform with no channel must say so rather than shelling out to a binary
// that cannot exist there. Silence beats a destination that only looks real.
func TestLocalUnsupportedPlatformIsExplicit(t *testing.T) {
	if LocalSupported() {
		t.Skipf("GOOS=%s has a local channel; nothing to assert", runtime.GOOS)
	}
	if err := Local("x"); err != ErrLocalUnsupported {
		t.Errorf("Local() = %v, want ErrLocalUnsupported", err)
	}
}

// REGRESSION: the notification text must reach PowerShell only through the
// environment, never through argv.
//
// `powershell -Command <script> <arg>` does not bind $args — it joins the
// remaining arguments onto the script and executes the result. Measured with a
// real alert body ("2 stale worker(s): [signalbt-weekly weekly-report]"), the
// apostrophe in "worker(s)" reopened a quote and PowerShell tried to run the
// rest as code. Alert bodies carry worker names, DQ details and upstream error
// strings, so argv is an injection sink here, not just a quoting hazard.
func TestToastMessageNeverEntersArgv(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("toastCmd is the Windows path")
	}
	// Every metacharacter a real alert body has actually contained, plus a
	// payload that would be unmistakable if it were ever evaluated.
	const msg = `2 stale worker(s): [a b] "q" $env:PATH ; echo PWNED | rm -rf & $(1+1)`

	cmd := toastCmd(context.Background(), msg)

	for i, a := range cmd.Args {
		if strings.Contains(a, "PWNED") || strings.Contains(a, "stale worker") {
			t.Fatalf("message leaked into argv[%d] = %q — it must travel in %s only",
				i, a, toastMsgEnv)
		}
	}
	var found bool
	for _, kv := range cmd.Env {
		if kv == toastMsgEnv+"="+msg {
			found = true
		}
	}
	if !found {
		t.Fatalf("%s missing from child env — the message would arrive empty", toastMsgEnv)
	}
}

// The child's stderr must survive into the error, and one failing notification
// must not dump a multi-KB PowerShell stack trace into the log on every tick.
func TestTrimForLogIsOneBoundedLine(t *testing.T) {
	got := trimForLog("some\r\n  error\n\n\ttext")
	if got != "some error text" {
		t.Errorf("trimForLog collapsed to %q, want %q", got, "some error text")
	}
	if long := trimForLog(strings.Repeat("x", 5000)); len(long) > 320 {
		t.Errorf("trimForLog left %d chars, want it capped", len(long))
	}
	if strings.ContainsAny(trimForLog("a\nb\rc\td"), "\n\r\t") {
		t.Error("trimForLog left a line break — the log line would split")
	}
}
