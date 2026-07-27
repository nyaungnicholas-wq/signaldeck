package pipeline

// Pipeline-level proof of the Decision Engine (internal/ev) wiring: the tests
// here run the REAL worker against a real store and check that the EV gate —
// not the old bare cal_prob threshold — decides entries, and that every
// refusal lands in the ev_decisions ledger with its enumerated reason.

import (
	"context"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// THE point of the whole layer: a candidate with a HIGH calibrated probability
// whose cost-adjusted expected value is NEGATIVE (the move is smaller than its
// cost) must be REFUSED — the bare threshold at the old paper.go:194 would
// have traded it on sight.
func TestEVGate_RefusesHighProbNegativeNetEV(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	sym, _ := st.UpsertSymbol(ctx, "AAA", md.Stocks, "")
	seedDailyPx(t, st, sym.ID, [][3]float64{
		{1, 100, 100},
		{2, 100, 100},
		{3, 100, 100},
	})
	// cal_prob 0.95: the old gate's dream candidate — DecideTarget says GoLong.
	seedPrediction(t, st, sym.ID, md.H1d, 2*86400, 0.95)
	// But the distribution says the expected move does not pay for its own
	// cost band: expectedValue (E[ret|side] − tau) is negative.
	if err := st.UpsertReturnForecast(ctx, store.ReturnForecast{
		SymbolID: sym.ID, Horizon: md.H1d, Ts: 2 * 86400, Regime: "calm", N: 200,
		Tau: 0.02, Mean: 0.005, Sigma: 0.02, PUp: 0.40, PDown: 0.30, PInside: 0.30,
		Edge: 0.10, ExpectedValue: -0.015,
	}); err != nil {
		t.Fatalf("seed forecast: %v", err)
	}

	w := &PaperTrader{St: st}
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}

	// No position, no trade — where the old threshold would have bought.
	if _, ok, _ := st.PaperPosition(ctx, "flagship-1d", sym.ID); ok {
		t.Fatal("negative net EV was traded — the EV gate is not deciding entries")
	}
	trades, _ := st.PaperTrades(ctx, "flagship-1d", 10)
	if len(trades) != 0 {
		t.Fatalf("want no trades, got %+v", trades)
	}

	// And the refusal is LEDGERED, with the enumerated reason and the net EV
	// it refused at — auditable, not an entry that silently never happened.
	decs, err := st.EVDecisions(ctx, "DO_NOTHING", "AAA", 10)
	if err != nil {
		t.Fatalf("decisions: %v", err)
	}
	if len(decs) == 0 {
		t.Fatal("the DO_NOTHING was not ledgered")
	}
	d := decs[0]
	if d.Reason != "net-ev-below-floor" {
		t.Fatalf("reason=%q want net-ev-below-floor", d.Reason)
	}
	if d.NetEV == nil || *d.NetEV >= 0 {
		t.Fatalf("ledgered netEV=%v want a recorded negative value", d.NetEV)
	}
	if d.Rank != 1 || d.RankOf != 1 {
		t.Fatalf("rank %d/%d want 1/1 (sole candidate in the pass)", d.Rank, d.RankOf)
	}
	if d.InputsJSON == "" {
		t.Fatal("inputs snapshot missing")
	}
}

// Fail closed: a strong-long prediction with NO stored return distribution has
// no expected value to gate on, so the engine must refuse with the
// missing-input reason — never decide over a silently-defaulted zero.
func TestEVGate_RefusesWhenDistributionMissing(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	sym, _ := st.UpsertSymbol(ctx, "BBB", md.Stocks, "")
	seedDailyPx(t, st, sym.ID, [][3]float64{
		{1, 100, 100},
		{2, 100, 100},
		{3, 100, 100},
	})
	seedPrediction(t, st, sym.ID, md.H1d, 2*86400, 0.95)
	// Deliberately NO seedGoodForecast.

	w := &PaperTrader{St: st}
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}
	if _, ok, _ := st.PaperPosition(ctx, "flagship-1d", sym.ID); ok {
		t.Fatal("entered with no distribution — the required-input refusal did not fire")
	}
	decs, _ := st.EVDecisions(ctx, "DO_NOTHING", "BBB", 10)
	if len(decs) == 0 || decs[0].Reason != "missing-required-input" {
		t.Fatalf("want a ledgered missing-required-input refusal, got %+v", decs)
	}
	if decs[0].NetEV != nil {
		t.Fatalf("netEV must be NULL when unmeasurable, got %v", *decs[0].NetEV)
	}
}

