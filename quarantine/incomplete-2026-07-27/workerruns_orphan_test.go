package store

import (
	"context"
	"testing"
	"time"
)

// TestReconcileOrphanRuns_NoRowSurvivesRestart simulates a daemon that died
// mid-run: rows opened before boot are left at 'running'. After the boot-time
// sweep NO row may remain non-terminal, the orphans must NOT be recorded as
// 'ok' (their outcome is genuinely unknown), and they must NOT be deleted —
// deleting a look refunds multiplicity the fleet actually spent, which would
// shrink the research loop's Bonferroni divisor.
func TestReconcileOrphanRuns_NoRowSurvivesRestart(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()

	// Previous process: two runs opened, one of them completed normally.
	deadA, err := st.StartWorkerRun(ctx, "research-loop")
	if err != nil {
		t.Fatalf("start A: %v", err)
	}
	deadB, err := st.StartWorkerRun(ctx, "13f-poller")
	if err != nil {
		t.Fatalf("start B: %v", err)
	}
	if err := st.FinishWorkerRun(ctx, deadB, "ok", "stored 12 holdings"); err != nil {
		t.Fatalf("finish B: %v", err)
	}
	// Push both into the previous process's lifetime.
	boot := time.Now().Unix()
	if _, err := st.DB().Exec(
		`UPDATE worker_runs SET started_at = ?`, boot-3600); err != nil {
		t.Fatalf("age rows: %v", err)
	}

	// A run belonging to THIS process must be left alone by the sweep.
	live, err := st.StartWorkerRun(ctx, "crypto-live")
	if err != nil {
		t.Fatalf("start live: %v", err)
	}

	n, err := st.ReconcileOrphanRuns(ctx, boot)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if n != 1 {
		t.Fatalf("reconciled %d rows, want 1 (only the pre-boot 'running' row)", n)
	}

	// No pre-boot row may still be non-terminal.
	var stuck int
	if err := st.DB().QueryRow(
		`SELECT COUNT(*) FROM worker_runs WHERE status='running' AND started_at < ?`,
		boot).Scan(&stuck); err != nil {
		t.Fatalf("count stuck: %v", err)
	}
	if stuck != 0 {
		t.Fatalf("%d rows still 'running' across the restart boundary, want 0", stuck)
	}

	// Nothing was deleted: the look count cannot shrink.
	var total int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM worker_runs`).Scan(&total); err != nil {
		t.Fatalf("count total: %v", err)
	}
	if total != 3 {
		t.Fatalf("worker_runs has %d rows after reconcile, want 3 — rows must never be dropped", total)
	}

	var status, detail string
	if err := st.DB().QueryRow(
		`SELECT status, detail FROM worker_runs WHERE id=?`, deadA).Scan(&status, &detail); err != nil {
		t.Fatalf("read orphan: %v", err)
	}
	if status != "orphaned" {
		t.Fatalf("orphan status = %q, want %q (never 'ok' — the outcome is unknown)", status, "orphaned")
	}
	if detail == "" {
		t.Fatal("orphan detail is empty; it must name the boot boundary")
	}

	// The completed row keeps its honest outcome, and the live run is untouched.
	if err := st.DB().QueryRow(`SELECT status FROM worker_runs WHERE id=?`, deadB).Scan(&status); err != nil {
		t.Fatalf("read finished: %v", err)
	}
	if status != "ok" {
		t.Fatalf("completed run rewritten to %q", status)
	}
	if err := st.DB().QueryRow(`SELECT status FROM worker_runs WHERE id=?`, live).Scan(&status); err != nil {
		t.Fatalf("read live: %v", err)
	}
	if status != "running" {
		t.Fatalf("this process's in-flight run was reconciled as %q; only pre-boot rows are orphans", status)
	}
}
