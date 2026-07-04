package signalbt

import (
	"math"
	"sort"
)

// ── skill statistics ─────────────────────────────────────────────────────

// mean is the arithmetic mean of xs (0 for an empty slice).
func mean(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	var s float64
	for _, x := range xs {
		s += x
	}
	return s / float64(len(xs))
}

// spearman is the Spearman rank correlation between x and y (paired,
// equal-length). It is the information coefficient the quant literature reports:
// the Pearson correlation of the RANKS, so it is robust to the non-normal, fat-
// tailed distribution of returns and invariant to any monotone rescaling of the
// signal. Ties are handled with average ranks. Returns 0 when there are fewer
// than 3 pairs or either series has no rank spread (all identical).
func spearman(x, y []float64) float64 {
	n := len(x)
	if n != len(y) || n < 3 {
		return 0
	}
	rx := rank(x)
	ry := rank(y)
	return pearson(rx, ry)
}

// rank returns the average-rank of each element of xs (1-based ranks; tied
// values share the mean of the ranks they span). len(out)==len(xs).
func rank(xs []float64) []float64 {
	n := len(xs)
	idx := make([]int, n)
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool { return xs[idx[a]] < xs[idx[b]] })
	out := make([]float64, n)
	i := 0
	for i < n {
		j := i
		for j+1 < n && xs[idx[j+1]] == xs[idx[i]] {
			j++
		}
		// Ranks i..j (0-based) → average of (i+1 .. j+1) 1-based.
		avg := float64(i+j)/2.0 + 1.0
		for k := i; k <= j; k++ {
			out[idx[k]] = avg
		}
		i = j + 1
	}
	return out
}

// pearson is the Pearson correlation of two equal-length series. Returns 0 for
// <3 pairs or a degenerate (zero-variance) series.
func pearson(x, y []float64) float64 {
	n := float64(len(x))
	if len(x) != len(y) || n < 3 {
		return 0
	}
	var sx, sy, sxx, syy, sxy float64
	for i := range x {
		sx += x[i]
		sy += y[i]
		sxx += x[i] * x[i]
		syy += y[i] * y[i]
		sxy += x[i] * y[i]
	}
	den := (n*sxx - sx*sx) * (n*syy - sy*sy)
	if den <= 0 {
		return 0
	}
	return (n*sxy - sx*sy) / math.Sqrt(den)
}

// hitRate is the fraction of observations where the signal's directional lean
// (Signal>0.5 => predict up) matched the realized forward return's sign
// (fwd>0 == up, fwd<0 == down). Zero-forward observations are treated as a miss
// for an up-lean and a hit for a down-lean (a flat move did not confirm an up
// call). Returns 0 for an empty set.
func hitRate(sigs, fwds []float64) float64 {
	if len(sigs) == 0 || len(sigs) != len(fwds) {
		return 0
	}
	hits := 0
	for i := range sigs {
		predUp := sigs[i] > 0.5
		wasUp := fwds[i] > 0
		if predUp == wasUp {
			hits++
		}
	}
	return float64(hits) / float64(len(sigs))
}

