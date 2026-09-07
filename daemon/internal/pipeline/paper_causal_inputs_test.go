package pipeline

import (
	"context"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/papertrade"
)

func TestPaperExecutionCannotSeeFillBarFuture(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "CAUSAL", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}
	prior := md.Bar{SymbolID: sym.ID, TF: md.TF1d, Ts: 86400, Open: 100, High: 102, Low: 98, Close: 100, Volume: 10000}
	fill := md.Bar{SymbolID: sym.ID, TF: md.TF1d, Ts: 172800, Open: 101, High: 105, Low: 95, Close: 104, Volume: 20000}
	if err := st.UpsertBars(ctx, []md.Bar{prior, fill}); err != nil {
		t.Fatal(err)
	}
	w := &PaperTrader{St: st}
	calculate := func(bar md.Bar) (papertrade.Fill, float64) {
		t.Helper()
		adv, err := w.advUSD(ctx, sym.ID, bar.Ts)
		if err != nil {
			t.Fatal(err)
		}
		in, err := w.executionInputs(ctx, bar, md.Stocks, adv)
		if err != nil {
			t.Fatal(err)
		}
		f, _, _, ok := papertrade.EnterLong(1000, in)
		if !ok {
			t.Fatal(f.Reason)
		}
		evCost, ok := roundTripCostFrac(in, 1000)
		if !ok {
			t.Fatal("EV cost unavailable")
		}
		return f, evCost
	}
	before, evBefore := calculate(fill)
	fill.High, fill.Low, fill.Close, fill.Volume = 10000, 1, 9000, 1e12
	if err := st.UpsertBars(ctx, []md.Bar{fill}); err != nil {
		t.Fatal(err)
	}
	after, evAfter := calculate(fill)
	if before != after || evBefore != evAfter {
		t.Fatalf("future bar data changed execution: before=%+v after=%+v EV=%v/%v", before, after, evBefore, evAfter)
	}
}
