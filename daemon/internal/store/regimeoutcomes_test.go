// Tests for the live regime-forecast grading loop persistence: the freeze
// (per-day dedup), the due-window read, the single-shot resolve, the graded
// track-record read, miss postmortems, and the weekly-digest call read.
package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/structregime"
)

func TestRegimeOutcomes_FreezeResolveGrade(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()

	sym, _ := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	if err := st.UpsertRegimeForecast(ctx, sym.ID, 1000, structregime.Forecast{
		Kind: structregime.Kind("trend21"), HorizonDays: 21, Regime: "uptrend",
		Conviction: 0.9, HistoricalAccuracy: 0.62, Tier: "high", Rank: 0.8, N: 40,
	}); err != nil {
		t.Fatalf("upsert forecast: %v", err)
	}

	calls, err := st.RegimeForecastCalls(ctx)
	if err != nil || len(calls) != 1 {
		t.Fatalf("RegimeForecastCalls = %d rows, %v; want 1", len(calls), err)
	}
	c := calls[0]
	if c.SymbolID != sym.ID || c.Kind != "trend21" || c.Conviction != 0.9 {
		t.Fatalf("call mangled: %+v", c)
	}

	// Freeze once per (symbol, kind, day): the second insert dedups.
	fresh, err := st.InsertRegimeOutcome(ctx, c)
	if err != nil || !fresh {
		t.Fatalf("first freeze = %v, %v; want true", fresh, err)
	}
	c2 := c
	c2.Ts = c.Ts + 3600 // same UTC day
	if fresh, _ := st.InsertRegimeOutcome(ctx, c2); fresh {
		t.Fatal("same-day re-freeze was not deduped")
	}

	// Not due before the forward window elapses; due after.
	windowEnd := c.Ts + int64(float64(c.HorizonDays)*1.45*86400)
	if due, _ := st.DueRegimeOutcomes(ctx, windowEnd-100, 0); len(due) != 0 {
		t.Fatal("outcome due before its window elapsed")
	}
	due, err := st.DueRegimeOutcomes(ctx, windowEnd+100, 0)
	if err != nil || len(due) != 1 || due[0].Correct != -1 {
		t.Fatalf("due = %+v, %v; want 1 unresolved row", due, err)
	}

	if err := st.ResolveRegimeOutcome(ctx, due[0].ID, "downtrend", false, 999999); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	// Resolve is single-shot: a second grade must not overwrite the first.
	if err := st.ResolveRegimeOutcome(ctx, due[0].ID, "uptrend", true, 999999); err != nil {
		t.Fatalf("re-resolve: %v", err)
	}
	if due, _ := st.DueRegimeOutcomes(ctx, windowEnd+100, 0); len(due) != 0 {
		t.Fatal("resolved outcome still listed as due")
	}
	res, err := st.ResolvedRegimeOutcomes(ctx, 0)
	if err != nil || len(res) != 1 {
		t.Fatalf("resolved = %d rows, %v; want 1", len(res), err)
	}
	r := res[0]
	if r.Actual != "downtrend" || r.Correct != 0 || r.ResolvedAt != 999999 {
		t.Fatalf("second resolve overwrote the grade: %+v", r)
	}

	// High-conviction miss → postmortem, at most once per outcome.
	if err := st.InsertRegimePostmortem(ctx, r.ID, sym.ID, r, "ret21", -0.08,
		"trend reversed on earnings", 5000); err != nil {
		t.Fatalf("insert postmortem: %v", err)
	}
	if err := st.InsertRegimePostmortem(ctx, r.ID, sym.ID, r, "ret21", -0.08,
		"duplicate", 6000); err != nil {
		t.Fatalf("re-insert postmortem: %v", err)
	}
	pms, err := st.RecentRegimePostmortems(ctx, 0)
	if err != nil || len(pms) != 1 {
		t.Fatalf("postmortems = %d rows, %v; want 1 (deduped)", len(pms), err)
	}
	if pms[0].Symbol != "AAPL" || pms[0].Narrative != "trend reversed on earnings" ||
		pms[0].KeyName != "ret21" {
		t.Fatalf("postmortem mangled: %+v", pms[0])
	}

	// Weekly-digest read: frozen calls since a floor, active symbols only.
	week, err := st.RegimeOutcomeCallsSince(ctx, 0)
	if err != nil || len(week) != 1 || week[0].Symbol != "AAPL" || week[0].Regime != "uptrend" {
		t.Fatalf("RegimeOutcomeCallsSince = %+v, %v", week, err)
	}
	if week, _ := st.RegimeOutcomeCallsSince(ctx, c.Ts+1); len(week) != 0 {
		t.Fatal("since floor not applied")
	}
}

