package pipeline

import (
	"context"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// A label must not be frozen against a bar that is still forming. The resolver's
// only time check used to be `now < target`, and target is the next session's
// bar STAMP — a bar ingest creates at the open — so a pass during the session
// wrote the live price in as a close-to-close outcome and never revisited it.
// Measured on the live record: 37.8% of 1d labels were frozen mid-session and
// disagreed with the final close 9.7% of the time.
//
// The guard is "a later bar exists", so this test drives it by adding one.
func TestResolverWaitsForTheForwardSessionToSettle(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	sym, err := st.UpsertSymbol(ctx, "SETTLE", md.Stocks, "")
	if err != nil {
		t.Fatalf("UpsertSymbol: %v", err)
	}
	// 2026-08-20 (was 05-28, +12 weeks): inside the graded window the resolved-pair
	// read is restricted to (GradingEpochTS).
	const d0 = int64(1780000000 + 12*7*86400)
	bar := func(ts int64, c float64) md.Bar {
		return md.Bar{SymbolID: sym.ID, TF: md.TF1d, Ts: ts, Open: c, High: c, Low: c, Close: c, Volume: 1}
	}
	// D0 and D2 only. A prediction stamped inside D0 takes D0 as its base, so
	// its target is D1 and the forward bar is D2 — the NEWEST bar, i.e. the
	// session that has not settled yet.
	if err := st.UpsertBars(ctx, []md.Bar{bar(d0, 100), bar(d0+2*86400, 110)}); err != nil {
		t.Fatalf("UpsertBars: %v", err)
	}
	if err := st.UpsertPrediction(ctx, store.Prediction{
		SymbolID: sym.ID, Horizon: md.H1d, Ts: d0 + 12*3600,
		RawProb: 0.6, CalProb: 0.6, NUsed: 2, Components: `{}`,
	}); err != nil {
		t.Fatalf("UpsertPrediction: %v", err)
	}

	r := &PredictionResolver{St: st}
	if _, err := r.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if _, ups, _, err := st.ResolvedRawPredictionPairs(ctx, md.H1d, 10); err != nil {
		t.Fatalf("ResolvedRawPredictionPairs: %v", err)
	} else if len(ups) != 0 {
		t.Fatalf("resolved %d rows against an unsettled forward bar; want 0", len(ups))
	}

	// The next session prints. D2 is now settled, so the label may be frozen.
	if err := st.UpsertBars(ctx, []md.Bar{bar(d0+3*86400, 111)}); err != nil {
		t.Fatalf("UpsertBars: %v", err)
	}
	if _, err := r.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	_, ups, _, err := st.ResolvedRawPredictionPairs(ctx, md.H1d, 10)
	if err != nil {
		t.Fatalf("ResolvedRawPredictionPairs: %v", err)
	}
	if len(ups) != 1 {
		t.Fatalf("resolved %d rows once the forward session settled; want 1", len(ups))
	}
	if ups[0] != 1 {
		t.Fatalf("100 -> 110 must label up, got %v", ups[0])
	}
}
