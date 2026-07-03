package ranking

import (
	"math"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

const eps = 1e-9

func approx(a, b float64) bool { return math.Abs(a-b) <= 1e-6 }

// TestRelativeStrengthOrder checks that three symbols with clearly different
// momentum rank correctly (Rank 1 == highest composite) and that percentiles
// span the full 0..100 range.
func TestRelativeStrengthOrder(t *testing.T) {
	// Composite = 0.4*1M + 0.6*3M.
	//   HOT:  0.4*0.10 + 0.6*0.30 = 0.22  (strongest)
	//   MID:  0.4*0.02 + 0.6*0.05 = 0.038
	//   COLD: 0.4*-0.05 + 0.6*-0.10 = -0.08 (weakest)
	ms := []Metric{
		{Symbol: "MID", Return1M: 0.02, Return3M: 0.05},
		{Symbol: "COLD", Return1M: -0.05, Return3M: -0.10},
		{Symbol: "HOT", Return1M: 0.10, Return3M: 0.30},
	}

	got := RelativeStrength(ms)

	wantOrder := []struct {
		symbol string
		rank   int
		score  float64
	}{
		{"HOT", 1, 100},
		{"MID", 2, 50},
		{"COLD", 3, 0},
	}
	if len(got) != len(wantOrder) {
		t.Fatalf("got %d ranked, want %d", len(got), len(wantOrder))
	}
	for i, w := range wantOrder {
		if got[i].Symbol != w.symbol {
			t.Errorf("position %d: symbol = %q, want %q", i, got[i].Symbol, w.symbol)
		}
		if got[i].Rank != w.rank {
			t.Errorf("%s: Rank = %d, want %d", w.symbol, got[i].Rank, w.rank)
		}
		if !approx(got[i].Score, w.score) {
			t.Errorf("%s: Score = %v, want %v", w.symbol, got[i].Score, w.score)
		}
	}

	// Percentiles must span 0..100.
	if !approx(got[0].Score, 100) {
		t.Errorf("top Score = %v, want 100", got[0].Score)
	}
	if !approx(got[len(got)-1].Score, 0) {
		t.Errorf("bottom Score = %v, want 0", got[len(got)-1].Score)
	}

	// Momentum echoed correctly on the strongest.
	if !approx(got[0].Momentum, 0.22) {
		t.Errorf("HOT Momentum = %v, want 0.22", got[0].Momentum)
	}
}

// TestRelativeStrengthSingle documents the single-symbol edge case: it scores
// 100 and ranks 1.
func TestRelativeStrengthSingle(t *testing.T) {
	got := RelativeStrength([]Metric{{Symbol: "ONLY", Return1M: 0.01, Return3M: 0.02}})
	if len(got) != 1 {
		t.Fatalf("got %d, want 1", len(got))
	}
	if got[0].Rank != 1 || !approx(got[0].Score, 100) {
		t.Errorf("single: Rank=%d Score=%v, want Rank=1 Score=100", got[0].Rank, got[0].Score)
	}
}

// TestRelativeStrengthTieBreak checks the deterministic ascending-symbol
// tie-break for equal composites.
func TestRelativeStrengthTieBreak(t *testing.T) {
	// All three have identical composite; only the symbol string decides order.
	ms := []Metric{
		{Symbol: "CHARLIE", Return1M: 0.05, Return3M: 0.05},
		{Symbol: "ALPHA", Return1M: 0.05, Return3M: 0.05},
		{Symbol: "BRAVO", Return1M: 0.05, Return3M: 0.05},
	}
	// Run twice to confirm determinism regardless of input order.
	for run := 0; run < 2; run++ {
		got := RelativeStrength(ms)
		want := []string{"ALPHA", "BRAVO", "CHARLIE"}
		for i, w := range want {
			if got[i].Symbol != w {
				t.Errorf("run %d position %d: symbol = %q, want %q", run, i, got[i].Symbol, w)
			}
			if got[i].Rank != i+1 {
				t.Errorf("run %d %s: Rank = %d, want %d", run, w, got[i].Rank, i+1)
			}
		}
	}
}

// makeBars builds a bar series from a slice of closes; other OHLCV fields are
// filled trivially since ranking only reads Close.
func makeBars(closes []float64) []marketdata.Bar {
	bars := make([]marketdata.Bar, len(closes))
	for i, c := range closes {
		bars[i] = marketdata.Bar{Ts: int64(i), Open: c, High: c, Low: c, Close: c, Volume: 1}
	}
	return bars
}

// rampCloses returns n closes starting at start and increasing by step each bar.
func rampCloses(n int, start, step float64) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = start + step*float64(i)
	}
	return out
}

