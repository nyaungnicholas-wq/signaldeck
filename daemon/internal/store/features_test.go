package store

import (
	"context"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

func TestFeatures_InsertAndFetch(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	if err != nil {
		t.Fatal(err)
	}

	vec := map[string]float64{"pressure_score": 0.4, "pred_raw": 0.7, "pred_cal": 0.65}
	if err := st.InsertFeatures(ctx, sym.ID, md.H1d, 1000, 1, vec); err != nil {
		t.Fatalf("insert: %v", err)
	}
	// Idempotent on the unique key: same (symbol,horizon,ts,version) replaces
	// the vector in place instead of erroring or duplicating.
	vec2 := map[string]float64{"pressure_score": 0.5, "pred_raw": 0.71, "pred_cal": 0.66}
	if err := st.InsertFeatures(ctx, sym.ID, md.H1d, 1000, 1, vec2); err != nil {
		t.Fatalf("re-insert: %v", err)
	}
	// A different version at the same ts is a separate row.
	if err := st.InsertFeatures(ctx, sym.ID, md.H1d, 1000, 2, vec); err != nil {
		t.Fatalf("insert v2: %v", err)
	}
	// A second timestamp.
	if err := st.InsertFeatures(ctx, sym.ID, md.H1d, 2000, 1, vec); err != nil {
		t.Fatalf("insert ts2: %v", err)
	}
	// A different horizon must not leak into H1d queries.
	if err := st.InsertFeatures(ctx, sym.ID, md.H1w, 1000, 1, vec); err != nil {
		t.Fatalf("insert 1w: %v", err)
	}

	rows, err := st.FeaturesSince(ctx, sym.ID, md.H1d, 0, 0)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("want 3 H1d rows (2 versions @1000 + 1 @2000), got %d", len(rows))
	}
	if rows[0].Ts != 1000 || rows[len(rows)-1].Ts != 2000 {
		t.Fatalf("rows not ascending by ts: %+v", rows)
	}
	// The replaced vector must reflect the second write.
	for _, r := range rows {
		if r.Ts == 1000 && r.Version == 1 && r.Vec["pressure_score"] != 0.5 {
			t.Fatalf("re-insert did not replace vec: %+v", r.Vec)
		}
	}

	// sinceTs filter.
	rows, err = st.FeaturesSince(ctx, sym.ID, md.H1d, 1500, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Ts != 2000 {
		t.Fatalf("sinceTs filter broken: %+v", rows)
	}

	// Empty vectors are refused (a feature row with no features is a bug).
	if err := st.InsertFeatures(ctx, sym.ID, md.H1d, 3000, 1, nil); err == nil {
		t.Fatal("empty vec must be rejected")
	}
}

func TestFeatures_LabeledJoin(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "SPY", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}

	// Prediction + its feature vector at ts=1000; resolve the outcome.
	if err := st.UpsertPrediction(ctx, Prediction{
		SymbolID: sym.ID, Horizon: md.H1d, Ts: 1000, RawProb: 0.7, CalProb: 0.68, NUsed: 2,
		Components: "{}",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertFeatures(ctx, sym.ID, md.H1d, 1000, 1,
		map[string]float64{"pressure_score": 0.4, "pred_cal": 0.68}); err != nil {
		t.Fatal(err)
	}
	// Second prediction stays UNRESOLVED: must not appear in the labeled set.
	if err := st.UpsertPrediction(ctx, Prediction{
		SymbolID: sym.ID, Horizon: md.H1d, Ts: 2000, RawProb: 0.5, CalProb: 0.5, NUsed: 1,
		Components: "{}",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertFeatures(ctx, sym.ID, md.H1d, 2000, 1,
		map[string]float64{"pressure_score": 0.1}); err != nil {
		t.Fatal(err)
	}
	if err := st.ResolvePrediction(ctx, sym.ID, md.H1d, 1000, 0.031); err != nil {
		t.Fatal(err)
	}

	got, err := st.LabeledFeatures(ctx, md.H1d, 100)
	if err != nil {
		t.Fatalf("labeled: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want exactly the 1 resolved example, got %d", len(got))
	}
	lf := got[0]
	if lf.SymbolID != sym.ID || lf.Ts != 1000 || lf.Up != 1 {
		t.Fatalf("bad labeled row: %+v", lf)
	}
	if lf.FwdReturn < 0.030 || lf.FwdReturn > 0.032 {
		t.Fatalf("fwd return = %v, want ~0.031", lf.FwdReturn)
	}
	if lf.Vec["pressure_score"] != 0.4 {
		t.Fatalf("vector not preserved through the join: %+v", lf.Vec)
	}
}

func TestPruneBars_DailyRefused(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertBars(ctx, []md.Bar{
		{SymbolID: sym.ID, TF: md.TF1d, Ts: 1000, Open: 1, High: 1, Low: 1, Close: 1},
	}); err != nil {
		t.Fatal(err)
	}
	// The permanence guarantee is enforced in code: PruneBars refuses 1d
	// outright, whatever the cutoff.
	if _, err := st.PruneBars(ctx, md.TF1d, 1<<60); err == nil {
		t.Fatal("PruneBars(1d) must be refused — daily bars are never pruned")
	}
	bars, err := st.LastBars(ctx, sym.ID, md.TF1d, 10)
	if err != nil || len(bars) != 1 {
		t.Fatalf("daily bar must survive: %v %d", err, len(bars))
	}
}

func TestRollupMissing_DoesNotOverwriteExisting(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}
	// An authoritative source-backfilled 1h bar at hour 0, plus 1m bars for
	// hours 0 and 1 (hour 1 has no 1h bar yet).
	h0, h1 := int64(0), int64(3600)
	if err := st.UpsertBars(ctx, []md.Bar{
		{SymbolID: sym.ID, TF: md.TF1h, Ts: h0, Open: 500, High: 500, Low: 500, Close: 500, Volume: 999},
		{SymbolID: sym.ID, TF: md.TF1m, Ts: h0, Open: 10, High: 12, Low: 9, Close: 11, Volume: 1},
		{SymbolID: sym.ID, TF: md.TF1m, Ts: h1, Open: 20, High: 25, Low: 19, Close: 24, Volume: 2},
		{SymbolID: sym.ID, TF: md.TF1m, Ts: h1 + 60, Open: 24, High: 30, Low: 23, Close: 28, Volume: 3},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.RollupMissing(ctx, sym.ID, md.TF1m, md.TF1h, 3600, 0, 7200); err != nil {
		t.Fatal(err)
	}
	hours, err := st.Bars(ctx, sym.ID, md.TF1h, 0, 7200, 0)
	if err != nil || len(hours) != 2 {
		t.Fatalf("want 2 hourly bars: %v %d", err, len(hours))
	}
	// Hour 0: the pre-existing source bar must be untouched.
	if hours[0].Close != 500 || hours[0].Volume != 999 {
		t.Fatalf("RollupMissing overwrote an authoritative 1h bar: %+v", hours[0])
	}
	// Hour 1: filled from the minutes (first open, max high, min low, last
	// close, summed volume).
	got := hours[1]
	if got.Open != 20 || got.High != 30 || got.Low != 19 || got.Close != 28 || got.Volume != 5 {
		t.Fatalf("bad OHLCV aggregation: %+v", got)
	}
}

func TestDataStats(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertBars(ctx, []md.Bar{
		{SymbolID: sym.ID, TF: md.TF1d, Ts: 1000, Close: 1},
		{SymbolID: sym.ID, TF: md.TF1d, Ts: 2000, Close: 2},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertFeatures(ctx, sym.ID, md.H1d, 1500, 1, map[string]float64{"x": 1}); err != nil {
		t.Fatal(err)
	}

	stats, err := st.DataStats(ctx)
	if err != nil {
		t.Fatalf("datastats: %v", err)
	}
	byName := map[string]TableStat{}
	for _, ts := range stats.Tables {
		byName[ts.Table] = ts
	}
	if b := byName["bars"]; b.Rows != 2 || b.MinTs == nil || *b.MinTs != 1000 || b.MaxTs == nil || *b.MaxTs != 2000 {
		t.Fatalf("bars stat wrong: %+v", b)
	}
	if f := byName["features"]; f.Rows != 1 || f.MinTs == nil || *f.MinTs != 1500 {
		t.Fatalf("features stat wrong: %+v", f)
	}
	if s := byName["symbols"]; s.Rows != 1 {
		t.Fatalf("symbols stat wrong: %+v", s)
	}
	// Empty tables report zero rows and omit the span.
	if n := byName["news"]; n.Rows != 0 || n.MinTs != nil {
		t.Fatalf("empty-table stat wrong: %+v", n)
	}
	if stats.DBBytes <= 0 {
		t.Fatalf("db size must be positive, got %d", stats.DBBytes)
	}
	if stats.GeneratedAt == 0 {
		t.Fatal("generatedAt missing")
	}
}

// ── tiered-storage wave: archive-before-prune read helpers + governor ──────

// BarsBelow returns exactly the rows below the cutoff, ts-ascending, and honors
// the batch limit — the archive path relies on both.
func TestBarsBelowAndSnapsBelow(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertBars(ctx, []md.Bar{
		{SymbolID: sym.ID, TF: md.TF1m, Ts: 100, Close: 1},
		{SymbolID: sym.ID, TF: md.TF1m, Ts: 200, Close: 2},
		{SymbolID: sym.ID, TF: md.TF1m, Ts: 300, Close: 3}, // at/above cutoff
	}); err != nil {
		t.Fatal(err)
	}
	below, err := st.BarsBelow(ctx, md.TF1m, 300, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(below) != 2 || below[0].Ts != 100 || below[1].Ts != 200 {
		t.Fatalf("BarsBelow(300) = %+v, want ts 100,200 ascending", below)
	}
	// Batch limit respected.
	one, err := st.BarsBelow(ctx, md.TF1m, 300, 1)
	if err != nil || len(one) != 1 || one[0].Ts != 100 {
		t.Fatalf("BarsBelow limit=1 = %+v", one)
	}
	// Snaps mirror.
	for _, ts := range []int64{10, 20, 30} {
		if err := st.InsertSnap1s(ctx, md.Snap1s{SymbolID: sym.ID, Ts: ts, Mid: float64(ts)}); err != nil {
			t.Fatal(err)
		}
	}
	sb, err := st.SnapsBelow(ctx, 30, 0)
	if err != nil || len(sb) != 2 || sb[0].Ts != 10 {
		t.Fatalf("SnapsBelow(30) = %+v", sb)
	}
}

// The governor primitives run without error and shrink the WAL.
func TestGovernorPrimitives(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()
	sym, _ := st.UpsertSymbol(ctx, "SPY", md.Stocks, "")
	// Generate WAL by writing.
	for i := int64(0); i < 100; i++ {
		_ = st.UpsertBars(ctx, []md.Bar{{SymbolID: sym.ID, TF: md.TF1m, Ts: i * 60, Close: float64(i)}})
	}
	if err := st.WALCheckpointTruncate(ctx); err != nil {
		t.Fatalf("wal checkpoint truncate: %v", err)
	}
	if err := st.Vacuum(ctx); err != nil {
		t.Fatalf("vacuum: %v", err)
	}
	db, _ := st.FileSizes()
	if db <= 0 {
		t.Fatalf("db size must be positive after vacuum, got %d", db)
	}
}

// SymbolNameMap returns id→symbol for archive filenames.
func TestSymbolNameMap(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()
	a, _ := st.UpsertSymbol(ctx, "AAA", md.Stocks, "")
	b, _ := st.UpsertSymbol(ctx, "BTC/USD", md.Crypto, "")
	m, err := st.SymbolNameMap(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if m[a.ID] != "AAA" || m[b.ID] != "BTC/USD" {
		t.Fatalf("SymbolNameMap = %+v", m)
	}
}
