package api

import (
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/config"
)

// Probes must stay reachable without a credential. /api/ready was omitted from
// the exemption list and began 401-ing the moment PublicReads closed — a
// readiness endpoint nothing can probe, which is the same as not having one.
func TestProbesStayReachableWhenReadsAreClosed(t *testing.T) {
	d := Deps{Cfg: config.Config{PublicReads: false}}
	for _, p := range []string{"/api/health", "/api/ready", "/api/auth/login", "/api/auth/register"} {
		if d.requiresAuth(p) {
			t.Errorf("%s requires auth with PublicReads=false — a monitor or the login page cannot reach it", p)
		}
	}
	// Everything else must still be closed, or the exemption is a hole.
	for _, p := range []string{"/api/dashboard", "/api/companies", "/api/track-record", "/api/readyish"} {
		if !d.requiresAuth(p) {
			t.Errorf("%s is anonymously readable with PublicReads=false", p)
		}
	}
}
