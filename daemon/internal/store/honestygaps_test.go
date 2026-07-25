package store

import (
	"context"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

func TestReturnForecastRoundTrip(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	if err != nil {
		t.Fatal(err)
	}
	skill := 0.12
	cov := 0.81
	f := ReturnForecast{
		SymbolID: sym.ID, Horizon: md.H1d, Ts: 1000, Regime: "calm", N: 240,
		Tau: 0.001, Mean: 0.0004, Sigma: 0.014, Q10: -0.017, Q50: 0.0003, Q90: 0.018,
		PUp: 0.44, PDown: 0.41, PInside: 0.15, Edge: 0.03, ExpectedValue: -0.0006,
		Skill: &skill, Coverage80: &cov, GradedN: 180,
	}
	if err := st.UpsertReturnForecast(ctx, f); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	// Idempotent on (symbol, horizon): a second write replaces, never duplicates.
	f.Ts, f.Regime, f.N = 2000, "elevated", 300
	if err := st.UpsertReturnForecast(ctx, f); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	rows, err := st.ReturnForecasts(ctx, "", "", 0)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1 (upsert must replace)", len(rows))
	}
	got := rows[0]
	if got.Symbol != "AAPL" || got.Regime != "elevated" || got.Ts != 2000 || got.N != 300 {
		t.Fatalf("round trip lost data: %+v", got)
	}
	if got.Skill == nil || *got.Skill != skill || got.Coverage80 == nil || *got.Coverage80 != cov {
		t.Fatalf("nullable grades lost: %+v", got)
	}

	// A NULL skill must come back as nil, not as a fabricated zero — "not graded"
	// and "graded at zero skill" mean different things.
	f.Skill, f.Coverage80, f.GradedN = nil, nil, 0
	if err := st.UpsertReturnForecast(ctx, f); err != nil {
		t.Fatal(err)
	}
	rows, _ = st.ReturnForecasts(ctx, "", "", 0)
	if rows[0].Skill != nil || rows[0].Coverage80 != nil {
		t.Fatalf("ungraded forecast came back with a skill: %+v", rows[0])
	}

	// Filters.
	if rows, _ := st.ReturnForecasts(ctx, "1w", "", 0); len(rows) != 0 {
		t.Fatalf("horizon filter leaked %d rows", len(rows))
	}
	if rows, _ := st.ReturnForecasts(ctx, "", "MSFT", 0); len(rows) != 0 {
		t.Fatalf("symbol filter leaked %d rows", len(rows))
	}
	if rows, _ := st.ReturnForecasts(ctx, "1d", "AAPL", 0); len(rows) != 1 {
		t.Fatal("matching filters returned nothing")
	}

	// Deletion is how a refusal clears a stale forecast.
	if err := st.DeleteReturnForecasts(ctx, sym.ID); err != nil {
		t.Fatal(err)
	}
	if rows, _ := st.ReturnForecasts(ctx, "", "", 0); len(rows) != 0 {
		t.Fatalf("delete left %d rows", len(rows))
	}
}

func TestDatasetVersionRoundTrip(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	if err != nil {
		t.Fatal(err)
	}
	// The first read must report "none stored", because a first observation can
	// never be a revision.
	if _, ok, err := st.DatasetVersion(ctx, sym.ID, "1d"); err != nil || ok {
		t.Fatalf("unstored slice reported ok=%v err=%v", ok, err)
	}
	v := DatasetVersion{SymbolID: sym.ID, Timeframe: "1d", FirstTs: 100, LastTs: 900,
		N: 500, Hash: "abc", CheckedAt: 1000}
	if err := st.UpsertDatasetVersion(ctx, v); err != nil {
		t.Fatal(err)
	}
	got, ok, err := st.DatasetVersion(ctx, sym.ID, "1d")
	if err != nil || !ok {
		t.Fatalf("read back failed: ok=%v err=%v", ok, err)
	}
	if got.Hash != "abc" || got.N != 500 || got.Revisions != 0 {
		t.Fatalf("round trip lost data: %+v", got)
	}
	// A recorded revision must persist and sort the symbol to the front.
	v.Hash, v.Revisions, v.CheckedAt = "def", 1, 2000
	if err := st.UpsertDatasetVersion(ctx, v); err != nil {
		t.Fatal(err)
	}
	rows, err := st.DatasetVersions(ctx, 0)
	if err != nil || len(rows) != 1 {
		t.Fatalf("list = %d rows, err=%v", len(rows), err)
	}
	if rows[0].Revisions != 1 || rows[0].Hash != "def" || rows[0].Symbol != "AAPL" {
		t.Fatalf("revision not persisted: %+v", rows[0])
	}
}

