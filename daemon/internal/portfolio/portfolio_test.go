package portfolio

import (
	"errors"
	"math"
	"testing"
)

const eps = 1e-9

func almostEqual(a, b float64) bool {
	return math.Abs(a-b) <= eps
}

// ramp builds a length-n series of closes that produces a repeating,
// non-degenerate return pattern (so variance is non-zero), starting at 100.
func ramp(n int) []float64 {
	closes := make([]float64, n)
	price := 100.0
	for i := 0; i < n; i++ {
		closes[i] = price
		// alternate up 1% / down ~0.99% so returns vary
		if i%2 == 0 {
			price *= 1.01
		} else {
			price *= 0.995
		}
	}
	return closes
}

// antiOf builds a series whose daily returns are the exact negatives of the
// returns implied by base, giving Pearson correlation -1 of the return series.
func antiOf(base []float64) []float64 {
	rets := dailyReturns(base)
	out := make([]float64, len(base))
	out[0] = 100.0
	for i, r := range rets {
		out[i+1] = out[i] * (1 - r)
	}
	return out
}

func TestCorrelationMatrix(t *testing.T) {
	a := ramp(40)
	// identical series -> correlation 1.0
	identical := []Series{
		{Symbol: "A", Closes: append([]float64(nil), a...)},
		{Symbol: "B", Closes: append([]float64(nil), a...)},
	}
	// perfectly anti-correlated
	anti := []Series{
		{Symbol: "A", Closes: append([]float64(nil), a...)},
		{Symbol: "Z", Closes: antiOf(a)},
	}

	tests := []struct {
		name      string
		series    []Series
		wantErr   error
		wantOff   float64 // expected off-diagonal m[0][1] (for the 2-series cases)
		checkDiag bool
	}{
		{name: "identical -> 1.0", series: identical, wantOff: 1.0, checkDiag: true},
		{name: "anti-correlated -> -1.0", series: anti, wantOff: -1.0, checkDiag: true},
		{name: "too few series", series: identical[:1], wantErr: ErrTooFewSeries},
		{
			name: "too few aligned points",
			series: []Series{
				{Symbol: "A", Closes: ramp(10)},
				{Symbol: "B", Closes: ramp(10)},
			},
			wantErr: ErrTooFewPoints,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			syms, m, err := CorrelationMatrix(tt.series)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("want err %v, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if len(syms) != len(tt.series) {
				t.Fatalf("want %d symbols, got %d", len(tt.series), len(syms))
			}
			if tt.checkDiag {
				for i := range m {
					if !almostEqual(m[i][i], 1.0) {
						t.Errorf("diagonal[%d] = %v, want 1.0", i, m[i][i])
					}
				}
				if !almostEqual(m[0][1], tt.wantOff) {
					t.Errorf("off-diagonal = %v, want %v", m[0][1], tt.wantOff)
				}
				// symmetry
				if !almostEqual(m[0][1], m[1][0]) {
					t.Errorf("not symmetric: m[0][1]=%v m[1][0]=%v", m[0][1], m[1][0])
				}
			}
		})
	}
}

