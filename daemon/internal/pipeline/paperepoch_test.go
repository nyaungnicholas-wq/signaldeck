package pipeline

// Proof that the 2026-08-04 record split is a REAL boundary and not a label:
// the sizing edge refuses to see the previous strategy's round trips.

import (
	"context"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// tradeAt appends one fill to the paper log.
func tradeAt(t *testing.T, st *store.Store, strategy string, symID int64, side string, px float64, ts int64) {
	t.Helper()
	if _, err := st.InitPaperBook(context.Background(), strategy, 100000, 0); err != nil {
		t.Fatalf("init book: %v", err)
	}
	if _, err := st.ApplyPaperStep(context.Background(), store.PaperApply{
		Strategy: strategy, BarTs: ts,
		Trades: []store.PaperTrade{{
			Strategy: strategy, SymbolID: symID, Side: side,
			Qty: 10, Px: px, Cost: 0.01, Ts: ts, Reason: "test",
		}},
	}); err != nil {
		t.Fatalf("apply: %v", err)
	}
}

// THE POINT OF THE WHOLE SPLIT. A book with a long, profitable record under the
// OLD rules must not size a trade under the NEW rules from it. Epoch 2's edge
// is unmeasurable until epoch 2 has produced round trips of its own.
func TestEpochScopedEdgeIgnoresThePreviousStrategy(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	sym, _ := st.UpsertSymbol(ctx, "AAA", "stocks", "")
	w := &PaperTrader{St: st}
	if err := w.ensureEpochs(ctx, "flagship-1d"); err != nil {
		t.Fatal(err)
	}

	// 30 profitable round trips, all comfortably BEFORE the boundary.
	// Mixed outcomes: a payoff RATIO needs both winners and losers, so an
	// all-winning fixture would read unmeasurable for the wrong reason.
	before := barrierEpochTs - 400*86400
	for i := int64(0); i < 30; i++ {
		exit := 110.0
		if i%3 == 0 {
			exit = 95.0
		}
		tradeAt(t, st, "flagship-1d", sym.ID, "buy", 100, before+i*2*86400)
		tradeAt(t, st, "flagship-1d", sym.ID, "sell", exit, before+i*2*86400+86400)
	}

	// Measured at a time inside EPOCH 1, the edge is there — this is what the
	// old strategy earned, and epoch 1 is entitled to it. Probe just before the
	// FIRST boundary, not the barrier: the integrity boundary sits between them,
	// so barrierEpochTs-86400 is inside epoch 2 and would correctly see nothing.
	e1, err := w.tradedEdge(ctx, "flagship-1d", backdatedFillEpochTs-86400)
	if err != nil {
		t.Fatal(err)
	}
	if !e1.Valid || e1.Trips < 20 {
		t.Fatalf("epoch 1 should see its own 30 trips, got %+v", e1)
	}

	// Measured after the boundary, the SAME database yields nothing. Epoch 2 has
	// made no round trips, so it has no measured edge — and quarter Kelly must
	// therefore fall back to the equal-slice budget rather than stake size on a
	// win rate a different strategy earned.
	e2, err := w.tradedEdge(ctx, "flagship-1d", barrierEpochTs+86400)
	if err != nil {
		t.Fatal(err)
	}
	if e2.Valid || e2.Trips != 0 {
		t.Fatalf("epoch 2 must not inherit epoch 1's edge, got %+v", e2)
	}
}

// A round trip that STRADDLES the boundary is evidence about neither strategy:
// its entry was chosen by the old rules and its exit by the new ones. It must
// be counted by neither epoch, not silently by the later one.
func TestStraddlingRoundTripIsCountedByNeitherEpoch(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	sym, _ := st.UpsertSymbol(ctx, "BBB", "stocks", "")
	w := &PaperTrader{St: st}
	if err := w.ensureEpochs(ctx, "flagship-1d"); err != nil {
		t.Fatal(err)
	}

	// Enough straddling trips that, if they WERE counted, the edge would be
	// measurable and the assertion below would fail loudly.
	for i := int64(0); i < 30; i++ {
		tradeAt(t, st, "flagship-1d", sym.ID, "buy", 100, barrierEpochTs-(60-i)*86400)
		tradeAt(t, st, "flagship-1d", sym.ID, "sell", 110, barrierEpochTs+(i+1)*86400)
	}

	after, err := w.tradedEdge(ctx, "flagship-1d", barrierEpochTs+100*86400)
	if err != nil {
		t.Fatal(err)
	}
	if after.Trips != 0 {
		t.Fatalf("straddling trips must not count toward epoch 2, got %d", after.Trips)
	}
	// And epoch 1 does not get them either: its window ends at the boundary, so
	// the sells fall outside it.
	beforeEdge, err := w.tradedEdge(ctx, "flagship-1d", barrierEpochTs-86400)
	if err != nil {
		t.Fatal(err)
	}
	if beforeEdge.Trips != 0 {
		t.Fatalf("straddling trips must not count toward epoch 1 either, got %d", beforeEdge.Trips)
	}
}

// The schedule is idempotent and lands on any database, including one the
// worker has never run against.
func TestEnsureEpochsIsIdempotent(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	w := &PaperTrader{St: st}
	for i := 0; i < 3; i++ {
		if err := w.ensureEpochs(ctx, "flagship-1d"); err != nil {
			t.Fatal(err)
		}
	}
	got, err := st.PaperEpochs(ctx, "flagship-1d")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(paperEpochSchedule) {
		t.Fatalf("got %d epochs, want %d", len(got), len(paperEpochSchedule))
	}
	if got[1].FromTs != backdatedFillEpochTs || got[1].Label != "backdated-fills-fixed" {
		t.Fatalf("epoch 2 = %+v, want the back-dated-fill integrity boundary", got[1])
	}
	if got[2].FromTs != barrierEpochTs || got[2].Label != "triple-barrier" {
		t.Fatalf("epoch 3 = %+v, want the barrier boundary", got[2])
	}
	// The boundaries must be the documented dates, not whatever a refactor left.
	if barrierEpochTs != 1785801600 {
		t.Fatalf("the split date moved: %d — history does not move", barrierEpochTs)
	}
	if backdatedFillEpochTs != 1784678400 {
		t.Fatalf("the integrity boundary moved: %d — history does not move", backdatedFillEpochTs)
	}
	// Ascending in time, which EpochBounds' half-open windows depend on.
	for i := 1; i < len(got); i++ {
		if got[i].FromTs <= got[i-1].FromTs {
			t.Fatalf("epoch %d starts at %d, not after epoch %d at %d",
				got[i].Epoch, got[i].FromTs, got[i-1].Epoch, got[i-1].FromTs)
		}
	}
}

// No declared epochs must mean "do not scope", never "boundary at 0 seconds".
// A filter that silently stops filtering is worse than no filter.
func TestNoEpochsMeansUnscoped(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	sym, _ := st.UpsertSymbol(ctx, "CCC", "stocks", "")
	w := &PaperTrader{St: st}

	for i := int64(0); i < 30; i++ {
		exit := 110.0
		if i%3 == 0 {
			exit = 95.0
		}
		tradeAt(t, st, "flagship-1d", sym.ID, "buy", 100, 1000+i*2*86400)
		tradeAt(t, st, "flagship-1d", sym.ID, "sell", exit, 1000+i*2*86400+86400)
	}
	edge, err := w.tradedEdge(ctx, "flagship-1d", barrierEpochTs+86400)
	if err != nil {
		t.Fatal(err)
	}
	if !edge.Valid {
		t.Fatal("with no epochs declared the whole log is one record and the edge must be measurable")
	}
}

// The schedule must land on a pass that does NOTHING ELSE.
//
// ensureEpochs used to be called from buildStep, which Run skips whenever no new
// daily bar has arrived — so on a quiet day the boundaries were never written,
// despite the comment promising they are declared "every pass". A boundary added
// to the code then sat unapplied until the next new bar and had to be written by
// hand with `sdmaint paper-epochs -apply`. This drives the book's cursor to the
// as-of clock FIRST, so the pass under test is a guaranteed no-op.
func TestEnsureEpochsLandsOnANoOpPass(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	sym, _ := st.UpsertSymbol(ctx, "AAA", "stocks", "")
	seedDailyPx(t, st, sym.ID, [][3]float64{{1, 100, 100}, {2, 100, 100}, {3, 100, 100}})

	// Advance the cursor to the newest bar so Run's guard skips buildStep.
	if _, err := st.InitPaperBook(ctx, "flagship-1d", 100_000, 0); err != nil {
		t.Fatalf("init: %v", err)
	}
	applied, err := st.ApplyPaperStep(ctx, store.PaperApply{
		Strategy: "flagship-1d", BarTs: 3 * 86400, NewCash: 100_000,
		EquityTs: 3 * 86400, EquityValue: 100_000,
	})
	if err != nil || !applied {
		t.Fatalf("cursor advance: applied=%v err=%v", applied, err)
	}
	if got, _ := st.PaperEpochs(ctx, "flagship-1d"); len(got) != 0 {
		t.Fatalf("fixture broken: %d epoch(s) before the run, want 0", len(got))
	}

	w := &PaperTrader{St: st}
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}

	got, err := st.PaperEpochs(ctx, "flagship-1d")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(paperEpochSchedule) {
		t.Fatalf("a no-op pass wrote %d epoch(s), want %d — the schedule only lands "+
			"when the book also trades, so a new boundary sits unapplied", len(got), len(paperEpochSchedule))
	}
}
