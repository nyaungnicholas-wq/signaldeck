package api

import (
	"math"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/papertrade"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// NO STATISTIC MAY SPAN THE INTEGRITY BOUNDARY.
//
// The epoch at 2026-07-22 exists because 46 of 123 fills before it were
// back-dated by up to 22 days, each booking the intervening market move as one
// step's P&L. The fills are preserved; only the measurement splits. But the book
// is CONTINUOUS — cash and open positions carried across — so the equity LEVEL
// after the boundary still contains that fabricated P&L, and a total return,
// Sharpe, drawdown or win rate computed over a window that crosses it is derived
// partly from moves that never happened.
//
// MUTATION CHECKS, all verified:
//   - make SpansIntegrityBoundary always return false → TestPaperClean_Refuses
//     fails, publishing a spanning summary;
//   - make buildCleanPerformance skip the rebasing (index = raw equity) →
//     TestPaperClean_RebasesRatherThanCarryingTheContaminatedLevel fails;
//   - change integrityEpochLabel so no epoch matches →
//     TestPaperClean_EmptyAndMissingEpochs fails with a boundary of 0. (The
//     fixture writes the label literally for exactly this reason; built from the
//     constant, both sides would move together and the check would pass.)
//
// The two fill-window bounds that created the defect are pinned in
// pipeline/paper_test.go (TestPaperTrader_NextBarFillNoLookahead for the upper,
// TestPaperTrader_NeverFillsBehindTheBooksClock for the lower); both are
// mutation-verified.

const testBoundary = int64(1784678400) // 2026-07-22 00:00 UTC

func testEpochs() []store.PaperEpoch {
	return []store.PaperEpoch{
		{Strategy: "flagship-1d", Epoch: 1, FromTs: 0, Label: "flip-exit"},
		// The label is written out LITERALLY, not as integrityEpochLabel. Building
		// the fixture from the constant under test makes the constant untestable:
		// both sides move together and a rename that stops matching every live
		// epoch row still passes.
		{Strategy: "flagship-1d", Epoch: 2, FromTs: testBoundary, Label: "backdated-fills-fixed"},
		{Strategy: "flagship-1d", Epoch: 3, FromTs: testBoundary + 13*86400, Label: "triple-barrier"},
	}
}

// curveAround builds marks either side of the boundary. The contaminated half
// climbs steeply (that is the fabricated P&L); the clean half drifts.
func curveAround(before, after int) []papertrade.EquityPoint {
	var out []papertrade.EquityPoint
	eq := 100000.0
	for i := before; i > 0; i-- {
		eq *= 1.02 // the fabricated run-up
		out = append(out, papertrade.EquityPoint{Ts: testBoundary - int64(i)*86400, Cash: eq, Equity: eq})
	}
	for i := 0; i < after; i++ {
		eq *= 0.999
		out = append(out, papertrade.EquityPoint{Ts: testBoundary + int64(i)*86400, Cash: eq, Equity: eq})
	}
	return out
}

func TestPaperClean_SpanDetection(t *testing.T) {
	eps := testEpochs()
	b := integrityBoundary(eps)
	if b != testBoundary {
		t.Fatalf("boundary = %d, want %d — the integrity epoch must be found by label", b, testBoundary)
	}

	// Entirely before.
	if SpansIntegrityBoundary(curveAround(5, 0), b) {
		t.Fatal("a window entirely before the boundary does not span it")
	}
	// Entirely after.
	if SpansIntegrityBoundary(curveAround(0, 5), b) {
		t.Fatal("a window entirely after the boundary does not span it")
	}
	// Crossing.
	if !SpansIntegrityBoundary(curveAround(5, 5), b) {
		t.Fatal("a window crossing the boundary MUST be detected: this is the case that publishes fabricated P&L as performance")
	}
	// Empty.
	if SpansIntegrityBoundary(nil, b) {
		t.Fatal("an empty window spans nothing")
	}
	// No boundary declared.
	if SpansIntegrityBoundary(curveAround(5, 5), 0) {
		t.Fatal("with no boundary declared there is nothing to span")
	}
	// Exactly ON the boundary is AFTER it: the epoch's from_ts is inclusive.
	onlyOn := []papertrade.EquityPoint{{Ts: testBoundary, Equity: 100}}
	if SpansIntegrityBoundary(onlyOn, b) {
		t.Fatal("a single mark at the boundary instant is inside the clean epoch, not across it")
	}
}

func TestPaperClean_Refuses(t *testing.T) {
	eps := testEpochs()
	b := integrityBoundary(eps)
	if !SpansIntegrityBoundary(curveAround(5, 5), b) {
		t.Fatal("fixture does not span the boundary; the refusal below would prove nothing")
	}
	r := refuseAcrossBoundary(b)
	if !r.Refused || r.BoundaryTs != testBoundary || r.Reason == "" || r.UseInstead == "" {
		t.Fatalf("a refusal must carry the instant, the reason and where to look instead: %+v", r)
	}
}

func TestPaperClean_RebasesRatherThanCarryingTheContaminatedLevel(t *testing.T) {
	eps := testEpochs()
	curve := curveAround(6, 6)
	got := buildCleanPerformance(eps, curve, nil)

	if !got.Available {
		t.Fatalf("clean performance unavailable with 6 post-boundary marks: %q", got.Reason)
	}
	if got.Marks != 6 {
		t.Fatalf("marks = %d, want 6 — the clean window is the post-boundary marks ONLY", got.Marks)
	}
	if len(got.Index) == 0 || math.Abs(got.Index[0].Equity-CleanIndexBase) > 1e-9 {
		t.Fatalf("index does not start at %.0f: %v. Carrying the crossed-over equity level forward puts the "+
			"back-dated P&L into the denominator of every clean return.", CleanIndexBase, got.Index[0])
	}
	// The contaminated run-up must be absent from the clean return.
	if got.Summary.TotalReturn > 0 {
		t.Fatalf("clean total return = %.4f, but the post-boundary marks drift DOWN. "+
			"A positive number here means the pre-boundary climb leaked in.", got.Summary.TotalReturn)
	}
	if got.Summary.StartEquity != CleanIndexBase {
		t.Fatalf("startEquity = %.4f, want the index base %.0f", got.Summary.StartEquity, CleanIndexBase)
	}
	// The clean series must be a faithful RESCALE, not a different shape.
	rawFirst, rawLast := curve[6].Equity, curve[len(curve)-1].Equity
	wantRet := rawLast/rawFirst - 1
	if math.Abs(got.Summary.TotalReturn-wantRet) > 1e-9 {
		t.Fatalf("clean return %.8f != the post-boundary window's own return %.8f — rebasing must change the "+
			"LEVEL, never the shape", got.Summary.TotalReturn, wantRet)
	}
}

func TestPaperClean_EmptyAndMissingEpochs(t *testing.T) {
	// No epochs at all: nothing is declared contaminated, so nothing is refused.
	got := buildCleanPerformance(nil, curveAround(3, 3), nil)
	if got.Available || got.Reason == "" {
		t.Fatalf("with no epochs the block must be unavailable WITH a reason: %+v", got)
	}

	// An epoch set with no post-boundary marks: honest unavailability, not a
	// zero-length series presented as a record.
	got = buildCleanPerformance(testEpochs(), curveAround(4, 0), nil)
	if got.Available {
		t.Fatal("no mark falls after the boundary, so there is no clean record to publish")
	}
	if got.Reason == "" {
		t.Fatal("an unavailable clean record must say why")
	}
	if got.BoundaryTs != testBoundary {
		t.Fatalf("the boundary must still be reported even when the window is empty: %d", got.BoundaryTs)
	}

	// Multiple epochs after the boundary must not split the clean window: the
	// integrity boundary is one instant, and epoch 3 is a strategy change graded
	// separately in epochs[].
	got = buildCleanPerformance(testEpochs(), curveAround(2, 20), nil)
	if !got.Available || got.Marks != 20 {
		t.Fatalf("marks = %d, want all 20 post-boundary marks: a later STRATEGY epoch does not shorten the "+
			"clean window, it is reported separately", got.Marks)
	}
}

func TestPaperCleanUsesLatestIntegrityFix(t *testing.T) {
	eps := append(testEpochs(), store.PaperEpoch{Epoch: 4, FromTs: testBoundary + 100*86400, Label: "causal-execution-fixed"})
	got := buildCleanPerformance(eps, curveAround(5, 20), nil)
	if got.Available || got.BoundaryTs != testBoundary+100*86400 {
		t.Fatalf("old execution history reported as clean: %+v", got)
	}
}
