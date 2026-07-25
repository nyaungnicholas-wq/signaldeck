// Package histfeat computes point-in-time weekly research rows — the raw
// observations behind the research discovery engine's historical backfill.
// Every feature at an anchor derives only from that anchor's trailing bars
// (window capped at 500, matching the live scorer's LastBars(TF1d, 500)), so
// truncating a series never changes earlier rows — the structural
// no-lookahead guarantee the leakage sentinel relies on. Pure: bars and
// macro points in, rows out — no I/O, no clock, no RNG.
package histfeat

import (
	"math"
	"sort"

	"github.com/nyaungnicholas-wq/signaldeck/internal/macrofeat"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/signals"
)

// WeekSecs is one calendar week in seconds; week buckets are ts/WeekSecs.
const WeekSecs = 7 * 86400

const (
	// featureWindow caps every trailing feature window, matching the live
	// scorer's LastBars(TF1d, 500).
	featureWindow = 500
	// minTrailingBars is the anchor gate: fewer trailing bars and no row.
	minTrailingBars = 60
	// pctLookback / pctMinValues govern the rsi_pct and vol_pct percentile
	// histories: rank within the last 252 bar positions, needing at least
	// 60 values.
	pctLookback  = 252
	pctMinValues = 60
	// voidAfter mirrors the live resolver: a forward bar more than 21 days
	// past the target voids the anchor.
	voidAfter = 3 * WeekSecs
)

// Calendar eras for regime-survival grading (fixed UTC day boundaries).
const (
	EraPreCovid   = "pre_covid"        // ..2020-02-14
	EraCovidCrash = "covid_crash"      // 2020-02-15..2020-06-30
	EraBull2021   = "bull_2020_21"     // 2020-07-01..2021-12-31
	EraBear2022   = "bear_2022"        // 2022-01-01..2022-12-31
	EraAIRally    = "ai_rally_2023_25" // 2023-01-01..2025-12-31
	EraY2026      = "y2026"            // 2026-01-01..
)

// Era start instants, unix UTC.
const (
	covidCrashStart int64 = 1581724800 // 2020-02-15T00:00:00Z
	bull2021Start   int64 = 1593561600 // 2020-07-01T00:00:00Z
	bear2022Start   int64 = 1640995200 // 2022-01-01T00:00:00Z
	aiRallyStart    int64 = 1672531200 // 2023-01-01T00:00:00Z
	y2026Start      int64 = 1767225600 // 2026-01-01T00:00:00Z
)

// EraOf maps a unix timestamp to its calendar era.
func EraOf(ts int64) string {
	switch {
	case ts >= y2026Start:
		return EraY2026
	case ts >= aiRallyStart:
		return EraAIRally
	case ts >= bear2022Start:
		return EraBear2022
	case ts >= bull2021Start:
		return EraBull2021
	case ts >= covidCrashStart:
		return EraCovidCrash
	}
	return EraPreCovid
}

// EraOrder returns the gradeable eras in chronological order, excluding
// pre_covid (it predates the backfill's bar coverage).
func EraOrder() []string {
	return []string{EraCovidCrash, EraBull2021, EraBear2022, EraAIRally, EraY2026}
}

// Point is one macro-series observation (adapter for the store's MacroPoint).
type Point struct {
	Ts    int64
	Value float64
}

// WeekRow is one labeled point-in-time research observation: features from
// the anchor's trailing window, outcome from the following week.
type WeekRow struct {
	Ts        int64 // anchor daily-bar ts (last bar of its week bucket)
	Week      int64 // Ts / WeekSecs
	Vec       map[string]float64
	FwdReturn float64
	Up        bool
	Era       string
	HighVol   bool // vix_high_vol == 1 at the anchor's week
}

// marketWeek is the per-week market state joined onto every symbol's row.
type marketWeek struct {
	trend    float64
	trendOK  bool
	ret13w   float64
	ret13wOK bool
	vix      macrofeat.VIXFeatures
	vixOK    bool
}

// MarketCtx is per-week market state precomputed once by BuildMarketCtx and
// shared across every symbol's WeeklyRows pass. The zero value joins nothing.
type MarketCtx struct {
	weeks map[int64]marketWeek
}

