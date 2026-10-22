package api

import (
	"slices"
	"testing"
	"time"
)

// A retired worker keeps its rows in worker_runs, and its last run is "orphaned"
// at the boot that stops wiring it. /api/health and /api/fleet-health must judge
// only the registered fleet, or health stays red forever with nothing to fix.
func TestHealthJudgesOnlyTheCurrentFleet(t *testing.T) {
	_, st, d := newTestServer(t, nil)
	now := time.Now().Unix()
	ins := func(w string, ago int64, status string) {
		if _, err := st.DB().Exec(`INSERT INTO worker_runs (worker, started_at, finished_at, status) VALUES (?,?,?,?)`,
			w, now-ago, now-ago+1, status); err != nil {
			t.Fatal(err)
		}
	}
	ins("crypto-live", 60, "orphaned") // retired stream ingestor
	for i := int64(1); i <= 3; i++ {
		ins("fred-poller", (9+i)*86400, "error") // retired: failing AND 10 days stale
		ins("bars-poller", i*60, "error")        // current fleet, really failing
	}
	d.WorkerIntervals = func() map[string]time.Duration { return map[string]time.Duration{"bars-poller": time.Minute} }

	if failing, err := d.failingWorkers(t.Context()); err != nil || len(failing) != 1 || failing["bars-poller"] != "error" {
		t.Fatalf("/api/health failing = %v (err %v), want only bars-poller", failing, err)
	}
	sys, err := d.systemHealth(t.Context())
	if err != nil || !slices.Equal(sys.FailingWorkers, []string{"bars-poller"}) || len(sys.StaleWorkers) != 0 {
		t.Fatalf("fleet-health failing=%v stale=%v (err %v), want failing [bars-poller], no stale", sys.FailingWorkers, sys.StaleWorkers, err)
	}
}