func TestCorrelationMatrixSymmetryAndDiagonal3Asset(t *testing.T) {
	a := ramp(50)
	b := antiOf(a)
	c := ramp(60) // different length -> exercises min-length alignment

	syms, m, err := CorrelationMatrix([]Series{
		{Symbol: "A", Closes: a},
		{Symbol: "B", Closes: b},
		{Symbol: "C", Closes: c},
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(syms) != 3 || len(m) != 3 {
		t.Fatalf("want 3x3 matrix, got %d symbols / %d rows", len(syms), len(m))
	}
	for i := 0; i < 3; i++ {
		if !almostEqual(m[i][i], 1.0) {
			t.Errorf("diagonal[%d] = %v, want 1.0", i, m[i][i])
		}
		for j := 0; j < 3; j++ {
			if !almostEqual(m[i][j], m[j][i]) {
				t.Errorf("not symmetric at (%d,%d): %v vs %v", i, j, m[i][j], m[j][i])
			}
			if m[i][j] < -1-eps || m[i][j] > 1+eps {
				t.Errorf("m[%d][%d]=%v out of [-1,1]", i, j, m[i][j])
			}
		}
	}
	// A and B are anti-correlated over their full overlap.
	if !almostEqual(m[0][1], -1.0) {
		t.Errorf("A/B correlation = %v, want -1.0", m[0][1])
	}
}

func TestMinLengthAlignment(t *testing.T) {
	// Long and short series that share an identical recent tail must correlate
	// 1.0 over the aligned (min-length) window.
	tail := ramp(35)
	long := append(ramp(20), tail...) // 55 closes, last 35 == tail
	short := append([]float64(nil), tail...)

	syms, m, err := CorrelationMatrix([]Series{
		{Symbol: "LONG", Closes: long},
		{Symbol: "SHORT", Closes: short},
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(syms) != 2 {
		t.Fatalf("want 2 symbols, got %d", len(syms))
	}
	if !almostEqual(m[0][1], 1.0) {
		t.Errorf("aligned-tail correlation = %v, want 1.0", m[0][1])
	}
}

func TestMostAndLeastCorrelated(t *testing.T) {
	// Hand-set matrix: A-B are the most correlated (0.9), A-C the least (-0.8).
	symbols := []string{"A", "B", "C"}
	m := [][]float64{
		{1.0, 0.9, -0.8},
		{0.9, 1.0, 0.1},
		{-0.8, 0.1, 1.0},
	}
	mostPair, leastPair, mostR, leastR := MostAndLeastCorrelated(symbols, m)

	tests := []struct {
		name string
		got  interface{}
		want interface{}
	}{
		{"most pair", mostPair, [2]string{"A", "B"}},
		{"least pair", leastPair, [2]string{"A", "C"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("got %v, want %v", tt.got, tt.want)
			}
		})
	}
	if !almostEqual(mostR, 0.9) {
		t.Errorf("mostR = %v, want 0.9", mostR)
	}
	if !almostEqual(leastR, -0.8) {
		t.Errorf("leastR = %v, want -0.8", leastR)
	}
}

func TestMostAndLeastCorrelatedDegenerate(t *testing.T) {
	// Fewer than 2 symbols -> zero values, no panic.
	mostPair, leastPair, mostR, leastR := MostAndLeastCorrelated([]string{"A"}, [][]float64{{1.0}})
	if mostPair != ([2]string{}) || leastPair != ([2]string{}) || mostR != 0 || leastR != 0 {
		t.Errorf("degenerate case should return zero values, got %v %v %v %v",
			mostPair, leastPair, mostR, leastR)
	}
}

func TestPositionPnL(t *testing.T) {
	tests := []struct {
		name      string
		p         Position
		lastPrice float64
		wantAbs   float64
		wantPct   float64
	}{
		{
			name:      "long gain",
			p:         Position{Symbol: "AAPL", Qty: 10, EntryPrice: 100},
			lastPrice: 110,
			wantAbs:   100,  // 10 * (110-100)
			wantPct:   0.10, // 100 / 1000
		},
		{
			name:      "long loss",
			p:         Position{Symbol: "AAPL", Qty: 10, EntryPrice: 100},
			lastPrice: 90,
			wantAbs:   -100,
			wantPct:   -0.10,
		},
		{
			name:      "short gain on price drop",
			p:         Position{Symbol: "TSLA", Qty: -5, EntryPrice: 200},
			lastPrice: 180,
			wantAbs:   100, // -5 * (180-200) = 100
			wantPct:   0.10,
		},
		{
			name:      "zero basis -> zero pct",
			p:         Position{Symbol: "X", Qty: 0, EntryPrice: 100},
			lastPrice: 150,
			wantAbs:   0,
			wantPct:   0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotAbs, gotPct := PositionPnL(tt.p, tt.lastPrice)
			if !almostEqual(gotAbs, tt.wantAbs) {
				t.Errorf("pnlAbs = %v, want %v", gotAbs, tt.wantAbs)
			}
			if !almostEqual(gotPct, tt.wantPct) {
				t.Errorf("pnlPct = %v, want %v", gotPct, tt.wantPct)
			}
		})
	}
}

func TestEvaluate(t *testing.T) {
	// Hand-set book:
	//   WIN : 10 @ 100 -> 120  => +200
	//   LOSE: 5  @ 200 -> 150  => -250
	//   FLAT: 2  @ 50  -> 50   => 0
	//   NOP : 1  @ 10  (no last price) -> skipped
	positions := []Position{
		{Symbol: "WIN", Qty: 10, EntryPrice: 100},
		{Symbol: "LOSE", Qty: 5, EntryPrice: 200},
		{Symbol: "FLAT", Qty: 2, EntryPrice: 50},
		{Symbol: "NOP", Qty: 1, EntryPrice: 10},
	}
	last := map[string]float64{
		"WIN":  120,
		"LOSE": 150,
		"FLAT": 50,
		// NOP intentionally missing
	}
	stat := Evaluate(positions, last)

	wantGross := 10*120.0 + 5*150.0 + 2*50.0 // 1200 + 750 + 100 = 2050
	wantPnL := 200.0 - 250.0 + 0.0           // -50
	wantBasis := 10*100.0 + 5*200.0 + 2*50.0 // 1000 + 1000 + 100 = 2100

	tests := []struct {
		name string
		got  float64
		want float64
	}{
		{"gross value", stat.GrossValue, wantGross},
		{"total pnl abs", stat.TotalPnLAbs, wantPnL},
		{"total pnl pct", stat.TotalPnLPct, wantPnL / wantBasis},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !almostEqual(tt.got, tt.want) {
				t.Errorf("%s = %v, want %v", tt.name, tt.got, tt.want)
			}
		})
	}
	if stat.Winners != 1 {
		t.Errorf("Winners = %d, want 1", stat.Winners)
	}
	if stat.Losers != 1 {
		t.Errorf("Losers = %d, want 1", stat.Losers)
	}
	if stat.Best != "WIN" {
		t.Errorf("Best = %q, want WIN", stat.Best)
	}
	if stat.Worst != "LOSE" {
		t.Errorf("Worst = %q, want LOSE", stat.Worst)
	}
}