func TestCanaryTrialRoundTrip(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()
	tr := CanaryTrial{
		Model: "directional-ensemble-1d", Incumbent: "v9", Challenger: "v10",
		Decision: "hold", Serving: "v9", Reason: "too few observations",
		IncN: 5000, IncAcc: 0.48, ChN: 12, ChAcc: 0.58, ChLower: 0.3, ChUpper: 0.8,
		Baseline: 0.54, DecidedAt: 1000,
	}
	if err := st.UpsertCanaryTrial(ctx, tr); err != nil {
		t.Fatal(err)
	}
	// One row per model family — a re-decision replaces, so history cannot
	// accumulate stale verdicts that contradict each other.
	tr.Decision, tr.ChN, tr.DecidedAt = "promote", 900, 2000
	tr.Serving = "v10"
	if err := st.UpsertCanaryTrial(ctx, tr); err != nil {
		t.Fatal(err)
	}
	rows, err := st.CanaryTrials(ctx)
	if err != nil || len(rows) != 1 {
		t.Fatalf("got %d trials, err=%v", len(rows), err)
	}
	if rows[0].Decision != "promote" || rows[0].Serving != "v10" || rows[0].ChN != 900 {
		t.Fatalf("round trip lost data: %+v", rows[0])
	}
}

// VersionedOutcomes is what makes the canary possible without new plumbing, and
// its whole value depends on the independent-observation dedup being right.
func TestVersionedOutcomesDedupesToOnePerSymbolDay(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()
	a, err := st.UpsertSymbol(ctx, "AAA", md.Stocks, "A")
	if err != nil {
		t.Fatal(err)
	}
	b, err := st.UpsertSymbol(ctx, "BBB", md.Stocks, "B")
	if err != nil {
		t.Fatal(err)
	}
	const day = int64(86400)
	// Three intraday rows on the SAME day for AAA. Pooling them would inflate n
	// threefold and count one market move as three observations.
	type row struct {
		sym     int64
		ts      int64
		prob    float64
		up      int
		version int
	}
	rows := []row{
		{a.ID, day + 100, 0.6, 1, 9},
		{a.ID, day + 200, 0.6, 1, 9},
		{a.ID, day + 300, 0.4, 1, 10}, // latest on the day: this is the kept one
		{a.ID, 2*day + 100, 0.7, 0, 10},
		{b.ID, day + 100, 0.55, 1, 10},
	}
	for _, r := range rows {
		if err := st.InsertFeatures(ctx, r.sym, md.H1d, r.ts, r.version,
			map[string]float64{"x": 1}); err != nil {
			t.Fatal(err)
		}
		if err := st.UpsertPrediction(ctx, Prediction{
			SymbolID: r.sym, Horizon: md.H1d, Ts: r.ts, RawProb: r.prob,
			CalProb: r.prob, NUsed: 3, Components: "{}",
		}); err != nil {
			t.Fatal(err)
		}
		fwd := -0.01
		if r.up == 1 {
			fwd = 0.01
		}
		if err := st.ResolvePrediction(ctx, r.sym, md.H1d, r.ts, fwd); err != nil {
			t.Fatal(err)
		}
	}
	got, err := st.VersionedOutcomes(ctx, md.H1d, 0)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d observations, want 3 (AAA day1 + AAA day2 + BBB day1); dedup is broken", len(got))
	}
	// Exactly which rows survived: AAA's LATEST day-1 row (day+300), AAA's day-2
	// row, and BBB's day-1 row. The superseded AAA rows at day+100 and day+200
	// must be gone — but BBB legitimately also has a row at day+100, so the
	// check is on the multiset of timestamps, not on any single one.
	counts := map[int64]int{}
	for _, o := range got {
		counts[o.Ts]++
	}
	want := map[int64]int{day + 300: 1, 2*day + 100: 1, day + 100: 1}
	for ts, n := range want {
		if counts[ts] != n {
			t.Fatalf("timestamp %d appears %d times, want %d (got %+v)", ts, counts[ts], n, got)
		}
	}
	if counts[day+200] != 0 {
		t.Fatalf("a superseded intraday row survived dedup: %+v", got)
	}
	// The kept AAA day-1 row is the LATEST one (prob 0.4, version 10), and since
	// it leaned down while the day closed up it must be scored wrong.
	for _, o := range got {
		if o.Ts != day+300 {
			continue
		}
		if o.Version != 10 {
			t.Fatalf("kept row has version %d, want 10", o.Version)
		}
		if o.Correct {
			t.Fatal("a 0.4 forecast on an up day was scored correct")
		}
	}
	// A different horizon must not leak in.
	if got, _ := st.VersionedOutcomes(ctx, md.H1w, 0); len(got) != 0 {
		t.Fatalf("1w query returned %d 1d rows", len(got))
	}
}
