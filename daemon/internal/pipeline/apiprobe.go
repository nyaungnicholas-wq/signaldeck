package pipeline

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/workers"
)

// ── API SELF-PROBE ──────────────────────────────────────────────────────────
//
// NOTHING ELSE CHECKS THAT THE API IS ACTUALLY LISTENING.
//
// api.Serve runs in a goroutine and is not a workers.Worker, so it has no
// worker_runs row at all — which means health.StaleWorkers and
// health.FailingWorkers are STRUCTURALLY incapable of seeing it. Everything
// else keeps reporting green while the product's whole surface is unreachable:
//
//   - health.Check returns "healthy: N workers checked" (it only walks Specs)
//   - data/health.json writes ok:true (computed from stale/failing/envcfg)
//   - the cache-warmer calls its build funcs IN-PROCESS, not over HTTP, so it
//     stays green with the listener dead
//   - backups keep succeeding
//   - and every /api/* health surface is itself reachable only through the
//     listener that is down, so asking them is circular
//
// A failed BIND is already fatal (the Serve goroutine cancels the run context),
// but that only covers the case where Serve RETURNS. This covers the rest:
// a listener that stops accepting without an error, a port taken over, a
// firewall change, a half-open socket. It is the difference between "the daemon
// is up" and "the daemon is answering", which is precisely the distinction
// /api/ready exists to make and could not make about itself.
//
// It probes over real TCP on the configured address rather than calling a
// handler in-process, because in-process is exactly the check that cannot fail
// for the reason we care about.

// APIProbe checks that the daemon's own HTTP listener accepts connections.
type APIProbe struct {
	// Addr is the daemon's HTTPAddr (host:port). Empty disables the probe.
	Addr string
	// Client is injectable for tests; nil uses a short-timeout default.
	Client *http.Client
}

func (w *APIProbe) Name() string { return "api-probe" }

// Interval: every 2 minutes. Fast enough that an unreachable API is noticed
// within one alerting window, slow enough to be free — it is one local GET.
func (w *APIProbe) Interval() time.Duration { return 2 * time.Minute }

func (w *APIProbe) Run(ctx context.Context) (string, error) {
	if w.Addr == "" {
		// Not a failure: a build with no HTTP surface has nothing to probe, and
		// reporting one would be a fabricated measurement.
		return "no HTTP address configured — nothing to probe", nil
	}
	c := w.Client
	if c == nil {
		// Deliberately short. A probe that waits 30s to fail is a probe that
		// reports last cycle's truth.
		c = &http.Client{Timeout: 10 * time.Second}
	}

	// /api/health is unauthenticated by contract (see api.requiresAuth: probes
	// must be reachable before anything holds a credential), so this works
	// whether or not PublicReads is on.
	url := "http://" + probeHost(w.Addr) + "/api/health"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := c.Do(req)
	if err != nil {
		// THE case this worker exists for. Returning a plain error files the run
		// as status="error", which is the only status FailingWorkers counts, so
		// a listener that stays down finally reaches /api/ready and health.json.
		return "", fmt.Errorf("api listener unreachable at %s: %w", w.Addr, err)
	}
	defer resp.Body.Close() //nolint:errcheck

	// Any HTTP response proves the listener is accepting and routing. The BODY
	// may legitimately say degraded — that is a different question, already
	// answered by the health surfaces themselves, and duplicating that verdict
	// here would give the fleet two places to disagree about it.
	if resp.StatusCode >= 500 {
		return "", fmt.Errorf("api listener answered %d at %s", resp.StatusCode, w.Addr)
	}
	return fmt.Sprintf("api listener answering on %s (HTTP %d)", w.Addr, resp.StatusCode), nil
}

// probeHost turns a bind address into something dialable. A listener bound to
// ":8322" or "0.0.0.0:8322" is reachable at 127.0.0.1; dialing the bind string
// verbatim fails on the empty host.
func probeHost(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port)
}

// compile-time assertion that this satisfies the fleet contract.
var _ workers.Worker = (*APIProbe)(nil)
