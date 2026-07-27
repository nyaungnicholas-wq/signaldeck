package stresslab

import (
	"math"
	"math/rand"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// flatWindow builds n daily bars at price px with a modest range and steady
// volume, one bar per day.
func flatWindow(n int, px float64) Window {
	bars := make([]md.Bar, n)
	for i := range bars {
		bars[i] = md.Bar{
			Ts:     int64(1_700_000_000 + i*86400),
			Open:   px, High: px * 1.01, Low: px * 0.99, Close: px,
			Volume: 1_000_000,
		}
	}
	return Window{Market: md.Stocks, Bars: bars, ADVUSD: px * 1_000_000, SpreadMult: 1}
}

func TestApply_FlashCrashExactEffect(t *testing.T) {
	w := flatWindow(20, 100)
	sc, err := Lookup([]string{"flash_crash"})
	if err != nil {
		t.Fatal(err)
	}
	out := Apply(w, Combine(sc...), rand.New(rand.NewSource(1)))

	idx := ShockIndex(20)
	if got := out.Bars[idx].Close; math.Abs(got-92) > 1e-9 {
		t.Fatalf("shock bar close = %v want 92 (-8%%)", got)
	}
	if got := out.Bars[idx-1].Close; got != 100 {
		t.Fatalf("pre-shock bar moved: %v", got)
	}
	// Level shift persists on every later bar.
	if got := out.Bars[19].Close; math.Abs(got-92) > 1e-9 {
		t.Fatalf("post-shock level = %v want 92", got)
	}
	if math.Abs(out.ADVUSD-0.3*w.ADVUSD) > 1e-6 {
		t.Fatalf("ADV mult: got %v want %v", out.ADVUSD, 0.3*w.ADVUSD)
	}
	if out.SpreadMult != 3 {
		t.Fatalf("spread mult = %v want 3", out.SpreadMult)
	}
	// Input untouched (pure transform).
	if w.Bars[idx].Close != 100 || w.ADVUSD != 100*1_000_000 {
		t.Fatal("Apply mutated its input")
	}
}

func TestApply_GapOpenShiftsOpenAndLevel(t *testing.T) {
	w := flatWindow(10, 200)
	sc, _ := Lookup([]string{"gap_open"})
	out := Apply(w, Combine(sc...), rand.New(rand.NewSource(1)))
	idx := ShockIndex(10)
	if got := out.Bars[idx].Open; math.Abs(got-190) > 1e-9 {
		t.Fatalf("gap bar open = %v want 190 (-5%%)", got)
	}
	if got := out.Bars[9].Close; math.Abs(got-190) > 1e-9 {
		t.Fatalf("post-gap close = %v want 190", got)
	}
}

func TestApply_VolSpikeWidensRangeAroundClose(t *testing.T) {
	w := flatWindow(10, 100)
	sc, _ := Lookup([]string{"vol_spike"})
	out := Apply(w, Combine(sc...), rand.New(rand.NewSource(1)))
	b := out.Bars[3]
	if math.Abs(b.High-103) > 1e-9 || math.Abs(b.Low-97) > 1e-9 {
		t.Fatalf("range not 3x around close: H=%v L=%v want 103/97", b.High, b.Low)
	}
	if b.Close != 100 {
		t.Fatalf("close moved under vol_spike: %v", b.Close)
	}
}

func TestApply_DelayedFeedAndMissingCandles(t *testing.T) {
	w := flatWindow(50, 100)
	sc, _ := Lookup([]string{"delayed_feed"})
	out := Apply(w, Combine(sc...), rand.New(rand.NewSource(1)))
	if out.DelayBars != 3 {
		t.Fatalf("delay = %d want 3", out.DelayBars)
	}

	sc, _ = Lookup([]string{"missing_candles"})
	a := Apply(w, Combine(sc...), rand.New(rand.NewSource(7)))
	b := Apply(w, Combine(sc...), rand.New(rand.NewSource(7)))
	if len(a.Bars) == len(w.Bars) {
		t.Fatal("missing_candles dropped nothing")
	}
	if len(a.Bars) != len(b.Bars) {
		t.Fatalf("same seed, different drops: %d vs %d", len(a.Bars), len(b.Bars))
	}
	for i := range a.Bars {
		if a.Bars[i].Ts != b.Bars[i].Ts {
			t.Fatal("same seed produced different surviving bars")
		}
	}
	if a.Bars[0].Ts != w.Bars[0].Ts {
		t.Fatal("first bar must never be dropped")
	}
}

func TestApply_ExchangeOutageContiguous(t *testing.T) {
	w := flatWindow(20, 100)
	sc, _ := Lookup([]string{"exchange_outage"})
	out := Apply(w, Combine(sc...), rand.New(rand.NewSource(1)))
	if len(out.Bars) != 15 {
		t.Fatalf("bars = %d want 15 (5 removed)", len(out.Bars))
	}
	idx := ShockIndex(20)
	// The bar after the gap is the original idx+5.
	if out.Bars[idx].Ts != w.Bars[idx+5].Ts {
		t.Fatalf("outage not contiguous at the shock point")
	}
}

func TestCombine_JointScenarios(t *testing.T) {
	sc, err := Lookup([]string{"vol_spike", "spread_widening"})
	if err != nil {
		t.Fatal(err)
	}
	e := Combine(sc...)
	if e.RangeMult != 3 || e.SpreadMult != 4 {
		t.Fatalf("joint effects wrong: %+v", e)
	}
	// Composition with flash_crash multiplies spreads and keeps the shock.
	sc, _ = Lookup([]string{"flash_crash", "spread_widening"})
	e = Combine(sc...)
	if e.SpreadMult != 12 || e.PriceShock != -0.08 {
		t.Fatalf("flash_crash+spread_widening: %+v", e)
	}
}

func TestLookup_UnknownScenario(t *testing.T) {
	if _, err := Lookup([]string{"volcano"}); err == nil {
		t.Fatal("unknown scenario must error")
	}
}