// quintiles cuts the observations into five EQUAL-COUNT buckets by SIGNAL value
// (Q1 = lowest signal, Q5 = highest) and reports each bucket's mean signal, mean
// forward return, and hit rate. The Q5-Q1 mean-forward spread (computed by the
// caller) is the monotonicity check: a signal with edge shows MeanFwd rising
// from Q1 to Q5.
//
// With fewer than 5 observations there are not enough points to form five
// buckets, so an empty slice is returned (the caller reports no spread). Buckets
// are cut by sorting on the signal and slicing into five near-equal ranges; ties
// at a boundary fall into whichever bucket the stable sort places them, which is
// fine for a coarse 5-way cut.
func quintiles(sigs, fwds []float64) []QuintileBucket {
	n := len(sigs)
	if n < 5 || n != len(fwds) {
		return nil
	}
	type pt struct{ s, f float64 }
	pts := make([]pt, n)
	for i := range sigs {
		pts[i] = pt{sigs[i], fwds[i]}
	}
	sort.SliceStable(pts, func(i, j int) bool { return pts[i].s < pts[j].s })

	out := make([]QuintileBucket, 5)
	for q := 0; q < 5; q++ {
		lo := q * n / 5
		hi := (q + 1) * n / 5
		if q == 4 {
			hi = n // last bucket absorbs the remainder
		}
		var sumS, sumF float64
		hits := 0
		for i := lo; i < hi; i++ {
			sumS += pts[i].s
			sumF += pts[i].f
			if pts[i].f > 0 {
				hits++
			}
		}
		cnt := hi - lo
		b := QuintileBucket{Quintile: q + 1, N: cnt}
		if cnt > 0 {
			b.MeanSig = sumS / float64(cnt)
			b.MeanFwd = sumF / float64(cnt)
			b.HitRate = float64(hits) / float64(cnt)
		}
		out[q] = b
	}
	return out
}

// icDecay computes the information coefficient at each forward lag present in
// the observations. For each lag it collects the (signal, forward-return) pairs
// that HAVE a return at that lag and computes the Spearman IC over them. A lag
// with too few pairs still appears (with its N) so the UI can see the sample
// thinning out; its IC is simply 0 when spearman can't be computed. Lags are
// reported in ascending order, de-duplicated.
func icDecay(indep []Observation, lags []int) []ICPoint {
	// De-dup + sort the requested lags.
	seen := map[int]struct{}{}
	uniq := make([]int, 0, len(lags))
	for _, l := range lags {
		if l <= 0 {
			continue
		}
		if _, ok := seen[l]; ok {
			continue
		}
		seen[l] = struct{}{}
		uniq = append(uniq, l)
	}
	sort.Ints(uniq)

	out := make([]ICPoint, 0, len(uniq))
	for _, lag := range uniq {
		sigs := make([]float64, 0, len(indep))
		fwds := make([]float64, 0, len(indep))
		for _, o := range indep {
			if f, ok := o.FwdByLag[lag]; ok {
				sigs = append(sigs, o.Signal)
				fwds = append(fwds, f)
			}
		}
		out = append(out, ICPoint{LagDays: lag, IC: spearman(sigs, fwds), N: len(sigs)})
	}
	return out
}

// ── costed equity curve ──────────────────────────────────────────────────

