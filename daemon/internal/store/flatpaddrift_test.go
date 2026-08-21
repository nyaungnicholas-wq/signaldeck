package store

import (
	"context"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// A PAD THAT DRIFTS IS STILL A PAD.
//
// The original predicate demanded exactly ONE distinct close across a run, so a
// vendor pad that was re-based every few months survived it. 1,849 bars across
// 13 symbols did exactly that on the live database — INFR held
// open=high=low=close at volume 0 for 245 consecutive sessions across four
// prices, PPEM across three separate runs totalling 469 bars — and every one of
// those days sat inside universe_membership, ranked in the cross-section
// printing exactly 0.0% return.
//
// The replacement is a rate: at most one distinct close per PadDistinctCloseRatio
// bars. This file pins both halves of that trade-off — the drifting pad is now
// caught, and the sparse-but-real instrument the rate deliberately spares is
// still spared.
//
// WHAT SPARES THE WARRANTS IS min-run, NOT THE RATE. Measured on the live table,
// at min-run 20 the rate is inert: one-in-1, one-in-2, one-in-5 and one-in-10 all
// select the identical 18 runs / 1,849 bars / 13 symbols. The rate only starts to
// matter in the other direction — at one-in-20 it drops 5 real pads (257 bars),
// and at one-in-50 it drops 12 of the 18. It is a guard rail against a future pad
// shape, and it is deliberately set loose enough to be inert on today's data
// rather than tuned to it.
//
// MUTATION CHECKS, all three verified:
//   - restore `COUNT(DISTINCT close) = 1` in FlatPadRuns and QuarantineFlatPads →
//     TestFlatPadRuns_CatchesADriftingPad fails with 0 runs found;
//   - tighten PadDistinctCloseRatio to 20 → TestFlatPadRuns_CatchesTheTightestPad
//     fails, losing the KLDW/INDF shape (2 closes in 23 bars);
//   - call FlatPadRuns with minRun 3 instead of 20 →
//     TestFlatPadRuns_SparesAThinlyTradedWarrant fails, reporting 30 runs over the
//     warrant's genuine no-trade sessions. That is the run-length threshold doing
//     the sparing, stated where a reader can check it.

// seedDrift writes a run of flat zero-volume bars whose price steps to a new
// level every `every` bars — the INFR/PPEM shape.
func seedDrift(t *testing.T, st *Store, id, from, to int64, px float64, every int64) {
	t.Helper()
	var bars []md.Bar
	for d := from; d <= to; d++ {
		p := px + float64((d-from)/every)*0.25
		bars = append(bars, md.Bar{
			SymbolID: id, TF: md.TF1d, Ts: d * 86400,
			Open: p, High: p, Low: p, Close: p, Volume: 0,
		})
	}
	if err := st.UpsertBars(context.Background(), bars); err != nil {
		t.Fatalf("seed drift: %v", err)
	}
}

func TestFlatPadRuns_CatchesADriftingPad(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "INFR", md.Stocks, "")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// 100 flat zero-volume sessions at four prices — 4% distinct, inside the
	// one-in-ten rate. This is INFR's live shape at a tenth of the length.
	seedDrift(t, st, sym.ID, 20000, 20099, 22.34, 25)

	runs, err := st.FlatPadRuns(ctx, string(md.TF1d), 20)
	if err != nil {
		t.Fatalf("FlatPadRuns: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("found %d run(s), want 1. A pad that steps to a new price every few "+
			"months is still a pad: 245 consecutive zero-volume sessions across four closes "+
			"is not illiquidity, and requiring a single close let 1,849 such bars stay in "+
			"the point-in-time universe.", len(runs))
	}
	if runs[0].Bars != 100 {
		t.Fatalf("run covers %d bars, want the whole 100", runs[0].Bars)
	}

	// The quarantine must move exactly what the report described, and putting it
	// back must be exact too — the whole reason this is quarantine and not delete.
	moved, err := st.QuarantineFlatPads(ctx, string(md.TF1d), 20, "drift-test", "test", 1)
	if err != nil {
		t.Fatalf("quarantine: %v", err)
	}
	if moved != 100 {
		t.Fatalf("quarantined %d bar(s), want 100 — the apply path must agree with the dry run", moved)
	}
	// Idempotence: a second apply finds nothing left to move.
	again, err := st.QuarantineFlatPads(ctx, string(md.TF1d), 20, "drift-test-2", "test", 1)
	if err != nil {
		t.Fatalf("second quarantine: %v", err)
	}
	if again != 0 {
		t.Fatalf("a second quarantine moved %d more bar(s); the operation must be idempotent", again)
	}
	restored, err := st.RestoreQuarantinedBars(ctx, "drift-test")
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if restored != 100 {
		t.Fatalf("restored %d bar(s), want 100 — quarantine without an exact undo is deletion", restored)
	}
}

// TestFlatPadRuns_CatchesTheTightestPad pins the loosest real pad in the live
// database: KLDW and INDF each hold 23 consecutive zero-volume flat sessions
// across 2 distinct closes — 8.7%, the highest ratio among all 18 residual runs.
// The rate has to admit this one or the threshold is tuned past its own evidence.
func TestFlatPadRuns_CatchesTheTightestPad(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "KLDW", md.Stocks, "")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	// 23 bars, price steps once at bar 12: exactly 2 distinct closes.
	seedDrift(t, st, sym.ID, 20000, 20022, 39.89, 12)

	runs, err := st.FlatPadRuns(ctx, string(md.TF1d), 20)
	if err != nil {
		t.Fatalf("FlatPadRuns: %v", err)
	}
	if len(runs) != 1 || runs[0].Bars != 23 {
		t.Fatalf("found %d run(s), want 1 of 23 bars. 2 distinct closes in 23 zero-volume "+
			"flat sessions is the loosest pad this database actually holds; a rate that "+
			"excludes it is tighter than the evidence supports.", len(runs))
	}
}

func TestFlatPadRuns_SparesAThinlyTradedWarrant(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "SAIHW", md.Stocks, "")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// A warrant that goes quiet for three sessions at a time and prints a new price
	// whenever it does trade. This is the live residue min-run spares: SAIHW alone
	// holds 665 such bars, longest run 13, across 1,287 sessions — genuine no-trade
	// days at a real instrument, not vendor padding.
	var bars []md.Bar
	px := 0.05
	for d := int64(20000); d < 20120; d++ {
		if d%4 == 0 {
			px += 0.01
			bars = append(bars, md.Bar{SymbolID: sym.ID, TF: md.TF1d, Ts: d * 86400,
				Open: px, High: px * 1.2, Low: px * 0.8, Close: px, Volume: 12_000})
			continue
		}
		bars = append(bars, md.Bar{SymbolID: sym.ID, TF: md.TF1d, Ts: d * 86400,
			Open: px, High: px, Low: px, Close: px, Volume: 0})
	}
	if err := st.UpsertBars(ctx, bars); err != nil {
		t.Fatalf("seed: %v", err)
	}

	runs, err := st.FlatPadRuns(ctx, string(md.TF1d), 20)
	if err != nil {
		t.Fatalf("FlatPadRuns: %v", err)
	}
	if len(runs) != 0 {
		t.Fatalf("found %d run(s) on a genuinely sparse warrant, want 0. Quarantining "+
			"real no-trade sessions is deleting market history, not cleaning it: runs = %+v", len(runs), runs)
	}
}
