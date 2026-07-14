// Package candles is SignalDeck's pure candlestick-pattern library. Given a bar
// window (ASCENDING by Ts) it names the classic single-, two- and three-bar
// patterns firing ON THE LAST BAR, each with a directional bias and a
// plain-English description.
//
// HONESTY NOTES (this is the brand):
//
//   - PATTERNS ARE WEAK, CONTEXT-ONLY SIGNALS. A named shape is a descriptive
//     read of the last few bars, not a forecast. The measured edge (edge.go)
//     grades each pattern against THIS symbol's own forward returns so the UI
//     can show whether the shape has ever paid on this instrument — descriptive,
//     never advice.
//
//   - NO LOOKAHEAD. A pattern detected on bars[..i] uses only bars up to and
//     including i. The reversal patterns (hammer/hanging-man/…) read a short
//     PRIOR-trend window from the bars BEFORE the shape — never a bar after it.
//
//   - ROBUST TO DEGENERATE BARS. Zero-range bars (high==low) never divide by
//     zero; a detector simply declines to fire rather than producing a NaN.
//
// Every function is pure: bars in, patterns out. No I/O, no persistence, no
// clock. It depends only on the stdlib and the marketdata contract types.
package candles

import (
	"math"

	"github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// Pattern is one candlestick pattern detected on the last bar of a window.
type Pattern struct {
	// Name is the canonical snake_case pattern id (e.g. "bullish_engulfing").
	Name string `json:"name"`
	// Bias is the pattern's conventional directional lean: +1 bullish, -1
	// bearish, 0 neutral/indecision.
	Bias int `json:"bias"`
	// Desc is a plain-English one-liner describing the shape.
	Desc string `json:"desc"`
}

// Tunable shape thresholds. Fixed, documented defaults — reasonable across
// liquid instruments, not per-symbol tuned. Fractions are OF THE BAR RANGE
// (high-low) unless noted, so they are scale-free.
const (
	// dojiBodyMaxFrac: body <= this fraction of range reads as a doji body.
	dojiBodyMaxFrac = 0.10
	// smallBodyMaxFrac: body <= this fraction of range is a "small" body
	// (spinning tops, stars, harami inners).
	smallBodyMaxFrac = 0.30
	// largeBodyMinFrac: body >= this fraction of range is a "real"/decisive
	// body (engulfing/harami/star anchors, three-soldiers/crows).
	largeBodyMinFrac = 0.55
	// longWickMinMult: a wick >= this multiple of the body is a "long" wick
	// (hammer/shooting-star shadows).
	longWickMinMult = 2.0
	// negWickMaxFrac: a wick <= this fraction of range is negligible (the flat
	// end of a hammer / dragonfly / gravestone / marubozu).
	negWickMaxFrac = 0.10
	// tweezerTolFrac: two highs/lows are "equal" within this fraction of their
	// average price.
	tweezerTolFrac = 0.0015
	// trendLookback is how many bars back the prior-trend proxy looks to
	// decide whether a reversal shape sits after an advance or a decline.
	trendLookback = 5
)

// candle is one bar's geometry, computed once so detectors read cleanly.
type candle struct {
	o, h, l, c float64
}

func at(b marketdata.Bar) candle { return candle{o: b.Open, h: b.High, l: b.Low, c: b.Close} }

func (k candle) body() float64  { return math.Abs(k.c - k.o) }
func (k candle) rng() float64   { return k.h - k.l }
func (k candle) upper() float64 { return k.h - math.Max(k.o, k.c) }
func (k candle) lower() float64 { return math.Min(k.o, k.c) - k.l }
func (k candle) bull() bool     { return k.c > k.o }
func (k candle) bear() bool     { return k.c < k.o }

// bodyMid is the midpoint of the real body (open..close).
func (k candle) bodyMid() float64 { return (k.o + k.c) / 2 }

// bodyTop / bodyBot are the higher / lower of open and close.
func (k candle) bodyTop() float64 { return math.Max(k.o, k.c) }
func (k candle) bodyBot() float64 { return math.Min(k.o, k.c) }

// Detect returns every pattern firing on the LAST bar of bars (ascending by
// Ts). The result is ordered single → two-bar → three-bar and is never nil-
// panicking; an empty/short window simply yields no patterns. A window may
// legitimately fire more than one pattern (e.g. a three-inside-up whose middle
// two bars are also a bullish harami) — callers treat the set as context.
func Detect(bars []marketdata.Bar) []Pattern {
	n := len(bars)
	if n == 0 {
		return nil
	}
	var out []Pattern
	add := func(p Pattern, ok bool) {
		if ok {
			out = append(out, p)
		}
	}

	// SINGLE-bar shapes (need only the last bar; reversal ones read prior trend).
	last := at(bars[n-1])
	priorTrend := trendDir(bars, n-2) // trend into the bar BEFORE the shape

	add(detectDoji(last))
	add(detectDragonfly(last))
	add(detectGravestone(last))
	add(detectMarubozu(last))
	add(detectSpinningTop(last))
	add(detectHammer(last, priorTrend))
	add(detectHangingMan(last, priorTrend))
	add(detectInvertedHammer(last, priorTrend))
	add(detectShootingStar(last, priorTrend))

	// TWO-bar shapes.
	if n >= 2 {
		prev, cur := at(bars[n-2]), last
		up2 := trendDir(bars, n-3)
		add(detectBullishEngulfing(prev, cur))
		add(detectBearishEngulfing(prev, cur))
		add(detectBullishHarami(prev, cur))
		add(detectBearishHarami(prev, cur))
		add(detectPiercingLine(prev, cur))
		add(detectDarkCloudCover(prev, cur))
		add(detectTweezerTop(prev, cur, up2))
		add(detectTweezerBottom(prev, cur, up2))
	}

	// THREE-bar shapes.
	if n >= 3 {
		b1, b2, b3 := at(bars[n-3]), at(bars[n-2]), last
		add(detectMorningStar(b1, b2, b3))
		add(detectEveningStar(b1, b2, b3))
		add(detectThreeWhiteSoldiers(b1, b2, b3))
		add(detectThreeBlackCrows(b1, b2, b3))
		add(detectThreeInsideUp(b1, b2, b3))
		add(detectThreeInsideDown(b1, b2, b3))
	}
	return out
}

// trendDir returns the sign of the close change over the trendLookback bars
// ending at index end (inclusive): +1 up, -1 down, 0 flat or too little
// history. Uses only bars[..end], so a reversal shape at end+1 never sees its
// own or a later bar.
func trendDir(bars []marketdata.Bar, end int) int {
	if end < 0 || end >= len(bars) {
		return 0
	}
	start := end - trendLookback
	if start < 0 {
		return 0
	}
	d := bars[end].Close - bars[start].Close
	switch {
	case d > 0:
		return 1
	case d < 0:
		return -1
	default:
		return 0
	}
}

// ── single-bar detectors ────────────────────────────────────────────────

func detectDoji(k candle) (Pattern, bool) {
	r := k.rng()
	if r <= 0 {
		return Pattern{}, false
	}
	// Tiny body AND both shadows meaningfully present (a one-sided tiny-body
	// bar is a dragonfly/gravestone, handled separately).
	if k.body() <= dojiBodyMaxFrac*r && k.upper() > negWickMaxFrac*r && k.lower() > negWickMaxFrac*r {
		return Pattern{"doji", 0, "open and close nearly equal with shadows on both sides — indecision, a possible turning point in context"}, true
	}
	return Pattern{}, false
}

func detectDragonfly(k candle) (Pattern, bool) {
	r := k.rng()
	if r <= 0 {
		return Pattern{}, false
	}
	if k.body() <= dojiBodyMaxFrac*r && k.lower() >= 2*k.body() && k.lower() > negWickMaxFrac*r && k.upper() <= negWickMaxFrac*r {
		return Pattern{"dragonfly_doji", +1, "doji with a long lower wick and almost no upper wick — sellers were rejected, a potential bullish reversal"}, true
	}
	return Pattern{}, false
}

func detectGravestone(k candle) (Pattern, bool) {
	r := k.rng()
	if r <= 0 {
		return Pattern{}, false
	}
	if k.body() <= dojiBodyMaxFrac*r && k.upper() >= 2*k.body() && k.upper() > negWickMaxFrac*r && k.lower() <= negWickMaxFrac*r {
		return Pattern{"gravestone_doji", -1, "doji with a long upper wick and almost no lower wick — buyers were rejected, a potential bearish reversal"}, true
	}
	return Pattern{}, false
}

func detectMarubozu(k candle) (Pattern, bool) {
	r := k.rng()
	if r <= 0 {
		return Pattern{}, false
	}
	// Body fills essentially the whole range: both shadows negligible.
	if k.upper() <= negWickMaxFrac*r && k.lower() <= negWickMaxFrac*r && k.body() >= (1-2*negWickMaxFrac)*r {
		if k.bull() {
			return Pattern{"marubozu", +1, "a full-range up bar with no meaningful wicks — one-sided buying pressure"}, true
		}
		if k.bear() {
			return Pattern{"marubozu", -1, "a full-range down bar with no meaningful wicks — one-sided selling pressure"}, true
		}
	}
	return Pattern{}, false
}

func detectSpinningTop(k candle) (Pattern, bool) {
	r := k.rng()
	if r <= 0 {
		return Pattern{}, false
	}
	b := k.body()
	// A small — but not doji-tiny — body centred between two long-ish shadows.
	if b > dojiBodyMaxFrac*r && b <= smallBodyMaxFrac*r &&
		k.upper() >= b && k.lower() >= b &&
		k.upper() > negWickMaxFrac*r && k.lower() > negWickMaxFrac*r {
		return Pattern{"spinning_top", 0, "a small body between two long shadows — balanced buying and selling, momentum stalling"}, true
	}
	return Pattern{}, false
}

func detectHammer(k candle, prior int) (Pattern, bool) {
	if prior < 0 && hammerShape(k) {
		return Pattern{"hammer", +1, "small body with a long lower wick after a decline — sellers pushed down but buyers reclaimed, a potential bullish reversal"}, true
	}
	return Pattern{}, false
}

func detectHangingMan(k candle, prior int) (Pattern, bool) {
	if prior > 0 && hammerShape(k) {
		return Pattern{"hanging_man", -1, "small body with a long lower wick after an advance — the first sign selling is appearing, a potential bearish reversal"}, true
	}
	return Pattern{}, false
}

func detectInvertedHammer(k candle, prior int) (Pattern, bool) {
	if prior < 0 && invertedShape(k) {
		return Pattern{"inverted_hammer", +1, "small body with a long upper wick after a decline — buyers tested higher, a potential bullish reversal"}, true
	}
	return Pattern{}, false
}

func detectShootingStar(k candle, prior int) (Pattern, bool) {
	if prior > 0 && invertedShape(k) {
		return Pattern{"shooting_star", -1, "small body with a long upper wick after an advance — buyers pushed up but were rejected, a potential bearish reversal"}, true
	}
	return Pattern{}, false
}

// hammerShape is the hammer/hanging-man geometry: a small body sitting near the
// TOP of the range with a long lower shadow and a negligible upper shadow.
func hammerShape(k candle) bool {
	r := k.rng()
	if r <= 0 {
		return false
	}
	b := k.body()
	if b <= 0 {
		return false
	}
	return b <= smallBodyMaxFrac*r && k.lower() >= longWickMinMult*b && k.upper() <= negWickMaxFrac*r
}

// invertedShape is the inverted-hammer/shooting-star geometry: a small body near
// the BOTTOM of the range with a long upper shadow and a negligible lower one.
func invertedShape(k candle) bool {
	r := k.rng()
	if r <= 0 {
		return false
	}
	b := k.body()
	if b <= 0 {
		return false
	}
	return b <= smallBodyMaxFrac*r && k.upper() >= longWickMinMult*b && k.lower() <= negWickMaxFrac*r
}

// ── two-bar detectors ───────────────────────────────────────────────────

func detectBullishEngulfing(prev, cur candle) (Pattern, bool) {
	if prev.bear() && cur.bull() && realBody(cur) &&
		cur.bodyBot() <= prev.bodyBot() && cur.bodyTop() >= prev.bodyTop() && cur.body() > prev.body() {
		return Pattern{"bullish_engulfing", +1, "an up bar whose body fully engulfs the prior down bar — buyers overwhelmed sellers, a bullish reversal"}, true
	}
	return Pattern{}, false
}

func detectBearishEngulfing(prev, cur candle) (Pattern, bool) {
	if prev.bull() && cur.bear() && realBody(cur) &&
		cur.bodyBot() <= prev.bodyBot() && cur.bodyTop() >= prev.bodyTop() && cur.body() > prev.body() {
		return Pattern{"bearish_engulfing", -1, "a down bar whose body fully engulfs the prior up bar — sellers overwhelmed buyers, a bearish reversal"}, true
	}
	return Pattern{}, false
}

func detectBullishHarami(prev, cur candle) (Pattern, bool) {
	if prev.bear() && largeBody(prev) && cur.bull() && smallBody(cur) && bodyInside(cur, prev) {
		return Pattern{"bullish_harami", +1, "a small up bar held inside the prior large down bar — downside momentum is fading, a potential bullish reversal"}, true
	}
	return Pattern{}, false
}

func detectBearishHarami(prev, cur candle) (Pattern, bool) {
	if prev.bull() && largeBody(prev) && cur.bear() && smallBody(cur) && bodyInside(cur, prev) {
		return Pattern{"bearish_harami", -1, "a small down bar held inside the prior large up bar — upside momentum is fading, a potential bearish reversal"}, true
	}
	return Pattern{}, false
}

func detectPiercingLine(prev, cur candle) (Pattern, bool) {
	// Prior down bar; current opens below the prior close (weak) but closes
	// back UP into the upper half of the prior body — a strong rejection.
	if prev.bear() && largeBody(prev) && cur.bull() &&
		cur.o < prev.c && cur.c > prev.bodyMid() && cur.c < prev.o {
		return Pattern{"piercing_line", +1, "opens below the prior down bar then closes back above its midpoint — buyers seized control, a bullish reversal"}, true
	}
	return Pattern{}, false
}

func detectDarkCloudCover(prev, cur candle) (Pattern, bool) {
	// Prior up bar; current opens above the prior close but closes DOWN into the
	// lower half of the prior body.
	if prev.bull() && largeBody(prev) && cur.bear() &&
		cur.o > prev.c && cur.c < prev.bodyMid() && cur.c > prev.o {
		return Pattern{"dark_cloud_cover", -1, "opens above the prior up bar then closes below its midpoint — sellers seized control, a bearish reversal"}, true
	}
	return Pattern{}, false
}

func detectTweezerTop(prev, cur candle, prior int) (Pattern, bool) {
	// After an advance, two bars print near-identical highs — a shared ceiling.
	if prior > 0 && prev.bull() && cur.bear() && nearEqual(prev.h, cur.h) {
		return Pattern{"tweezer_top", -1, "two bars printing the same high after an advance — a shared ceiling, a potential bearish reversal"}, true
	}
	return Pattern{}, false
}

func detectTweezerBottom(prev, cur candle, prior int) (Pattern, bool) {
	// After a decline, two bars print near-identical lows — a shared floor.
	if prior < 0 && prev.bear() && cur.bull() && nearEqual(prev.l, cur.l) {
		return Pattern{"tweezer_bottom", +1, "two bars printing the same low after a decline — a shared floor, a potential bullish reversal"}, true
	}
	return Pattern{}, false
}

// ── three-bar detectors ─────────────────────────────────────────────────

func detectMorningStar(b1, b2, b3 candle) (Pattern, bool) {
	// Large down bar, a small-bodied star sitting below it, then a large up bar
	// closing back above the first bar's midpoint.
	if b1.bear() && largeBody(b1) && smallBody(b2) && b2.bodyTop() < b1.c &&
		b3.bull() && largeBody(b3) && b3.c > b1.bodyMid() {
		return Pattern{"morning_star", +1, "a large down bar, a small indecision bar, then a strong up bar — a three-bar bullish reversal off a low"}, true
	}
	return Pattern{}, false
}

func detectEveningStar(b1, b2, b3 candle) (Pattern, bool) {
	// Large up bar, a small-bodied star sitting above it, then a large down bar
	// closing back below the first bar's midpoint.
	if b1.bull() && largeBody(b1) && smallBody(b2) && b2.bodyBot() > b1.c &&
		b3.bear() && largeBody(b3) && b3.c < b1.bodyMid() {
		return Pattern{"evening_star", -1, "a large up bar, a small indecision bar, then a strong down bar — a three-bar bearish reversal off a high"}, true
	}
	return Pattern{}, false
}

func detectThreeWhiteSoldiers(b1, b2, b3 candle) (Pattern, bool) {
	// Three tall up bars, each closing higher, each opening within the prior
	// real body — a steady advance.
	if b1.bull() && b2.bull() && b3.bull() &&
		realBody(b1) && realBody(b2) && realBody(b3) &&
		b2.c > b1.c && b3.c > b2.c &&
		openInBody(b2, b1) && openInBody(b3, b2) {
		return Pattern{"three_white_soldiers", +1, "three tall up bars each closing higher — sustained buying, a strong bullish continuation/reversal"}, true
	}
	return Pattern{}, false
}

func detectThreeBlackCrows(b1, b2, b3 candle) (Pattern, bool) {
	// Three tall down bars, each closing lower, each opening within the prior
	// real body — a steady decline.
	if b1.bear() && b2.bear() && b3.bear() &&
		realBody(b1) && realBody(b2) && realBody(b3) &&
		b2.c < b1.c && b3.c < b2.c &&
		openInBody(b2, b1) && openInBody(b3, b2) {
		return Pattern{"three_black_crows", -1, "three tall down bars each closing lower — sustained selling, a strong bearish continuation/reversal"}, true
	}
	return Pattern{}, false
}

func detectThreeInsideUp(b1, b2, b3 candle) (Pattern, bool) {
	// A bullish harami (b1 large down, b2 small up inside it) confirmed by b3
	// closing above b1's open.
	if b1.bear() && largeBody(b1) && b2.bull() && smallBody(b2) && bodyInside(b2, b1) &&
		b3.bull() && b3.c > b1.o {
		return Pattern{"three_inside_up", +1, "a bullish harami confirmed by a third up bar closing above the pattern — a bullish reversal"}, true
	}
	return Pattern{}, false
}

func detectThreeInsideDown(b1, b2, b3 candle) (Pattern, bool) {
	// A bearish harami (b1 large up, b2 small down inside it) confirmed by b3
	// closing below b1's open.
	if b1.bull() && largeBody(b1) && b2.bear() && smallBody(b2) && bodyInside(b2, b1) &&
		b3.bear() && b3.c < b1.o {
		return Pattern{"three_inside_down", -1, "a bearish harami confirmed by a third down bar closing below the pattern — a bearish reversal"}, true
	}
	return Pattern{}, false
}

// ── shared shape predicates ─────────────────────────────────────────────

func realBody(k candle) bool  { r := k.rng(); return r > 0 && k.body() >= largeBodyMinFrac*r }
func largeBody(k candle) bool { return realBody(k) }
func smallBody(k candle) bool { r := k.rng(); return r > 0 && k.body() <= smallBodyMaxFrac*r }

// bodyInside reports whether inner's real body sits entirely within outer's.
func bodyInside(inner, outer candle) bool {
	return inner.bodyTop() <= outer.bodyTop() && inner.bodyBot() >= outer.bodyBot()
}

// openInBody reports whether k opened within prior's real body (the three-
// soldiers/crows "orderly advance" condition).
func openInBody(k, prior candle) bool {
	return k.o >= prior.bodyBot() && k.o <= prior.bodyTop()
}

// nearEqual reports whether a and b are within tweezerTolFrac of their average
// magnitude — robust to zero (identical zeros are equal).
func nearEqual(a, b float64) bool {
	avg := math.Abs(a+b) / 2
	if avg == 0 {
		return a == b
	}
	return math.Abs(a-b) <= tweezerTolFrac*avg
}
