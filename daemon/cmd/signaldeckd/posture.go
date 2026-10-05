package main

import (
	"errors"
	"net"
	"os/exec"
	"runtime"
	"strings"
)

// publicWebDaemonPort is the port the web tier proxies every visitor to
// (web/src/app/api/[...path]/route.ts, SIGNALDECK_DAEMON default :8322).
const publicWebDaemonPort = "8322"

// startupPosture refuses to serve when the daemon on the public web tier's
// port reads as unpublished while a cloudflared tunnel runs on the host
// (2026-10-05 audit, AUD-09). The web tier forwards every visitor with the
// local-proxy key, so published() is all that keeps them from operator
// authority and raw data. The ctl and guard checks are early warnings; this is
// the one place every start passes through (the Daemon task, its
// restart-on-failure, market-close, refresh, collect). A copy on any other port
// (tests, the restore drill, an audit) is out of the tunnel's reach.
func startupPosture(published bool, httpAddr string, tunnel func() (running, known bool)) error {
	if published {
		return nil
	}
	if _, port, err := net.SplitHostPort(httpAddr); err != nil || port != publicWebDaemonPort {
		return nil
	}
	if running, _ := tunnel(); running {
		return errors.New("a cloudflared tunnel is running on this host but this daemon reads as unpublished, " +
			"so every tunnel visitor would be local: set SIGNALDECK_PUBLIC_URL, SIGNALDECK_TUNNEL_LOG or " +
			"SIGNALDECK_PUBLIC_SURFACE=1 in daemon/.env (ops/CLOUDFLARE_TUNNEL.md section 3)")
	}
	return nil
}

// cloudflaredRunning reports whether a cloudflared process runs on this host;
// known is false when the platform tool is missing or fails.
func cloudflaredRunning() (running, known bool) {
	if runtime.GOOS == "windows" {
		out, err := exec.Command("tasklist", "/FI", "IMAGENAME eq cloudflared.exe", "/NH").Output()
		if err != nil {
			return false, false
		}
		return strings.Contains(strings.ToLower(string(out)), "cloudflared.exe"), true
	}
	err := exec.Command("pgrep", "-x", "cloudflared").Run()
	if err == nil {
		return true, true
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ExitCode() == 1 { // pgrep: no match
		return false, true
	}
	return false, false
}
