package api

import (
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// run builds one worker_runs row. ranFor < 0 leaves it in flight.
func run(worker string, startedAt, ranFor int64, status string) md.WorkerRun {
	r := md.WorkerRun{Worker: worker, StartedAt: startedAt, Status: status}
	if ranFor >= 0 {
		fin := startedAt + ranFor
		r.FinishedAt = &fin
	}
	return r
}

// TestFailingFromRuns_RecoveredStreamIngestorClears pins the fix for a status
// that could never clear. A stream ingestor errors on a cycle while its source is
// down, then connects and stays in ONE run that never finishes. Reporting its
// last COMPLETED run left it permanently red while it streamed healthily.
func TestFailingFromRuns_RecoveredStreamIngestorClears(t *testing.T) {
	const now = int64(10_000)
	runs := []md.WorkerRun{ // newest first
		run("crypto-live", 9_400, -1, "running"), // in flight 600s
		run("crypto-live", 9_330, 60, "error"),   // the 60s failure it replaced
	}
	got := failingFromRuns(runs, now)
	if s, bad := got["crypto-live"]; bad {
		t.Fatalf("crypto-live reported %q: an in-flight run that has outlasted the "+
			"failure it followed is recovery, and a status that cannot clear is one "+
			"nobody can act on", s)
	}
}

// TestFailingFromRuns_FastCyclerStaysRed is the other half, and the reason the
// original rule skipped every in-flight run: a worker that fails every 60s and is
// 5s into its next attempt has not recovered. Crediting that gap would let a
// flapping worker read green between two failures.
func TestFailingFromRuns_FastCyclerStaysRed(t *testing.T) {
	const now = int64(10_000)
	runs := []md.WorkerRun{
		run("flappy", 9_995, -1, "running"), // only 5s in
		run("flappy", 9_930, 60, "error"),
	}
	if got := failingFromRuns(runs, now)["flappy"]; got != "error" {
		t.Fatalf("flappy = %q, want \"error\": 5s into a retry from a 60s failure is "+
			"not recovery", got)
	}
}

// TestFailingFromRuns_PlainStatuses covers the unchanged behaviour: newest
// completed run decides, "ok" clears, and a worker with no in-flight run keeps
// its failure.
func TestFailingFromRuns_PlainStatuses(t *testing.T) {
	const now = int64(10_000)
	runs := []md.WorkerRun{
		run("healthy", 9_900, 5, "ok"),
		run("healthy", 9_800, 5, "error"), // older, must not win
		run("broken", 9_900, 5, "error"),
		run("degraded-one", 9_900, 5, "degraded"),
	}
	got := failingFromRuns(runs, now)
	if _, bad := got["healthy"]; bad {
		t.Fatalf("healthy should clear on its newest ok run, got %v", got["healthy"])
	}
	if got["broken"] != "error" {
		t.Fatalf("broken = %q, want error", got["broken"])
	}
	if got["degraded-one"] != "degraded" {
		t.Fatalf("degraded-one = %q, want degraded", got["degraded-one"])
	}
}
