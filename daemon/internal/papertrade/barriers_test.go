package papertrade

import (
	"math"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// bar builds a daily bar. ts is a day index for readability.
func bar(ts int64, o, h, l, c float64) md.Bar {
	return md.Bar{Ts: ts * 86400, TF: md.TF1d, Open: o, High: h, Low: l, Close: c, Volume: 1e6}
}

// flat builds n identical bars at px, so a test can extend a window without
// moving the volatility estimate.
func flat(from int64, n int, px float64) []md.Bar {
	out := make([]md.Bar, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, bar(from+int64(i), px, px, px, px))
	}
	return out
}

// ── ATR ────────────────────────────────────────────────────────────────────

func TestATRAveragesTrueRange(t *testing.T) {
	// Each bar has a 2.00 range and no gap, so every true range is exactly 2.
	bars := []md.Bar{
		bar(1, 100, 101, 99, 100),
		bar(2, 100, 101, 99, 100),
		bar(3, 100, 101, 99, 100),
		bar(4, 100, 101, 99, 100),
	}
	atr, ok := ATR(bars, 20)
	if !ok {
		t.Fatal("ATR must be measurable from four clean bars")
	}
	if math.Abs(atr-2.0) > 1e-9 {
		t.Fatalf("ATR = %v, want 2.0", atr)
	}
}

// True range must include the gap: a bar that opens far from the previous close
// is more volatile than its own high-low says.
func TestATRIncludesGaps(t *testing.T) {
	bars := []md.Bar{
		bar(1, 100, 100.5, 99.5, 100),
		bar(2, 110, 110.5, 109.5, 110), // 10-point gap up, 1-point own range
	}
	atr, ok := ATR(bars, 20)
	if !ok {
		t.Fatal("ATR must be measurable")
	}
	// |high - prevClose| = 10.5, which dominates the 1.0 own range.
	if math.Abs(atr-10.5) > 1e-9 {
		t.Fatalf("ATR = %v, want 10.5 — the gap must dominate", atr)
	}
}

// An unmeasurable ATR is not a zero ATR. A zero would collapse both barriers
// onto the entry price and stop every position out on its first close.
func TestATRUnmeasurableIsNotZero(t *testing.T) {
	if _, ok := ATR(nil, 20); ok {
		t.Fatal("no bars must be unmeasurable")
	}
	if _, ok := ATR([]md.Bar{bar(1, 100, 101, 99, 100)}, 20); ok {
		t.Fatal("one bar yields no true range and must be unmeasurable")
	}
	// A perfectly still market has a real, measured zero range — and a zero ATR
	// is still refused, because it cannot place a usable barrier.
	if _, ok := ATR(flat(1, 5, 100), 20); ok {
		t.Fatal("a zero ATR must be refused, not returned as a level of zero")
	}
}

func TestATRHonoursThePeriod(t *testing.T) {
	// 3 quiet bars (TR 1) then 3 wild ones (TR 10). A period of 3 must see only
	// the wild ones.
	bars := []md.Bar{
		bar(1, 100, 100.5, 99.5, 100),
		bar(2, 100, 100.5, 99.5, 100),
		bar(3, 100, 100.5, 99.5, 100),
		bar(4, 100, 105, 95, 100),
		bar(5, 100, 105, 95, 100),
		bar(6, 100, 105, 95, 100),
	}
	atr, ok := ATR(bars, 3)
	if !ok {
		t.Fatal("measurable")
	}
	if math.Abs(atr-10) > 1e-9 {
		t.Fatalf("ATR(3) = %v, want 10 — only the last three true ranges count", atr)
	}
}

// ── Levels ─────────────────────────────────────────────────────────────────

func TestLevels(t *testing.T) {
	fav, adv, ok := Levels(100, 2)
	if !ok {
		t.Fatal("levels must be placeable")
	}
	if math.Abs(fav-106) > 1e-9 { // 100 + 3.0*2
		t.Fatalf("favorable = %v, want 106", fav)
	}
	if math.Abs(adv-96) > 1e-9 { // 100 - 2.0*2
		t.Fatalf("adverse = %v, want 96", adv)
	}
}