// TestFromBarsMath verifies the return math on a known, controlled series.
func TestFromBarsMath(t *testing.T) {
	// 64 bars (indices 0..63) so len == Bars3M+1 exactly, the minimum.
	// Closes ramp linearly 100, 101, ..., 163.
	n := Bars3M + 1 // 64
	closes := rampCloses(n, 100, 1)
	bars := makeBars(closes)

	m, ok := FromBars("RAMP", bars)
	if !ok {
		t.Fatalf("FromBars ok = false, want true for %d bars", n)
	}

	last := closes[n-1]                   // 163
	want1M := last/closes[n-1-Bars1M] - 1 // 163/142 - 1
	want3M := last/closes[n-1-Bars3M] - 1 // 163/100 - 1 = 0.63
	if !approx(m.Return1M, want1M) {
		t.Errorf("Return1M = %v, want %v", m.Return1M, want1M)
	}
	if !approx(m.Return3M, want3M) {
		t.Errorf("Return3M = %v, want %v", m.Return3M, want3M)
	}
	if !approx(m.Return3M, 0.63) {
		t.Errorf("Return3M = %v, want 0.63", m.Return3M)
	}
	// A strictly rising series ends above its trailing SMA.
	if !m.Above200 {
		t.Errorf("Above200 = false, want true for a rising series")
	}
	// Vol of a linear ramp is positive but small; just assert it is finite and >0.
	if m.Vol <= 0 || math.IsNaN(m.Vol) || math.IsInf(m.Vol, 0) {
		t.Errorf("Vol = %v, want a small positive finite number", m.Vol)
	}
	if m.Symbol != "RAMP" {
		t.Errorf("Symbol = %q, want RAMP", m.Symbol)
	}
}

// TestFromBarsInsufficient covers the not-enough-bars and bad-price guards.
func TestFromBarsInsufficient(t *testing.T) {
	tests := []struct {
		name   string
		closes []float64
		wantOK bool
	}{
		{"too few bars", rampCloses(Bars3M, 100, 1), false},    // len == Bars3M, need Bars3M+1
		{"exactly enough", rampCloses(Bars3M+1, 100, 1), true}, // minimum accepted
		{"plenty", rampCloses(300, 100, 0.5), true},            // > BarsTrendLong
		{"empty", nil, false},                                  // no bars
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, ok := FromBars("S", makeBars(tt.closes))
			if ok != tt.wantOK {
				t.Errorf("ok = %v, want %v", ok, tt.wantOK)
			}
		})
	}
}

// TestFromBarsBadBasePrice ensures a non-positive base close yields ok=false
// rather than an infinite/garbage return.
func TestFromBarsBadBasePrice(t *testing.T) {
	closes := rampCloses(Bars3M+1, 100, 1)
	// Zero out the 3M base bar (index last-Bars3M == 0 here).
	closes[0] = 0
	_, ok := FromBars("BAD", makeBars(closes))
	if ok {
		t.Errorf("ok = true, want false when the 3M base close is zero")
	}
}

// TestFromBarsShortTrendFallback checks that when there are fewer than
// BarsTrendLong+1 bars the SMA50 fallback is used and still resolves Above200
// sensibly. A falling recent series should sit below its short SMA.
func TestFromBarsShortTrendFallback(t *testing.T) {
	// 100 bars: rise for the first 60, then fall for the last 40 so the last
	// close is below the SMA50 of the recent window.
	closes := make([]float64, 0, 100)
	closes = append(closes, rampCloses(60, 100, 1)...)  // 100..159
	closes = append(closes, rampCloses(40, 158, -1)...) // 158..119, falling
	bars := makeBars(closes)
	if len(bars) >= BarsTrendLong+1 {
		t.Fatalf("test setup: want < %d bars to exercise SMA50 fallback", BarsTrendLong+1)
	}
	m, ok := FromBars("FALL", bars)
	if !ok {
		t.Fatalf("ok = false, want true")
	}
	if m.Above200 {
		t.Errorf("Above200 = true, want false for a recently-falling series under SMA50 fallback")
	}
}

// TestSpreadSeparates constructs a set where high scores map to high forward
// returns and confirms Spread is positive (the ranking separates winners from
// losers), plus the cohort means are correct.
func TestSpreadSeparates(t *testing.T) {
	// Score perfectly correlated with forward return.
	outcomes := []RankOutcome{
		{Score: 0, FwdReturn: -0.10},
		{Score: 25, FwdReturn: -0.04},
		{Score: 50, FwdReturn: 0.01},
		{Score: 75, FwdReturn: 0.06},
		{Score: 100, FwdReturn: 0.12},
	}
	// Top/bottom 20% of 5 outcomes -> round(0.2*5)=1 each.
	top, bottom, spread := Spread(outcomes, 0.2, 0.2)
	if !approx(top, 0.12) {
		t.Errorf("topMeanFwd = %v, want 0.12", top)
	}
	if !approx(bottom, -0.10) {
		t.Errorf("bottomMeanFwd = %v, want -0.10", bottom)
	}
	if !approx(spread, 0.22) {
		t.Errorf("spread = %v, want 0.22", spread)
	}
	if spread <= 0 {
		t.Errorf("spread = %v, want positive (ranking should separate)", spread)
	}
}

