package config

import (
	"path/filepath"
	"testing"
)

// The tunnel check decides whether the daemon's convenience defaults open, so
// it must describe THIS MACHINE. tunnelAgentPaths[0] was "ops/com.signaldeck.tunnel.plist"
// — a RELATIVE path to a file committed to the repo, so os.Stat found it in
// every checkout on every machine forever. tunnelConfigured() was therefore a
// constant, not a signal, and the second (genuinely machine-specific) entry was
// unreachable: the loop returned true before ever reaching it.
//
// It failed closed, so nothing was exposed. But a heuristic that cannot vary is
// not a heuristic, and the next reader will trust it to mean what it says.
func TestTunnelAgentPathsAreAbsolute(t *testing.T) {
	for i, p := range tunnelAgentPaths {
		if p == "" {
			continue
		}
		if !filepath.IsAbs(p) {
			t.Errorf("tunnelAgentPaths[%d] = %q is relative; it resolves against the "+
				"daemon's working directory and matches a repo-tracked file in every "+
				"checkout, making tunnelConfigured() constantly true", i, p)
		}
	}
}
