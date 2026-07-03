// Package regime labels a symbol's market regime and — more importantly —
// detects regime CHANGES, because the transition is the signal. Given a bar
// series it answers "what state is this market in right now (trend up, trend
// down, ranging, or a low-volatility squeeze) and how convinced am I?", and it
// can replay that classification over history to produce a regime timeline plus
// the exact points where the label flipped.
//
// Every function is pure: bars in, labels out. No I/O, no persistence, no
// clock, no network. It depends only on the stdlib and the marketdata contract
// types.
//
// HONESTY NOTES (this is the brand):
//
//   - NO LOOKAHEAD. A regime computed at bar index i uses ONLY bars[..i]
//     (indices 0..i inclusive). SMA20/50, the ADX-like trend strength, and the
//     Bollinger band-width percentile at i are all built from the window ending
//     at i. History replays Classify at each step so every point on the timeline
//     is a decision that could have been made in real time at that bar — no
//     future information leaks in. This is what makes the "regime just changed"
//     alerts honest: the change point is dated at the first bar where the new
//     label held, using only data available up to that bar.
//
//   - IT IS A LABEL, NOT A FORECAST. A regime state describes the market's
//     recent behavior; it does not predict the next bar. "Uptrend" means the
//     trailing window trended up with conviction, not that price will keep
//     rising. Regimes end, often abruptly — that is precisely why the change
//     detector exists. To GRADE regime calls out of sample, replay History over
//     a holdout window and measure, per label, the realized forward return that
//     followed each state (the marketdata.Expectancy machinery is built for
//     exactly this: state key = the regime label). This package deliberately
//     exposes History so that grading is a pure replay, not a re-derivation.
//
//   - ASSUMPTIONS / CAVEATS. Bars are assumed chronologically ordered, gap-free,
//     and same-timeframe; the caller aligns them. The ADX here is an
//     ADX-*like* directional-strength proxy (Wilder-smoothed +DM/-DM/TR over a
//     14-bar window), close enough to rank trendiness but not tick-for-tick
//     identical to a charting package. Band-width percentile is measured over
//     the last 90 bars of band width, so "squeeze" is relative to this symbol's
//     own recent volatility, not an absolute vol level. Thresholds (ADX 25,
//     squeeze < 20th pct, SMA-separation scaling) are fixed constants documented
//     at their definitions; they are reasonable defaults, not tuned per symbol.
package regime

