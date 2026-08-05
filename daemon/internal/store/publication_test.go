// Persistence tests for publication verdicts and grader heartbeats.
//
// The load-bearing one is TestRetirementIsStickyAtTheDatabase: the schema
// guard is a BEFORE INSERT trigger, not the BEFORE UPDATE trigger the obvious
// design reaches for, because this table is append-only and an un-retire
// arrives as a fresh retired=0 row that an UPDATE trigger would never see.
package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func retiredRow() PublicationVerdictRow {
	return PublicationVerdictRow{
		Predictor: "directional-ensemble", Horizon: "1d",
		PublicationStatus: "FAILED", Retired: true, RetirementSticky: true,
		RetireReason:     "clustered Wilson upper bound below prequential null",
		RetirementSource: "wilson",
		Reasons:          []string{"upper bound below null"},
		EvidenceRefs:     []string{"directional-ensemble-1d"},
		EvaluatedAt:      "2026-07-26T14:05:00.000Z",
	}
}

func TestRetirementIsStickyAtTheDatabase(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()

	if err := st.PutPublicationVerdict(ctx, retiredRow()); err != nil {
		t.Fatalf("seed retired verdict: %v", err)
	}

	// A later pass on a thinner or friendlier window tries to publish the same
	// row as healthy. The database must refuse it even if every layer above
	// this one has been bypassed.
	revive := retiredRow()
	revive.PublicationStatus = "OK"
	revive.Retired = false
	revive.RetirementSticky = false
	revive.EvaluatedAt = "2026-08-04T14:05:00.000Z"

	err := st.PutPublicationVerdict(ctx, revive)
	if err == nil {
		t.Fatal("the database accepted an un-retire; the sticky guard is decorative")
	}
	if !errors.Is(err, ErrUnretireRefused) {
		t.Fatalf("un-retire must fail as ErrUnretireRefused so callers cannot treat it as a retryable write error, got %v", err)
	}
}

func TestRetirementHistoryOutlivesTheWindow(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()

	if err := st.PutPublicationVerdict(ctx, retiredRow()); err != nil {
		t.Fatal(err)
	}
	// A later RETIRED row is fine — retirement is one-way, not write-once.
	later := retiredRow()
	later.PublicationStatus = "RETIRED"
	later.RetirementSource = "history"
	later.EvaluatedAt = "2026-08-04T14:05:00.000Z"
	if err := st.PutPublicationVerdict(ctx, later); err != nil {
		t.Fatalf("re-asserting retirement must be allowed: %v", err)
	}

	retired, reason, source, err := st.RetirementHistory(ctx, "directional-ensemble", "1d", "")
	if err != nil {
		t.Fatal(err)
	}
	if !retired {
		t.Fatal("history must report this row as retired")
	}
	// The FIRST condemnation is the one that explains why, not the latest
	// restatement of it.
	if source != "wilson" || reason == "" {
		t.Fatalf("history must carry the original reason and source, got source=%q reason=%q", source, reason)
	}
}

func TestLatestVerdictsReturnsNewestPerRow(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()

	for _, at := range []string{"2026-08-01T00:00:00.000Z", "2026-08-04T00:00:00.000Z"} {
		r := retiredRow()
		r.EvaluatedAt = at
		r.PublicationStatus = "RETIRED"
		if err := st.PutPublicationVerdict(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	got, err := st.LatestVerdicts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected one current row per (predictor,horizon,variant), got %d", len(got))
	}
	if got[0].EvaluatedAt != "2026-08-04T00:00:00.000Z" {
		t.Fatalf("expected the newest evaluation, got %s", got[0].EvaluatedAt)
	}
}

// Every unknown must resolve to stale. A grader that has never reported is not
// a grader that is healthy — that reading is what let a 33-hour outage pass.
func TestGraderStaleTreatsUnknownsAsStale(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)

	stale, why, err := st.GraderStale(ctx, "accuracy_registry", 26*time.Hour, now)
	if err != nil {
		t.Fatal(err)
	}
	if !stale || why == "" {
		t.Fatalf("no heartbeat at all must read stale with a reason, got stale=%v why=%q", stale, why)
	}

	// A failed run is stale regardless of how recent it is.
	if err := st.PutGraderHeartbeat(ctx, GraderHeartbeat{
		Task: "accuracy_registry", Success: false,
		FinishedAt: now.Add(-time.Minute).Format("2006-01-02T15:04:05.000Z"),
		Error:      "grader refused",
	}); err != nil {
		t.Fatal(err)
	}
	if stale, _, _ := st.GraderStale(ctx, "accuracy_registry", 26*time.Hour, now); !stale {
		t.Fatal("a recent FAILED run must still read stale")
	}

	// A successful run inside the window is fresh — but only once it is the
	// NEWEST heartbeat. The latest attempt is what describes current state: a
	// success from this morning does not un-fail a refusal from this afternoon,
	// and reading it as though it did is the "stale refusal, no alarm" defect.
	if err := st.PutGraderHeartbeat(ctx, GraderHeartbeat{
		Task: "accuracy_registry", Success: true,
		FinishedAt: now.Add(-30 * time.Second).Format("2006-01-02T15:04:05.000Z"),
	}); err != nil {
		t.Fatal(err)
	}
	if stale, why, _ := st.GraderStale(ctx, "accuracy_registry", 26*time.Hour, now); stale {
		t.Fatalf("the newest run succeeding inside the window must read fresh, got %q", why)
	}

	// And the same run, once it ages past the window, must go stale again.
	if stale, _, _ := st.GraderStale(ctx, "accuracy_registry", time.Second, now); !stale {
		t.Fatal("a successful run older than maxAge must read stale")
	}
}