func TestLatestPeriodicFiling_ExcludesNonPeriodicForms(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()

	sym, _ := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	if _, ok, err := st.LatestPeriodicFiling(ctx, sym.ID); err != nil || ok {
		t.Fatalf("absent filing = ok=%v, %v; want false", ok, err)
	}
	for _, f := range []FilingRow{
		{ID: "a1", SymbolID: sym.ID, Form: "10-Q", FiledTs: 1000, Title: "q1", URL: "u"},
		{ID: "a2", SymbolID: sym.ID, Form: "8-K", FiledTs: 3000, Title: "pr", URL: "u"},
		{ID: "a3", SymbolID: sym.ID, Form: "10-K", FiledTs: 2000, Title: "fy", URL: "u"},
	} {
		if _, err := st.InsertFiling(ctx, f); err != nil {
			t.Fatalf("insert filing: %v", err)
		}
	}
	p, ok, err := st.LatestPeriodicFiling(ctx, sym.ID)
	if err != nil || !ok {
		t.Fatalf("LatestPeriodicFiling = ok=%v, %v", ok, err)
	}
	// The newer 8-K must not win: only 10-Q/10-K count, newest of those.
	if p.Form != "10-K" || p.FiledTs != 2000 {
		t.Fatalf("wrong filing selected: %+v", p)
	}
}

// The matched persistence null is enforced at the WRITE path, not just by the
// worker: a post-amendment structural call with no frozen baseline must be
// impossible to store, and the invariant read must see zero such rows.
func TestRegimeOutcomes_RefusesUnmatchedNull(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()
	sym, _ := st.UpsertSymbol(ctx, "MSFT", md.Stocks, "Microsoft")

	ts := NullAmendmentEpoch + 86400
	base := RegimeCall{SymbolID: sym.ID, Kind: structregime.KindTrend21, Ts: ts,
		HorizonDays: 21, Regime: "uptrend", Conviction: 0.9, HistoricalAccuracy: 0.62, Rank: 0.8}

	if _, err := st.InsertRegimeOutcome(ctx, base); err == nil {
		t.Fatal("baseline-less structural freeze was accepted; the null would be unmatched")
	}

	// Same call WITH a frozen baseline lands normally.
	ok := base
	ok.NaiveLabel = "uptrend"
	if fresh, err := st.InsertRegimeOutcome(ctx, ok); err != nil || !fresh {
		t.Fatalf("baselined freeze = %v, %v; want true, nil", fresh, err)
	}

	// Pre-amendment rows are untouched by the guard — they are never backfilled.
	old := base
	old.Ts = NullAmendmentEpoch - 86400
	if _, err := st.InsertRegimeOutcome(ctx, old); err != nil {
		t.Fatalf("pre-amendment freeze rejected: %v", err)
	}

	n, err := st.UnmatchedNullCount(ctx)
	if err != nil || n != 0 {
		t.Fatalf("UnmatchedNullCount = %d, %v; want 0", n, err)
	}
}

// TestRegimeOutcomes_DedupDropsBaseline pins the exact collision that
// manufactured the 1,157 quarantined rows: a row already stored WITHOUT a
// naive-persistence baseline, and a later freeze for the same
// (symbol, kind, UTC-day) that DOES carry one. INSERT OR IGNORE silently
// discarded the baseline and reported (false, nil) — indistinguishable from
// ordinary deduplication, so nothing downstream could ever notice. It must now
// be a distinct typed error, and the stored row must stay untouched (no
// backfill: a null written after the fact is hindsight, not a baseline).
func TestRegimeOutcomes_DedupDropsBaseline(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()
	sym, _ := st.UpsertSymbol(ctx, "MSFT", md.Stocks, "Microsoft")

	// A pre-guard row: post-epoch, structural kind, no baseline. Written
	// directly because the write path now (correctly) refuses to produce one.
	day := (NullAmendmentEpoch + 86400) / 86400
	if _, err := st.w.ExecContext(ctx, `
		INSERT INTO regime_outcomes (symbol_id, kind, ts, day, horizon_days, regime,
		  conviction, historical_accuracy, rank, naive_label)
		VALUES (?,?,?,?,?,?,?,?,?,NULL)`,
		sym.ID, string(structregime.KindTrend21), day*86400, day, 21, "uptrend",
		0.9, 0.97, 0.8); err != nil {
		t.Fatalf("seed pre-guard row: %v", err)
	}

	c := RegimeCall{
		SymbolID: sym.ID, Kind: structregime.KindTrend21, Ts: day*86400 + 3600,
		HorizonDays: 21, Regime: "uptrend", Conviction: 0.9,
		HistoricalAccuracy: 0.97, Rank: 0.8, NaiveLabel: "uptrend",
	}
	fresh, err := st.InsertRegimeOutcome(ctx, c)
	if fresh {
		t.Fatal("collision reported as a new row")
	}
	if !errors.Is(err, ErrNaiveLabelDropped) {
		t.Fatalf("dropped baseline = %v; want ErrNaiveLabelDropped (the silent (false, nil) "+
			"no-op is the mechanism that produced the unmatched-null rows)", err)
	}
	var stored sql.NullString
	if err := st.db.QueryRowContext(ctx,
		`SELECT naive_label FROM regime_outcomes WHERE symbol_id=? AND day=?`,
		sym.ID, day).Scan(&stored); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if stored.Valid {
		t.Fatalf("the stored row was backfilled with %q — a baseline written after the call "+
			"is hindsight and must never be filled in", stored.String)
	}

	// An ordinary same-day re-freeze, where the stored row already HAS a
	// baseline, stays a quiet (false, nil) dedup.
	c2 := c
	c2.Kind = structregime.KindVol21
	if fresh, err := st.InsertRegimeOutcome(ctx, c2); !fresh || err != nil {
		t.Fatalf("first vol21 freeze = %v, %v; want true", fresh, err)
	}
	if fresh, err := st.InsertRegimeOutcome(ctx, c2); fresh || err != nil {
		t.Fatalf("plain dedup = %v, %v; want (false, nil)", fresh, err)
	}
}

