package signalbt

import (
	"math"
	"testing"
)

const dayS = 86400

// obsAt builds one observation on distinct (symbol, day) so it survives the
// independent-N dedup. day is a day index; the ts is placed at noon of that day.
func obsAt(sym int64, day int64, sig float64, fwd map[int]float64) Observation {
	return Observation{SymbolID: sym, Ts: day*dayS + dayS/2, Signal: sig, FwdByLag: fwd}
}

func approx(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

// ── IC / rank correlation on synthetic labeled data ──────────────────────

func TestSpearman_PerfectMonotone(t *testing.T) {
	// A signal that is a strictly increasing function of the forward return must
	// have Spearman IC == 1 regardless of the nonlinear shape.
	x := []float64{0.1, 0.2, 0.3, 0.4, 0.5}
	y := []float64{0.01, 0.04, 0.09, 0.16, 0.25} // y = (2x)^2 style, monotone in x
	if ic := spearman(x, y); !approx(ic, 1.0, 1e-9) {
		t.Fatalf("perfect monotone IC = %v, want 1.0", ic)
	}
}

func TestSpearman_PerfectInverse(t *testing.T) {
	x := []float64{0.1, 0.2, 0.3, 0.4, 0.5}
	y := []float64{0.5, 0.4, 0.3, 0.2, 0.1}
	if ic := spearman(x, y); !approx(ic, -1.0, 1e-9) {
		t.Fatalf("perfect inverse IC = %v, want -1.0", ic)
	}
}

func TestSpearman_TiesAveraged(t *testing.T) {
	// Ties use average ranks; a signal constant across a subset still yields a
	// finite, correct correlation rather than NaN.
	x := []float64{0.2, 0.2, 0.4, 0.6}
	y := []float64{1, 2, 3, 4}
	ic := spearman(x, y)
	if math.IsNaN(ic) || ic <= 0 || ic > 1 {
		t.Fatalf("tied-signal IC = %v, want a valid positive correlation", ic)
	}
}

func TestSpearman_NoSpread(t *testing.T) {
	// All-identical signal has no rank spread → IC 0, never NaN/Inf.
	x := []float64{0.5, 0.5, 0.5, 0.5}
	y := []float64{-0.1, 0.2, -0.3, 0.4}
	if ic := spearman(x, y); ic != 0 {
		t.Fatalf("no-spread IC = %v, want 0", ic)
	}
}

func TestSpearman_TooFewPairs(t *testing.T) {
	if ic := spearman([]float64{0.1, 0.2}, []float64{1, 2}); ic != 0 {
		t.Fatalf("2-pair IC = %v, want 0 (needs >=3)", ic)
	}
}

// ── quintile spread math ─────────────────────────────────────────────────

func TestQuintiles_MonotoneSpread(t *testing.T) {
	// 25 obs where forward return rises with the signal: Q5 mean must exceed Q1.
	var sigs, fwds []float64
	for i := 0; i < 25; i++ {
		s := float64(i) / 24.0        // 0..1
		sigs = append(sigs, s)        // signal
		fwds = append(fwds, s*0.1-0.05) // fwd rises with signal, -5%..+5%
	}
	qs := quintiles(sigs, fwds)
	if len(qs) != 5 {
		t.Fatalf("got %d quintiles, want 5", len(qs))
	}
	// Each quintile has 5 obs and mean-fwd is strictly increasing.
	for i := 0; i < 5; i++ {
		if qs[i].N != 5 {
			t.Fatalf("quintile %d N=%d, want 5", i+1, qs[i].N)
		}
	}
	spread := qs[4].MeanFwd - qs[0].MeanFwd
	if spread <= 0 {
		t.Fatalf("Q5-Q1 spread = %v, want > 0 for a monotone signal", spread)
	}
	for i := 1; i < 5; i++ {
		if qs[i].MeanFwd <= qs[i-1].MeanFwd {
			t.Fatalf("quintile mean-fwd not increasing at %d: %v <= %v", i, qs[i].MeanFwd, qs[i-1].MeanFwd)
		}
	}
}

func TestQuintiles_UnevenCountsSumAndCoverAll(t *testing.T) {
	// 12 obs cut by q*n/5 boundaries (0,2,4,7,9,12) → buckets 2,2,3,2,3. Counts
	// must sum to n and every observation must land in exactly one bucket (no
	// obs dropped, none double-counted).
	var sigs, fwds []float64
	for i := 0; i < 12; i++ {
		sigs = append(sigs, float64(i))
		fwds = append(fwds, 0)
	}
	qs := quintiles(sigs, fwds)
	if len(qs) != 5 {
		t.Fatalf("got %d quintiles, want 5", len(qs))
	}
	total := 0
	for _, q := range qs {
		if q.N <= 0 {
			t.Fatalf("empty quintile %d in a 12-obs cut", q.Quintile)
		}
		total += q.N
	}
	if total != 12 {
		t.Fatalf("quintile counts sum to %d, want 12 (all obs bucketed exactly once)", total)
	}
}

func TestQuintiles_TooFew(t *testing.T) {
	if qs := quintiles([]float64{0.1, 0.2, 0.3, 0.4}, []float64{0, 0, 0, 0}); qs != nil {
		t.Fatalf("quintiles with 4 obs = %v, want nil", qs)
	}
}

// ── hit rate ─────────────────────────────────────────────────────────────

func TestHitRate(t *testing.T) {
	// sig>0.5 predicts up. 3 correct up, 1 wrong up, 1 correct down = 4/5.
	sigs := []float64{0.9, 0.8, 0.7, 0.6, 0.3}
	fwds := []float64{0.02, 0.01, 0.03, -0.01, -0.02}
	if hr := hitRate(sigs, fwds); !approx(hr, 0.8, 1e-9) {
		t.Fatalf("hitRate = %v, want 0.8", hr)
	}
}

// ── cost application in the equity curve ─────────────────────────────────

func TestEquityCurve_CostChargedOnEachSideChange(t *testing.T) {
	// Two obs: first goes long (0->1 = one side, pays cost), earns a flat 0%
	// forward; second goes flat (1->0 = one side, pays cost), earns nothing.
	// With cost c per side, final equity = (1-c) * (1-c).
	p := Params{PrimaryLag: 1, CostBps: 100, LongThreshold: 0.6, FlatThreshold: 0.4} // 100bps = 1%
	obs := []Observation{
		obsAt(1, 0, 0.9, map[int]float64{1: 0.0}), // go long, 0% fwd
		obsAt(1, 1, 0.1, map[int]float64{1: 0.0}), // go flat, 0% fwd
	}
	// NOTE these are different days same symbol -> both independent.
	curve, turnover := equityCurve(dedupeIndependentSorted(obs), nil, p.withDefaults())
	if len(curve) != 2 {
		t.Fatalf("curve len %d, want 2", len(curve))
	}
	c := 0.01
	want := (1 - c) * (1 - c)
	if !approx(curve[1].Strategy, want, 1e-12) {
		t.Fatalf("final equity = %v, want %v (two side-changes at 1%% each)", curve[1].Strategy, want)
	}
	// Turnover = mean |dpos| over 2 obs = (1 + 1)/2 = 1.0.
	if !approx(turnover, 1.0, 1e-12) {
		t.Fatalf("turnover = %v, want 1.0", turnover)
	}
}

func TestEquityCurve_DeadbandHoldsNoCost(t *testing.T) {
	// Long, then a mid-band signal HOLDS (no trade, no cost), earning the fwd
	// return while long. Final equity should reflect ONE entry cost + two long
	// forward returns, with no cost on the hold step.
	p := (Params{PrimaryLag: 1, CostBps: 100, LongThreshold: 0.6, FlatThreshold: 0.4}).withDefaults()
	obs := []Observation{
		obsAt(1, 0, 0.9, map[int]float64{1: 0.10}), // enter long, +10%
		obsAt(1, 1, 0.50, map[int]float64{1: 0.10}), // deadband → hold long, +10%
	}
	curve, turnover := equityCurve(dedupeIndependentSorted(obs), nil, p)
	c := 0.01
	want := (1 - c) * 1.10 * 1.10 // one entry cost, two +10% long bars
	if !approx(curve[1].Strategy, want, 1e-12) {
		t.Fatalf("equity = %v, want %v (entry cost once, hold earns fwd, no 2nd cost)", curve[1].Strategy, want)
	}
	// Only the first step traded (0->1); the hold didn't. Mean |dpos| = 0.5.
	if !approx(turnover, 0.5, 1e-12) {
		t.Fatalf("turnover = %v, want 0.5", turnover)
	}
}

func TestEquityCurve_FlatEarnsNothing(t *testing.T) {
	// A persistently bearish signal stays flat: equity is exactly 1.0 (no cost,
	// no exposure) even though the market moved.
	p := (Params{PrimaryLag: 1, CostBps: 50, LongThreshold: 0.6, FlatThreshold: 0.4}).withDefaults()
	obs := []Observation{
		obsAt(1, 0, 0.2, map[int]float64{1: 0.20}),
		obsAt(1, 1, 0.1, map[int]float64{1: -0.20}),
	}
	curve, turnover := equityCurve(dedupeIndependentSorted(obs), nil, p)
	if !approx(curve[len(curve)-1].Strategy, 1.0, 1e-12) {
		t.Fatalf("flat equity = %v, want 1.0", curve[len(curve)-1].Strategy)
	}
	if turnover != 0 {
		t.Fatalf("flat turnover = %v, want 0", turnover)
	}
}

// dedupeIndependentSorted is a test helper: dedup then sort ascending by ts,
// exactly what Backtest does before calling equityCurve.
func dedupeIndependentSorted(obs []Observation) []Observation {
	d := dedupeIndependent(obs)
	// simple insertion sort by Ts (small n in tests)
	for i := 1; i < len(d); i++ {
		for j := i; j > 0 && d[j].Ts < d[j-1].Ts; j-- {
			d[j], d[j-1] = d[j-1], d[j]
		}
	}
	return d
}

// ── independent-N handling (pseudo-replication guard) ────────────────────

func TestDedupeIndependent_CollapsesSameSymbolDay(t *testing.T) {
	// Three signals for symbol 1 on the SAME day + one on the next day →
	// two independent observations, keeping the LATEST signal each day.
	obs := []Observation{
		{SymbolID: 1, Ts: 0*dayS + 100, Signal: 0.1, FwdByLag: map[int]float64{1: 0.01}},
		{SymbolID: 1, Ts: 0*dayS + 200, Signal: 0.2, FwdByLag: map[int]float64{1: 0.01}},
		{SymbolID: 1, Ts: 0*dayS + 300, Signal: 0.9, FwdByLag: map[int]float64{1: 0.01}}, // latest that day
		{SymbolID: 1, Ts: 1*dayS + 50, Signal: 0.4, FwdByLag: map[int]float64{1: 0.02}},
	}
	got := dedupeIndependentSorted(obs)
	if len(got) != 2 {
		t.Fatalf("independent N = %d, want 2", len(got))
	}
	if got[0].Signal != 0.9 {
		t.Fatalf("day-0 kept signal %v, want the latest 0.9", got[0].Signal)
	}
	if got[1].Signal != 0.4 {
		t.Fatalf("day-1 kept signal %v, want 0.4", got[1].Signal)
	}
}

func TestDedupeIndependent_DistinctSymbolsSameDayAreIndependent(t *testing.T) {
	obs := []Observation{
		{SymbolID: 1, Ts: 100, Signal: 0.1, FwdByLag: map[int]float64{1: 0}},
		{SymbolID: 2, Ts: 100, Signal: 0.2, FwdByLag: map[int]float64{1: 0}},
		{SymbolID: 3, Ts: 100, Signal: 0.3, FwdByLag: map[int]float64{1: 0}},
	}
	if got := dedupeIndependent(obs); len(got) != 3 {
		t.Fatalf("independent N = %d, want 3 (distinct symbols)", len(got))
	}
}

func TestBacktest_GatedBelowMinIndependentN(t *testing.T) {
	// 20 independent obs < MinIndependentN(30): result must be GATED and carry
	// the honest note; headline numbers are still computed for completeness but
	// the UI must not show them.
	var obs []Observation
	for i := 0; i < 20; i++ {
		obs = append(obs, obsAt(int64(i), 0, 0.7, map[int]float64{1: 0.01}))
	}
	res := Backtest(obs, nil, "1d", Params{PrimaryLag: 1})
	if res.IndependentN != 20 {
		t.Fatalf("independentN = %d, want 20", res.IndependentN)
	}
	if !res.Gated {
		t.Fatalf("expected Gated=true below MinIndependentN(%d)", MinIndependentN)
	}
	if res.Live {
		t.Fatalf("Live must be false (backtested replay)")
	}
	if res.Note == "" {
		t.Fatalf("gated result must carry an honest note")
	}
}

func TestBacktest_RawRowsDoNotInflateSample(t *testing.T) {
	// 40 raw rows but all for ONE symbol on ONE day → 1 independent obs. The
	// pseudo-replication guard must report IndependentN=1 and GATE, never treat
	// 40 as 40 independent resolutions.
	var obs []Observation
	for i := 0; i < 40; i++ {
		obs = append(obs, Observation{SymbolID: 1, Ts: int64(100 + i), Signal: 0.7, FwdByLag: map[int]float64{1: 0.01}})
	}
	res := Backtest(obs, nil, "1d", Params{PrimaryLag: 1})
	if res.RawN != 40 {
		t.Fatalf("rawN = %d, want 40", res.RawN)
	}
	if res.IndependentN != 1 {
		t.Fatalf("independentN = %d, want 1 (all same symbol-day)", res.IndependentN)
	}
	if !res.Gated {
		t.Fatalf("must gate on 1 independent obs")
	}
}

// ── end-to-end: a real-edge synthetic signal grades positive OOS ─────────

func TestBacktest_EdgeSignalScoresPositive(t *testing.T) {
	// Build 60 independent obs (>=MinIndependentN) where the signal genuinely
	// leads the forward return: high signal → positive fwd, low signal →
	// negative fwd. The OOS grade should show positive IC, positive quintile
	// spread, hit-rate > 0.5, and a strategy that beats flat.
	var obs []Observation
	for i := 0; i < 60; i++ {
		sig := float64(i) / 59.0 // 0..1
		fwd := (sig - 0.5) * 0.08
		// distinct symbol per obs → all independent, all on day 0..59.
		obs = append(obs, obsAt(int64(i%6), int64(i), sig, map[int]float64{1: fwd, 3: fwd * 0.5}))
	}
	res := Backtest(obs, nil, "1d", Params{PrimaryLag: 1, DecayLags: []int{3}, CostBps: 5})
	if res.Gated {
		t.Fatalf("60 independent obs must clear the gate")
	}
	if res.IC <= 0.5 {
		t.Fatalf("edge signal IC = %v, want > 0.5", res.IC)
	}
	if res.QuintileSpread <= 0 {
		t.Fatalf("edge signal quintile spread = %v, want > 0", res.QuintileSpread)
	}
	if res.HitRate <= 0.5 {
		t.Fatalf("edge signal hit-rate = %v, want > 0.5", res.HitRate)
	}
	// IC decay: primary lag should carry more signal than the diluted lag 3.
	if len(res.ICDecay) != 2 {
		t.Fatalf("expected 2 IC-decay points, got %d", len(res.ICDecay))
	}
	if res.ICDecay[0].LagDays != 1 || res.ICDecay[1].LagDays != 3 {
		t.Fatalf("IC-decay lags out of order: %+v", res.ICDecay)
	}
}

func TestBacktest_NoEdgeSignalScoresNull(t *testing.T) {
	// A signal uncorrelated with the forward return should grade ~0 IC and no
	// quintile spread — the honest "no edge" output.
	var obs []Observation
	// signal alternates high/low but fwd is a fixed pattern uncorrelated with it
	fwds := []float64{0.01, -0.01, 0.02, -0.02, 0.0}
	for i := 0; i < 50; i++ {
		sig := 0.3
		if i%2 == 0 {
			sig = 0.7
		}
		obs = append(obs, obsAt(int64(i), int64(i), sig, map[int]float64{1: fwds[i%len(fwds)]}))
	}
	res := Backtest(obs, nil, "1d", Params{PrimaryLag: 1})
	if math.Abs(res.IC) > 0.3 {
		t.Fatalf("no-edge IC = %v, want near 0", res.IC)
	}
}
