// Package xsfactor ranks a supplied universe CROSS-SECTIONALLY on the three
// factor legs that survived an independent re-validation: low volatility,
// low dollar-volume (size/liquidity), and momentum-12-1.
//
// WHY CROSS-SECTIONAL AND NOT DIRECTIONAL: the same re-validation tested
// absolute direction at every horizon and it failed 20 for 20 (no CI above
// zero). What did work is the RELATIVE question — "will this symbol beat the
// same-day universe median forward return?" — whose median-split label has a
// base rate of exactly 50%, so measured accuracy is skill rather than an
// artifact of class balance. Everything here answers only that question. See
// Caveat in edge.go, which ships verbatim in every payload.
//
// HONESTY NOTES (this is the brand):
//
//   - POINT IN TIME. Every metric is computed from the trailing closes and
//     dollar volumes the caller supplies, ending at the last bar. Nothing after
//     the last bar is read, so a row is exactly what a reader could have
//     computed on that day.
//
//   - ABSENT != ZERO. A symbol whose history is too short for a leg simply
//     does not have that leg: the composite renormalizes over the legs that ARE
//     computable, and a symbol with no measured-edge leg is reported as skipped
//     with a stated reason rather than ranked at 0.
//
//   - UNMEASURED LEGS ARE NOT WEIGHTED. Momentum-12-1 was only significant at
//     5d, so at 21d/63d its percentile is reported as a diagnostic and left out
//     of the composite. The composite never averages an unmeasured assumption.
//
//   - CORRUPT SERIES ARE REFUSED, NOT SMOOTHED. This repo's bars carry known
//     split artifacts; a trailing series containing a |daily return| above
//     ImpossibleDailyMove is rejected outright and named in the response.
//
// Every function is pure: series in, ranking out. Stdlib only — no I/O, no
// persistence, no clock, no randomness. Same inputs always give the same
// ranking, including tie order.
package xsfactor

import (
	"fmt"
	"math"
	"sort"
)

// Window and gate constants — fixed, documented, never per-symbol tuned.
const (
	// TrailingBars is the trailing daily window a caller should supply so
	// every leg is computable: momentum-12-1 needs momMinCloses closes, and the
	// slack absorbs symbols with holiday gaps in their stored bars.
	TrailingBars = 300

	// volWindow is the number of daily RETURNS in the realized-vol estimate,
	// so it needs volWindow+1 closes.
	volWindow = 21
	// dollarVolWindow is the number of trailing days the median dollar volume
	// is taken over.
	dollarVolWindow = 21
	// momLookback / momSkip define momentum-12-1: the return from 252 trading
	// days ago to 21 trading days ago (12 months minus the most recent month,
	// whose short-term reversal is deliberately skipped).
	momLookback = 252
	momSkip     = 21

	// minVolCloses is the shortest series that yields realized 21d volatility.
	minVolCloses = volWindow + 1
	// momMinCloses is the shortest series that yields momentum-12-1.
	momMinCloses = momLookback + 1

	// tradingDaysPerYear annualizes the daily volatility estimate.
	tradingDaysPerYear = 252.0

	// ImpossibleDailyMove is the split/data-artifact guard. It mirrors
	// splitfix.ImpossibleJump and the predictors' maxSaneReturn=0.65: a single
	// daily move larger than this is an uncorrected split in the stored bars,
	// not a market move, and it would blow up both the vol and the momentum
	// leg. Such a series is refused and named, never quietly used.
	ImpossibleDailyMove = 0.65
)

// Input is one symbol's trailing daily series, oldest→newest. DollarVols is
// index-aligned to Closes; a caller with no volume for a bar should pass 0,
// which simply makes the liquidity leg uncomputable for that symbol.
type Input struct {
	Symbol     string
	Market     string
	Closes     []float64
	DollarVols []float64
}

