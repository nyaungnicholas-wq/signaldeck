package structregime

import (
	"math"
	"testing"
)

// The three Naive*At helpers had zero coverage. They are the NULL a structural
// verdict is graded against — resolve.go's own header says "the only baseline
// that can falsify one is the 'nothing changes' guess" and that the package
// "tests assert truncation invariance". No test did. Nothing about a published
// "the model beat naive persistence" claim is checkable if the naive side is
// wrong, and a baseline that peeks at the future is the way it goes wrong
// silently: it makes the null look better than it was and the model look worse,
// or the reverse, with no symptom anywhere.

// ── generators ───────────────────────────────────────────────────────────

// stepCloses is a flat series that steps to `to` from index `at` onward.
func stepCloses(n int, from, to float64, at int) []float64 {
	out := make([]float64, n)
	for i := range out {
		if i >= at {
			out[i] = to
		} else {
			out[i] = from
		}
	}
	return out
}

func constSeries(n int, v float64) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = v
	}
	return out
}

// altReturns alternates +/-a so the realized vol of any 21-bar window is ~a.
func altReturns(n int, a func(i int) float64) []float64 {
	out := make([]float64, n)
	for i := range out {
		s := 1.0
		if i%2 == 1 {
			s = -1
		}
		out[i] = s * a(i)
	}
	return out
}

// ── NaiveTrendAt ─────────────────────────────────────────────────────────

func TestNaiveTrendAtReadsTheSideOfTheSMA200(t *testing.T) {
	const at = 250

	up, ok := NaiveTrendAt(stepCloses(300, 100, 120, 240), at)
	if !ok || up != "uptrend" {
		t.Errorf("price above its SMA200 gave (%q, %v), want (uptrend, true)", up, ok)
	}
	down, ok := NaiveTrendAt(stepCloses(300, 100, 80, 240), at)
	if !ok || down != "downtrend" {
		t.Errorf("price below its SMA200 gave (%q, %v), want (downtrend, true)", down, ok)
	}
}

// Exactly on the average is not a side. Catches a change that resolves the tie
// to "uptrend" — which would hand the null a free directional call and score it
// against a coin flip, quietly inflating or deflating the benchmark.
func TestNaiveTrendAtAbstainsWhenPriceSitsExactlyOnTheAverage(t *testing.T) {
	if side, ok := NaiveTrendAt(constSeries(300, 100), 250); ok {
		t.Errorf("a perfectly flat series produced side %q — there is no side to call", side)
	}
}

// Before 200 bars the average does not exist. It arrives as NaN, and NaN fails
// every ordering comparison in Go, so the `sma[t] <= 0` guard above it does NOT
// catch this case — only the finite(d) check below does. Delete that check and
// the function starts emitting "downtrend" for every young symbol.
func TestNaiveTrendAtRefusesBeforeTheAverageExists(t *testing.T) {
	// A steady ramp, so index 199 is genuinely off its own 200-bar mean and the
	// only thing that can refuse it is the missing window.
	closes := make([]float64, 300)
	for i := range closes {
		closes[i] = 100 + 0.1*float64(i)
	}
	for _, at := range []int{0, 1, 100, 198} {
		if side, ok := NaiveTrendAt(closes, at); ok {
			t.Errorf("t=%d has only %d bars of history but produced (%q, true)", at, at+1, side)
		}
	}
	if side, ok := NaiveTrendAt(closes, 199); !ok || side != "uptrend" {
		t.Errorf("t=199 is the first bar with a full 200-bar window; got (%q, %v), want (uptrend, true)", side, ok)
	}
}

func TestNaiveTrendAtRejectsOutOfRangeIndices(t *testing.T) {
	closes := stepCloses(300, 100, 120, 240)
	for _, at := range []int{-1, 300, 1000} {
		if _, ok := NaiveTrendAt(closes, at); ok {
			t.Errorf("t=%d is outside the series but was answered", at)
		}
	}
	if _, ok := NaiveTrendAt(nil, 0); ok {
		t.Error("an empty series was answered")
	}
}

// ── NaiveLiquidityAt ─────────────────────────────────────────────────────

func TestNaiveLiquidityAtComparesTheRolling21ToItsTrailingMedian(t *testing.T) {
	const at = 260
	closes := constSeries(300, 100)

	rising := append(constSeries(230, 1000), constSeries(70, 2000)...)
	side, ok := NaiveLiquidityAt(closes, rising, at)
	if !ok || side != "active" {
		t.Errorf("dollar volume above its trailing median gave (%q, %v), want (active, true)", side, ok)
	}

	falling := append(constSeries(230, 2000), constSeries(70, 1000)...)
	side, ok = NaiveLiquidityAt(closes, falling, at)
	if !ok || side != "quiet" {
		t.Errorf("dollar volume below its trailing median gave (%q, %v), want (quiet, true)", side, ok)
	}
}

func TestNaiveLiquidityAtRefusesOnDegenerateInput(t *testing.T) {
	closes := constSeries(300, 100)
	if side, ok := NaiveLiquidityAt(closes, constSeries(300, 1000), 260); ok {
		t.Errorf("a series whose current level EQUALS its median produced %q — there is no side", side)
	}
	if _, ok := NaiveLiquidityAt(closes, constSeries(299, 1000), 260); ok {
		t.Error("mismatched closes/volumes lengths were answered instead of refused")
	}
	for _, at := range []int{-1, 300} {
		if _, ok := NaiveLiquidityAt(closes, constSeries(300, 1000), at); ok {
			t.Errorf("t=%d is outside the series but was answered", at)
		}
	}
	// Non-positive dollar volume becomes NaN and must not be silently averaged in.
	halted := constSeries(300, 1000)
	for i := 255; i <= 260; i++ {
		halted[i] = 0
	}
	if _, ok := NaiveLiquidityAt(closes, halted, 260); ok {
		t.Error("a window containing zero-volume bars produced a liquidity side")
	}
}

