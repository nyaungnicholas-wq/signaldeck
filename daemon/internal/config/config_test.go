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
	if reachablePrivately("127.0.0.1:8322") {
		t.Error("a loopback bind with a tunnel configured still read as private — " +
			"this is the exact combination that publishes the daemon")
	}

	t.Setenv("SIGNALDECK_ASSUME_TUNNEL", "0")
	if !reachablePrivately("127.0.0.1:8322") {
		t.Error("loopback with no tunnel must stay private, or localhost development closes for no reason")
	}
	// The bind check must still bind: a public bind is not rescued by the
	// absence of a tunnel.
	for _, addr := range []string{":8322", "0.0.0.0:8322", "192.168.1.10:8322"} {
		if reachablePrivately(addr) {
			t.Errorf("%q read as private", addr)
		}
	}
}

// The env var must be able to open reads deliberately — the gate is a default,
// not a lock, and an operator who states their intent should be obeyed.
func TestExplicitEnvStillWins(t *testing.T) {
	t.Setenv("SIGNALDECK_ASSUME_TUNNEL", "1")
	t.Setenv("SIGNALDECK_PUBLIC_READS", "true")
	if !boolEnv("SIGNALDECK_PUBLIC_READS", reachablePrivately("127.0.0.1:8322")) {
		t.Error("an explicit SIGNALDECK_PUBLIC_READS=true was overridden by the tunnel default")
	}
}