// BuildMarketCtx precomputes per-week market state from SPY daily bars
// (ascending) and the VIX series (ascending): mkt_trend (+1 bull when close
// and SMA50 are both above SMA200, -1 bear when both below, 0 sideways),
// mkt_ret_13w (the week's SPY anchor close vs the anchor close exactly 13
// week buckets earlier), and vix_* via macrofeat from the latest VIX
// observation at or before the week's SPY anchor ts. Absent inputs leave
// the matching keys omitted rather than zeroed.
func BuildMarketCtx(spyDaily []md.Bar, vix []Point) MarketCtx {
	ctx := MarketCtx{weeks: make(map[int64]marketWeek)}
	anchorClose := make(map[int64]float64)
	for i, b := range spyDaily {
		w := b.Ts / WeekSecs
		if i+1 < len(spyDaily) && spyDaily[i+1].Ts/WeekSecs == w {
			continue // not the last bar of its week bucket
		}
		mw := marketWeek{}
		if sma200, ok := signals.SMA(spyDaily[:i+1], 200); ok {
			sma50, _ := signals.SMA(spyDaily[:i+1], 50) // exists whenever SMA200 does
			switch {
			case b.Close > sma200 && sma50 > sma200:
				mw.trend = 1
			case b.Close < sma200 && sma50 < sma200:
				mw.trend = -1
			}
			mw.trendOK = true
		}
		anchorClose[w] = b.Close
		if prev, ok := anchorClose[w-13]; ok && prev > 0 {
			mw.ret13w = b.Close/prev - 1
			mw.ret13wOK = true
		}
		if v, ok := latestAtOrBefore(vix, b.Ts); ok {
			mw.vix = macrofeat.FromVIX(v)
			mw.vixOK = true
		}
		ctx.weeks[w] = mw
	}
	return ctx
}

// latestAtOrBefore returns the newest Value with Ts <= ts.
func latestAtOrBefore(pts []Point, ts int64) (float64, bool) {
	i := sort.Search(len(pts), func(i int) bool { return pts[i].Ts > ts })
	if i == 0 {
		return 0, false
	}
	return pts[i-1].Value, true
}

