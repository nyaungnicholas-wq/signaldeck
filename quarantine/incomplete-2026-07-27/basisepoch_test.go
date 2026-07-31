package pipeline

import (
	"context"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// TestBasisEpochReopensResolvedRowsAfterRepair pins the propagation edge from a
// source-data correction to the labels that correction invalidates: a successful
// split repair bumps the symbol's bar epoch, every outcome graded on the old
// epoch becomes pending again, the re-grade overwrites in place and stamps the
// new epoch (so it settles instead of looping), and a FAILED repair — where the
// bars did not change — invalidates nothing.
func TestBasisEpochReopensResolvedRowsAfterRepair(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(t.TempDir() + "/s.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	sym, err := st.UpsertSymbol(ctx, "TST", md.Market("stocks"), "Test Co")
	if err != nil {
		t.Fatal(err)
	}
	var bars []md.Bar
	base := int64(1700000000) / 86400 * 86400
	for i := 0; i < 10; i++ {
		bars = append(bars, md.Bar{SymbolID: sym.ID, TF: md.TF1d, Ts: base + int64(i)*86400,
			Open: 10, High: 10, Low: 10, Close: 10 + float64(i), Volume: 1})
	}
	if err := st.UpsertBars(ctx, bars); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertPrediction(ctx, store.Prediction{SymbolID: sym.ID, Horizon: md.H1d,
		Ts: bars[0].Ts, RawProb: 0.6, CalProb: 0.6, NUsed: 10, Components: "{}"}); err != nil {
		t.Fatal(err)
	}
	w := &PredictionResolver{St: st}
	if _, err := w.Run(ctx); err != nil {
		t.Fatal(err)
	}
	pend, err := st.UnresolvedPredictions(ctx, md.H1d, base+100*86400, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pend) != 0 {
		t.Fatalf("expected resolved+stamped, got %d pending", len(pend))
	}
	// A successful repair bumps the epoch; the resolved row must become pending again.
	if err := st.RecordSplitRepair(ctx, sym.ID, "2026-01-01", "2:1", 1, true, "", 1700000000); err != nil {
		t.Fatal(err)
	}
	pend, err = st.UnresolvedPredictions(ctx, md.H1d, base+100*86400, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pend) != 1 || !pend[0].Regraded {
		t.Fatalf("epoch bump did not re-open the resolved row: %+v", pend)
	}
	if _, err := w.Run(ctx); err != nil {
		t.Fatal(err)
	}
	pend, _ = st.UnresolvedPredictions(ctx, md.H1d, base+100*86400, 10)
	if len(pend) != 0 {
		t.Fatalf("re-resolve did not stamp the new epoch: %d still pending", len(pend))
	}
	// A FAILED repair must bump nothing.
	if err := st.RecordSplitRepair(ctx, sym.ID, "2026-01-01", "2:1", 1, false, "boom", 1700000001); err != nil {
		t.Fatal(err)
	}
	pend, _ = st.UnresolvedPredictions(ctx, md.H1d, base+100*86400, 10)
	if len(pend) != 0 {
		t.Fatalf("failed repair bumped the epoch: %d pending", len(pend))
	}
}
