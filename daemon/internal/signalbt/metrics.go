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
// ts) as a long/flat PORTFOLIO and returns the net-of-cost equity path plus the
// realized turnover.
//
// The unit of compounding is the DAY, not the row. Observations are one per
// (symbol, UTC-day), so a universe of S symbols over D days is S*D rows. This
// used to compound every row against a single scalar position shared by every
// symbol, which made ~1,000 names compound as if they were 1,000 sequential
// days — and put the strategy on a row index while BenchmarkCurve built SPY on a
// day index, so the two series were not comparable at all. It published
// strategyReturn=-0.9995 at turnover=0.00032: an unlevered book cannot lose
// 99.95% of its capital while trading 0.03% of itself, so the figure graded the
// accounting, not the signal.
//
// Mechanics (no lookahead, costs explicit — same discipline as internal/backtest
// and internal/papertrade):
//
//   - Per symbol, the CALIBRATED signal maps to a target position via the
//     LongThreshold / FlatThreshold deadband: >=Long → long, <=Flat → flat, in
//     between → hold THAT SYMBOL's prior position. The decision uses only the
//     signal available at that observation.
//   - A day's book weights each of that day's N names at target/N, so the
//     invested fraction is (#long)/N and can never exceed 1: the book is capped
//     and unlevered, and capital left over earns nothing rather than being
//     silently redeployed. The day's return is the weighted mean of the
//     PRIMARY-LAG forward returns of the names held — realized
//     close-to-forward-close moves that begin that day, strictly later than the
//     signals that chose the book.
//   - A per-side CostBps is charged on the fraction of the book that actually
//     changed hands, Σ|Δwᵢ| over the union of yesterday's and today's names. A
//     name leaving the universe is a sale; a name joining it is a buy.
//
// Turnover is the mean per-DAY Σ|Δwᵢ| — the average fraction of the book that
// traded per rebalance, on the same footing as the paper book.
//
// When the primary lag is longer than a day the day index is STEPPED by that
// lag, so the compounded windows do not overlap. Compounding a 5-day forward
// return on every one of those days would count each market move five times —
// the same double-counting in the time dimension that the row-wise bug was in
// the name dimension.
//
// Marks are stamped at DAY START, the same key BenchmarkCurve uses, so strategy
// and benchmark share one x-axis; each mark carries the SPY equity as of its own
// day. When benchmark is nil/short the Benchmark field stays at its last known
// value (or 1.0), and BenchmarkReturn ends at 0.
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

	// Group into ascending days — indep is sorted by ts, so first sight of a day
	// is its position in the calendar.
	days := make([]int64, 0, len(indep))
	byDay := make(map[int64][]Observation, len(indep))
	for _, o := range indep {
		d := o.Ts / secondsPerDay
		if _, seen := byDay[d]; !seen {
			days = append(days, d)
		}
		byDay[d] = append(byDay[d], o)
	}

	step := p.PrimaryLag
	if step < 1 {
		step = 1
	}

	out := make([]EquityPoint, 0, len(days)/step+1)
	eq := 1.0
	lastBench := 1.0
	// Position is carried PER SYMBOL: the deadband's "hold" means hold this
	// name's own prior stance. A shared scalar made every name inherit whichever
	// one the loop happened to visit last.
	prevPos := make(map[int64]float64, len(byDay))
	prevW := map[int64]float64{}
	var turnoverSum float64
	marks := 0

	for i := 0; i < len(days); i += step {
		d := days[i]
		// Only names with a realized return at the primary lag are tradeable;
		// a still-open forward window is not a position we can grade.
		tradeable := make([]Observation, 0, len(byDay[d]))
		for _, o := range byDay[d] {
			if _, ok := o.FwdByLag[p.PrimaryLag]; ok {
				tradeable = append(tradeable, o)
			}
		}
		if len(tradeable) == 0 {
			continue
		}

		n := float64(len(tradeable))
		curW := make(map[int64]float64, len(tradeable))
		var bookRet float64
		for _, o := range tradeable {
			target := prevPos[o.SymbolID]
			switch {
			case o.Signal >= p.LongThreshold:
				target = 1.0
			case o.Signal <= p.FlatThreshold:
				target = 0.0
			}
			prevPos[o.SymbolID] = target
			w := target / n
			curW[o.SymbolID] = w
			bookRet += w * o.FwdByLag[p.PrimaryLag]
		}

		var traded float64
		for sym, w := range curW {
			traded += math.Abs(w - prevW[sym])
		}
		for sym, w := range prevW {
			if _, stillHeld := curW[sym]; !stillHeld {
				traded += math.Abs(w)
			}
		}
		turnoverSum += traded
		marks++

		// Cost on the rebalance, charged before the book earns the day.
		if traded > 0 {
			eq *= (1 - cost*traded)
		}
		eq *= (1 + bookRet)
		prevW = curW

		if b, ok := benchByDay[d]; ok {
			lastBench = b
		}
		out = append(out, EquityPoint{Ts: d * secondsPerDay, Strategy: eq, Benchmark: lastBench})
	}
	if marks == 0 {
		return nil, 0
	}
	return out, turnoverSum / float64(marks)
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