// WeeklyRows computes the point-in-time weekly research rows for one symbol.
// daily must be ascending TF1d bars; anchors before rowsFrom are skipped. An
// anchor is the LAST bar of each calendar-week bucket (ts/WeekSecs) with at
// least 60 trailing bars. Features come from bars[..anchor] capped at the
// trailing 500. The label replicates the live resolver geometry exactly:
// target = anchor.Ts+WeekSecs, forward bar = first daily bar at or after
// target, and the anchor is VOID (no row) when no such bar exists within 21
// days of the target or the anchor close is non-positive; Up means a
// strictly positive forward return.
//
// Vec keys (each omitted when uncomputable): pressure_score, pressure_abs
// and comp_<name> from the 1w Pressure Score; the extension block rsi14,
// rsi_pct, vwap_dist_atr, vwap_dist_pct, atr_ext_20, ma_dist_20, ma_dist_50,
// ma_dist_200, vol_anomaly, vol_pct, price_accel, consec_dir, ext_score; and
// the market block vix_level, vix_regime, vix_high_vol, mkt_trend,
// mkt_ret_13w joined from mkt at the anchor's week.
func WeeklyRows(daily []md.Bar, mkt MarketCtx, rowsFrom int64) []WeekRow {
	if len(daily) == 0 {
		return nil
	}
	rsiVals, rsiValid := rsiSeries(daily, 14)
	volVals, volValid := realizedVolSeries(daily, 20)
	rows := make([]WeekRow, 0, len(daily)/5)
	buf := make([]float64, 0, pctLookback)
	for i := range daily {
		w := daily[i].Ts / WeekSecs
		if i+1 < len(daily) && daily[i+1].Ts/WeekSecs == w {
			continue // not the last bar of its week bucket
		}
		anchor := daily[i]
		if anchor.Ts < rowsFrom || i+1 < minTrailingBars {
			continue
		}
		// Label first: void anchors never pay for feature computation.
		target := anchor.Ts + WeekSecs
		rest := daily[i+1:]
		j := sort.Search(len(rest), func(k int) bool { return rest[k].Ts >= target })
		if j == len(rest) || rest[j].Ts-target > voidAfter || anchor.Close <= 0 {
			continue
		}
		fwd := rest[j].Close/anchor.Close - 1

		win := daily[:i+1]
		if len(win) > featureWindow {
			win = win[len(win)-featureWindow:]
		}
		vec := make(map[string]float64, 26)

		if s, ok := signals.ComputeScores(win, nil, signals.MicroInputs{})[md.H1w]; ok {
			vec["pressure_score"] = s.Score
			vec["pressure_abs"] = math.Abs(s.Score)
			for _, c := range s.Components {
				vec["comp_"+c.Name] = c.Contrib
			}
		}

		c := anchor.Close
		rsi, rsiOK := signals.RSI(win, 14)
		if rsiOK {
			vec["rsi14"] = rsi
		}
		vwap, vwapOK := signals.VWAP(win, 20)
		atr, atrOK := signals.ATR(win, 14)
		atrOK = atrOK && atr > 0
		if vwapOK && atrOK {
			vec["vwap_dist_atr"] = (c - vwap) / atr
		}
		if vwapOK && vwap != 0 {
			vec["vwap_dist_pct"] = (c - vwap) / vwap
		}
		sma20, sma20OK := signals.SMA(win, 20)
		if sma20OK && atrOK {
			vec["atr_ext_20"] = (c - sma20) / atr
		}
		if sma20OK && sma20 != 0 {
			vec["ma_dist_20"] = c/sma20 - 1
		}
		if sma50, ok := signals.SMA(win, 50); ok && sma50 != 0 {
			vec["ma_dist_50"] = c/sma50 - 1
		}
		if sma200, ok := signals.SMA(win, 200); ok && sma200 != 0 {
			vec["ma_dist_200"] = c/sma200 - 1
		}
		if va, ok := volAnomaly(daily, i); ok {
			vec["vol_anomaly"] = va
		}
		if rsiValid[i] {
			buf = trailingValid(rsiVals, rsiValid, i, buf[:0])
			if len(buf) >= pctMinValues {
				vec["rsi_pct"] = pctRank(buf)
			}
		}
		if volValid[i] {
			buf = trailingValid(volVals, volValid, i, buf[:0])
			if len(buf) >= pctMinValues {
				vec["vol_pct"] = pctRank(buf)
			}
		}
		if r1, ok := signals.ROC(win, 5); ok {
			if r0, ok0 := signals.ROC(win[:len(win)-5], 5); ok0 {
				vec["price_accel"] = r1 - r0
			}
		}
		vec["consec_dir"] = consecDir(daily, i)

		// ext_score: mean of the AVAILABLE extension members.
		extSum, extN := 0.0, 0
		if rsiOK {
			extSum += math.Abs(rsi-50) / 50
			extN++
		}
		if v, ok := vec["vwap_dist_atr"]; ok {
			extSum += clamp01(math.Abs(v) / 3)
			extN++
		}
		if v, ok := vec["atr_ext_20"]; ok {
			extSum += clamp01(math.Abs(v) / 3)
			extN++
		}
		if v, ok := vec["vol_pct"]; ok {
			extSum += v
			extN++
		}
		if extN > 0 {
			vec["ext_score"] = extSum / float64(extN)
		}

		highVol := false
		if mw, ok := mkt.weeks[w]; ok {
			if mw.vixOK {
				vec["vix_level"] = mw.vix.Level
				vec["vix_regime"] = mw.vix.Regime
				vec["vix_high_vol"] = mw.vix.HighVol
				highVol = mw.vix.HighVol == 1
			}
			if mw.trendOK {
				vec["mkt_trend"] = mw.trend
			}
			if mw.ret13wOK {
				vec["mkt_ret_13w"] = mw.ret13w
			}
		}

		rows = append(rows, WeekRow{
			Ts:        anchor.Ts,
			Week:      w,
			Vec:       vec,
			FwdReturn: fwd,
			Up:        fwd > 0,
			Era:       EraOf(anchor.Ts),
			HighVol:   highVol,
		})
	}
	return rows
}

// rsiSeries computes the Wilder RSI(period) at every bar index in O(n),
// matching signals.RSI run on every prefix daily[:i+1]: seed with the mean
// of the first period changes, then avg=(avg*(p-1)+x)/p; a dead-flat series
// reads 50. valid[i] is false during the warmup (i < period).
func rsiSeries(daily []md.Bar, period int) (vals []float64, valid []bool) {
	n := len(daily)
	vals = make([]float64, n)
	valid = make([]bool, n)
	if n < period+1 {
		return vals, valid
	}
	var avgGain, avgLoss float64
	for i := 1; i <= period; i++ {
		if d := daily[i].Close - daily[i-1].Close; d > 0 {
			avgGain += d
		} else {
			avgLoss -= d
		}
	}
	avgGain /= float64(period)
	avgLoss /= float64(period)
	set := func(i int) {
		switch {
		case avgLoss == 0 && avgGain == 0:
			vals[i] = 50
		case avgLoss == 0:
			vals[i] = 100
		default:
			vals[i] = 100 - 100/(1+avgGain/avgLoss)
		}
		valid[i] = true
	}
	set(period)
	for i := period + 1; i < n; i++ {
		var g, l float64
		if d := daily[i].Close - daily[i-1].Close; d > 0 {
			g = d
		} else {
			l = -d
		}
		avgGain = (avgGain*float64(period-1) + g) / float64(period)
		avgLoss = (avgLoss*float64(period-1) + l) / float64(period)
		set(i)
	}
	return vals, valid
}