// Row is one ranked symbol. Every metric and percentile is a pointer so an
// uncomputable leg is null in JSON — an honest hole, not a zero.
type Row struct {
	Symbol string `json:"symbol"`
	Market string `json:"market"`
	Rank   int    `json:"rank"`
	// Composite is the equal-weighted mean of the measured-edge legs present
	// for this symbol, renormalized over those present legs, in [0,1].
	Composite float64  `json:"composite"`
	LegsUsed  []string `json:"legsUsed"`

	// Point-in-time metric values (display / audit).
	Vol21dAnn       *float64 `json:"vol21dAnn"`
	MedDollarVol21d *float64 `json:"medDollarVol21d"`
	Mom121          *float64 `json:"mom12_1"`

	// Cross-sectional percentiles in [0,1] across this request's universe.
	LiquidityPct *float64 `json:"liquidityPct"`
	LowVolPct    *float64 `json:"lowVolPct"`
	Mom121Pct    *float64 `json:"mom12_1Pct"`
}

// Skipped is one symbol that could not be ranked, with the reason stated.
type Skipped struct {
	Symbol string `json:"symbol"`
	Market string `json:"market"`
	Reason string `json:"reason"`
}

// Result is the whole ranked cross-section plus the measured-edge metadata for
// the requested horizon.
type Result struct {
	Horizon Horizon `json:"horizon"`
	// CompositeLegs are the legs averaged into Composite at this horizon (the
	// legs with a measured edge); other percentiles are diagnostics.
	CompositeLegs []string `json:"compositeLegs"`
	Rows          []Row    `json:"rows"`
	// UniverseN is how many symbols formed the cross-section the percentiles
	// were computed against — a percentile is only meaningful relative to it.
	UniverseN     int       `json:"universeN"`
	Skipped       []Skipped `json:"skipped"`
	SplitRejected int       `json:"splitRejected"`
	// Edge carries the PUBLISHED legs; Withheld carries every leg that was
	// measured and did not survive, each with its reason. Withheld ships so a
	// reader sees what failed — a leg that silently disappears from a payload
	// teaches nothing, and one that silently reappears is how the liquidity leg
	// shipped with the wrong sign.
	Edge     []LegEdge `json:"edge"`
	Withheld []LegEdge `json:"withheld"`
}

// metrics holds one symbol's point-in-time factor inputs. A nil pointer means
// the trailing series was too short for that leg.
type metrics struct {
	in     Input
	vol    *float64
	dolVol *float64
	mom    *float64
}

