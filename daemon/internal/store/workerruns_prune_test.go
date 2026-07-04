package store

import (
	"context"
	"fmt"
	"testing"
)

// TestPruneWorkerRuns_PerWorkerFloor is the "13f-poller never ran" regression:
// a flapping high-frequency worker (crypto-live erroring every ~5s) used to
// flood the global keep-N window and prune away every trace of rare-cadence
// workers (13f-poller runs once per 24h). The prune must now retain the newest
// 20 rows per worker regardless of the global cap.
func TestPruneWorkerRuns_PerWorkerFloor(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()

	// One lone run for the rare worker (inserted FIRST ⇒ oldest by id).
	rareID, err := st.StartWorkerRun(ctx, "13f-poller")
	if err != nil {
		t.Fatalf("start rare: %v", err)
	}
	if err := st.FinishWorkerRun(ctx, rareID, "ok", "stored 12 holdings"); err != nil {
		t.Fatalf("finish rare: %v", err)
	}

	// A flood of newer runs from one noisy worker.
	for i := 0; i < 40; i++ {
		id, err := st.StartWorkerRun(ctx, "crypto-live")
		if err != nil {
			t.Fatalf("start noisy %d: %v", i, err)
		}
		if err := st.FinishWorkerRun(ctx, id, "error", fmt.Sprintf("unreachable %d", i)); err != nil {
			t.Fatalf("finish noisy %d: %v", i, err)
		}
	}

	// Global cap far below the flood: without the per-worker floor the rare
	// worker's only row would be deleted.
	if err := st.PruneWorkerRuns(ctx, 10); err != nil {
		t.Fatalf("prune: %v", err)
	}

	runs, err := st.RecentWorkerRuns(ctx, 1000)
	if err != nil {
		t.Fatalf("recent: %v", err)
	}
	var rare, noisy int
	for _, r := range runs {
		switch r.Worker {
		case "13f-poller":
			rare++
		case "crypto-live":
			noisy++
		}
	}
	if rare != 1 {
		t.Fatalf("rare worker rows = %d, want 1 (per-worker floor must protect it)", rare)
	}
	if noisy != 20 {
		t.Fatalf("noisy worker rows = %d, want 20 (per-worker floor)", noisy)
	}
}