// equityCurve replays the independent observations (already sorted ascending by
// ts) as a long/flat strategy and returns the net-of-cost equity path plus the
// realized turnover.
//
// Mechanics (no lookahead, costs explicit — same discipline as internal/backtest
// and internal/papertrade):
//
//   - At each observation the CALIBRATED signal maps to a target position via
//     the LongThreshold / FlatThreshold deadband: >=Long → fully long (pos=1),
//     <=Flat → flat (pos=0), in between → hold the prior position. The decision
//     uses only the signal available AT that observation.
//   - The position taken at observation i earns observation i's PRIMARY-LAG
//     forward return (the realized close-to-forward-close move that begins at i).
//     The signal at i was computable from data up to i; its forward return is
//     strictly later — so equity never earns a return the signal peeked at.
//   - A per-side CostBps is charged on the equity whenever the target position
//     CHANGES from the prior observation's position (|Δpos| units of turnover).
//     Going 0→1 or 1→0 pays the cost once; there is no shorting so |Δpos|∈{0,1}.
//
// Turnover is the mean |Δpos| across observations — the average fraction of the
// book that traded per step, a churn proxy consistent with the paper book.
//
// The benchmark series (SPY buy-and-hold, aligned to the distinct trading days
// of the independent set) is copied through onto each equity point so the UI can
// plot both on one axis. When benchmark is nil/short the Benchmark field stays
// at its last known value (or 1.0), and BenchmarkReturn ends at 0.
func equityCurve(indep []Observation, benchmark []EquityPoint, p Params) ([]EquityPoint, float64) {
	if len(indep) == 0 {
		return nil, 0
	}
	cost := p.CostBps / 10000.0

	// Benchmark lookup by day so a strategy mark can carry the SPY equity as of
	// its own day even though the two series may not be 1:1.
	benchByDay := map[int64]float64{}
	for _, b := range benchmark {
		benchByDay[b.Ts/secondsPerDay] = b.Benchmark
	}

	out := make([]EquityPoint, 0, len(indep))
	eq := 1.0
	prevPos := 0.0
	var turnoverSum float64
	lastBench := 1.0
	for _, o := range indep {
		// Target position from the deadband on the calibrated signal.
		target := prevPos
		switch {
		case o.Signal >= p.LongThreshold:
			target = 1.0
		case o.Signal <= p.FlatThreshold:
			target = 0.0
		}
		// Cost on the change, charged before earning the forward return.
		dPos := math.Abs(target - prevPos)
		turnoverSum += dPos
		if dPos > 0 {
			eq *= (1 - cost*dPos)
		}
		// Earn the primary-lag forward return if long (target==1).
		if f, ok := o.FwdByLag[p.PrimaryLag]; ok {
			eq *= (1 + target*f)
		}
		prevPos = target

		if b, ok := benchByDay[o.Ts/secondsPerDay]; ok {
			lastBench = b
		}
		out = append(out, EquityPoint{Ts: o.Ts, Strategy: eq, Benchmark: lastBench})
	}
	turnover := turnoverSum / float64(len(indep))
	return out, turnover
}

// BenchmarkCurve builds a SPY buy-and-hold equity path (start 1.0) over the
// DISTINCT UTC-days that appear in the independent observation set, from the SPY
// daily closes provided as (ts, close) pairs (ascending). For each distinct day
// d in the observation set it uses the SPY close AT OR BEFORE that day; the
// equity at day d is spyClose(d)/spyClose(firstDay). Days before the first SPY
// close, or when SPY data is empty, yield an empty curve.
//
// This is a helper the API handler uses to keep the benchmark aligned to the
// same timeline the strategy is graded on, so the "vs SPY" comparison is on the
// identical calendar. It is pure (no I/O) so it is unit-tested directly.
func BenchmarkCurve(obs []Observation, spyTs []int64, spyClose []float64) []EquityPoint {
	if len(spyTs) == 0 || len(spyTs) != len(spyClose) {
		return nil
	}
	// Distinct days present in the observation set, ascending.
	dayset := map[int64]struct{}{}
	for _, o := range obs {
		dayset[o.Ts/secondsPerDay] = struct{}{}
	}
	days := make([]int64, 0, len(dayset))
	for d := range dayset {
		days = append(days, d)
	}
	sort.Slice(days, func(i, j int) bool { return days[i] < days[j] })
	if len(days) == 0 {
		return nil
	}

	// SPY close at-or-before a given day (bars ascending; binary search on ts).
	closeAtOrBefore := func(dayStart int64) (float64, bool) {
		// Largest index with spyTs[idx] <= dayStart + (secondsPerDay-1).
		target := dayStart*secondsPerDay + secondsPerDay - 1
		lo, hi := 0, len(spyTs)-1
		idx := -1
		for lo <= hi {
			mid := (lo + hi) / 2
			if spyTs[mid] <= target {
				idx = mid
				lo = mid + 1
			} else {
				hi = mid - 1
			}
		}
		if idx < 0 {
			return 0, false
		}
		return spyClose[idx], true
	}

	base, ok := closeAtOrBefore(days[0])
	if !ok || base <= 0 {
		return nil
	}
	out := make([]EquityPoint, 0, len(days))
	for _, d := range days {
		c, ok := closeAtOrBefore(d)
		if !ok || c <= 0 {
			continue
		}
		out = append(out, EquityPoint{Ts: d * secondsPerDay, Benchmark: c / base})
	}
	return out
}