// Rank computes the point-in-time metrics for every input, ranks each metric
// cross-sectionally into a [0,1] percentile, and returns the universe sorted by
// composite descending (ties broken by symbol, then market, so the order is
// fully deterministic). An unknown horizon is an error rather than a guess.
func Rank(h Horizon, inputs []Input) (Result, error) {
	if _, ok := ParseHorizon(h.String()); !ok {
		return Result{}, fmt.Errorf("unknown horizon %q (want 5d|21d|63d)", string(h))
	}
	res := Result{
		Horizon:       h,
		CompositeLegs: CompositeLegs(h),
		Rows:          []Row{},
		Skipped:       []Skipped{},
		Edge:          MeasuredEdge(h),
		Withheld:      WithheldEdge(h),
	}

	// Pass 1 — per-symbol metrics, refusing corrupt or too-short series.
	ms := make([]metrics, 0, len(inputs))
	for _, in := range inputs {
		if reason, split, bad := rejectSeries(in.Closes); bad {
			res.Skipped = append(res.Skipped, Skipped{Symbol: in.Symbol, Market: in.Market, Reason: reason})
			if split {
				res.SplitRejected++
			}
			continue
		}
		m := metrics{in: in}
		m.vol = realizedVol(in.Closes)
		m.dolVol = medianDollarVol(in.DollarVols)
		m.mom = mom121(in.Closes)
		if m.vol == nil && m.dolVol == nil && m.mom == nil {
			res.Skipped = append(res.Skipped, Skipped{
				Symbol: in.Symbol, Market: in.Market,
				Reason: fmt.Sprintf("no leg computable from %d trailing closes (realized vol needs %d, median dollar volume needs %d positive days, momentum-12-1 needs %d)", len(in.Closes), minVolCloses, dollarVolWindow, momMinCloses),
			})
			continue
		}
		ms = append(ms, m)
	}
	res.UniverseN = len(ms)

	// Pass 2 — cross-sectional percentiles over the symbols that have each
	// metric. Ranking ASCENDING throughout: low vol is turned into a high
	// score by the 1-pct flip below, matching the measured low-vol leg.
	volPct := percentiles(collect(ms, func(m metrics) *float64 { return m.vol }))
	dolPct := percentiles(collect(ms, func(m metrics) *float64 { return m.dolVol }))
	momPct := percentiles(collect(ms, func(m metrics) *float64 { return m.mom }))

	// Pass 3 — rows: flip the vol and liquidity legs into "high = the side the
	// measurement favored", then average the measured legs actually present.
	inComposite := map[string]bool{}
	for _, leg := range res.CompositeLegs {
		inComposite[leg] = true
	}
	for i, m := range ms {
		row := Row{
			Symbol: m.in.Symbol, Market: m.in.Market,
			Vol21dAnn: m.vol, MedDollarVol21d: m.dolVol, Mom121: m.mom,
			LegsUsed: []string{},
		}
		var sum float64
		var n int
		add := func(leg string, pct *float64) {
			if pct == nil || !inComposite[leg] {
				return
			}
			sum += *pct
			n++
			row.LegsUsed = append(row.LegsUsed, leg)
		}
		if p, ok := volPct[i]; ok {
			// Low-vol percentile: the calmest name in the cross-section scores
			// 1, the wildest scores 0.
			lv := 1 - p
			row.LowVolPct = &lv
		}
		if p, ok := dolPct[i]; ok {
			// Liquidity leg: THIN scores high (the size/liquidity premium is
			// paid on the small, less-traded side), so flip the ascending rank.
			liq := 1 - p
			row.LiquidityPct = &liq
		}
		if p, ok := momPct[i]; ok {
			mp := p
			row.Mom121Pct = &mp
		}
		add(LegLowVol, row.LowVolPct)
		add(LegLiquidity, row.LiquidityPct)
		add(LegMom121, row.Mom121Pct)
		if n == 0 {
			res.Skipped = append(res.Skipped, Skipped{
				Symbol: m.in.Symbol, Market: m.in.Market,
				Reason: fmt.Sprintf("no measured-edge leg computable at horizon %s (legs there: %v)", h, res.CompositeLegs),
			})
			continue
		}
		row.Composite = sum / float64(n) // renormalized over PRESENT legs only
		res.Rows = append(res.Rows, row)
	}

	sort.SliceStable(res.Rows, func(a, b int) bool {
		ra, rb := res.Rows[a], res.Rows[b]
		if ra.Composite != rb.Composite {
			return ra.Composite > rb.Composite
		}
		if ra.Symbol != rb.Symbol {
			return ra.Symbol < rb.Symbol
		}
		return ra.Market < rb.Market
	})
	for i := range res.Rows {
		res.Rows[i].Rank = i + 1
	}
	sort.SliceStable(res.Skipped, func(a, b int) bool {
		if res.Skipped[a].Symbol != res.Skipped[b].Symbol {
			return res.Skipped[a].Symbol < res.Skipped[b].Symbol
		}
		return res.Skipped[a].Market < res.Skipped[b].Market
	})
	return res, nil
}

// String makes Horizon printable in reasons and query echoes.
func (h Horizon) String() string { return string(h) }

// rejectSeries names why a trailing series cannot be used AT ALL — unusable
// prices, or a split-sized daily move — and whether the split guard was what
// fired, so the response can count artifacts separately from thin history.
// Short-but-clean series are NOT rejected here: they fall through and simply
// carry fewer legs.
func rejectSeries(closes []float64) (reason string, split, bad bool) {
	for _, c := range closes {
		if c <= 0 || math.IsNaN(c) || math.IsInf(c, 0) {
			return "trailing series contains a non-positive or non-finite close — unusable price data", false, true
		}
	}
	if mx, ok := maxAbsReturn(closes); ok && mx > ImpossibleDailyMove {
		return fmt.Sprintf("trailing series contains a |daily return| of %.2f, above the %.2f guard — a split/data artifact in the stored bars, not a market move", mx, ImpossibleDailyMove), true, true
	}
	return "", false, false
}

