// Package expectancy computes conditional forward-return statistics:
// "when the symbol looked like THIS, what happened next". It is pure —
// bars in, marketdata.Expectancy rows out — and never touches the store.
//
// The output is the app's honest 'prediction': measured tendencies with
// sample sizes attached, never point forecasts. To keep thin market states
// from producing overconfident rows, every historical sample is credited
// both to its full state key and to every coarser prefix of that key, so
// rare states roll up into broader (better-sampled) buckets.
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
	walkStart     = 60
	minDailyBars  = 80  // fewer daily bars → no 1d/1w stats at all
	maxDailyBars  = 500 // cap the daily walk to recent history
	minMinuteBars = 300 // fewer minute bars → no 1h stats at all
	minuteStep    = 15  // sample minute states every 15 bars (decorrelates overlap)

	minSamples = 5 // states thinner than this are not emitted
	// Lookup prefers the most specific matching row with at least this many
	// samples; see Lookup for the full policy.
	lookupMinN = 8
)

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
// honestly. Only states with N >= 5 are emitted. SymbolID and UpdatedAt are
// left zero — the caller owns identity and timestamps. The returned map is
// never nil; a horizon key is present only when it produced rows. Rows are
// sorted by StateKey for deterministic output.
func Build(daily, minute []marketdata.Bar) map[marketdata.Horizon][]marketdata.Expectancy {
	acc := map[marketdata.Horizon]map[string][]float64{}
	record := func(h marketdata.Horizon, fullKey string, fwd float64) {
		m := acc[h]
		if m == nil {
			m = map[string][]float64{}
			acc[h] = m
		}
		for _, k := range prefixChain(fullKey) {
			m[k] = append(m[k], fwd)
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
			if i+1 < len(d) {
				record(marketdata.H1d, key, closes[i+1]/closes[i]-1)
			}
			if i+5 < len(d) {
				record(marketdata.H1w, key, closes[i+5]/closes[i]-1)
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
			record(marketdata.H1h, key, closes[i+60]/closes[i]-1)
		}
	}

	out := map[marketdata.Horizon][]marketdata.Expectancy{}
	for h, states := range acc {
		var rows []marketdata.Expectancy
		for key, samples := range states {
			if len(samples) < minSamples {
				continue
			}
			mean, median, hit, stdev := summarize(samples)
			rows = append(rows, marketdata.Expectancy{
				Horizon:   h,
				StateKey:  key,
				N:         len(samples),
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
