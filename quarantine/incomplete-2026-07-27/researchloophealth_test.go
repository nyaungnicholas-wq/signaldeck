package store

import (
	"context"
	"testing"
	"time"
)

// THE LIVE SHAPE THE DETECTOR GOT WRONG.
// research_loop_runs held only reconstruction placeholders — rows that state
// outright (git_rev='backfill:worker_runs', judged=0) that their judgments are
// unrecoverable — while research_loop_judgments was empty. The detector read
// MAX(day) off those rows and returned Healthy=true, which is the one answer it
// exists to make impossible: it renders "no search is on record" as "searched
// and found nothing".
func TestLoopEngineHealthRejectsBackfillPlaceholders(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)

	for i, off := range []int{1, 2} {
		ts := now.AddDate(0, 0, -off).Unix()
		if _, err := st.DB().ExecContext(ctx,
			`INSERT INTO worker_runs (id, worker, started_at, status, detail)
			 VALUES (?, 'research-loop', ?, 'ok', 'searched a 12-rule grid')`,
			i+1, ts); err != nil {
			t.Fatal(err)
		}
	}
	n, err := st.BackfillLoopRuns(ctx)
	if err != nil || n != 2 {
		t.Fatalf("backfill placeholders: n=%d err=%v", n, err)
	}

	var judgments int
	if err := st.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM research_loop_judgments`).Scan(&judgments); err != nil {
		t.Fatal(err)
	}
	if judgments != 0 {
		t.Fatalf("fixture must have an empty judgment ledger, got %d", judgments)
	}

	h, err := st.LoopEngineHealth(ctx, 0, now)
	if err != nil {
		t.Fatal(err)
	}
	if h.Healthy {
		t.Fatalf("placeholder rows must not read as a live engine: %+v", h)
	}
	if h.State != LoopEngineUnrecorded {
		t.Fatalf("state = %q, want %q (%s)", h.State, LoopEngineUnrecorded, h.Detail)
	}

	// A real pass — grid searched, rules judged — is what makes it live again.
	day := now.Format("2006-01-02")
	if err := st.RecordLoopSearch(ctx,
		LoopRun{Day: day, RanAt: now.Unix(), GridSize: 12, Judged: 1},
		[]LoopHypothesis{{ID: "r1", Status: "rejected", ObsWindow: "w"}},
		[]LoopJudgment{{Day: day, RuleID: "r1", Status: "rejected"}}); err != nil {
		t.Fatal(err)
	}
	live, err := st.LoopEngineHealth(ctx, 0, now)
	if err != nil {
		t.Fatal(err)
	}
	if !live.Healthy || live.State != LoopEngineLive {
		t.Fatalf("an evidenced pass must read live: %+v", live)
	}
}