// ── NaiveVol21At ─────────────────────────────────────────────────────────

func TestNaiveVol21AtComparesRealizedVolToItsTrailingMedian(t *testing.T) {
	const at = 360

	burst := altReturns(400, func(i int) float64 {
		if i >= 340 {
			return 0.01
		}
		return 0.001
	})
	side, ok := NaiveVol21At(burst, at)
	if !ok || side != "elevated" {
		t.Errorf("vol above its trailing median gave (%q, %v), want (elevated, true)", side, ok)
	}

	settling := altReturns(400, func(i int) float64 {
		if i >= 340 {
			return 0.001
		}
		return 0.01
	})
	side, ok = NaiveVol21At(settling, at)
	if !ok || side != "calm" {
		t.Errorf("vol below its trailing median gave (%q, %v), want (calm, true)", side, ok)
	}
}

func TestNaiveVol21AtRefusesWithoutAFullWindow(t *testing.T) {
	rets := altReturns(400, func(i int) float64 {
		if i >= 340 {
			return 0.01
		}
		return 0.001
	})
	// horizon is 21, so index 19 cannot have a full realized-vol window.
	for _, at := range []int{-1, 0, 19, 400, 1000} {
		if _, ok := NaiveVol21At(rets, at); ok {
			t.Errorf("t=%d cannot carry a full 21-bar window but was answered", at)
		}
	}
	// A single missing return poisons the whole window rather than being skipped:
	// a 20-of-21 vol is not the statistic the forward side is graded on.
	holed := append([]float64(nil), rets...)
	holed[355] = math.NaN()
	if _, ok := NaiveVol21At(holed, 360); ok {
		t.Error("a window containing a NaN return produced a vol side")
	}
}

// ── the causality contract ───────────────────────────────────────────────

// resolve.go:7 states the contract in prose: "everything 'at call time' ... is
// computed from indices <= t only ... (tests assert truncation invariance)".
// This is that test. Truncating the series immediately after t, or replacing
// everything after t with values that would flip any answer that looked at
// them, must leave the baseline at t byte-identical.
//
// A lookahead here is invisible in every other check: the labels still look
// plausible, the accuracy tally still runs, and the published "the model beats
// naive persistence" verdict is graded against a null that knew the answer.
// The series run FAR past the call bar and the poisoned tail is extreme in the
// direction that flips each answer. Both matter: a lookahead that reaches for a
// full-sample median is only detectable when the future outnumbers the past and
// sits on the other side of the comparison. A short, mildly-perturbed tail
// leaves a full-sample statistic looking exactly like the trailing one, and the
// test passes while the lookahead is right there — verified by mutating each
// trailing window into a full-sample one and watching this fail.
func TestNaiveBaselinesNeverReadPastTheCallBar(t *testing.T) {
	const priceAt, volAt = 260, 360

	closes := stepCloses(900, 100, 120, 240)
	volumes := append(constSeries(230, 1000), constSeries(670, 2000)...)
	rets := altReturns(1200, func(i int) float64 {
		if i >= 340 {
			return 0.01
		}
		return 0.001
	})

	poisonedCloses := append([]float64(nil), closes...)
	poisonedVolumes := append([]float64(nil), volumes...)
	poisonedRets := append([]float64(nil), rets...)
	for i := priceAt + 1; i < len(poisonedCloses); i++ {
		poisonedCloses[i] = 1e6 // above any trailing average -> would flip trend
		poisonedVolumes[i] = 1e9
	}
	for i := volAt + 1; i < len(poisonedRets); i++ {
		poisonedRets[i] = 5.0 // vol far above the call bar's -> would flip elevated/calm
	}

	trendSide, trendOK := NaiveTrendAt(closes, priceAt)
	liqSide, liqOK := NaiveLiquidityAt(closes, volumes, priceAt)
	volSide, volOK := NaiveVol21At(rets, volAt)
	if !trendOK || !liqOK || !volOK {
		t.Fatalf("fixture does not produce all three baselines (trend=%v liq=%v vol=%v) — "+
			"the invariance check below would be vacuous", trendOK, liqOK, volOK)
	}

	check := func(name string, gotSide string, gotOK bool, wantSide string) {
		t.Helper()
		if !gotOK || gotSide != wantSide {
			t.Errorf("%s: baseline changed to (%q, %v), want (%q, true) — "+
				"the at-call-time baseline read data from after the call", name, gotSide, gotOK, wantSide)
		}
	}

	s, ok := NaiveTrendAt(poisonedCloses, priceAt)
	check("trend/poisoned-future", s, ok, trendSide)
	s, ok = NaiveTrendAt(closes[:priceAt+1], priceAt)
	check("trend/truncated", s, ok, trendSide)

	s, ok = NaiveLiquidityAt(poisonedCloses, poisonedVolumes, priceAt)
	check("liquidity/poisoned-future", s, ok, liqSide)
	s, ok = NaiveLiquidityAt(closes[:priceAt+1], volumes[:priceAt+1], priceAt)
	check("liquidity/truncated", s, ok, liqSide)

	s, ok = NaiveVol21At(poisonedRets, volAt)
	check("vol/poisoned-future", s, ok, volSide)
	s, ok = NaiveVol21At(rets[:volAt+1], volAt)
	check("vol/truncated", s, ok, volSide)
}
