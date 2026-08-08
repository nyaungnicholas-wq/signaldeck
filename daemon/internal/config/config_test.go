package config

import "testing"

// A9 (2026-07-26 re-audit, CRITICAL): the open-by-default settings derived from
// the BIND ADDRESS, which a reverse tunnel does not change.
//
// ngrok dials out and connects back from 127.0.0.1, so `loopbackOnly` answered
// "yes, private" at exactly the moment a stranger could reach the daemon — and
// this machine ships a launchd-managed tunnel agent pointed at :8322 with a
// reserved public hostname the daemon's own allowlist already names. The
// heuristic was safest-looking on the one deployment that was public.
func TestOpenDefaultsClosedWhenATunnelIsConfigured(t *testing.T) {
	t.Setenv("SIGNALDECK_ASSUME_TUNNEL", "1")
	if reachablePrivately("127.0.0.1:8322", defaultAllowedHosts) {
		t.Error("a loopback bind with a tunnel configured still read as private — " +
			"this is the exact combination that publishes the daemon")
	}

	t.Setenv("SIGNALDECK_ASSUME_TUNNEL", "0")
	if !reachablePrivately("127.0.0.1:8322", defaultAllowedHosts) {
		t.Error("loopback with no tunnel must stay private, or localhost development closes for no reason")
	}
	// The bind check must still bind: a public bind is not rescued by the
	// absence of a tunnel.
	for _, addr := range []string{":8322", "0.0.0.0:8322", "192.168.1.10:8322"} {
		if reachablePrivately(addr, defaultAllowedHosts) {
			t.Errorf("%q read as private", addr)
		}
	}
}

// A9 REGRESSION, second time (2026-08-07 pre-ship audit): the A9 fix above was
// correct and then silently un-fixed itself by changing operating system.
//
// tunnelConfigured() proved publication by stat-ing a macOS LaunchAgent path.
// The machine moved to Windows, where that path can never exist, so the check
// became a constant false — while daemon/.env allowlisted
// `spearfish-dwindle-module.ngrok-free.dev` and PublicReads/OpenSignup both
// defaulted OPEN. Verified live against the running daemon before the fix:
// GET /api/dashboard returned 200 with no session, and POST /api/auth/register
// answered 400 (a validation error) rather than 403 "registration is closed".
//
// The allowlist is the platform-independent proof. Serving a host you cannot
// reach from loopback IS publication, whatever starts the tunnel.
func TestNonLoopbackAllowedHostClosesTheOpenDefaults(t *testing.T) {
	// No tunnel agent, no override — only the allowlist names a public host.
	t.Setenv("SIGNALDECK_ASSUME_TUNNEL", "")

	const withNgrok = "127.0.0.1:8322,localhost:8322,spearfish-dwindle-module.ngrok-free.dev"
	if reachablePrivately("127.0.0.1:8322", withNgrok) {
		t.Error("a loopback bind whose OWN allowlist serves a public hostname read as private — " +
			"this is the Windows regression: the defaults open on a daemon one `ngrok start` from the internet")
	}
	if boolEnv("SIGNALDECK_PUBLIC_READS", reachablePrivately("127.0.0.1:8322", withNgrok)) {
		t.Error("PublicReads defaulted true while a public hostname was allowlisted")
	}
	if boolEnv("SIGNALDECK_OPEN_SIGNUP", reachablePrivately("127.0.0.1:8322", withNgrok)) {
		t.Error("OpenSignup defaulted true while a public hostname was allowlisted")
	}

	// Loopback-only allowlists must NOT trip it, or localhost development
	// closes for no reason — including the IPv6 and bare-port spellings.
	for _, hosts := range []string{
		defaultAllowedHosts,
		"127.0.0.1:8322",
		"localhost:8322",
		"[::1]:8322",
		"127.0.0.1:8322,localhost:8322",
	} {
		if !reachablePrivately("127.0.0.1:8322", hosts) {
			t.Errorf("allowlist %q is loopback-only but read as published", hosts)
		}
	}
}

// The env var must be able to open reads deliberately — the gate is a default,
// not a lock, and an operator who states their intent should be obeyed.
func TestExplicitEnvStillWins(t *testing.T) {
	t.Setenv("SIGNALDECK_ASSUME_TUNNEL", "1")
	t.Setenv("SIGNALDECK_PUBLIC_READS", "true")
	if !boolEnv("SIGNALDECK_PUBLIC_READS", reachablePrivately("127.0.0.1:8322", defaultAllowedHosts)) {
		t.Error("an explicit SIGNALDECK_PUBLIC_READS=true was overridden by the tunnel default")
	}
}