// TestNullQuarantine_FrozenAndNotGrowable pins the manifest's two load-bearing
// properties: it exempts exactly the rows present when it was frozen, and it
// cannot be extended afterwards — a later unmatched row still fails the
// invariant, and an edit to the exempt set fails digest verification.
func TestNullQuarantine_FrozenAndNotGrowable(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()
	sym, _ := st.UpsertSymbol(ctx, "NVDA", md.Stocks, "Nvidia")

	seed := func(day int64) int64 {
		res, err := st.w.ExecContext(ctx, `
			INSERT INTO regime_outcomes (symbol_id, kind, ts, day, horizon_days, regime,
			  conviction, historical_accuracy, rank, naive_label)
			VALUES (?,?,?,?,?,?,?,?,?,NULL)`,
			sym.ID, string(structregime.KindTrend21), day*86400, day, 21, "uptrend",
			0.9, 0.97, 0.8)
		if err != nil {
			t.Fatalf("seed: %v", err)
		}
		id, _ := res.LastInsertId()
		return id
	}
	base := (NullAmendmentEpoch + 86400) / 86400
	seed(base)
	seed(base + 1)

	if n, err := st.UnmatchedNullCount(ctx); err != nil || n != 2 {
		t.Fatalf("pre-freeze unmatched = %d, %v; want 2", n, err)
	}
	m, frozen, err := st.FreezeNullQuarantine(ctx, 1000)
	if err != nil || !frozen || m.NRows != 2 {
		t.Fatalf("freeze = %+v, %v, %v; want 2 rows frozen", m, frozen, err)
	}
	if n, err := st.UnmatchedNullCount(ctx); err != nil || n != 0 {
		t.Fatalf("post-freeze unmatched = %d, %v; want 0", n, err)
	}

	// Re-freezing is a no-op — the exempt set is not growable.
	late := seed(base + 2)
	m2, frozen2, err := st.FreezeNullQuarantine(ctx, 2000)
	if err != nil || frozen2 {
		t.Fatalf("second freeze = %v, %v; want a no-op", frozen2, err)
	}
	if m2.Digest != m.Digest || m2.NRows != 2 {
		t.Fatalf("manifest moved: %+v vs %+v", m2, m)
	}
	if n, err := st.UnmatchedNullCount(ctx); err != nil || n != 1 {
		t.Fatalf("post-freeze new unmatched row = %d, %v; want 1 (the guard still bites)", n, err)
	}
	if _, ok, err := st.VerifyNullQuarantine(ctx); err != nil || !ok {
		t.Fatalf("verify on an untouched manifest = %v, %v; want ok", ok, err)
	}

	// Hand-extending the exempt set must fail verification rather than widen it.
	if _, err := st.w.ExecContext(ctx, `
		INSERT INTO regime_outcome_quarantine (outcome_id, symbol_id, kind, day, frozen_ts)
		VALUES (?,?,?,?,?)`, late, sym.ID, string(structregime.KindTrend21), base+2, 3000); err != nil {
		t.Fatalf("extend: %v", err)
	}
	if _, ok, err := st.VerifyNullQuarantine(ctx); err == nil || ok {
		t.Fatal("an extended quarantine set passed digest verification")
	}
}
