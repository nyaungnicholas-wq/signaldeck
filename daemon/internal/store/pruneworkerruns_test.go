package store

import (
	"context"
	"fmt"
	"testing"
)

// PruneWorkerRuns has four survival rules and they interact. Only the
// liveness floor had a test; this pins the provenance floor added alongside it
// and, more importantly, that adding it did not loosen the others.
//
// The shape that matters: prediction-runner rows outlive their own prune
// window because every predictions row and every prediction_ledger entry
// points back at one of its passes, and the 20-row liveness floor is ~3.3
// hours at a 10-minute cadence.

// seedRuns inserts n runs for a worker, oldest first, and returns nothing --
// started_at is the ordering key the prune uses.
func seedRuns(t *testing.T, st *Store, worker string, n int, base int64) {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < n; i++ {
		if _, err := st.w.ExecContext(ctx,
			`INSERT INTO worker_runs (worker, started_at, status, detail) VALUES (?,?,?,?)`,
			worker, base+int64(i), "ok", fmt.Sprintf("run %d", i)); err != nil {
			t.Fatalf("seed %s: %v", worker, err)
		}
	}
}

func countRuns(t *testing.T, st *Store, worker string) int {
	t.Helper()
	var n int
	if err := st.db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM worker_runs WHERE worker=?`, worker).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", worker, err)
	}
	return n
}

// TestPruneWorkerRuns_KeepsProvenanceWorkerDeeper is the new floor. A
// high-cadence ordinary worker is cut to its 20-row liveness floor while
// prediction-runner keeps its full history, so a stored forecast can still be
// tied to the pass that produced it.
func TestPruneWorkerRuns_KeepsProvenanceWorkerDeeper(t *testing.T) {
	st := openTestStore(t)

	// Both well past the 20-row floor, and both outside a deliberately tiny
	// global window so only the per-worker rules can save them.
	seedRuns(t, st, "hud-sync", 300, 1_000_000)
	seedRuns(t, st, "prediction-runner", 300, 1_000_000)

	if err := st.PruneWorkerRuns(context.Background(), 10); err != nil {
		t.Fatalf("prune: %v", err)
	}

	if got := countRuns(t, st, "hud-sync"); got > 30 {
		t.Fatalf("ordinary worker kept %d rows, want ~20 (the liveness floor, plus any inside the global window)", got)
	}
	if got := countRuns(t, st, "prediction-runner"); got != 300 {
		t.Fatalf("prediction-runner kept %d of 300 rows; its history must survive the prune", got)
	}
}

// TestPruneWorkerRuns_StillPrunesAndStillKeepsTheOtherFloors guards against
// the provenance floor having quietly turned the prune into a no-op, and
// against it having displaced the rules that were already there.
func TestPruneWorkerRuns_StillPrunesAndStillKeepsTheOtherFloors(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	seedRuns(t, st, "hud-sync", 500, 1_000_000)
	// The research-loop grid-search exemption: a record of a LOOK taken at the
	// corpus, which multiplicity accounting has already been charged for.
	if _, err := st.w.ExecContext(ctx,
		`INSERT INTO worker_runs (worker, started_at, status, detail) VALUES (?,?,?,?)`,
		"research-loop", 1, "ok", "searched a 12-rule grid over 3 kinds"); err != nil {
		t.Fatalf("seed research-loop: %v", err)
	}

	before := countRuns(t, st, "hud-sync")
	if err := st.PruneWorkerRuns(ctx, 10); err != nil {
		t.Fatalf("prune: %v", err)
	}
	after := countRuns(t, st, "hud-sync")
	if after >= before {
		t.Fatalf("prune removed nothing: %d -> %d rows", before, after)
	}
	if after < 20 {
		t.Fatalf("liveness floor breached: %d rows left, want at least 20", after)
	}
	// The oldest row in the table, exempt only by its detail text.
	if got := countRuns(t, st, "research-loop"); got != 1 {
		t.Fatalf("grid-search record was pruned (%d left); deleting one refunds multiplicity that was actually spent", got)
	}
}
