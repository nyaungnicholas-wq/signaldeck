package signalbt

import "testing"

// ── SPY buy-and-hold benchmark curve ─────────────────────────────────────

func TestBenchmarkCurve_BuyAndHold(t *testing.T) {
	// Obs on days 0,1,2. SPY closes 100,110,121 on those days → benchmark equity
	// 1.0, 1.1, 1.21 (buy-and-hold from the first day).
	obs := []Observation{
		obsAt(1, 0, 0.5, nil),
		obsAt(1, 1, 0.5, nil),
		obsAt(1, 2, 0.5, nil),
	}
	spyTs := []int64{0*dayS + 300, 1*dayS + 300, 2*dayS + 300}
	spyClose := []float64{100, 110, 121}
	curve := BenchmarkCurve(obs, spyTs, spyClose)
	if len(curve) != 3 {
		t.Fatalf("benchmark len %d, want 3", len(curve))
	}
	wants := []float64{1.0, 1.1, 1.21}
	for i, w := range wants {
		if !approx(curve[i].Benchmark, w, 1e-9) {
			t.Fatalf("benchmark[%d] = %v, want %v", i, curve[i].Benchmark, w)
		}
	}
}

func TestBenchmarkCurve_UsesCloseAtOrBefore(t *testing.T) {
	// A day with no SPY close of its own uses the most recent prior close (a
	// holiday/weekend gap), never a future one — no lookahead in the benchmark.
	obs := []Observation{
		obsAt(1, 0, 0.5, nil),
		obsAt(1, 1, 0.5, nil), // no SPY bar this day
		obsAt(1, 2, 0.5, nil),
	}
	spyTs := []int64{0*dayS + 300, 2*dayS + 300} // gap on day 1
	spyClose := []float64{100, 120}
	curve := BenchmarkCurve(obs, spyTs, spyClose)
	if len(curve) != 3 {
		t.Fatalf("benchmark len %d, want 3", len(curve))
	}
	// Day 1 carries day-0's close (100) → equity 1.0, NOT day-2's 120.
	if !approx(curve[1].Benchmark, 1.0, 1e-9) {
		t.Fatalf("gap day benchmark = %v, want 1.0 (prior close, no lookahead)", curve[1].Benchmark)
	}
	if !approx(curve[2].Benchmark, 1.2, 1e-9) {
		t.Fatalf("benchmark[2] = %v, want 1.2", curve[2].Benchmark)
	}
}

func TestBenchmarkCurve_EmptySPY(t *testing.T) {
	obs := []Observation{obsAt(1, 0, 0.5, nil)}
	if c := BenchmarkCurve(obs, nil, nil); c != nil {
		t.Fatalf("empty SPY benchmark = %v, want nil", c)
	}
}

func TestBacktest_CarriesBenchmarkOntoEquity(t *testing.T) {
	// 30 independent obs (clears the gate) + a rising benchmark: the equity
	// points must carry the SPY equity as of each obs's day, and BenchmarkReturn
	// must reflect the SPY total move.
	var obs []Observation
	for i := 0; i < 30; i++ {
		obs = append(obs, obsAt(int64(i), int64(i), 0.5, map[int]float64{1: 0.0}))
	}
	bench := BenchmarkCurve(obs, benchmarkTs(30), benchmarkRising(30))
	res := Backtest(obs, bench, "1d", Params{PrimaryLag: 1})
	if len(res.Equity) != 30 {
		t.Fatalf("equity len %d, want 30", len(res.Equity))
	}
	if res.BenchmarkReturn <= 0 {
		t.Fatalf("benchmark return = %v, want > 0 for a rising SPY", res.BenchmarkReturn)
	}
	// Deadband-neutral signal (0.5) holds flat: strategy return 0, so excess is
	// negative vs a rising market — the honest read.
	if res.StrategyReturn != 0 {
		t.Fatalf("neutral-signal strategy return = %v, want 0 (held flat)", res.StrategyReturn)
	}
	if res.ExcessReturn >= 0 {
		t.Fatalf("excess vs rising SPY = %v, want negative", res.ExcessReturn)
	}
}

func benchmarkTs(n int) []int64 {
	out := make([]int64, n)
	for i := range out {
		out[i] = int64(i)*dayS + 300
	}
	return out
}

func benchmarkRising(n int) []float64 {
	out := make([]float64, n)
	px := 100.0
	for i := range out {
		out[i] = px
		px *= 1.01
	}
	return out
}