// maxAbsReturn returns the largest |simple daily return| in the series.
func maxAbsReturn(closes []float64) (float64, bool) {
	if len(closes) < 2 {
		return 0, false
	}
	var mx float64
	for i := 1; i < len(closes); i++ {
		if closes[i-1] <= 0 {
			continue
		}
		if r := math.Abs(closes[i]/closes[i-1] - 1); r > mx {
			mx = r
		}
	}
	return mx, true
}

// realizedVol is the annualized sample standard deviation of the last
// volWindow daily simple returns, or nil when the series is too short.
func realizedVol(closes []float64) *float64 {
	if len(closes) < minVolCloses {
		return nil
	}
	tail := closes[len(closes)-(volWindow+1):]
	rets := make([]float64, 0, volWindow)
	for i := 1; i < len(tail); i++ {
		if tail[i-1] <= 0 {
			return nil
		}
		rets = append(rets, tail[i]/tail[i-1]-1)
	}
	var mean float64
	for _, r := range rets {
		mean += r
	}
	mean /= float64(len(rets))
	var ss float64
	for _, r := range rets {
		ss += (r - mean) * (r - mean)
	}
	sd := math.Sqrt(ss/float64(len(rets)-1)) * math.Sqrt(tradingDaysPerYear)
	if math.IsNaN(sd) || math.IsInf(sd, 0) {
		return nil
	}
	return &sd
}

// medianDollarVol is the median of the last dollarVolWindow dollar volumes.
// nil when the window is short or the median is non-positive (no real turnover
// recorded — absent, not zero-liquidity).
func medianDollarVol(dv []float64) *float64 {
	if len(dv) < dollarVolWindow {
		return nil
	}
	tail := make([]float64, dollarVolWindow)
	copy(tail, dv[len(dv)-dollarVolWindow:])
	for _, v := range tail {
		if v < 0 || math.IsNaN(v) || math.IsInf(v, 0) {
			return nil
		}
	}
	sort.Float64s(tail)
	m := median(tail)
	if m <= 0 {
		return nil
	}
	return &m
}

// mom121 is the 252d return excluding the most recent 21d: the price 21 days
// ago over the price 252 days ago. nil when the series is too short.
func mom121(closes []float64) *float64 {
	if len(closes) < momMinCloses {
		return nil
	}
	n := len(closes)
	end := closes[n-1-momSkip]       // 21 trading days ago
	start := closes[n-1-momLookback] // 252 trading days ago
	if start <= 0 {
		return nil
	}
	m := end/start - 1
	if math.IsNaN(m) || math.IsInf(m, 0) {
		return nil
	}
	return &m
}

// median of an ALREADY-SORTED slice.
func median(sorted []float64) float64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	if n%2 == 1 {
		return sorted[n/2]
	}
	return (sorted[n/2-1] + sorted[n/2]) / 2
}

// collect pulls one metric out of the universe, keyed by universe index, so
// percentiles are computed only over the symbols that HAVE the metric.
func collect(ms []metrics, pick func(metrics) *float64) map[int]float64 {
	out := make(map[int]float64, len(ms))
	for i, m := range ms {
		if v := pick(m); v != nil {
			out[i] = *v
		}
	}
	return out
}

// percentiles ranks values ASCENDING into [0,1], keyed by the same index.
// Ties share the average of their ranks, the lowest value maps to 0 and the
// highest to 1, and a single-member cross-section gets 0.5 (a percentile
// against yourself is meaningless — the neutral midpoint is the honest answer).
func percentiles(vals map[int]float64) map[int]float64 {
	out := make(map[int]float64, len(vals))
	n := len(vals)
	if n == 0 {
		return out
	}
	if n == 1 {
		for i := range vals {
			out[i] = 0.5
		}
		return out
	}
	sorted := make([]float64, 0, n)
	for _, v := range vals {
		sorted = append(sorted, v)
	}
	sort.Float64s(sorted)
	// less[v] = count strictly below v; eq[v] = count equal to v. Averaging the
	// tied block's 0-based ranks keeps the mapping order-independent.
	for i, v := range vals {
		lo := sort.SearchFloat64s(sorted, v)
		hi := lo
		for hi < n && sorted[hi] == v {
			hi++
		}
		midRank := float64(lo) + float64(hi-lo-1)/2
		out[i] = midRank / float64(n-1)
	}
	return out
}