// A stop at or below zero is not a stop. Refuse the level rather than pretend
// the position is protected.
func TestLevelsRefuseWhenTheStopWouldBeNonPositive(t *testing.T) {
	if _, _, ok := Levels(10, 5); ok { // 10 - 2*5 = 0
		t.Fatal("a stop at zero must be refused")
	}
	if _, _, ok := Levels(10, 50); ok { // deeply negative
		t.Fatal("a negative stop must be refused")
	}
	for _, bad := range []float64{0, -1, math.NaN(), math.Inf(1)} {
		if _, _, ok := Levels(bad, 2); ok {
			t.Fatalf("entry %v must be refused", bad)
		}
		if _, _, ok := Levels(100, bad); ok {
			t.Fatalf("atr %v must be refused", bad)
		}
	}
}

// ── FindBarrierExit ────────────────────────────────────────────────────────

// THE CENTRAL GUARANTEE. A bar that trades THROUGH the stop intrabar but closes
// back above it is NOT an exit. Reading the low here is the intrabar assumption
// the whole design refuses to make, and this test is what stops someone
// "improving" the scan by consulting it.
func TestWicksThroughTheStopDoNotExit(t *testing.T) {
	// entry 100, atr 2 → adverse 96. The bar's LOW is 90, well through it; the
	// CLOSE is 99, well above it.
	held := []md.Bar{bar(1, 100, 101, 90, 99)}
	if _, found := FindBarrierExit(100, 2, 10, held); found {
		t.Fatal("a wick through the stop must NOT exit — only a CLOSE beyond it does")
	}

	// And the same for the target: a spike through 106 that closes at 101 is not
	// a take-profit.
	held = []md.Bar{bar(1, 100, 120, 99, 101)}
	if _, found := FindBarrierExit(100, 2, 10, held); found {
		t.Fatal("a wick through the target must NOT exit")
	}
}

func TestAdverseBarrierFiresOnTheClose(t *testing.T) {
	held := []md.Bar{
		bar(1, 100, 101, 99, 100),
		bar(2, 100, 101, 95, 95), // closes at 95, through the 96 stop
	}
	x, found := FindBarrierExit(100, 2, 10, held)
	if !found {
		t.Fatal("a close through the stop must exit")
	}
	if x.Kind != BarrierAdverse {
		t.Fatalf("kind = %q, want adverse", x.Kind)
	}
	if x.TriggerTs != 2*86400 {
		t.Fatalf("trigger ts = %d, want the CLOSE that confirmed it", x.TriggerTs)
	}
	if x.HeldBars != 2 {
		t.Fatalf("heldBars = %d, want 2", x.HeldBars)
	}
}

func TestFavorableBarrierFiresOnTheClose(t *testing.T) {
	held := []md.Bar{
		bar(1, 100, 101, 99, 100),
		bar(2, 100, 108, 99, 107), // closes at 107, through the 106 target
	}
	x, found := FindBarrierExit(100, 2, 10, held)
	if !found || x.Kind != BarrierFavorable {
		t.Fatalf("want a favorable exit, got %+v found=%v", x, found)
	}
	if x.Level != 106 {
		t.Fatalf("level = %v, want 106", x.Level)
	}
}

// The entry bar's own close can stop the position out: a position opened at
// that bar's OPEN is exposed to that bar's CLOSE.
func TestTheEntryBarsCloseCanExit(t *testing.T) {
	held := []md.Bar{bar(1, 100, 101, 90, 92)} // opened at 100, closed at 92
	x, found := FindBarrierExit(100, 2, 10, held)
	if !found || x.Kind != BarrierAdverse {
		t.Fatalf("the entry bar's close must be able to stop the position out, got %+v", x)
	}
	if x.HeldBars != 1 {
		t.Fatalf("heldBars = %d, want 1", x.HeldBars)
	}
}

