package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

// seedRun inserts one worker_runs row directly. finishedAt nil writes NULL,
// which is how an in-flight run is recorded.
func seedRun(t *testing.T, st *Store, worker string, startedAt int64, finishedAt *int64, status string) {
	t.Helper()
	var fin any
	if finishedAt != nil {
		fin = *finishedAt
	}
	if _, err := st.w.ExecContext(context.Background(),
		`INSERT INTO worker_runs (worker, started_at, finished_at, status, detail) VALUES (?,?,?,?,?)`,
		worker, startedAt, fin, status, ""); err != nil {
		t.Fatalf("seed %s@%d: %v", worker, startedAt, err)
	}
}

// TestRecentWorkerRunsPerWorker_FastWorkerCannotCrowdOutSlowOne is THE
// regression. A global newest-N read is dominated by whichever worker cycles
// fastest, so a rare-cadence worker vanishes from the result entirely and every
// verdict computed downstream silently excludes it. That is what made
// /api/ready blind to 67 of 101 workers. If anyone reverts this to a global
// LIMIT, this test must fail.
func TestRecentWorkerRunsPerWorker_FastWorkerCannotCrowdOutSlowOne(t *testing.T) {
	st := openTemp(t)
	for i := 0; i < 200; i++ {
		seedRun(t, st, "fast", int64(10000+i), nil, "ok")
	}
	for _, ts := range []int64{100, 200, 300} {
		seedRun(t, st, "slow", ts, nil, "ok")
	}

	rows, err := st.RecentWorkerRunsPerWorker(context.Background(), 20)
	if err != nil {
		t.Fatalf("RecentWorkerRunsPerWorker: %v", err)
	}
	got := map[string]int{}
	for _, r := range rows {
		got[r.Worker]++
	}
	if got["fast"] != 20 {
		t.Fatalf("fast: expected exactly 20 rows (the per-worker cap), got %d", got["fast"])
	}
	if got["slow"] != 3 {
		t.Fatalf("slow: expected all 3 rows, got %d — a rare-cadence worker was crowded out", got["slow"])
	}
}

// TestRecentWorkerRunsPerWorker_ReturnsNewestPerWorkerInDescendingOrder pins
// within-worker ordering: cadence is computed from consecutive differences, so
// newest-first is load-bearing, not cosmetic.
func TestRecentWorkerRunsPerWorker_ReturnsNewestPerWorkerInDescendingOrder(t *testing.T) {
	st := openTemp(t)
	for _, ts := range []int64{1, 2, 3, 4, 5} {
		seedRun(t, st, "w", ts, nil, "ok")
	}

	rows, err := st.RecentWorkerRunsPerWorker(context.Background(), 3)
	if err != nil {
		t.Fatalf("RecentWorkerRunsPerWorker: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}
	want := []int64{5, 4, 3}
	for i, w := range want {
		if rows[i].StartedAt != w {
			t.Fatalf("row %d: expected started_at %d, got %d", i, w, rows[i].StartedAt)
		}
	}
}

// TestRecentWorkerRunsPerWorker_RejectsNonPositivePerWorker guards the sentinel
// choice. Callers test sql.ErrNoRows to mean "no data", so returning it for a
// bad argument would let a programming error read as an empty fleet — a
// watchdog reporting health it never measured.
func TestRecentWorkerRunsPerWorker_RejectsNonPositivePerWorker(t *testing.T) {
	st := openTemp(t)
	for _, n := range []int{0, -1} {
		rows, err := st.RecentWorkerRunsPerWorker(context.Background(), n)
		if err == nil {
			t.Fatalf("perWorker=%d: expected an error, got nil and %d rows", n, len(rows))
		}
		if errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("perWorker=%d: error must NOT be sql.ErrNoRows, callers read that as 'no data'", n)
		}
	}
}

// TestRecentWorkerRunsPerWorker_PreservesNullFinishedAt keeps the in-flight
// signal intact: a NULL finished_at is what separates "currently retrying" from
// "failed and stopped", and failingFromRuns depends on that distinction.
func TestRecentWorkerRunsPerWorker_PreservesNullFinishedAt(t *testing.T) {
	st := openTemp(t)
	fin := int64(500)
	seedRun(t, st, "w", 100, &fin, "ok")
	seedRun(t, st, "w", 600, nil, "running")

	rows, err := st.RecentWorkerRunsPerWorker(context.Background(), 20)
	if err != nil {
		t.Fatalf("RecentWorkerRunsPerWorker: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	// Newest first: the running row leads.
	if rows[0].Status != "running" || rows[0].FinishedAt != nil {
		t.Fatalf("in-flight run must keep a nil FinishedAt; got status=%q finishedAt=%v",
			rows[0].Status, rows[0].FinishedAt)
	}
	if rows[1].FinishedAt == nil || *rows[1].FinishedAt != 500 {
		t.Fatalf("finished run must carry finished_at=500, got %v", rows[1].FinishedAt)
	}
}