import (
	"fmt"

	"github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// Label is a market-regime classification.
type Label string

const (
	// Uptrend is a directional up-move with trend conviction (price above
	// SMA50, SMAs stacked bullishly, ADX-like strength elevated).
	Uptrend Label = "uptrend"
	// Downtrend is a directional down-move with trend conviction (price below
	// SMA50, SMAs stacked bearishly, ADX-like strength elevated).
	Downtrend Label = "downtrend"
	// Range is a non-trending, mean-reverting state: no stacked SMAs and/or
	// weak directional strength, with volatility not compressed enough to be a
	// squeeze.
	Range Label = "range"
	// Squeeze is a low-volatility coil: Bollinger band width sits in the bottom
	// SqueezePctThreshold percentile of its recent range. It supersedes the
	// directional labels because a coil resolves in either direction and the
	// trade is the breakout, not the (currently absent) trend.
	Squeeze Label = "squeeze"
)

// Tunable constants for classification. They are fixed, documented defaults —
// reasonable across liquid instruments, not per-symbol tuned.
const (
	// MinBars is the minimum number of bars Classify needs. It must cover the
	// slow SMA (50) plus enough band-width history to form a stable percentile;
	// ~60 is the practical floor.
	MinBars = 60

	// smaFast / smaSlow are the two simple-moving-average lookbacks whose
	// stacking defines trend direction.
	smaFast = 20
	smaSlow = 50

	// bbLen is the Bollinger lookback (period of the middle SMA and stdev).
	bbLen = 20
	// bbK is the standard-deviation multiplier for the Bollinger bands.
	bbK = 2.0
	// bbWidthWindow is how many trailing band-width samples define the
	// percentile universe. Squeeze is relative to this symbol's own last ~90.
	bbWidthWindow = 90

	// adxLen is the Wilder smoothing window for the ADX-like strength proxy.
	adxLen = 14

	// SqueezePctThreshold: band width below this percentile (0..100) => Squeeze.
	SqueezePctThreshold = 20.0
	// adxTrendThreshold is the ADX-like level at/above which the market is
	// considered to have real directional strength (Wilder's classic 25).
	adxTrendThreshold = 25.0
)

// State is the regime read at one point in time.
type State struct {
	// Label is the classified regime.
	Label Label `json:"label"`
	// Strength is 0..1 conviction in the label (blend of ADX-like strength and
	// SMA separation for trends; band-width tightness for squeeze).
	Strength float64 `json:"strength"`
	// ADX is the ADX-like directional-strength proxy at this point (0..100-ish).
	ADX float64 `json:"adx"`
	// BBWidthPct is where current Bollinger band width sits in its recent
	// distribution, as a percentile 0..100 (low = compressed / coiling).
	BBWidthPct float64 `json:"bbWidthPct"`
	// Note is a human explanation carrying the numbers behind the label.
	Note string `json:"note"`
}

// Timestamped is one point on the regime timeline: the label (and conviction)
// as of bar Ts, using only bars up to and including Ts.
type Timestamped struct {
	// Ts is the bar open time (unix seconds UTC) this classification is as-of.
	Ts int64 `json:"ts"`
	// Label is the regime as of Ts.
	Label Label `json:"label"`
	// Strength is 0..1 conviction as of Ts.
	Strength float64 `json:"strength"`
}

// Change is a detected regime transition: at bar Ts the label went From -> To.
// Ts is the timestamp of the first bar carrying the new label, so alerts are
// dated at the moment the change became observable (no lookahead).
type Change struct {
	// Ts is the bar open time (unix seconds UTC) where To first held.
	Ts int64 `json:"ts"`
	// From is the prior regime label.
	From Label `json:"from"`
	// To is the new regime label.
	To Label `json:"to"`
}

// Classify labels the regime as of the LAST bar in bars, using only bars[..last]
// (no lookahead). It returns ok=false when there are fewer than MinBars bars.
//
// Rules (in priority order):
//
//  1. Compute SMA20, SMA50, an ADX-like directional-strength proxy, and the
//     Bollinger band-width percentile over the last bbWidthWindow band-width
//     samples.
//  2. SQUEEZE FIRST: if BBWidthPct < SqueezePctThreshold the market is coiling
//     at low volatility -> Squeeze, regardless of direction. A coil has no trend
//     to trade; the breakout is the signal.
//  3. Otherwise, if there is real directional strength (ADX >= adxTrendThreshold
//     OR the SMAs are cleanly stacked) AND price is above SMA50 with a bullish
//     stack -> Uptrend; below SMA50 with a bearish stack -> Downtrend.
//  4. Otherwise -> Range.
//
// Strength is 0..1: for trends it blends normalized ADX with SMA separation; for
// squeezes it rises as the coil tightens (lower percentile); for ranges it is
// low by construction.
func Classify(bars []marketdata.Bar) (State, bool) {
	if len(bars) < MinBars {
		return State{}, false
	}

	closes := closesOf(bars)
	last := len(closes) - 1
	price := closes[last]

	sma20 := smaAt(closes, last, smaFast)
	sma50 := smaAt(closes, last, smaSlow)
	adx := adxLike(bars, adxLen)
	bbPct := bbWidthPercentile(closes, bbLen, bbK, bbWidthWindow)

	// Stacking: bullish when price > sma20 > sma50, bearish when price < sma20 < sma50.
	bullStack := price > sma20 && sma20 > sma50
	bearStack := price < sma20 && sma20 < sma50

	// SMA separation as a fraction of price (magnitude of the stack).
	sep := 0.0
	if price > 0 {
		sep = absf(sma20-sma50) / price
	}

	// 2) Squeeze supersedes direction — but a squeeze is low volatility AND
	//    non-trending. A coil resolves into a breakout; it is not a market that
	//    is already trending hard. So we require BOTH compressed band width
	//    (bottom SqueezePctThreshold percentile) AND the absence of real
	//    directional strength (ADX below the trend threshold). This gate also
	//    rejects the classic false positive: a clean linear trend whose
	//    Bollinger width mechanically shrinks as price rises (constant stdev over
	//    a rising mid) would otherwise masquerade as a coil — but its ADX is
	//    high, so it stays a trend.
	if bbPct < SqueezePctThreshold && adx < adxTrendThreshold {
		// Tighter coil (lower percentile) => higher conviction it is a squeeze.
		strength := clamp01((SqueezePctThreshold - bbPct) / SqueezePctThreshold)
		note := fmt.Sprintf(
			"squeeze: band width in bottom %.0f%% of last %d bars (pct=%.1f < %.0f) with no trend (adx=%.1f < %.0f); low-vol coil, breakout pending. price=%.4f vs sma50=%.4f",
			SqueezePctThreshold, bbWidthWindow, bbPct, SqueezePctThreshold, adx, adxTrendThreshold, price, sma50)
		return State{Label: Squeeze, Strength: strength, ADX: adx, BBWidthPct: bbPct, Note: note}, true
	}

	strongTrend := adx >= adxTrendThreshold

	// 3) Directional trend needs BOTH a clean stack AND strength (ADX high, or a
	// wide SMA separation standing in for strength when ADX is borderline).
	if bullStack && (strongTrend || sep >= sepStrong) {
		strength := trendStrength(adx, sep)
		note := fmt.Sprintf(
			"uptrend: price %.4f > sma20 %.4f > sma50 %.4f (sep=%.2f%%), adx=%.1f (>=%.0f=%v). directional up-move with conviction",
			price, sma20, sma50, sep*100, adx, adxTrendThreshold, strongTrend)
		return State{Label: Uptrend, Strength: strength, ADX: adx, BBWidthPct: bbPct, Note: note}, true
	}
	if bearStack && (strongTrend || sep >= sepStrong) {
		strength := trendStrength(adx, sep)
		note := fmt.Sprintf(
			"downtrend: price %.4f < sma20 %.4f < sma50 %.4f (sep=%.2f%%), adx=%.1f (>=%.0f=%v). directional down-move with conviction",
			price, sma20, sma50, sep*100, adx, adxTrendThreshold, strongTrend)
		return State{Label: Downtrend, Strength: strength, ADX: adx, BBWidthPct: bbPct, Note: note}, true
	}

	// 4) Everything else is a range: no clean stack, or strength too weak.
	strength := clamp01(adx / adxTrendThreshold * 0.5) // low by construction, caps at 0.5
	note := fmt.Sprintf(
		"range: no clean trend (bullStack=%v bearStack=%v, adx=%.1f<%.0f or weak stack, sep=%.2f%%). mean-reverting, vol not compressed (bbPct=%.1f)",
		bullStack, bearStack, adx, adxTrendThreshold, sep*100, bbPct)
	return State{Label: Range, Strength: strength, ADX: adx, BBWidthPct: bbPct, Note: note}, true
}

// sepStrong is the SMA-separation fraction (of price) that counts as "strong"
// on its own when ADX is borderline — ~1.5% of price between the fast and slow
// SMA is a decisive stack even if the ADX proxy hasn't caught up.
const sepStrong = 0.015

// trendStrength blends normalized ADX (capped near 50 -> ~1.0) with SMA
// separation into a 0..1 conviction for trend labels.
func trendStrength(adx, sep float64) float64 {
	adxPart := clamp01(adx / 50.0)            // 25->0.5, 50->1.0
	sepPart := clamp01(sep / (sepStrong * 3)) // ~4.5% sep -> 1.0
	// Weight ADX more; it is the primary trend-strength signal.
	return clamp01(0.7*adxPart + 0.3*sepPart)
}

// History replays Classify across bars, stepping by step bars, and returns the
// regime timeline plus the list of Changes (consecutive different labels). This
// powers the regime-history view and the "regime just changed" alerts.
//
// NO LOOKAHEAD: at each evaluated index i the classification sees only bars[..i]
// (bars[:i+1]). step controls timeline granularity — step=1 evaluates every bar,
// step=5 every fifth. Changes are detected on the (possibly downsampled)
// timeline, so a Change's Ts is the first EVALUATED bar carrying the new label;
// for exact change points use step=1. A step<1 is treated as 1.
//
// If there are fewer than MinBars bars, both return values are empty (no panic).
func History(bars []marketdata.Bar, step int) ([]Timestamped, []Change) {
	if step < 1 {
		step = 1
	}
	if len(bars) < MinBars {
		return nil, nil
	}

	timeline := make([]Timestamped, 0, (len(bars)-MinBars)/step+1)
	var changes []Change

	havePrev := false
	var prev Label

	for i := MinBars - 1; i < len(bars); i += step {
		st, ok := Classify(bars[:i+1])
		if !ok {
			continue
		}
		ts := bars[i].Ts
		timeline = append(timeline, Timestamped{Ts: ts, Label: st.Label, Strength: st.Strength})

		if havePrev && st.Label != prev {
			changes = append(changes, Change{Ts: ts, From: prev, To: st.Label})
		}
		prev = st.Label
		havePrev = true
	}

	return timeline, changes
}
