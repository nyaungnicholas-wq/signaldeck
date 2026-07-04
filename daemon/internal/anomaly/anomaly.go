// Package anomaly is the Signal8-wave Stage-3 anomaly layer: trade-imbalance
// and unusual-volatility/volume detection as DESCRIPTIVE statistics.
//
// HONESTY DOCTRINE (this package's contract):
//   - Every detection is a z-score of recent activity measured against the
//     SAME symbol's own trailing baseline. It describes "this is statistically
//     unusual vs this symbol's recent past"; it PREDICTS nothing.
//   - Every Event.Detail states the recent window AND the baseline it was
//     measured against, so the number can never be quoted without its basis.
//   - Crypto imbalance is real order-book imbalance (imb_signed from
//     snapshots_1s). Stock "imbalance" has NO order book on free data, so it
//     is an up-volume vs down-volume PROXY on 1m bars and its Detail says so
//     verbatim: "volume-side proxy (no order-book on free stock data)".
//   - Insufficient data yields NO signal (ok=false) — never a made-up z.
//
// The pure detectors in this file take data in and return (Event, bool);
// storage, scheduling, and market-calendar gating live in worker.go.
package anomaly

import (
	"fmt"
	"math"
	"strconv"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// Anomaly kinds (mirror the CHECK constraint on the anomalies table and the
// alert kinds appended in internal/alerts).
const (
	KindImbalance = "anomaly_imbalance"
	KindVol       = "anomaly_vol"
	KindVolume    = "anomaly_volume"
)

// DefaultZ is the default |z| threshold; override with SIGNALDECK_ANOM_Z.
const DefaultZ = 2.5

// proxyLabel is the verbatim honesty label stamped on every stock-imbalance
// detail (stocks have no order book on free data — only volume sides).
const proxyLabel = "volume-side proxy (no order-book on free stock data)"

// Threshold parses a SIGNALDECK_ANOM_Z-style string; empty, unparsable, or
// non-positive values fall back to DefaultZ (the scanner must never run with
// a nonsensical threshold).
func Threshold(s string) float64 {
	v, err := strconv.ParseFloat(s, 64)
	if s == "" || err != nil || v <= 0 || math.IsNaN(v) || math.IsInf(v, 0) {
		return DefaultZ
	}
	return v
}

// Event is one detected anomaly. Z is the signed z-score (or, for the
// true-range spike form of KindVol, the stated TR/ATR ratio — Detail always
// says which). Ts is the detection instant (the last bar/snap timestamp).
type Event struct {
	Kind   string
	Ts     int64
	Z      float64
	Detail string
}

// ── pure statistics ──────────────────────────────────────────────────────

func mean(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	var s float64
	for _, v := range vals {
		s += v
	}
	return s / float64(len(vals))
}

// stdev is the sample standard deviation (n-1); 0 when n < 2.
func stdev(vals []float64) float64 {
	n := len(vals)
	if n < 2 {
		return 0
	}
	m := mean(vals)
	var ss float64
	for _, v := range vals {
		d := v - m
		ss += d * d
	}
	return math.Sqrt(ss / float64(n-1))
}

// ZScore standardizes value against baseline. ok=false when the baseline has
// fewer than minN samples or (near-)zero dispersion — insufficient data is
// NO SIGNAL, never a fabricated z.
func ZScore(value float64, baseline []float64, minN int) (float64, bool) {
	if minN < 2 {
		minN = 2
	}
	if len(baseline) < minN {
		return 0, false
	}
	sd := stdev(baseline)
	if sd < 1e-12 {
		return 0, false
	}
	return (value - mean(baseline)) / sd, true
}

// SignedVolumeShare is the up-volume vs down-volume balance of a bar window:
// (upVol - downVol) / (upVol + downVol) in [-1, +1], where a bar's volume
// counts "up" when it closed above its open and "down" when below (doji bars
// are excluded from both sides). ok=false with no directional volume.
func SignedVolumeShare(bars []md.Bar) (float64, bool) {
	var up, down float64
	for _, b := range bars {
		switch {
		case b.Close > b.Open:
			up += b.Volume
		case b.Close < b.Open:
			down += b.Volume
		}
	}
	total := up + down
	if total <= 0 {
		return 0, false
	}
	return (up - down) / total, true
}

// LogReturnVol is the sample stdev of close-to-close log returns over the
// window — the realized-volatility measure. ok=false below 3 bars or on
// non-positive closes.
func LogReturnVol(bars []md.Bar) (float64, bool) {
	if len(bars) < 3 {
		return 0, false
	}
	rets := make([]float64, 0, len(bars)-1)
	for i := 1; i < len(bars); i++ {
		if bars[i-1].Close <= 0 || bars[i].Close <= 0 {
			return 0, false
		}
		rets = append(rets, math.Log(bars[i].Close/bars[i-1].Close))
	}
	return stdev(rets), true
}

// trueRange is the Wilder true range of bar b given the previous close.
func trueRange(prevClose float64, b md.Bar) float64 {
	tr := b.High - b.Low
	if v := math.Abs(b.High - prevClose); v > tr {
		tr = v
	}
	if v := math.Abs(b.Low - prevClose); v > tr {
		tr = v
	}
	return tr
}

// windowStats splits bars (oldest→newest) into consecutive windows of w bars
// counted back from the END, applies f to each, and returns f(newest window)
// plus the older windows' values as the baseline. ok=false when the recent
// window fails f or fewer than minBaseline baseline windows produce a value.
func windowStats(bars []md.Bar, w, minBaseline int, f func([]md.Bar) (float64, bool)) (recent float64, baseline []float64, ok bool) {
	if w <= 0 || len(bars) < w*(minBaseline+1) {
		return 0, nil, false
	}
	k := len(bars) / w
	aligned := bars[len(bars)-k*w:]
	recent, rok := f(aligned[(k-1)*w:])
	if !rok {
		return 0, nil, false
	}
	baseline = make([]float64, 0, k-1)
	for i := 0; i < k-1; i++ {
		if v, vok := f(aligned[i*w : (i+1)*w]); vok {
			baseline = append(baseline, v)
		}
	}
	if len(baseline) < minBaseline {
		return 0, nil, false
	}
	return recent, baseline, true
}

func pressureWord(z float64) string {
	if z >= 0 {
		return "buy"
	}
	return "sell"
}

// ── detectors ────────────────────────────────────────────────────────────

// DetectImbalanceSnaps detects unusual order-book imbalance from crypto 1Hz
// snapshots (REAL book data, not a proxy): mean signed imbalance over the
// last recentSec seconds z-scored against the MEANS of the trailing
// recentSec-sized windows inside the preceding baseSec seconds (12 five-
// minute window means over a 60m baseline at the default scan windows).
// snaps must be ascending by Ts.
//
// The baseline MUST be same-sized window means, not individual snapshots —
// a window MEAN has ~sqrt(n) less dispersion than single 1Hz readings, so
// z-scoring a mean against per-snapshot dispersion understates z by ~sqrt(n)
// and mutes real detections. This mirrors DetectImbalanceBars' windowStats
// approach: like is always compared with like.
func DetectImbalanceSnaps(snaps []md.Snap1s, recentSec, baseSec int64, thr float64) (Event, bool) {
	const (
		minRecent      = 30 // ≥30 snapshots inside a window for its mean to count
		minBaselineWin = 6  // ≥6 trailing window means (30m of baseline at defaults)
	)
	if len(snaps) == 0 || recentSec <= 0 || baseSec < recentSec {
		return Event{}, false
	}
	lastTs := snaps[len(snaps)-1].Ts
	cut := lastTs - recentSec
	nWin := int(baseSec / recentSec) // trailing same-sized windows in the baseline

	var recentVals []float64
	sums := make([]float64, nWin)
	counts := make([]int, nWin)
	for _, sn := range snaps {
		switch {
		case sn.Ts > cut:
			recentVals = append(recentVals, sn.ImbSigned)
		case sn.Ts > cut-baseSec:
			// Window k counts back from the recent cut: k=0 is the window
			// immediately preceding it. sn.Ts in (cut-baseSec, cut] ⇒ k ∈ [0,nWin).
			if k := int((cut - sn.Ts) / recentSec); k >= 0 && k < nWin {
				sums[k] += sn.ImbSigned
				counts[k]++
			}
		}
	}
	if len(recentVals) < minRecent {
		return Event{}, false
	}
	// Baseline = the trailing windows' means; a window with too few snapshots
	// (feed gap) contributes NO value — never a mean built on scraps.
	baseline := make([]float64, 0, nWin)
	for k := 0; k < nWin; k++ {
		if counts[k] >= minRecent {
			baseline = append(baseline, sums[k]/float64(counts[k]))
		}
	}
	rm := mean(recentVals)
	z, ok := ZScore(rm, baseline, minBaselineWin)
	if !ok || math.Abs(z) < thr {
		return Event{}, false
	}
	return Event{
		Kind: KindImbalance, Ts: lastTs, Z: z,
		Detail: fmt.Sprintf(
			"unusual %s pressure: mean order-book imbalance %+.2f over last %dm (z=%+.1f vs the means of %d trailing %dm windows across a %dm baseline) — descriptive statistic, not a prediction",
			pressureWord(z), rm, recentSec/60, z, len(baseline), recentSec/60, baseSec/60),
	}, true
}

// DetectImbalanceBars detects unusual one-sided volume on 1m STOCK bars —
// the free-data stand-in for order-book imbalance. The recent window's
// up-vs-down volume share is z-scored against the same statistic over the
// trailing windows of the same symbol. The Detail carries the verbatim
// proxy label; this is NOT book imbalance and is never presented as such.
func DetectImbalanceBars(bars []md.Bar, w, minBaseline int, thr float64) (Event, bool) {
	recent, baseline, ok := windowStats(bars, w, minBaseline, SignedVolumeShare)
	if !ok {
		return Event{}, false
	}
	z, ok := ZScore(recent, baseline, minBaseline)
	if !ok || math.Abs(z) < thr {
		return Event{}, false
	}
	return Event{
		Kind: KindImbalance, Ts: bars[len(bars)-1].Ts, Z: z,
		Detail: fmt.Sprintf(
			"unusual %s-side volume: up/down-volume share %+.2f over last %d×1m bars (z=%+.1f vs trailing baseline of %d windows) — %s; descriptive, not a prediction",
			pressureWord(z), recent, w, z, len(baseline), proxyLabel),
	}, true
}

// trSpikeRatio: a last-bar true range at/above this multiple of the trailing
// ATR(14) counts as a volatility spike even when the realized-vol z is quiet.
const trSpikeRatio = 3.0

// atrLookback is the Wilder-style ATR window used for the TR-spike check.
const atrLookback = 14

// DetectVolatility detects unusual realized volatility on bars of one
// timeframe: the recent window's log-return vol z-scored against trailing
// same-size windows, OR (fallback) a single-bar true-range spike at/above
// trSpikeRatio× the trailing ATR(14). tfLabel names the bar size in the
// detail ("1m" / "1d"). Fires on the HIGH side only — unusually low vol is a
// squeeze, which the breakout detector already covers.
func DetectVolatility(bars []md.Bar, w, minBaseline int, thr float64, tfLabel string) (Event, bool) {
	lastTs := int64(0)
	if len(bars) > 0 {
		lastTs = bars[len(bars)-1].Ts
	}
	if recent, baseline, ok := windowStats(bars, w, minBaseline, LogReturnVol); ok {
		if z, zok := ZScore(recent, baseline, minBaseline); zok && z >= thr {
			return Event{
				Kind: KindVol, Ts: lastTs, Z: z,
				Detail: fmt.Sprintf(
					"unusual volatility: realized vol of last %d×%s bars %.4f (z=%+.1f vs trailing baseline of %d windows) — descriptive, not a prediction",
					w, tfLabel, recent, z, len(baseline)),
			}, true
		}
	}
	// True-range spike vs ATR: needs the last bar + atrLookback prior bars
	// (+1 more for the first TR's previous close).
	if len(bars) >= atrLookback+2 {
		trs := make([]float64, 0, atrLookback)
		start := len(bars) - 1 - atrLookback
		for i := start; i < len(bars)-1; i++ {
			trs = append(trs, trueRange(bars[i-1].Close, bars[i]))
		}
		atr := mean(trs)
		last := bars[len(bars)-1]
		lastTR := trueRange(bars[len(bars)-2].Close, last)
		if atr > 1e-12 && lastTR/atr >= trSpikeRatio {
			ratio := lastTR / atr
			return Event{
				Kind: KindVol, Ts: last.Ts, Z: ratio,
				Detail: fmt.Sprintf(
					"true-range spike: last %s bar range %.1f× the trailing ATR(%d) (value here is the TR/ATR ratio, not a z-score) — descriptive, not a prediction",
					tfLabel, ratio, atrLookback),
			}, true
		}
	}
	return Event{}, false
}

// DetectVolumeMinute detects unusual 1m volume against a SAME-TIME-OF-DAY
// baseline: the summed volume of the last w minute bars vs the summed volume
// of the same clock minutes on each prior day present in bars (read in loc,
// the exchange clock). Days missing half the window's minutes are skipped;
// fewer than minDays usable prior days ⇒ no signal. High side only.
func DetectVolumeMinute(bars []md.Bar, w, minDays int, thr float64, loc *time.Location) (Event, bool) {
	if w <= 0 || len(bars) <= w {
		return Event{}, false
	}
	recentBars := bars[len(bars)-w:]
	lastDay := time.Unix(recentBars[len(recentBars)-1].Ts, 0).In(loc).Format("2006-01-02")

	// The clock window we compare across days: minute-of-day of each recent bar.
	minuteSet := make(map[int]bool, w)
	var recentSum float64
	for _, b := range recentBars {
		t := time.Unix(b.Ts, 0).In(loc)
		minuteSet[t.Hour()*60+t.Minute()] = true
		recentSum += b.Volume
	}

	// Per prior day: sum volume over the same clock minutes.
	type acc struct {
		sum float64
		n   int
	}
	days := map[string]*acc{}
	for _, b := range bars[:len(bars)-w] {
		t := time.Unix(b.Ts, 0).In(loc)
		if !minuteSet[t.Hour()*60+t.Minute()] {
			continue
		}
		day := t.Format("2006-01-02")
		if day == lastDay {
			continue // same-day earlier overlap can't be a "prior day" sample
		}
		a := days[day]
		if a == nil {
			a = &acc{}
			days[day] = a
		}
		a.sum += b.Volume
		a.n++
	}
	baseline := make([]float64, 0, len(days))
	for _, a := range days {
		if a.n >= w/2 { // skip half-covered days (holiday half-days, gaps)
			baseline = append(baseline, a.sum)
		}
	}
	if minDays < 2 {
		minDays = 2
	}
	z, ok := ZScore(recentSum, baseline, minDays)
	if !ok || z < thr {
		return Event{}, false
	}
	return Event{
		Kind: KindVolume, Ts: recentBars[len(recentBars)-1].Ts, Z: z,
		Detail: fmt.Sprintf(
			"unusual volume: %.0f over last %dm vs same-time-of-day baseline across %d prior days (z=%+.1f) — descriptive, not a prediction",
			recentSum, w, len(baseline), z),
	}, true
}

// DetectVolumeDaily detects unusual daily volume: the last daily bar's volume
// z-scored against up to baselineN prior daily volumes (need ≥ minBaseline).
// High side only.
func DetectVolumeDaily(bars []md.Bar, baselineN, minBaseline int, thr float64) (Event, bool) {
	if len(bars) < 2 || baselineN <= 0 {
		return Event{}, false
	}
	last := bars[len(bars)-1]
	prior := bars[:len(bars)-1]
	if len(prior) > baselineN {
		prior = prior[len(prior)-baselineN:]
	}
	baseline := make([]float64, 0, len(prior))
	for _, b := range prior {
		baseline = append(baseline, b.Volume)
	}
	z, ok := ZScore(last.Volume, baseline, minBaseline)
	if !ok || z < thr {
		return Event{}, false
	}
	return Event{
		Kind: KindVolume, Ts: last.Ts, Z: z,
		Detail: fmt.Sprintf(
			"unusual volume: %.0f on the last 1d bar vs trailing %d-day baseline (z=%+.1f) — descriptive, not a prediction",
			last.Volume, len(baseline), z),
	}, true
}