// TestSpreadCohortMeans checks multi-member cohorts and that the input slice is
// not mutated.
func TestSpreadCohortMeans(t *testing.T) {
	outcomes := []RankOutcome{
		{Score: 90, FwdReturn: 0.08},
		{Score: 10, FwdReturn: -0.06},
		{Score: 80, FwdReturn: 0.04},
		{Score: 20, FwdReturn: -0.02},
		{Score: 50, FwdReturn: 0.00},
	}
	before := make([]RankOutcome, len(outcomes))
	copy(before, outcomes)

	// 40% of 5 -> round(2.0)=2 in each cohort.
	// Top 2 scores: 90(0.08), 80(0.04) -> mean 0.06
	// Bottom 2 scores: 10(-0.06), 20(-0.02) -> mean -0.04
	top, bottom, spread := Spread(outcomes, 0.4, 0.4)
	if !approx(top, 0.06) {
		t.Errorf("topMeanFwd = %v, want 0.06", top)
	}
	if !approx(bottom, -0.04) {
		t.Errorf("bottomMeanFwd = %v, want -0.04", bottom)
	}
	if !approx(spread, 0.10) {
		t.Errorf("spread = %v, want 0.10", spread)
	}

	for i := range before {
		if outcomes[i] != before[i] {
			t.Errorf("Spread mutated input at %d: %+v != %+v", i, outcomes[i], before[i])
		}
	}
}

// TestSpreadNoSignal confirms that a ranking with no relationship to forward
// returns yields a spread near zero — the honesty check catches it.
func TestSpreadNoSignal(t *testing.T) {
	// Forward returns alternate independently of score order.
	outcomes := []RankOutcome{
		{Score: 100, FwdReturn: 0.05},
		{Score: 75, FwdReturn: -0.05},
		{Score: 50, FwdReturn: 0.05},
		{Score: 25, FwdReturn: -0.05},
		{Score: 0, FwdReturn: 0.05},
	}
	// Top 1 = score 100 -> 0.05; bottom 1 = score 0 -> 0.05; spread == 0.
	_, _, spread := Spread(outcomes, 0.2, 0.2)
	if math.Abs(spread) > eps {
		t.Errorf("spread = %v, want ~0 for a ranking with no forward signal", spread)
	}
}

// TestSpreadEdgeCases covers empty input and non-positive percentages.
func TestSpreadEdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		outcomes []RankOutcome
		topPct   float64
		botPct   float64
	}{
		{"empty", nil, 0.2, 0.2},
		{"zero top pct", []RankOutcome{{Score: 1, FwdReturn: 1}}, 0, 0.2},
		{"zero bottom pct", []RankOutcome{{Score: 1, FwdReturn: 1}}, 0.2, 0},
		{"negative pct", []RankOutcome{{Score: 1, FwdReturn: 1}}, -0.5, 0.2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			top, bottom, spread := Spread(tt.outcomes, tt.topPct, tt.botPct)
			if top != 0 || bottom != 0 || spread != 0 {
				t.Errorf("got (%v,%v,%v), want (0,0,0)", top, bottom, spread)
			}
		})
	}
}

// TestSpreadWholeSetOverlap documents behavior when pct=1.0: both cohorts are
// the whole set, so the spread is zero by construction.
func TestSpreadWholeSetOverlap(t *testing.T) {
	outcomes := []RankOutcome{
		{Score: 100, FwdReturn: 0.10},
		{Score: 50, FwdReturn: 0.02},
		{Score: 0, FwdReturn: -0.08},
	}
	top, bottom, spread := Spread(outcomes, 1.0, 1.0)
	mean := (0.10 + 0.02 - 0.08) / 3.0
	if !approx(top, mean) || !approx(bottom, mean) {
		t.Errorf("cohort means = (%v,%v), want both %v", top, bottom, mean)
	}
	if math.Abs(spread) > eps {
		t.Errorf("spread = %v, want 0 when both cohorts are the whole set", spread)
	}
}

// TestEndToEnd wires FromBars -> RelativeStrength across a small universe with
// clearly different trajectories and confirms the strongest trajectory ranks 1.
func TestEndToEnd(t *testing.T) {
	n := 120
	universe := map[string][]float64{
		"WINNER": rampCloses(n, 100, 1.0),  // steep uptrend
		"FLAT":   rampCloses(n, 100, 0.0),  // no move
		"LOSER":  rampCloses(n, 200, -1.0), // downtrend
	}
	var metrics []Metric
	for sym, closes := range universe {
		m, ok := FromBars(sym, makeBars(closes))
		if !ok {
			t.Fatalf("FromBars(%s) ok=false", sym)
		}
		metrics = append(metrics, m)
	}
	ranked := RelativeStrength(metrics)
	if ranked[0].Symbol != "WINNER" || ranked[0].Rank != 1 {
		t.Errorf("top = %q rank %d, want WINNER rank 1", ranked[0].Symbol, ranked[0].Rank)
	}
	if ranked[len(ranked)-1].Symbol != "LOSER" {
		t.Errorf("bottom = %q, want LOSER", ranked[len(ranked)-1].Symbol)
	}
}
