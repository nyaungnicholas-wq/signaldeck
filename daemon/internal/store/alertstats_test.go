package store

import (
	"context"
	"math"
	"path/filepath"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

func newAlertStatsStore(t *testing.T) (*Store, context.Context) {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "alertstats.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st, context.Background()
}

// seedDailyCloses inserts len(closes) daily bars, one per day starting at t0.
func seedDailyCloses(t *testing.T, st *Store, ctx context.Context, symbolID, t0 int64, closes []float64) {
	t.Helper()
	bars := make([]md.Bar, 0, len(closes))
	for i, c := range closes {
		bars = append(bars, md.Bar{
			SymbolID: symbolID, TF: md.TF1d, Ts: t0 + int64(i)*86400,
			Open: c, High: c, Low: c, Close: c, Volume: 100,
		})
	}
	if err := st.UpsertBars(ctx, bars); err != nil {
		t.Fatal(err)
	}
}

func TestAlertKindOutcomes(t *testing.T) {
	st, ctx := newAlertStatsStore(t)
	sym, err := st.UpsertSymbol(ctx, "NVDA", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}
	sid := sym.ID

	// 40 daily bars: close doubles every bar's index step by +1 (100, 101, …),
	// so fwd 1d return after bar i is (c[i+1]/c[i])-1 > 0 always.
	t0 := int64(1_000_000)
	closes := make([]float64, 40)
	for i := range closes {
		closes[i] = 100 + float64(i)
	}
	seedDailyCloses(t, st, ctx, sid, t0, closes)

	// Kind "breakout": 25 distinct events (one per day, fired mid-bar-day),
	// each ALSO inserted for a second user — the dedup must count 25, not 50.
	for i := 0; i < 25; i++ {
		ts := t0 + int64(i)*86400 + 3600
		for _, uid := range []int64{1, 2} {
			if err := st.InsertAlert(ctx, Alert{
				UserID: uid, SymbolID: &sid, Kind: "breakout", Detail: "d", Ts: ts,
			}); err != nil {
				t.Fatal(err)
			}
		}
	}
	// Kind "regime_change": only 3 events — must gate (n < 20) with n shown.
	for i := 0; i < 3; i++ {
		ts := t0 + int64(i)*86400 + 7200
		if err := st.InsertAlert(ctx, Alert{
			UserID: 1, SymbolID: &sid, Kind: "regime_change", Detail: "d", Ts: ts,
		}); err != nil {
			t.Fatal(err)
		}
	}
	// A symbol-less alert (correlation break) must be skipped entirely.
	if err := st.InsertAlert(ctx, Alert{
		UserID: 1, Kind: "corr_break", Detail: "d", Ts: t0 + 3600,
	}); err != nil {
		t.Fatal(err)
	}

	rows, err := st.AlertKindOutcomes(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d kinds %+v, want 2 (symbol-less kind skipped)", len(rows), rows)
	}
	// Alphabetical: breakout, regime_change.
	bo, rc := rows[0], rows[1]
	if bo.Kind != "breakout" || rc.Kind != "regime_change" {
		t.Fatalf("kind order = %s, %s", bo.Kind, rc.Kind)
	}

	// breakout: 25 fired; all 25 have a next bar (last alert is at bar 24 of
	// 40) so n1d=25, n5d=25 — ungated, every fwd 1d return positive.
	if bo.N != 25 || bo.N1d != 25 || bo.N5d != 25 {
		t.Fatalf("breakout counts = %+v", bo)
	}
	if bo.Gated1d || bo.Mean1d == nil || bo.Median1d == nil || bo.HitRate1d == nil {
		t.Fatalf("breakout must be ungated with stats: %+v", bo)
	}
	if *bo.HitRate1d != 1.0 {
		t.Fatalf("hitRate1d = %v, want 1.0 (monotonic rising closes)", *bo.HitRate1d)
	}
	// Base close for alert i is 100+i; fwd 1d = 1/(100+i). Mean over i=0..24.
	var want float64
	for i := 0; i < 25; i++ {
		want += 1 / (100 + float64(i))
	}
	want /= 25
	if math.Abs(*bo.Mean1d-want) > 1e-12 {
		t.Fatalf("mean1d = %v, want %v", *bo.Mean1d, want)
	}
	// Median of a 25-element strictly-decreasing series is the 13th value (i=12).
	if math.Abs(*bo.Median1d-1/112.0) > 1e-12 {
		t.Fatalf("median1d = %v, want %v", *bo.Median1d, 1/112.0)
	}
	if bo.Gated5d || bo.Mean5d == nil {
		t.Fatalf("breakout 5d must be ungated: %+v", bo)
	}

	// regime_change: n=3 < 20 — stats NULL, n shown, gate explicit.
	if rc.N != 3 || !rc.Gated1d || !rc.Gated5d {
		t.Fatalf("regime_change gate = %+v", rc)
	}
	if rc.Mean1d != nil || rc.Median1d != nil || rc.HitRate1d != nil || rc.Mean5d != nil || rc.Median5d != nil {
		t.Fatalf("gated kind leaked stats: %+v", rc)
	}

	// sinceTs filters: nothing after the last alert -> no kinds at all.
	rows, err = st.AlertKindOutcomes(ctx, t0+100*86400)
	if err != nil || len(rows) != 0 {
		t.Fatalf("far-future sinceTs: %d kinds (err %v), want 0", len(rows), err)
	}
}

// TestAlertKindOutcomesNoBars: alerts on a symbol with no daily bars count as
// fired but resolve nothing — n shown, stats gated, never an error.
func TestAlertKindOutcomesNoBars(t *testing.T) {
	st, ctx := newAlertStatsStore(t)
	sym, _ := st.UpsertSymbol(ctx, "GHOST", md.Stocks, "")
	sid := sym.ID
	for i := 0; i < 25; i++ {
		if err := st.InsertAlert(ctx, Alert{
			UserID: 1, SymbolID: &sid, Kind: "prediction_high", Detail: "d", Ts: int64(1000 + i),
		}); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := st.AlertKindOutcomes(ctx, 0)
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows = %+v (err %v)", rows, err)
	}
	r := rows[0]
	if r.N != 25 || r.N1d != 0 || !r.Gated1d || r.Mean1d != nil {
		t.Fatalf("bar-less kind = %+v, want fired 25, resolved 0, gated", r)
	}
}
