// Package trend reads a symbol's market STRUCTURE off recent price geometry:
// it classifies the visible window as an uptrend, downtrend, or range from the
// sequence of swing highs and lows (higher-highs + higher-lows = uptrend, etc.)
// confirmed by a linear-regression slope, and it fits auto-trendlines — a
// support line through the recent swing lows and a resistance line through the
// recent swing highs — reporting a channel when the two are roughly parallel.
//
// HONESTY NOTES (this is the brand):
//
//   - IT IS A DESCRIPTION, NOT A FORECAST. "Uptrend" means the recent window
//     printed higher pivots with an upward slope; it does not predict the next
//     bar. Trendlines are FITTED to past pivots (least squares), not levels the
//     market has promised to respect.
//
//   - NO LOOKAHEAD. A swing pivot is confirmed only when price has moved away
//     from it on BOTH sides (a symmetric swingWindow), so the most recent
//     swingWindow bars are never yet eligible as pivots — you cannot know a top
//     held until price left it. Every number is computable from the window
//     alone; nothing after the last bar is used.
//
// Every function is pure: bars in, structure out. No I/O, no persistence, no
// clock. Depends only on the stdlib and the marketdata contract types.
package trend

import (
	"math"

	"github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// Class is a geometric trend classification.
type Class string

const (
	// Uptrend: higher swing highs AND higher swing lows, with an upward slope.
	Uptrend Class = "uptrend"
	// Downtrend: lower swing highs AND lower swing lows, with a downward slope.
	Downtrend Class = "downtrend"
	// Range: no clean directional pivot structure / a flat slope.
	Range Class = "range"
)

// Tunable constants — fixed, documented defaults, not per-symbol tuned.
const (
	// MinBars is the minimum window Analyze needs for a meaningful read.
	MinBars = 20
	// swingWindow is the half-width of the symmetric pivot filter: a swing high
	// is the strict maximum of the 2*swingWindow+1 bars centred on it. Larger =
	// fewer, more significant pivots.
	swingWindow = 3
	// slopeMinPctPerBar is the regression slope (as a fraction of mean price,
	// per bar) below which the trend is treated as flat. ~0.05%/bar ≈ 3% over
	// 60 bars — a modest but real drift.
	slopeMinPctPerBar = 0.0005
	// touchTolFrac: a pivot within this fraction of price of the fitted line
	// counts as a "touch".
	touchTolFrac = 0.01
	// channelParallelTol: support and resistance slopes (each as a fraction of
	// mean price per bar) within this of each other read as parallel — a
	// channel rather than a wedge.
	channelParallelTol = 0.004
)

// Line is one fitted trendline over the window.
type Line struct {
	FromTs     int64   `json:"fromTs"`
	FromPrice  float64 `json:"fromPrice"`
	ToTs       int64   `json:"toTs"`
	ToPrice    float64 `json:"toPrice"`
	Kind       string  `json:"kind"` // "support" | "resistance"
	TouchCount int     `json:"touchCount"`
}

// Result is the full structural read of a window.
type Result struct {
	// Class is the geometric classification.
	Class Class `json:"classification"`
	// SlopePctPerBar is the least-squares slope of closes as a fraction of mean
	// price, per bar (e.g. 0.002 = +0.2%/bar).
	SlopePctPerBar float64 `json:"slopePctPerBar"`
	// Trendlines holds the fitted support/resistance lines that could be built
	// (each needs >= 2 pivots); may be empty when the window has too few pivots.
	Trendlines []Line `json:"trendlines"`
	// Channel is true when a support AND resistance line exist with roughly
	// parallel slopes.
	Channel bool `json:"channel"`
}

// Analyze classifies the window and fits its trendlines. ok=false when there
// are fewer than MinBars bars. Uses only the supplied bars (no lookahead).
func Analyze(bars []marketdata.Bar) (Result, bool) {
	if len(bars) < MinBars {
		return Result{}, false
	}

	slope := slopePctPerBar(bars)
	highs := swingHighs(bars)
	lows := swingLows(bars)

	res := Result{SlopePctPerBar: slope}

	// Trendlines: resistance through swing highs, support through swing lows.
	if l, ok := fitTrendline(bars, highs, true, "resistance"); ok {
		res.Trendlines = append(res.Trendlines, l)
	}
	if l, ok := fitTrendline(bars, lows, false, "support"); ok {
		res.Trendlines = append(res.Trendlines, l)
	}

	// Channel: both lines present and roughly parallel.
	res.Channel = channelParallel(bars, highs, lows)

	// Classification: structure (higher/lower pivots) confirmed by slope. When
	// there is too little pivot structure, slope alone decides — a strong drift
	// with no pivots is still a clear trend.
	res.Class = classify(bars, highs, lows, slope)
	return res, true
}

// classify combines pivot structure with the regression slope.
func classify(bars []marketdata.Bar, highs, lows []int, slope float64) Class {
	up := slope > slopeMinPctPerBar
	down := slope < -slopeMinPctPerBar

	haveStruct := len(highs) >= 2 && len(lows) >= 2
	if haveStruct {
		hh := bars[highs[len(highs)-1]].High > bars[highs[0]].High
		hl := bars[lows[len(lows)-1]].Low > bars[lows[0]].Low
		lh := bars[highs[len(highs)-1]].High < bars[highs[0]].High
		ll := bars[lows[len(lows)-1]].Low < bars[lows[0]].Low
		switch {
		case up && hh && hl:
			return Uptrend
		case down && lh && ll:
			return Downtrend
		default:
			return Range
		}
	}
	// Slope-only fallback (e.g. a smooth drift with no confirmed pivots yet).
	switch {
	case up:
		return Uptrend
	case down:
		return Downtrend
	default:
		return Range
	}
}

// slopePctPerBar fits closes ~ a + b*i by least squares and returns b divided
// by the mean close — the slope as a fraction of price per bar. 0 when the
// mean price is non-positive (degenerate).
func slopePctPerBar(bars []marketdata.Bar) float64 {
	n := len(bars)
	xs := make([]float64, n)
	ys := make([]float64, n)
	for i, b := range bars {
		xs[i] = float64(i)
		ys[i] = b.Close
	}
	_, b, ok := fitLine(xs, ys)
	if !ok {
		return 0
	}
	mean := meanOf(ys)
	if mean <= 0 {
		return 0
	}
	return b / mean
}

// swingHighs returns the indices of strict local-maximum highs: bars[i].High is
// greater than every other high within swingWindow bars on each side. The last
// swingWindow bars are never eligible (no confirmed right side) — no lookahead.
func swingHighs(bars []marketdata.Bar) []int {
	var out []int
	for i := swingWindow; i < len(bars)-swingWindow; i++ {
		isPivot := true
		for j := i - swingWindow; j <= i+swingWindow; j++ {
			if j != i && bars[j].High >= bars[i].High {
				isPivot = false
				break
			}
		}
		if isPivot {
			out = append(out, i)
		}
	}
	return out
}

// swingLows returns the indices of strict local-minimum lows (mirror of
// swingHighs).
func swingLows(bars []marketdata.Bar) []int {
	var out []int
	for i := swingWindow; i < len(bars)-swingWindow; i++ {
		isPivot := true
		for j := i - swingWindow; j <= i+swingWindow; j++ {
			if j != i && bars[j].Low <= bars[i].Low {
				isPivot = false
				break
			}
		}
		if isPivot {
			out = append(out, i)
		}
	}
	return out
}

// fitTrendline least-squares fits a line through the given pivot indices
// (bars[i].High when useHigh, else .Low) and reports endpoints at the first
// and last pivot plus how many pivots lie within touchTolFrac of the line.
// ok=false with fewer than 2 pivots.
func fitTrendline(bars []marketdata.Bar, idxs []int, useHigh bool, kind string) (Line, bool) {
	if len(idxs) < 2 {
		return Line{}, false
	}
	xs := make([]float64, len(idxs))
	ys := make([]float64, len(idxs))
	for k, i := range idxs {
		xs[k] = float64(i)
		if useHigh {
			ys[k] = bars[i].High
		} else {
			ys[k] = bars[i].Low
		}
	}
	a, b, ok := fitLine(xs, ys)
	if !ok {
		return Line{}, false
	}
	tol := touchTolFrac * math.Abs(meanOf(ys))
	touches := 0
	for k := range idxs {
		if math.Abs(ys[k]-(a+b*xs[k])) <= tol {
			touches++
		}
	}
	first, lastP := idxs[0], idxs[len(idxs)-1]
	return Line{
		FromTs:     bars[first].Ts,
		FromPrice:  a + b*float64(first),
		ToTs:       bars[lastP].Ts,
		ToPrice:    a + b*float64(lastP),
		Kind:       kind,
		TouchCount: touches,
	}, true
}

// channelParallel reports whether a support and resistance line both exist and
// their slopes (normalized to fraction-of-price per bar) are within
// channelParallelTol — a channel rather than a converging wedge.
func channelParallel(bars []marketdata.Bar, highs, lows []int) bool {
	if len(highs) < 2 || len(lows) < 2 {
		return false
	}
	bHigh, ok1 := pivotSlope(bars, highs, true)
	bLow, ok2 := pivotSlope(bars, lows, false)
	if !ok1 || !ok2 {
		return false
	}
	mean := meanClose(bars)
	if mean <= 0 {
		return false
	}
	return math.Abs(bHigh-bLow)/mean <= channelParallelTol
}

// pivotSlope returns the least-squares slope (price per bar) through the pivots.
func pivotSlope(bars []marketdata.Bar, idxs []int, useHigh bool) (float64, bool) {
	xs := make([]float64, len(idxs))
	ys := make([]float64, len(idxs))
	for k, i := range idxs {
		xs[k] = float64(i)
		if useHigh {
			ys[k] = bars[i].High
		} else {
			ys[k] = bars[i].Low
		}
	}
	_, b, ok := fitLine(xs, ys)
	return b, ok
}

// ── small numeric helpers ───────────────────────────────────────────────

// fitLine returns the ordinary least-squares intercept a and slope b of
// ys ~ a + b*xs. ok=false with <2 points or a degenerate (zero-variance) x.
func fitLine(xs, ys []float64) (a, b float64, ok bool) {
	n := len(xs)
	if n < 2 {
		return 0, 0, false
	}
	var sx, sy, sxx, sxy float64
	for i := range xs {
		sx += xs[i]
		sy += ys[i]
		sxx += xs[i] * xs[i]
		sxy += xs[i] * ys[i]
	}
	d := float64(n)*sxx - sx*sx
	if d == 0 {
		return 0, 0, false
	}
	b = (float64(n)*sxy - sx*sy) / d
	a = (sy - b*sx) / float64(n)
	return a, b, true
}

func meanOf(ys []float64) float64 {
	if len(ys) == 0 {
		return 0
	}
	sum := 0.0
	for _, y := range ys {
		sum += y
	}
	return sum / float64(len(ys))
}

func meanClose(bars []marketdata.Bar) float64 {
	if len(bars) == 0 {
		return 0
	}
	sum := 0.0
	for _, b := range bars {
		sum += b.Close
	}
	return sum / float64(len(bars))
}
