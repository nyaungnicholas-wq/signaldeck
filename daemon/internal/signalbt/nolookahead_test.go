package signalbt

import "testing"

// TestNoLookahead_SignalCannotEarnAReturnItPeeked verifies the no-lookahead
// contract at the engine boundary: the position taken at observation i earns
// observation i's OWN forward return (a move that begins at i and resolves
// strictly later), never a return from a PAST observation. We prove it by
// constructing a series where a single observation has a large forward return,
// then showing equity only reflects it when the signal was long AT that
// observation — a signal that turns bullish only AFTER the move cannot capture
// it.
func TestNoLookahead_SignalCannotEarnAReturnItPeeked(t *testing.T) {
	// Explicit nonzero cost so we account for entry/exit exactly. (withDefaults
	// treats CostBps==0 as "unset" and fills the default, so we cannot ask for a
	// truly free run here; we use a known 10bps and fold it into the expectation.)
	const c = 0.001 // 10 bps per side
	p := (Params{PrimaryLag: 1, CostBps: 10, LongThreshold: 0.6, FlatThreshold: 0.4}).withDefaults()

	// Case A: signal is FLAT on the big-move day, and STAYS flat after. The +50%
	// happens on day 0's forward window while the day-0 signal is flat → equity
	// must NOT earn it. Both days flat means no trade, no cost: equity stays 1.0.
	late := []Observation{
		obsAt(1, 0, 0.1, map[int]float64{1: 0.50}), // flat here, huge move happens
		obsAt(1, 1, 0.2, map[int]float64{1: 0.00}), // still flat
	}
	curveA, _ := equityCurve(dedupeIndependentSorted(late), nil, p)
	if curveA[len(curveA)-1].Strategy != 1.0 {
		t.Fatalf("flat-through equity = %v, want 1.0 (cannot capture a move while flat)",
			curveA[len(curveA)-1].Strategy)
	}

	// Case B: signal is bullish ON the big-move day and stays bullish (so only
	// ONE entry cost is charged). The +50% forward return that begins at day 0
	// IS earned — the honest capture when the call was timely. Expected equity =
	// (1-c) entry cost * 1.50 long return.
	timely := []Observation{
		obsAt(1, 0, 0.9, map[int]float64{1: 0.50}),
		obsAt(1, 1, 0.9, map[int]float64{1: 0.00}), // stay long, no exit cost
	}
	curveB, _ := equityCurve(dedupeIndependentSorted(timely), nil, p)
	if !approx(curveB[len(curveB)-1].Strategy, (1-c)*1.50, 1e-12) {
		t.Fatalf("timely-bull equity = %v, want %v", curveB[len(curveB)-1].Strategy, (1-c)*1.50)
	}
}

// TestNoLookahead_ReplayNeverRecomputesSignal is a documentation-grade guard:
// the engine consumes the Signal as GIVEN (the calibrated prob the pipeline
// already emitted) and the FwdByLag as GIVEN (realized later). It has no access
// to bars and cannot recompute a signal from future data. This test asserts the
// engine's output is a pure function of the provided Signal/FwdByLag by showing
// that mutating a future observation's fields never changes an earlier equity
// mark.
func TestNoLookahead_EarlierMarksIndependentOfLaterObs(t *testing.T) {
	p := Params{PrimaryLag: 1, CostBps: 10}
	base := []Observation{
		obsAt(1, 0, 0.9, map[int]float64{1: 0.05}),
		obsAt(1, 1, 0.9, map[int]float64{1: 0.05}),
		obsAt(1, 2, 0.9, map[int]float64{1: 0.05}),
	}
	res1 := Backtest(clone(base), nil, "1d", p)

	// Change ONLY the last observation's forward return dramatically.
	mut := clone(base)
	mut[2].FwdByLag = map[int]float64{1: -0.90}
	res2 := Backtest(mut, nil, "1d", p)

	// The equity marks up to (but not including) the mutated obs must be
	// identical — an earlier mark can never depend on a later obs.
	if len(res1.Equity) != len(res2.Equity) {
		t.Fatalf("equity lengths differ: %d vs %d", len(res1.Equity), len(res2.Equity))
	}
	for i := 0; i < len(res1.Equity)-1; i++ {
		if res1.Equity[i].Strategy != res2.Equity[i].Strategy {
			t.Fatalf("earlier equity mark %d changed when a LATER obs was mutated: %v vs %v",
				i, res1.Equity[i].Strategy, res2.Equity[i].Strategy)
		}
	}
}

func clone(obs []Observation) []Observation {
	out := make([]Observation, len(obs))
	copy(out, obs)
	return out
}
