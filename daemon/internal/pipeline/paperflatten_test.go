package pipeline

import (
	"context"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// THE TERMINAL RUNG, WIRED. riskgate.ShouldFlatten decides; this proves the
// decision reaches a position and closes it.
//
// The isolation matters. Barriers are disabled for this test so that NOTHING
// ELSE can close the position: no adverse stop, no take-profit, no horizon
// expiry, and the prediction is held bullish so the probability flip cannot
// fire either. Whatever closes the position here closed it because the rung
// said so — which is the only way to tell a wired control from a coincidence.
//
// On the live flagship-1d book the expiry barrier closes a position after one
// bar anyway, so a flatten would rarely be the first thing to fire. That is
// correct and is why the rung is the WEAKEST exit candidate: an earlier, more
// specific control should always win. It exists for the book that is holding
// when the drawdown rung trips, not to race the stops.
func TestFlatten_ClosesAPositionNothingElseWouldClose(t *testing.T) {
	t.Setenv("SIGNALDECK_PAPER_BARRIERS", "0")
	st := openStore(t)
	ctx := context.Background()
	symID := seedTradeable(t, st, ctx, "FLT")

	w := &PaperTrader{St: st}
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("entry run: %v", err)
	}
	pos, ok, err := st.PaperPosition(ctx, "flagship-1d", symID)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("setup failed: no open position to flatten")
	}

	// A later bar so an exit has somewhere to fill, and a still-bullish
	// prediction so the flip stays silent.
	seedDailyPx(t, st, symID, [][3]float64{{4, 100, 100}, {5, 100, 100}})
	seedPrediction(t, st, symID, md.H1d, 4*86400, 0.90)

	sym, err := st.UpsertSymbol(ctx, "FLT", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}
	pred, okP, err := st.LatestPrediction(ctx, symID, md.H1d)
	if err != nil {
		t.Fatal(err)
	}
	asof := int64(5 * 86400)

	// Control: with the rung silent, nothing closes this position.
	if _, want, err := w.planExit(ctx, sym, md.H1d, pos, pred, okP, asof, ""); err != nil {
		t.Fatal(err)
	} else if want {
		t.Fatal("no barrier, no flip and no rung — the position must be held; " +
			"something else is closing it and this test proves nothing")
	}

	// Treatment: the rung fires and the position closes, carrying its reason.
	const reason = "drawdown flatten: test rung"
	plan, want, err := w.planExit(ctx, sym, md.H1d, pos, pred, okP, asof, reason)
	if err != nil {
		t.Fatal(err)
	}
	if !want {
		t.Fatal("the terminal rung did not close the position — ShouldFlatten is " +
			"decided but not wired, which is the defect this repository keeps finding")
	}
	if plan.reason != reason {
		t.Fatalf("a forced liquidation must carry the rung's reason into the "+
			"ledger, got %q", plan.reason)
	}
	// No lookahead: the fill is a bar STRICTLY AFTER the close that triggered it.
	if plan.fillBar.Ts <= 4*86400 {
		t.Fatalf("flatten filled at or before its trigger close (ts=%d) — a "+
			"liquidation may not trade on the bar that decided it", plan.fillBar.Ts)
	}
	if plan.fromBarrier {
		t.Fatal("a flatten is not a barrier exit and must not be ledgered as one")
	}
}

// A barrier or a flip that already fired is earlier and more specific, so it
// must win. Otherwise a liquidation would overwrite the reason a position was
// already leaving for, and the trade log would misattribute the exit.
func TestFlatten_DoesNotOverwriteAnEarlierExitReason(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	symID := seedTradeable(t, st, ctx, "FLT2")

	w := &PaperTrader{St: st}
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("entry run: %v", err)
	}
	pos, ok, _ := st.PaperPosition(ctx, "flagship-1d", symID)
	if !ok {
		t.Fatal("setup failed: no open position")
	}

	// Flip the signal flat so the probability-flip exit is pending, and fire the
	// rung at the same time.
	seedDailyPx(t, st, symID, [][3]float64{{4, 100, 100}, {5, 100, 100}})
	seedPrediction(t, st, symID, md.H1d, 4*86400, 0.10)
	sym, _ := st.UpsertSymbol(ctx, "FLT2", md.Stocks, "")
	pred, okP, _ := st.LatestPrediction(ctx, symID, md.H1d)

	plan, want, err := w.planExit(ctx, sym, md.H1d, pos, pred, okP, 5*86400,
		"drawdown flatten: should not win")
	if err != nil {
		t.Fatal(err)
	}
	if !want {
		t.Fatal("the position should be closing on its own account")
	}
	if plan.reason == "drawdown flatten: should not win" {
		t.Fatal("the rung overwrote an exit that had already fired for a more " +
			"specific reason")
	}
}
