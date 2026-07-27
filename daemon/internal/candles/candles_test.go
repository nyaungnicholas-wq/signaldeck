package candles

import (
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

func mkBar(ts int64, o, h, l, c float64) marketdata.Bar {
	return marketdata.Bar{Ts: ts, Open: o, High: h, Low: l, Close: c}
}

// trendBars returns n modest-bodied bars trending in dir (+1 up / -1 down),
// used only to establish PRIOR-trend context ahead of a reversal shape.
func trendBars(dir, n int) []marketdata.Bar {
	out := make([]marketdata.Bar, n)
	c := 100.0
	for i := 0; i < n; i++ {
		c += float64(dir) * 2
		o := c - float64(dir)*1.0
		hi, lo := c, o
		if c <= o {
			hi, lo = o, c
		}
		out[i] = mkBar(int64(i), o, hi+0.8, lo-0.8, c)
	}
	return out
}

// has reports whether the pattern set contains a pattern named name, and its
// bias when present.
func has(ps []Pattern, name string) (int, bool) {
	for _, p := range ps {
		if p.Name == name {
			return p.Bias, true
		}
	}
	return 0, false
}

// window appends the pattern bars after a trend prefix and returns Detect's
// result on the whole window.
func window(dir int, patternBars ...marketdata.Bar) []Pattern {
	bars := trendBars(dir, 7)
	base := int64(len(bars))
	for i, b := range patternBars {
		b.Ts = base + int64(i)
		bars = append(bars, b)
	}
	return Detect(bars)
}

func assertFires(t *testing.T, ps []Pattern, name string, wantBias int) {
	t.Helper()
	bias, ok := has(ps, name)
	if !ok {
		t.Fatalf("expected %q to fire, got %v", name, ps)
	}
	if bias != wantBias {
		t.Fatalf("%q bias = %d, want %d", name, bias, wantBias)
	}
}

func TestSinglePatterns(t *testing.T) {
	// doji: tiny body, shadows both sides. Trend-agnostic.
	assertFires(t, window(0, mkBar(0, 100, 105, 95, 100.1)), "doji", 0)
	// dragonfly: tiny body, long lower wick, no upper.
	assertFires(t, window(0, mkBar(0, 100, 100.2, 95, 100.1)), "dragonfly_doji", +1)
	// gravestone: tiny body, long upper wick, no lower.
	assertFires(t, window(0, mkBar(0, 100, 105, 99.9, 100.1)), "gravestone_doji", -1)
	// marubozu (bull): full-range body, no wicks.
	assertFires(t, window(0, mkBar(0, 100, 110.2, 99.8, 110)), "marubozu", +1)
	// spinning top: small body between two long shadows.
	assertFires(t, window(0, mkBar(0, 100, 104, 96, 101)), "spinning_top", 0)
	// hammer: prior DOWNtrend + hammer shape.
	assertFires(t, window(-1, mkBar(0, 104, 104.3, 98, 104.1)), "hammer", +1)
	// hanging man: SAME shape, prior UPtrend.
	assertFires(t, window(+1, mkBar(0, 104, 104.3, 98, 104.1)), "hanging_man", -1)
	// inverted hammer: prior DOWNtrend + long upper wick shape.
	assertFires(t, window(-1, mkBar(0, 100, 106, 99.8, 100.1)), "inverted_hammer", +1)
	// shooting star: SAME shape, prior UPtrend.
	assertFires(t, window(+1, mkBar(0, 100, 106, 99.8, 100.1)), "shooting_star", -1)
}

func TestHammerHangingManDistinguishedByTrend(t *testing.T) {
	shape := mkBar(0, 104, 104.3, 98, 104.1)
	down := window(-1, shape)
	if _, ok := has(down, "hammer"); !ok {
		t.Fatal("hammer must fire after a decline")
	}
	if _, ok := has(down, "hanging_man"); ok {
		t.Fatal("hanging_man must NOT fire after a decline")
	}
	up := window(+1, shape)
	if _, ok := has(up, "hanging_man"); !ok {
		t.Fatal("hanging_man must fire after an advance")
	}
	if _, ok := has(up, "hammer"); ok {
		t.Fatal("hammer must NOT fire after an advance")
	}
}

func TestTwoBarPatterns(t *testing.T) {
	// bullish engulfing (trend-agnostic).
	assertFires(t, window(0,
		mkBar(0, 102, 102.5, 99.5, 100),
		mkBar(0, 99, 104, 98.5, 103)), "bullish_engulfing", +1)
	// bearish engulfing.
	assertFires(t, window(0,
		mkBar(0, 100, 102.5, 99.5, 102),
		mkBar(0, 103, 103.5, 98, 99)), "bearish_engulfing", -1)
	// bullish harami.
	assertFires(t, window(0,
		mkBar(0, 110, 110.5, 99.5, 100),
		mkBar(0, 103, 105.5, 101.5, 104)), "bullish_harami", +1)
	// bearish harami.
	assertFires(t, window(0,
		mkBar(0, 100, 110.5, 99.5, 110),
		mkBar(0, 104, 105.5, 101.5, 103)), "bearish_harami", -1)
	// piercing line.
	assertFires(t, window(0,
		mkBar(0, 110, 110.5, 99.5, 100),
		mkBar(0, 98, 106.5, 97.5, 106)), "piercing_line", +1)
	// dark cloud cover.
	assertFires(t, window(0,
		mkBar(0, 100, 110.5, 99.5, 110),
		mkBar(0, 112, 112.5, 103.5, 104)), "dark_cloud_cover", -1)
	// tweezer top: near-equal highs after an advance.
	assertFires(t, window(+1,
		mkBar(0, 100, 106, 99.5, 105),
		mkBar(0, 105, 106.02, 101, 102)), "tweezer_top", -1)
	// tweezer bottom: near-equal lows after a decline.
	assertFires(t, window(-1,
		mkBar(0, 105, 105.5, 100, 101),
		mkBar(0, 102, 104, 100.02, 103)), "tweezer_bottom", +1)
}

func TestThreeBarPatterns(t *testing.T) {
	assertFires(t, window(0,
		mkBar(0, 110, 110.5, 99.5, 100),
		mkBar(0, 98, 99, 96, 97.5),
		mkBar(0, 99, 110, 98.5, 108)), "morning_star", +1)
	assertFires(t, window(0,
		mkBar(0, 100, 110.5, 99.5, 110),
		mkBar(0, 112, 113.5, 111.5, 112.5),
		mkBar(0, 111, 112.5, 101, 102)), "evening_star", -1)
	assertFires(t, window(0,
		mkBar(0, 100, 105.5, 99.5, 105),
		mkBar(0, 103, 110.5, 102.5, 110),
		mkBar(0, 108, 115.5, 107.5, 115)), "three_white_soldiers", +1)
	assertFires(t, window(0,
		mkBar(0, 105, 105.5, 99.5, 100),
		mkBar(0, 102, 102.5, 94.5, 95),
		mkBar(0, 97, 97.5, 89.5, 90)), "three_black_crows", -1)
	assertFires(t, window(0,
		mkBar(0, 110, 110.5, 99.5, 100),
		mkBar(0, 103, 105.5, 101.5, 104),
		mkBar(0, 105, 112, 104, 111)), "three_inside_up", +1)
	assertFires(t, window(0,
		mkBar(0, 100, 110.5, 99.5, 110),
		mkBar(0, 107, 108.5, 104.5, 106),
		mkBar(0, 105, 106, 98, 99)), "three_inside_down", -1)
}

func TestFlatSeriesFiresNothing(t *testing.T) {
	var bars []marketdata.Bar
	for i := 0; i < 20; i++ {
		bars = append(bars, mkBar(int64(i), 100, 100, 100, 100)) // zero-range
	}
	if ps := Detect(bars); len(ps) != 0 {
		t.Fatalf("flat/zero-range series fired %v, want none", ps)
	}
}

func TestZeroRangeBarNoPanicNoFire(t *testing.T) {
	bars := trendBars(-1, 7)
	bars = append(bars, mkBar(7, 50, 50, 50, 50)) // degenerate final bar
	ps := Detect(bars)                            // must not panic / divide by zero
	for _, p := range ps {
		if p.Name == "doji" || p.Name == "hammer" || p.Name == "marubozu" {
			t.Fatalf("a zero-range bar should not fire %q", p.Name)
		}
	}
}

func TestNormalUptrendNoSpuriousReversals(t *testing.T) {
	// Ordinary medium-bodied up bars — no doji/hammer/dragonfly should appear.
	bars := trendBars(+1, 20)
	ps := Detect(bars)
	for _, bad := range []string{"doji", "hammer", "dragonfly_doji", "gravestone_doji", "shooting_star"} {
		if _, ok := has(ps, bad); ok {
			t.Fatalf("normal uptrend spuriously fired %q: %v", bad, ps)
		}
	}
}

func TestDetectEmptyInput(t *testing.T) {
	if ps := Detect(nil); ps != nil {
		t.Fatalf("Detect(nil) = %v, want nil", ps)
	}
}
