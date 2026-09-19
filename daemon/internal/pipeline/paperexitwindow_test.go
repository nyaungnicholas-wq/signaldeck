package pipeline

import (
	"context"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// THE FILL WINDOW'S LOWER BOUND APPLIED TO ENTRIES ONLY.
//
// paperfillwindow_test.go documents the window as (LastBarTs, asof] and pins the
// lower bound with TestPaperTrader_NeverFillsBehindTheBooksClock -- but that test
// drives an ENTRY. The exit path never had the bound at all: planExit checks only
// the upper one (barrier/flip: `fillBar.Ts > asof`; forced flatten additionally
// `fb.Ts <= pos.OpenedTs`), and planExit is not even passed the cursor, so no
// lower bound was reachable inside it. Measured on pre-epoch history: 43
// flagship-1d writes more than three days behind the running max ts, worst 22
// days, 25 of them sells.
//
// THE REACHABLE SHAPE IS A PREDICTION REVISED IN PLACE, and two earlier attempts
// at this fixture were wrong in ways worth recording:
//
//   - `cur` is read ONCE at the start of a pass, so an exit one bar ahead of the
//     PREVIOUS pass's cursor is legitimately in window however far behind asof it
//     sits. Simply running the book forward does not reach the defect.
//   - A flagship-1d position cannot be held for days while the cursor overtakes
//     its fill anchor: a barrier expires it first.
//
// What does reach it: a prediction whose row is UPDATED in place. Its ts does not
// move, so planExit still anchors the exit at BarAtOrAfter(pred.Ts+1) -- the bar
// the entry already filled on and the cursor already stands at. This repo revises
// predictions (the revision epoch exists for exactly that), so this is the
// ordinary shape, not a contrivance.
//
// Prices are flat at 100 throughout, deliberately: a price move would let a
// barrier supply a different exit, and the defect under test is the TIMESTAMP.
func TestPaperTrader_ExitNeverFillsBehindTheBooksClock(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "AAA", md.Stocks, "")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	flat := func(from, to int) [][3]float64 {
		out := make([][3]float64, 0, to-from+1)
		for d := from; d <= to; d++ {
			out = append(out, [3]float64{float64(d), 100, 100})
		}
		return out
	}

	// Pass 1 — days 1..12, a day-11 signal opens the position at day 12 and
	// leaves the cursor there.
	seedDailyPx(t, st, sym.ID, flat(1, 12))
	seedPrediction(t, st, sym.ID, md.H1d, 11*86400, 0.90)
	seedGoodForecast(t, st, sym.ID, md.H1d, 11*86400)

	w := &PaperTrader{St: st}
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("run 1: %v", err)
	}
	cur, _, err := st.PaperCursor(ctx, "flagship-1d")
	if err != nil {
		t.Fatalf("cursor: %v", err)
	}
	if cur.LastBarTs != 12*86400 {
		t.Fatalf("fixture broken: cursor at day %d, want day 12 after pass 1", cur.LastBarTs/86400)
	}
	if _, held, _ := st.PaperPosition(ctx, "flagship-1d", sym.ID); !held {
		t.Fatal("fixture broken: AAA should hold after pass 1")
	}

	// Pass 2 — the SAME prediction row revised to a flip. Its ts stays day 11, so
	// the exit anchors at day 12: the bar the entry filled on and the cursor
	// already stands at.
	seedPrediction(t, st, sym.ID, md.H1d, 11*86400, 0.05)
	seedDailyPx(t, st, sym.ID, flat(13, 14))
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("run 2: %v", err)
	}

	trades, err := st.PaperTrades(ctx, "flagship-1d", 50)
	if err != nil {
		t.Fatalf("trades: %v", err)
	}
	var sells int
	for _, tr := range trades {
		if tr.Side != "sell" {
			continue
		}
		sells++
		if tr.Ts <= 12*86400 {
			t.Fatalf("exit filled at ts=%d (day %d), at or behind the day-12 cursor: the revised flip is "+
				"stamped day 11 and anchors its fill at day 12, so this books a sell on a bar the book had "+
				"already consumed -- and on the very bar the entry filled on, a same-bar round trip that "+
				"never happened", tr.Ts, tr.Ts/86400)
		}
	}
	if sells == 0 {
		t.Fatal("fixture broken: the revised flip should still have produced a sell, repriced forward")
	}
}
