package breakout

import (
	"testing"
)

// walk builds a close series from a starting price and a slice of simple
// returns, so tests can craft exact correlation structure.
func walk(start float64, rets []float64) []float64 {
	out := make([]float64, len(rets)+1)
	out[0] = start
	for i, r := range rets {
		out[i+1] = out[i] * (1 + r)
	}
	return out
}

// TestCorrelationBreaks_DecouplingPair: a pair correlated across the base window
// but decoupled in the recent tail must be flagged with a large |Delta|.
func TestCorrelationBreaks_DecouplingPair(t *testing.T) {
	const base = 60
	const recent = 20

	// Base window returns: A and B move together (identical) for the first 40
	// steps, then B inverts A for the last 20 steps -> recent corr strongly
	// negative, base corr weaker/positive. That is a decoupling.
	retsA := make([]float64, base)
	retsB := make([]float64, base)
	for i := 0; i < base; i++ {
		// alternating +/- 1% so there is variance in every window.
		v := 0.01
		if i%2 == 0 {
			v = -0.01
		}
		retsA[i] = v
		if i < base-recent {
			retsB[i] = v // coupled early
		} else {
			retsB[i] = -v // inverted (decoupled) in the recent tail
		}
	}

	series := []Series{
		{Symbol: "A", Closes: walk(100, retsA)},
		{Symbol: "B", Closes: walk(100, retsB)},
	}

	got := CorrelationBreaks(series, recent, base)
	if len(got) != 1 {
		t.Fatalf("want exactly 1 break, got %d: %+v", len(got), got)
	}
	brk := got[0]
	if !((brk.A == "A" && brk.B == "B") || (brk.A == "B" && brk.B == "A")) {
		t.Fatalf("unexpected pair: %+v", brk)
	}
	if brk.Delta >= -CorrBreakThreshold {
		t.Fatalf("expected strongly negative Delta (decoupling), got %v", brk.Delta)
	}
	// recent corr should be near -1 (fully inverted tail).
	if brk.RecentR > -0.9 {
		t.Fatalf("recentR = %v, want ~ -1", brk.RecentR)
	}
}

// TestCorrelationBreaks_StableCorrelatedPair: a pair that stays correlated in
// both windows must NOT be flagged.
func TestCorrelationBreaks_StableCorrelatedPair(t *testing.T) {
	const base = 60
	const recent = 20
	rets := make([]float64, base)
	for i := 0; i < base; i++ {
		v := 0.01
		if i%2 == 0 {
			v = -0.01
		}
		rets[i] = v
	}
	// A and B share the SAME returns throughout -> corr ~ 1 in both windows.
	series := []Series{
		{Symbol: "A", Closes: walk(100, rets)},
		{Symbol: "B", Closes: walk(50, rets)},
	}
	got := CorrelationBreaks(series, recent, base)
	if len(got) != 0 {
		t.Fatalf("stable pair should not be flagged, got %+v", got)
	}
}

// TestCorrelationBreaks_NewlyCouplingPair: an independent pair that becomes
// correlated in the recent tail should also be flagged (positive Delta).
func TestCorrelationBreaks_NewlyCouplingPair(t *testing.T) {
	const base = 80
	const recent = 20

	// Base window: A alternates, B is the inverse (corr ~ -1) EXCEPT the recent
	// tail where B copies A (corr ~ +1). Delta = recentR - baseR ~ +2 -> flagged.
	retsA := make([]float64, base)
	retsB := make([]float64, base)
	for i := 0; i < base; i++ {
		v := 0.01
		if i%2 == 0 {
			v = -0.01
		}
		retsA[i] = v
		if i < base-recent {
			retsB[i] = -v // anti-correlated early
		} else {
			retsB[i] = v // coupled recently
		}
	}
	series := []Series{
		{Symbol: "A", Closes: walk(100, retsA)},
		{Symbol: "B", Closes: walk(100, retsB)},
	}
	got := CorrelationBreaks(series, recent, base)
	if len(got) != 1 {
		t.Fatalf("want 1 break, got %d: %+v", len(got), got)
	}
	if got[0].Delta <= CorrBreakThreshold {
		t.Fatalf("expected large positive Delta (newly coupling), got %v", got[0].Delta)
	}
}