func TestHorizonExpiry(t *testing.T) {
	// A 1-bar horizon expires after the entry bar closes, price barriers untouched.
	x, found := FindBarrierExit(100, 2, 1, flat(1, 3, 100))
	if !found || x.Kind != BarrierExpiry {
		t.Fatalf("want expiry, got %+v found=%v", x, found)
	}
	if x.HeldBars != 1 || x.TriggerTs != 1*86400 {
		t.Fatalf("expiry must fire on the FIRST bar for a 1-bar horizon, got held=%d ts=%d", x.HeldBars, x.TriggerTs)
	}

	// A 5-bar horizon holds through four and expires on the fifth.
	x, found = FindBarrierExit(100, 2, 5, flat(1, 5, 100))
	if !found || x.Kind != BarrierExpiry || x.HeldBars != 5 {
		t.Fatalf("5-bar horizon must expire on the fifth bar, got %+v found=%v", x, found)
	}
	if _, found := FindBarrierExit(100, 2, 5, flat(1, 4, 100)); found {
		t.Fatal("four bars of a five-bar horizon must still be holding")
	}
}

// A price barrier reached ON the expiry bar is recorded as the price barrier.
// The fill is identical either way, so this only decides which fact the log
// records — and "the stop was hit on the last day" is the more useful one.
func TestPriceBarrierOutranksExpiryOnTheSameBar(t *testing.T) {
	held := []md.Bar{bar(1, 100, 101, 90, 92)} // 1-bar horizon AND through the stop
	x, found := FindBarrierExit(100, 2, 1, held)
	if !found || x.Kind != BarrierAdverse {
		t.Fatalf("kind = %q, want adverse to outrank expiry on the same bar", x.Kind)
	}
}

// Without a measurable ATR the price barriers cannot be placed — but the
// horizon has nothing to do with volatility, so the time stop must still fire.
func TestUnmeasurableATRStillExpires(t *testing.T) {
	held := flat(1, 3, 100)
	if _, found := FindBarrierExit(100, 0, 10, held); found {
		t.Fatal("no ATR and a long horizon must keep holding, not invent a price barrier")
	}
	x, found := FindBarrierExit(100, 0, 2, held)
	if !found || x.Kind != BarrierExpiry {
		t.Fatalf("the time stop must survive an unmeasurable ATR, got %+v found=%v", x, found)
	}
}

// The FIRST barrier reached wins, not the most dramatic one later in the window.
func TestFirstBarrierWins(t *testing.T) {
	held := []md.Bar{
		bar(1, 100, 101, 99, 100),
		bar(2, 100, 108, 99, 107), // target first
		bar(3, 100, 101, 90, 90),  // stop later — must never be reached
	}
	x, _ := FindBarrierExit(100, 2, 10, held)
	if x.Kind != BarrierFavorable || x.TriggerTs != 2*86400 {
		t.Fatalf("the first barrier must win, got %+v", x)
	}
}

func TestNoBarrierYetKeepsHolding(t *testing.T) {
	if _, found := FindBarrierExit(100, 2, 10, flat(1, 3, 100)); found {
		t.Fatal("a quiet window inside the horizon must keep holding")
	}
	if _, found := FindBarrierExit(100, 2, 10, nil); found {
		t.Fatal("no bars must keep holding")
	}
}

func TestBarriersEnabledDefaultsOn(t *testing.T) {
	if !BarriersEnabled() {
		t.Fatal("barriers must default ON")
	}
	t.Setenv("SIGNALDECK_PAPER_BARRIERS", "false")
	if BarriersEnabled() {
		t.Fatal("SIGNALDECK_PAPER_BARRIERS=false must disable them")
	}
	t.Setenv("SIGNALDECK_PAPER_BARRIERS", "nonsense")
	if !BarriersEnabled() {
		t.Fatal("an unrecognised value must keep the default")
	}
}