// realizedVolSeries computes the 20d-style realized vol (annualized sqrt-365
// sample stdev of the last n log returns, as signals.RealizedVol) at every
// bar index in O(len) via prefix sums. valid[i] is false during the warmup
// (i < n) and when any close inside the return window is non-positive.
func realizedVolSeries(daily []md.Bar, n int) (vals []float64, valid []bool) {
	m := len(daily)
	vals = make([]float64, m)
	valid = make([]bool, m)
	if n < 2 || m < n+1 {
		return vals, valid
	}
	sum := make([]float64, m)   // prefix sum of log returns r[1..i]
	sumSq := make([]float64, m) // prefix sum of r^2
	bad := make([]int, m)       // prefix count of uncomputable returns
	for i := 1; i < m; i++ {
		var r float64
		b := 0
		if daily[i-1].Close <= 0 || daily[i].Close <= 0 {
			b = 1
		} else {
			r = math.Log(daily[i].Close / daily[i-1].Close)
		}
		sum[i] = sum[i-1] + r
		sumSq[i] = sumSq[i-1] + r*r
		bad[i] = bad[i-1] + b
	}
	nf := float64(n)
	ann := math.Sqrt(365)
	for i := n; i < m; i++ {
		if bad[i]-bad[i-n] > 0 {
			continue
		}
		s := sum[i] - sum[i-n]
		q := sumSq[i] - sumSq[i-n]
		v := (q - s*s/nf) / (nf - 1)
		if v < 0 {
			v = 0
		}
		vals[i] = math.Sqrt(v) * ann
		valid[i] = true
	}
	return vals, valid
}

// trailingValid collects the valid values at the last pctLookback bar
// indices ending at i (inclusive, oldest first) into buf; the current value
// lands last.
func trailingValid(vals []float64, valid []bool, i int, buf []float64) []float64 {
	lo := i - pctLookback + 1
	if lo < 0 {
		lo = 0
	}
	for j := lo; j <= i; j++ {
		if valid[j] {
			buf = append(buf, vals[j])
		}
	}
	return buf
}

// pctRank is the midrank percentile (0..1) of the LAST value of vals within
// vals: ties share rank, so an all-equal window reads 0.5. Needs len >= 2.
func pctRank(vals []float64) float64 {
	cur := vals[len(vals)-1]
	less, equal := 0, 0
	for _, v := range vals {
		switch {
		case v < cur:
			less++
		case v == cur:
			equal++
		}
	}
	return (float64(less) + float64(equal-1)/2) / float64(len(vals)-1)
}

// volAnomaly is the anchor volume's z-score against the trailing 60 volumes
// (sample stdev, window includes the anchor). ok=false with fewer than 60
// bars or zero variance.
func volAnomaly(daily []md.Bar, i int) (float64, bool) {
	const n = 60
	if i+1 < n {
		return 0, false
	}
	w := daily[i+1-n : i+1]
	mean := 0.0
	for _, b := range w {
		mean += b.Volume
	}
	mean /= n
	ss := 0.0
	for _, b := range w {
		d := b.Volume - mean
		ss += d * d
	}
	std := math.Sqrt(ss / (n - 1))
	if std == 0 {
		return 0, false
	}
	return (daily[i].Volume - mean) / std, true
}

// consecDir is the signed count of consecutive same-direction daily closes
// ending at index i, capped at ±10 and scaled to ±1. A flat last change is 0.
func consecDir(daily []md.Bar, i int) float64 {
	if i < 1 {
		return 0
	}
	d := sign(daily[i].Close - daily[i-1].Close)
	if d == 0 {
		return 0
	}
	n := 1
	for j := i - 1; j >= 1 && n < 10; j-- {
		if sign(daily[j].Close-daily[j-1].Close) != d {
			break
		}
		n++
	}
	return d * float64(n) / 10
}

// sign is -1/0/+1.
func sign(v float64) float64 {
	switch {
	case v > 0:
		return 1
	case v < 0:
		return -1
	}
	return 0
}

// clamp01 clamps v into [0, 1].
func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