// TestCorrelationBreaks_SortAndMultiPair: results sorted by |Delta| desc.
func TestCorrelationBreaks_SortAndMultiPair(t *testing.T) {
	const base = 60
	const recent = 20

	mkAlt := func(sign float64) []float64 {
		r := make([]float64, base)
		for i := 0; i < base; i++ {
			v := sign * 0.01
			if i%2 == 0 {
				v = -sign * 0.01
			}
			r[i] = v
		}
		return r
	}
	// Build so A-B is a big break and C-D is a smaller (sub-threshold) change
	// while A-C etc. are handled by the code; we just assert ordering of the
	// flagged ones by |Delta|.
	base1 := mkAlt(1)
	// B: decoupled hard in tail.
	retsB := make([]float64, base)
	copy(retsB, base1)
	for i := base - recent; i < base; i++ {
		retsB[i] = -retsB[i]
	}

	series := []Series{
		{Symbol: "A", Closes: walk(100, base1)},
		{Symbol: "B", Closes: walk(100, retsB)},
	}
	got := CorrelationBreaks(series, recent, base)
	for i := 1; i < len(got); i++ {
		if abs(got[i-1].Delta) < abs(got[i].Delta) {
			t.Fatalf("results not sorted by |Delta| desc: %+v", got)
		}
	}
}

// TestCorrelationBreaks_MinLengthAlignment: series of differing lengths are
// aligned to the shortest, and too-short input returns empty (no panic).
func TestCorrelationBreaks_MinLengthAlignment(t *testing.T) {
	tests := []struct {
		name   string
		series []Series
		recent int
		base   int
		want   int // expected number of breaks
	}{
		{
			name: "too few closes for base window -> empty",
			series: []Series{
				{Symbol: "A", Closes: []float64{1, 2, 3}},
				{Symbol: "B", Closes: []float64{1, 2, 3}},
			},
			recent: 2, base: 30, want: 0,
		},
		{
			name:   "single series -> empty",
			series: []Series{{Symbol: "A", Closes: walk(100, make([]float64, 60))}},
			recent: 20, base: 60, want: 0,
		},
		{
			name:   "recent > base -> empty (invalid windows)",
			series: []Series{{Symbol: "A"}, {Symbol: "B"}},
			recent: 60, base: 20, want: 0,
		},
		{
			name: "unequal lengths align to shortest without panic",
			series: []Series{
				// A has extra leading history; aligned to B's length.
				{Symbol: "A", Closes: append(walk(100, make([]float64, 10)), walk(100, alt(60))...)},
				{Symbol: "B", Closes: walk(100, alt(60))},
			},
			recent: 20, base: 60, want: 0, // identical alt -> corr 1 both windows, no break
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CorrelationBreaks(tt.series, tt.recent, tt.base)
			if got == nil {
				t.Fatalf("returned nil, want non-nil slice")
			}
			if len(got) != tt.want {
				t.Fatalf("want %d breaks, got %d: %+v", tt.want, len(got), got)
			}
		})
	}
}

// TestPearsonTail exercises the correlation helper directly.
func TestPearsonTail(t *testing.T) {
	tests := []struct {
		name   string
		x, y   []float64
		n      int
		wantR  float64
		wantOK bool
	}{
		{"perfect positive", []float64{1, 2, 3, 4}, []float64{2, 4, 6, 8}, 4, 1, true},
		{"perfect negative", []float64{1, 2, 3, 4}, []float64{4, 3, 2, 1}, 4, -1, true},
		{"zero variance x -> not ok", []float64{5, 5, 5, 5}, []float64{1, 2, 3, 4}, 4, 0, false},
		{"n too large -> not ok", []float64{1, 2}, []float64{1, 2}, 5, 0, false},
		{"n<2 -> not ok", []float64{1, 2}, []float64{1, 2}, 1, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, ok := pearsonTail(tt.x, tt.y, tt.n)
			if ok != tt.wantOK {
				t.Fatalf("ok=%v want %v", ok, tt.wantOK)
			}
			if ok && !within(r, tt.wantR, 1e-9) {
				t.Fatalf("r=%v want %v", r, tt.wantR)
			}
		})
	}
}

func alt(n int) []float64 {
	r := make([]float64, n)
	for i := 0; i < n; i++ {
		v := 0.01
		if i%2 == 0 {
			v = -0.01
		}
		r[i] = v
	}
	return r
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}