func TestEvaluateEmpty(t *testing.T) {
	stat := Evaluate(nil, map[string]float64{})
	if stat.GrossValue != 0 || stat.TotalPnLAbs != 0 || stat.TotalPnLPct != 0 ||
		stat.Winners != 0 || stat.Losers != 0 || stat.Best != "" || stat.Worst != "" {
		t.Errorf("empty book should yield zero stat, got %+v", stat)
	}
}

func TestDiversificationScore(t *testing.T) {
	tests := []struct {
		name string
		m    [][]float64
		want float64
	}{
		{
			name: "identical (all corr 1) -> 0",
			m:    [][]float64{{1, 1}, {1, 1}},
			want: 0,
		},
		{
			name: "orthogonal (corr 0) -> 1",
			m:    [][]float64{{1, 0}, {0, 1}},
			want: 1,
		},
		{
			name: "anti-correlated uses abs -> 0",
			m:    [][]float64{{1, -1}, {-1, 1}},
			want: 0,
		},
		{
			name: "single asset -> 1",
			m:    [][]float64{{1}},
			want: 1,
		},
		{
			name: "3-asset average of |0.9|,|0.1|,|-0.8| = 0.6 -> 0.4",
			m: [][]float64{
				{1.0, 0.9, -0.8},
				{0.9, 1.0, 0.1},
				{-0.8, 0.1, 1.0},
			},
			want: 0.4,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DiversificationScore(tt.m)
			if !almostEqual(got, tt.want) {
				t.Errorf("DiversificationScore = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestDiversificationScoreFromIdenticalSeries ties the two halves together:
// identical series -> correlation 1.0 -> diversification score 0.
func TestDiversificationScoreFromIdenticalSeries(t *testing.T) {
	a := ramp(40)
	_, m, err := CorrelationMatrix([]Series{
		{Symbol: "A", Closes: append([]float64(nil), a...)},
		{Symbol: "B", Closes: append([]float64(nil), a...)},
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got := DiversificationScore(m); !almostEqual(got, 0) {
		t.Errorf("identical-series diversification = %v, want 0", got)
	}
}
