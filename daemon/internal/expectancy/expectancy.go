package expectancy

import (
	"math"
	"sort"
	"strings"

	"github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// Tunables. These are fixed constants (not config) so that state keys built
// at different times remain comparable — changing a threshold silently
// invalidates previously persisted keys.
const (
	rsiPeriod = 14
	rsiLow    = 35.0 // RSI below → "rsi:low"
	rsiHigh   = 65.0 // RSI above → "rsi:high"

	trendLong    = 200 // close vs SMA200 when enough history
	trendShort   = 50  // fallback SMA when history < trendLong bars
	trendMinBars = 60  // below this the trend dimension is omitted entirely

	rvolPeriod = 20  // RVOL = volume / SMA20(volume)
	rvolHigh   = 1.5 // RVOL above → "rvol:high", else "rvol:normal"

	dailyROCBars  = 20 // momentum lookback on daily bars
	minuteROCBars = 60 // momentum lookback on minute bars

	// Walk shape. walkStart=60 guarantees every dimension is computable
	// (RSI 14, ROC up to 60, SMA20 volume) at the first sampled index.
	walkStart = 60
	// minDailyBars/minMinuteBars must be able to PRODUCE minSamples, or the
	// walk floor and the evidence floor contradict each other: the walk would
	// admit a symbol whose every state cell is then dropped for thinness.
	//   daily:  walkStart + minSamples          = 60 + 30      =  90
	//   minute: walkStart + minSamples*minuteStep + 60 forward = 60+450+60 = 570
	// Both are rounded up for slack. Raised from 80/300 on 2026-08-04 when
	// minSamples went 5 -> 30; at the old floors a symbol could clear the walk
	// gate with 20 usable daily samples and emit nothing.
	minDailyBars  = 95  // fewer daily bars → no 1d/1w stats at all
	maxDailyBars  = 500 // cap the daily walk to recent history
	minMinuteBars = 600 // fewer minute bars → no 1h stats at all
	minuteStep    = 15  // sample minute states every 15 bars (decorrelates overlap)

	// minSamples matches the platform-wide independent-observation floor
	// (modelhealth.MinObservations, ensemble.MinCalibrationPairs) so one number
	// governs "is this evidence". It was 5, which let a five-row cell publish a
	// production probability.
	minSamples = 30
	// minDistinctDays: rows are not observations. The 1h walk samples every 15
	// minute-bars, so a single session can manufacture hundreds of rows that
	// carry the information of ONE day. Daily bars are one per day, so this
	// binds only on the minute walk — which is exactly where the inflation was.
	minDistinctDays = 5

	// expectancyPriorStrength is the pseudocount for shrinking a state's raw
	// hit rate toward the horizon's pooled base rate. 25 matches
	// ensemble.calibrationPriorStrength so one number governs shrinkage.
	expectancyPriorStrength = 25.0

	// Lookup prefers the most specific matching row with at least this many
	// samples; see Lookup for the full policy.
	lookupMinN = 8
)

// cell holds accumulated samples and distinct UTC days for a state key.
type cell struct {
	samples []float64
	days    map[int64]struct{}
}

// Build walks historical bars and returns per-horizon expectancy rows.
//
//   - 1d: daily walk (needs >= 80 daily bars, uses the most recent ~500);
//     at each index i >= 60 the state is built from bars[..i] and the
//     forward return is close[i+1]/close[i]-1.
//   - 1w: same walk with close[i+5]/close[i]-1 (5 trading days).
//   - 1h: minute walk in 15-bar steps (needs >= 300 minute bars); the state
//     uses RSI(14) on minutes, ROC(60), RVOL vs SMA20 of minute volume and
//     no trend dimension; forward return is close[i+60]/close[i]-1.
//
// Each sample is credited to its full state key and to every coarser
// prefix (dimensions dropped right-to-left), so thin states roll up
// honestly. Only states with N >= 30 and seen on at least 5 distinct UTC
// days are emitted. Hit rates are shrunk toward the horizon's pooled base
// rate. SymbolID and UpdatedAt are left zero — the caller owns identity
// and timestamps. The returned map is never nil; a horizon key is present
// only when it produced rows. Rows are sorted by StateKey for deterministic output.
func Build(daily, minute []marketdata.Bar) map[marketdata.Horizon][]marketdata.Expectancy {
	type horizonAcc struct {
		cells    map[string]*cell
		samples  []float64
	}
	acc := map[marketdata.Horizon]*horizonAcc{}

	record := func(h marketdata.Horizon, fullKey string, fwd float64, day int64) {
		ha := acc[h]
		if ha == nil {
			ha = &horizonAcc{cells: map[string]*cell{}}
			acc[h] = ha
		}
		ha.samples = append(ha.samples, fwd)
		for _, k := range prefixChain(fullKey) {
			c := ha.cells[k]
			if c == nil {
				c = &cell{days: map[int64]struct{}{}}
				ha.cells[k] = c
			}
			c.samples = append(c.samples, fwd)
			c.days[day] = struct{}{}
		}
	}

	if len(daily) >= minDailyBars {
		d := tail(daily, maxDailyBars)
		closes, vols := extract(d)
		rsi := rsiSeries(closes, rsiPeriod)
		for i := walkStart; i < len(d); i++ {
			key, ok := stateAt(closes, vols, rsi, i, dailyROCBars, true)
			if !ok || closes[i] == 0 {
				continue
			}
			day := d[i].Ts / 86400
			if i+1 < len(d) {
				record(marketdata.H1d, key, closes[i+1]/closes[i]-1, day)
			}
			if i+5 < len(d) {
				record(marketdata.H1w, key, closes[i+5]/closes[i]-1, day)
			}
		}
	}

	if len(minute) >= minMinuteBars {
		closes, vols := extract(minute)
		rsi := rsiSeries(closes, rsiPeriod)
		for i := walkStart; i+60 < len(minute); i += minuteStep {
			// A 60-bar-ahead move is only an honest "1h" return when those 60
			// bars span roughly one contiguous hour. Stock sessions are ~390
			// bars, so near a session's end minute[i+60] is the NEXT session's
			// bar and closes[i+60]/closes[i] would fold in the overnight/
			// weekend gap (same for any crypto coverage hole). Skip those —
			// mirroring the outcome resolver's own gap guard.
			if minute[i+60].Ts-minute[i].Ts > 3*3600 {
				continue
			}
			key, ok := stateAt(closes, vols, rsi, i, minuteROCBars, false)
			if !ok || closes[i] == 0 {
				continue
			}
			day := minute[i].Ts / 86400
			record(marketdata.H1h, key, closes[i+60]/closes[i]-1, day)
		}
	}

	out := map[marketdata.Horizon][]marketdata.Expectancy{}
	for h, ha := range acc {
		// Compute pooled base rate for this horizon using all samples recorded
		// for the horizon (via ha.samples). This is a shrinkage target only,
		// not a published statistic, and is straightforward because ha.samples
		// contains every sample exactly once (each forward return is appended
		// exactly once to ha.samples in the record closure).
		var totalHits int
		for _, v := range ha.samples {
			if v > 0 {
				totalHits++
			}
		}
		pooled := float64(totalHits) / float64(len(ha.samples))

		var rows []marketdata.Expectancy
		for key, c := range ha.cells {
			if len(c.samples) < minSamples || len(c.days) < minDistinctDays {
				continue
			}
			// summarize's raw hit rate is deliberately discarded: a thin cell's
			// raw rate is the number this leg used to publish, and it saturates
			// at 0.0/1.0 on small n. Only MeanFwd/MedianFwd/Stdev come from it.
			mean, median, _, stdev := summarize(c.samples)
			hit := shrinkHitRate(rawHits(c.samples), len(c.samples), pooled)
			rows = append(rows, marketdata.Expectancy{
				Horizon:   h,
				StateKey:  key,
				N:         len(c.samples),
				MeanFwd:   mean,
				MedianFwd: median,
				HitRate:   hit,
				Stdev:     stdev,
			})
		}
		if len(rows) == 0 {
			continue
		}
		sort.Slice(rows, func(a, b int) bool { return rows[a].StateKey < rows[b].StateKey })
		out[h] = rows
	}
	return out
}

// rawHits returns the count of positive forward returns in samples.
func rawHits(samples []float64) int {
	var hits int
	for _, v := range samples {
		if v > 0 {
			hits++
		}
	}
	return hits
}

// shrinkHitRate pulls a cell's raw hit rate toward globalBase with a
// pseudocount, so a thin cell can never publish a saturated 0.0 or 1.0.
// Returns globalBase when n <= 0.
func shrinkHitRate(hits, n int, globalBase float64) float64 {
	if n <= 0 {
		return globalBase
	}
	return (float64(hits) + expectancyPriorStrength*globalBase) / (float64(n) + expectancyPriorStrength)
}

// CurrentStateKeys returns the FULL state key describing the most recent
// bar per horizon, built exactly like the Build walk (daily history is
// capped to the most recent ~500 bars so Wilder RSI smoothing matches).
// The daily key serves both 1d and 1w; the minute key serves 1h. A horizon
// maps to "" when there is not enough trailing data to construct the key
// (fewer than 61 bars of the relevant timeframe).
func CurrentStateKeys(daily, minute []marketdata.Bar) map[marketdata.Horizon]string {
	out := map[marketdata.Horizon]string{
		marketdata.H1h: "",
		marketdata.H1d: "",
		marketdata.H1w: "",
	}

	if d := tail(daily, maxDailyBars); len(d) > walkStart {
		closes, vols := extract(d)
		rsi := rsiSeries(closes, rsiPeriod)
		if key, ok := stateAt(closes, vols, rsi, len(d)-1, dailyROCBars, true); ok {
			out[marketdata.H1d] = key
			out[marketdata.H1w] = key
		}
	}

	if len(minute) > walkStart {
		closes, vols := extract(minute)
		rsi := rsiSeries(closes, rsiPeriod)
		if key, ok := stateAt(closes, vols, rsi, len(minute)-1, minuteROCBars, false); ok {
			out[marketdata.H1h] = key
		}
	}
	return out
}

// Lookup resolves fullKey against rows using the roll-up policy:
//
//  1. Candidates are the full key followed by its fallback prefixes
//     (dimensions dropped right-to-left), most specific first.
//  2. The first candidate present in rows with N >= 8 wins — a well-sampled
//     specific state beats everything, but a thin exact match (N < 8) loses
//     to a better-sampled parent, because a 5-sample tendency is noise.
//  3. If no candidate reaches N >= 8, the most specific matching row is
//     returned anyway (with its honest small N) rather than nothing.
//  4. If nothing matches at all (or fullKey is empty), ok is false.
//
// When rows contains duplicate StateKeys the first occurrence wins.
func Lookup(rows []marketdata.Expectancy, fullKey string) (marketdata.Expectancy, bool) {
	if fullKey == "" || len(rows) == 0 {
		return marketdata.Expectancy{}, false
	}
	byKey := make(map[string]marketdata.Expectancy, len(rows))
	for _, r := range rows {
		if _, dup := byKey[r.StateKey]; !dup {
			byKey[r.StateKey] = r
		}
	}
	var best marketdata.Expectancy
	found := false
	for _, k := range prefixChain(fullKey) {
		r, ok := byKey[k]
		if !ok {
			continue
		}
		if r.N >= lookupMinN {
			return r, true
		}
		if !found {
			best, found = r, true
		}
	}
	return best, found
}

// ── state construction ──────────────────────────────────────────────────

// stateAt builds the full state key from the trailing window ending at i.
// Dimension order is fixed (rsi, trend, mom, rvol) because fallback keys
// are prefixes — reordering would orphan every persisted key. rsi, mom and
// rvol are required (ok=false when not computable); trend is included only
// when withTrend is set and at least trendMinBars bars are available, per
// the contract that short histories omit the dimension entirely.
func stateAt(closes, vols, rsi []float64, i, rocBars int, withTrend bool) (string, bool) {
	parts := make([]string, 0, 4)

	r := rsi[i]
	if math.IsNaN(r) {
		return "", false
	}
	switch {
	case r < rsiLow:
		parts = append(parts, "rsi:low")
	case r > rsiHigh:
		parts = append(parts, "rsi:high")
	default:
		parts = append(parts, "rsi:mid")
	}

	if withTrend && i+1 >= trendMinBars {
		period := trendShort
		if i+1 >= trendLong {
			period = trendLong
		}
		s, ok := sma(closes, period, i)
		if !ok {
			return "", false
		}
		if closes[i] > s {
			parts = append(parts, "trend:above")
		} else {
			parts = append(parts, "trend:below")
		}
	}

	mom, ok := roc(closes, rocBars, i)
	if !ok {
		return "", false
	}
	if mom > 0 {
		parts = append(parts, "mom:up")
	} else {
		parts = append(parts, "mom:down")
	}

	// The rvol dimension needs a full SMA20 volume window; a degenerate
	// (zero) average volume reads as "normal" — no spike is measurable.
	if i+1 < rvolPeriod {
		return "", false
	}
	if rv, ok := rvol(vols, i); ok && rv > rvolHigh {
		parts = append(parts, "rvol:high")
	} else {
		parts = append(parts, "rvol:normal")
	}

	return strings.Join(parts, "|"), true
}

// prefixChain returns fullKey followed by every coarser fallback key,
// dropping dimensions right-to-left down to the first dimension alone:
// "a|b|c" → ["a|b|c", "a|b", "a"].
func prefixChain(fullKey string) []string {
	parts := strings.Split(fullKey, "|")
	out := make([]string, 0, len(parts))
	for n := len(parts); n >= 1; n-- {
		out = append(out, strings.Join(parts[:n], "|"))
	}
	return out
}

// ── minimal internal indicators ─────────────────────────────────────────
// Deliberately unexported and re-implemented here: internal/signals is
// built concurrently and this package must stay dependency-free.

// sma is the simple moving average of vals[at-period+1 .. at].
func sma(vals []float64, period, at int) (float64, bool) {
	if period <= 0 || at+1 < period || at >= len(vals) {
		return 0, false
	}
	var sum float64
	for j := at - period + 1; j <= at; j++ {
		sum += vals[j]
	}
	return sum / float64(period), true
}

// rsiSeries computes Wilder-smoothed RSI(period) for the whole series;
// out[i] is NaN until index period (the seed needs `period` deltas).
func rsiSeries(closes []float64, period int) []float64 {
	out := make([]float64, len(closes))
	for i := range out {
		out[i] = math.NaN()
	}
	if len(closes) <= period {
		return out
	}
	var gain, loss float64
	for i := 1; i <= period; i++ {
		d := closes[i] - closes[i-1]
		if d > 0 {
			gain += d
		} else {
			loss -= d
		}
	}
	avgG := gain / float64(period)
	avgL := loss / float64(period)
	out[period] = rsiValue(avgG, avgL)
	for i := period + 1; i < len(closes); i++ {
		d := closes[i] - closes[i-1]
		var g, l float64
		if d > 0 {
			g = d
		} else {
			l = -d
		}
		avgG = (avgG*float64(period-1) + g) / float64(period)
		avgL = (avgL*float64(period-1) + l) / float64(period)
		out[i] = rsiValue(avgG, avgL)
	}
	return out
}

// rsiValue maps Wilder average gain/loss to RSI. All-flat history (both
// averages zero) reads as neutral 50 rather than NaN.
func rsiValue(avgG, avgL float64) float64 {
	if avgL == 0 {
		if avgG == 0 {
			return 50
		}
		return 100
	}
	return 100 - 100/(1+avgG/avgL)
}

// roc is the n-bar rate of change ending at index at: close/close[-n] - 1.
func roc(closes []float64, n, at int) (float64, bool) {
	if at < n || at >= len(closes) || closes[at-n] == 0 {
		return 0, false
	}
	return closes[at]/closes[at-n] - 1, true
}

// rvol is relative volume: volume at index at over SMA20 of volume.
func rvol(vols []float64, at int) (float64, bool) {
	s, ok := sma(vols, rvolPeriod, at)
	if !ok || s <= 0 {
		return 0, false
	}
	return vols[at] / s, true
}

// ── small helpers ───────────────────────────────────────────────────────

// summarize returns mean, median, hit rate (fraction strictly > 0) and
// population standard deviation of samples. samples must be non-empty.
func summarize(samples []float64) (mean, median, hitRate, stdev float64) {
	n := float64(len(samples))
	var sum float64
	hits := 0
	for _, v := range samples {
		sum += v
		if v > 0 {
			hits++
		}
	}
	mean = sum / n
	var sq float64
	for _, v := range samples {
		d := v - mean
		sq += d * d
	}
	stdev = math.Sqrt(sq / n)
	hitRate = float64(hits) / n

	sorted := append([]float64(nil), samples...)
	sort.Float64s(sorted)
	mid := len(sorted) / 2
	if len(sorted)%2 == 1 {
		median = sorted[mid]
	} else {
		median = (sorted[mid-1] + sorted[mid]) / 2
	}
	return mean, median, hitRate, stdev
}

// tail returns the last n elements of bars (or bars itself when shorter).
func tail(bars []marketdata.Bar, n int) []marketdata.Bar {
	if len(bars) > n {
		return bars[len(bars)-n:]
	}
	return bars
}

// extract pulls parallel close and volume slices out of bars.
func extract(bars []marketdata.Bar) (closes, vols []float64) {
	closes = make([]float64, len(bars))
	vols = make([]float64, len(bars))
	for i, b := range bars {
		closes[i] = b.Close
		vols[i] = b.Volume
	}
	return closes, vols
}