// A clean entry is ledgered as a BUY, and an exit — never blocked — as a SELL,
// so the decision log is the complete record of every transition.
func TestEVGate_LedgersBuyAndExit(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	sym, _ := st.UpsertSymbol(ctx, "CCC", md.Stocks, "")
	seedDailyPx(t, st, sym.ID, [][3]float64{
		{1, 100, 100},
		{2, 100, 100},
		{3, 100, 100},
	})
	seedPrediction(t, st, sym.ID, md.H1d, 2*86400, 0.90)
	seedGoodForecast(t, st, sym.ID, md.H1d, 2*86400)

	w := &PaperTrader{St: st}
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("entry run: %v", err)
	}
	if _, ok, _ := st.PaperPosition(ctx, "flagship-1d", sym.ID); !ok {
		t.Fatal("positive net EV entry did not fill")
	}
	buys, _ := st.EVDecisions(ctx, "BUY", "CCC", 10)
	if len(buys) != 1 || buys[0].Reason != "positive-net-ev" {
		t.Fatalf("want one ledgered BUY/positive-net-ev, got %+v", buys)
	}
	if buys[0].NetEV == nil || *buys[0].NetEV <= 0 {
		t.Fatalf("BUY must record its positive net EV, got %v", buys[0].NetEV)
	}

	// Flip flat: the exit must execute AND be ledgered as exit-never-blocked.
	seedDailyPx(t, st, sym.ID, [][3]float64{
		{4, 100, 100},
		{5, 100, 100},
	})
	seedPrediction(t, st, sym.ID, md.H1d, 4*86400, 0.10)
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("exit run: %v", err)
	}
	if _, ok, _ := st.PaperPosition(ctx, "flagship-1d", sym.ID); ok {
		t.Fatal("exit did not execute")
	}
	sells, _ := st.EVDecisions(ctx, "SELL", "CCC", 10)
	if len(sells) != 1 || sells[0].Reason != "exit-never-blocked" {
		t.Fatalf("want one ledgered SELL/exit-never-blocked, got %+v", sells)
	}
}

// Cross-candidate ranking: two tradeable candidates in one pass are ranked by
// net EV, and the ledger records where each stood — the opportunity-cost input
// that replaced the arbitrary symbol-order loop.
func TestEVGate_RanksCandidatesByNetEV(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	// "ZZZ" seeds the BETTER forecast: if the loop were still symbol-ordered,
	// AAA would be judged first regardless of EV.
	aaa, _ := st.UpsertSymbol(ctx, "AAA", md.Stocks, "")
	zzz, _ := st.UpsertSymbol(ctx, "ZZZ", md.Stocks, "")
	for _, id := range []int64{aaa.ID, zzz.ID} {
		seedDailyPx(t, st, id, [][3]float64{
			{1, 100, 100},
			{2, 100, 100},
			{3, 100, 100},
		})
		seedPrediction(t, st, id, md.H1d, 2*86400, 0.90)
	}
	seed := func(id int64, evVal float64) {
		if err := st.UpsertReturnForecast(ctx, store.ReturnForecast{
			SymbolID: id, Horizon: md.H1d, Ts: 2 * 86400, Regime: "calm", N: 200,
			Tau: 0.002, Mean: evVal + 0.002, Sigma: 0.02, PUp: 0.55, PDown: 0.15,
			PInside: 0.30, Edge: 0.40, ExpectedValue: evVal,
		}); err != nil {
			t.Fatalf("seed forecast: %v", err)
		}
	}
	seed(aaa.ID, 0.004)
	seed(zzz.ID, 0.020)

	w := &PaperTrader{St: st}
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}
	rankOf := func(symbol string) (int, int) {
		decs, _ := st.EVDecisions(ctx, "", symbol, 10)
		if len(decs) == 0 {
			t.Fatalf("no ledgered decision for %s", symbol)
		}
		return decs[0].Rank, decs[0].RankOf
	}
	if r, of := rankOf("ZZZ"); r != 1 || of != 2 {
		t.Fatalf("ZZZ (higher EV) rank %d/%d, want 1/2", r, of)
	}
	if r, of := rankOf("AAA"); r != 2 || of != 2 {
		t.Fatalf("AAA (lower EV) rank %d/%d, want 2/2", r, of)
	}
}
