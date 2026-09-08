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
	// /api/track-record used to stand in for "everything else" here. It is now
	// a DELIBERATE exemption (see TestProofReceiptsArePublicButNarrowly), so it
	// moved out rather than being deleted — the closed set still needs
	// representatives, and /api/readyish still guards against prefix matching.
	for _, p := range []string{"/api/dashboard", "/api/companies", "/api/readyish"} {
		if !d.requiresAuth(p) {
			t.Errorf("%s is anonymously readable with PublicReads=false", p)
		}
	}
}

// The /proof page is the one surface built to be shown to someone with no
// account, and it reads exactly two endpoints. They must be public even with
// PublicReads=false, because that flag defaults closed whenever a tunnel is
// configured (A9) and the receipts are meant to survive that.
//
// Both directions are asserted. A test that only checked the two paths were
// open would pass just as happily if the exemption had been written as a
// prefix and quietly published the whole ledger surface.
func TestProofReceiptsArePublicButNarrowly(t *testing.T) {
	d := Deps{Cfg: config.Config{PublicReads: false}}
	for _, p := range []string{"/api/track-record", "/api/ledger/verify", "/api/accuracy"} {
		if d.requiresAuth(p) {
			t.Errorf("%s requires auth with PublicReads=false — /proof renders its "+
				"error state to every anonymous visitor it exists for", p)
		}
	}
	// The exemption is two exact paths, NOT a prefix and NOT the ledger surface.
	// /api/ledger/anchors is the neighbour that must not ride along: it is the
	// signed-anchor history, and publishing it was never the decision made here.
	for _, p := range []string{
		"/api/ledger/anchors", "/api/ledger", "/api/track-record/raw",
		"/api/portfolio", "/api/watchlist", "/api/ai/ask", "/api/notify/test",
	} {
		if !d.requiresAuth(p) {
			t.Errorf("%s became anonymously readable — the /proof exemption widened "+
				"beyond the two endpoints it was scoped to", p)
		}
	}
}
