package anomaly

import (
	"math"
	"strings"
	"testing"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// ── threshold parsing ────────────────────────────────────────────────────

func TestThreshold(t *testing.T) {
	cases := []struct {
		in   string
		want float64
	}{
		{"", DefaultZ},
		{"3.0", 3.0},
		{"1.5", 1.5},
		{"nonsense", DefaultZ},
		{"0", DefaultZ},
		{"-2", DefaultZ},
		{"NaN", DefaultZ},
		{"+Inf", DefaultZ},
	}
	for _, c := range cases {
		if got := Threshold(c.in); got != c.want {
			t.Errorf("Threshold(%q) = %v want %v", c.in, got, c.want)
		}
	}
}

// ── z-score core ─────────────────────────────────────────────────────────

func TestZScore(t *testing.T) {
	base := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10} // mean 5.5, sd ≈ 3.0277
	z, ok := ZScore(11.5, base, 5)
	if !ok {
		t.Fatal("expected ok")
	}
	if math.Abs(z-(11.5-5.5)/3.0276503540974917) > 1e-9 {
		t.Errorf("z = %v", z)
	}

	// Insufficient baseline → NO signal, never a fabricated z.
	if _, ok := ZScore(10, []float64{1, 2, 3}, 5); ok {
		t.Error("z produced from 3 samples with minN=5")
	}
	// Zero-dispersion baseline → no signal (division by ~0 is meaningless).
	flat := []float64{2, 2, 2, 2, 2, 2}
	if _, ok := ZScore(3, flat, 5); ok {
		t.Error("z produced from zero-variance baseline")
	}
	// Empty baseline.
	if _, ok := ZScore(1, nil, 2); ok {
		t.Error("z produced from empty baseline")
	}
}

// ── building blocks ──────────────────────────────────────────────────────

func bar(ts int64, o, h, l, c, v float64) md.Bar {
	return md.Bar{Ts: ts, Open: o, High: h, Low: l, Close: c, Volume: v}
}

func TestSignedVolumeShare(t *testing.T) {
	bars := []md.Bar{
		bar(0, 10, 11, 9, 11, 300),    // up 300
		bar(60, 11, 12, 10, 10, 100),  // down 100
		bar(120, 10, 10, 10, 10, 999), // doji excluded
	}
	share, ok := SignedVolumeShare(bars)
	if !ok || math.Abs(share-0.5) > 1e-12 { // (300-100)/400
		t.Fatalf("share = %v ok=%v", share, ok)
	}
	// All-doji / zero volume → no value.
	if _, ok := SignedVolumeShare([]md.Bar{bar(0, 10, 10, 10, 10, 500)}); ok {
		t.Error("share from doji-only window")
	}
	if _, ok := SignedVolumeShare(nil); ok {
		t.Error("share from empty window")
	}
}

func TestLogReturnVol(t *testing.T) {
	// Constant price → zero vol.
	flat := []md.Bar{bar(0, 1, 1, 1, 100, 1), bar(1, 1, 1, 1, 100, 1), bar(2, 1, 1, 1, 100, 1)}
	v, ok := LogReturnVol(flat)
	if !ok || v != 0 {
		t.Fatalf("flat vol = %v ok=%v", v, ok)
	}
	// Too few bars → no value.
	if _, ok := LogReturnVol(flat[:2]); ok {
		t.Error("vol from 2 bars")
	}
	// Non-positive close → no value (log undefined).
	badBars := []md.Bar{bar(0, 1, 1, 1, 100, 1), bar(1, 1, 1, 1, 0, 1), bar(2, 1, 1, 1, 100, 1)}
	if _, ok := LogReturnVol(badBars); ok {
		t.Error("vol from non-positive close")
	}
}

// ── crypto imbalance (real order-book snapshots) ─────────────────────────

func snapSeries(n int, startTs int64, imb func(i int) float64) []md.Snap1s {
	out := make([]md.Snap1s, n)
	for i := range out {
		out[i] = md.Snap1s{Ts: startTs + int64(i), ImbSigned: imb(i)}
	}
	return out
}

func TestDetectImbalanceSnaps(t *testing.T) {
	// 3600s of noisy-flat baseline then 300s of strong +0.8 imbalance.
	n := 3900
	snaps := snapSeries(n, 1000, func(i int) float64 {
		if i >= 3600 {
			return 0.8
		}
		// deterministic "noise" in [-0.1, +0.1]
		return 0.1 * math.Sin(float64(i))
	})
	ev, ok := DetectImbalanceSnaps(snaps, 300, 3600, 2.5)
	if !ok {
		t.Fatal("expected buy-pressure detection")
	}
	if ev.Kind != KindImbalance || ev.Z < 2.5 {
		t.Errorf("event = %+v", ev)
	}
	if ev.Ts != snaps[len(snaps)-1].Ts {
		t.Errorf("Ts = %d want last snap ts %d", ev.Ts, snaps[len(snaps)-1].Ts)
	}
	if !strings.Contains(ev.Detail, "buy") || !strings.Contains(ev.Detail, "baseline") ||
		!strings.Contains(ev.Detail, "not a prediction") {
		t.Errorf("detail lacks honesty labeling: %q", ev.Detail)
	}

	// Sell side: strong negative recent imbalance.
	sell := snapSeries(n, 1000, func(i int) float64 {
		if i >= 3600 {
			return -0.8
		}
		return 0.1 * math.Sin(float64(i))
	})
	ev, ok = DetectImbalanceSnaps(sell, 300, 3600, 2.5)
	if !ok || ev.Z > -2.5 || !strings.Contains(ev.Detail, "sell") {
		t.Errorf("sell side: ok=%v ev=%+v", ok, ev)
	}

	// Quiet market — recent looks like baseline → no signal.
	quiet := snapSeries(n, 1000, func(i int) float64 { return 0.1 * math.Sin(float64(i)) })
	if _, ok := DetectImbalanceSnaps(quiet, 300, 3600, 2.5); ok {
		t.Error("quiet market fired")
	}

	// Insufficient baseline (only recent snaps present) → no signal.
	short := snapSeries(200, 1000, func(i int) float64 { return 0.9 })
	if _, ok := DetectImbalanceSnaps(short, 300, 3600, 2.5); ok {
		t.Error("fired with no baseline")
	}
	if _, ok := DetectImbalanceSnaps(nil, 300, 3600, 2.5); ok {
		t.Error("fired on empty snaps")
	}
}

// ── stock imbalance proxy (1m bars) ──────────────────────────────────────

// balancedThenBuyBars: many balanced 30-bar windows, then one heavily
// buy-side window. Alternating up/down bars with slightly varying volume so
// the baseline has non-zero dispersion.
func balancedThenBuyBars(windows, w int) []md.Bar {
	var bars []md.Bar
	ts := int64(0)
	for wi := 0; wi < windows; wi++ {
		for i := 0; i < w; i++ {
			v := 100.0 + float64((wi*w+i)%7) // small deterministic wobble
			if i%2 == 0 {
				bars = append(bars, bar(ts, 10, 11, 9, 11, v)) // up
			} else {
				bars = append(bars, bar(ts, 11, 12, 10, 10, v)) // down
			}
			ts += 60
		}
	}
	for i := 0; i < w; i++ { // recent window: all buys
		bars = append(bars, bar(ts, 10, 11, 9, 11, 100))
		ts += 60
	}
	return bars
}

func TestDetectImbalanceBars_ProxyLabeled(t *testing.T) {
	bars := balancedThenBuyBars(10, 30)
	ev, ok := DetectImbalanceBars(bars, 30, 8, 2.5)
	if !ok {
		t.Fatal("expected proxy imbalance detection")
	}
	if ev.Kind != KindImbalance || ev.Z < 2.5 {
		t.Errorf("event = %+v", ev)
	}
	// The HONESTY contract: the proxy label must appear verbatim.
	if !strings.Contains(ev.Detail, "volume-side proxy (no order-book on free stock data)") {
		t.Errorf("stock imbalance missing proxy label: %q", ev.Detail)
	}

	// Balanced recent window → no signal.
	balanced := balancedThenBuyBars(10, 30)
	balanced = balanced[:10*30] // drop the buy window
	if _, ok := DetectImbalanceBars(balanced, 30, 8, 2.5); ok {
		t.Error("balanced tape fired")
	}

	// Insufficient windows → no signal.
	if _, ok := DetectImbalanceBars(bars[:5*30], 30, 8, 2.5); ok {
		t.Error("fired with <8 baseline windows")
	}
}

// ── volatility ───────────────────────────────────────────────────────────

// calmThenWildBars: gentle ±0.1% moves for `windows` windows, then a recent
// window of ±3% swings.
func calmThenWildBars(windows, w int) []md.Bar {
	var bars []md.Bar
	ts, px := int64(0), 100.0
	for i := 0; i < windows*w; i++ {
		amp := 0.001 * (1 + 0.2*math.Sin(float64(i))) // small varying moves
		next := px * (1 + amp*sign(i))
		bars = append(bars, bar(ts, px, math.Max(px, next), math.Min(px, next), next, 100))
		px = next
		ts += 60
	}
	for i := 0; i < w; i++ {
		next := px * (1 + 0.03*sign(i))
		bars = append(bars, bar(ts, px, math.Max(px, next), math.Min(px, next), next, 100))
		px = next
		ts += 60
	}
	return bars
}

func sign(i int) float64 {
	if i%2 == 0 {
		return 1
	}
	return -1
}

func TestDetectVolatility_RealizedVolSpike(t *testing.T) {
	bars := calmThenWildBars(10, 30)
	ev, ok := DetectVolatility(bars, 30, 8, 2.5, "1m")
	if !ok {
		t.Fatal("expected vol detection")
	}
	if ev.Kind != KindVol || ev.Z < 2.5 {
		t.Errorf("event = %+v", ev)
	}
	if !strings.Contains(ev.Detail, "realized vol") || !strings.Contains(ev.Detail, "1m") ||
		!strings.Contains(ev.Detail, "not a prediction") {
		t.Errorf("detail = %q", ev.Detail)
	}

	// Calm throughout → no signal.
	calm := calmThenWildBars(10, 30)[:10*30]
	if _, ok := DetectVolatility(calm, 30, 8, 2.5, "1m"); ok {
		t.Error("calm tape fired")
	}
	// Insufficient data → no signal (not even the TR fallback: <16 bars).
	if _, ok := DetectVolatility(calm[:10], 30, 8, 2.5, "1m"); ok {
		t.Error("fired on 10 bars")
	}
}

func TestDetectVolatility_TrueRangeSpikeFallback(t *testing.T) {
	// Too few bars for the windowed z (needs 270) but enough for ATR(14):
	// 20 tight bars then one huge-range bar.
	var bars []md.Bar
	ts := int64(0)
	for i := 0; i < 20; i++ {
		bars = append(bars, bar(ts, 100, 100.5, 99.5, 100, 100)) // TR ≈ 1
		ts += 60
	}
	bars = append(bars, bar(ts, 100, 106, 94, 95, 500)) // TR = 12 ≈ 12× ATR
	ev, ok := DetectVolatility(bars, 30, 8, 2.5, "1m")
	if !ok {
		t.Fatal("expected TR-spike detection")
	}
	if ev.Kind != KindVol || ev.Z < trSpikeRatio {
		t.Errorf("event = %+v", ev)
	}
	// Honesty: the detail must say the number is a RATIO, not a z-score.
	if !strings.Contains(ev.Detail, "true-range spike") || !strings.Contains(ev.Detail, "not a z-score") {
		t.Errorf("detail = %q", ev.Detail)
	}
}

// ── volume ───────────────────────────────────────────────────────────────

// minuteDays builds `days` consecutive days of 1m bars covering the same
// clock window each day (60 bars from 14:30 UTC), with per-day volume from
// volFor(day).
func minuteDays(days, barsPerDay int, volFor func(day int) float64) []md.Bar {
	var bars []md.Bar
	base := time.Date(2026, 6, 1, 14, 30, 0, 0, time.UTC)
	for d := 0; d < days; d++ {
		dayStart := base.AddDate(0, 0, d)
		for i := 0; i < barsPerDay; i++ {
			ts := dayStart.Add(time.Duration(i) * time.Minute).Unix()
			// tiny deterministic wobble so the baseline has dispersion
			v := volFor(d) * (1 + 0.01*math.Sin(float64(d*barsPerDay+i)))
			bars = append(bars, bar(ts, 10, 11, 9, 10.5, v))
		}
	}
	return bars
}

func TestDetectVolumeMinute_SameTimeOfDay(t *testing.T) {
	// 7 prior days of ~100/bar volume, final day 500/bar over the same clock
	// window → z way above threshold.
	bars := minuteDays(8, 60, func(d int) float64 {
		if d == 7 {
			return 500
		}
		return 100
	})
	// Recent window = the last 30 bars of the final day.
	ev, ok := DetectVolumeMinute(bars, 30, 5, 2.5, time.UTC)
	if !ok {
		t.Fatal("expected volume detection")
	}
	if ev.Kind != KindVolume || ev.Z < 2.5 {
		t.Errorf("event = %+v", ev)
	}
	if !strings.Contains(ev.Detail, "same-time-of-day") || !strings.Contains(ev.Detail, "not a prediction") {
		t.Errorf("detail = %q", ev.Detail)
	}

	// Normal final day → no signal.
	normal := minuteDays(8, 60, func(int) float64 { return 100 })
	if _, ok := DetectVolumeMinute(normal, 30, 5, 2.5, time.UTC); ok {
		t.Error("normal volume fired")
	}

	// Too few prior days → no signal even with huge volume.
	few := minuteDays(3, 60, func(d int) float64 {
		if d == 2 {
			return 500
		}
		return 100
	})
	if _, ok := DetectVolumeMinute(few, 30, 5, 2.5, time.UTC); ok {
		t.Error("fired with 2 baseline days (min 5)")
	}
	if _, ok := DetectVolumeMinute(nil, 30, 5, 2.5, time.UTC); ok {
		t.Error("fired on empty bars")
	}
}

func TestDetectVolumeDaily(t *testing.T) {
	var bars []md.Bar
	ts := int64(0)
	for i := 0; i < 60; i++ {
		bars = append(bars, bar(ts, 10, 11, 9, 10.5, 1000+float64(i%50)))
		ts += 86400
	}
	bars = append(bars, bar(ts, 10, 11, 9, 10.5, 10000)) // 10× day
	ev, ok := DetectVolumeDaily(bars, 60, 20, 2.5)
	if !ok {
		t.Fatal("expected daily volume detection")
	}
	if ev.Kind != KindVolume || ev.Z < 2.5 || !strings.Contains(ev.Detail, "trailing 60-day baseline") {
		t.Errorf("event = %+v", ev)
	}

	// Ordinary last day → quiet.
	if _, ok := DetectVolumeDaily(bars[:60], 60, 20, 2.5); ok {
		t.Error("ordinary day fired")
	}
	// Too few baseline days → no signal.
	if _, ok := DetectVolumeDaily(bars[50:], 60, 20, 2.5); ok {
		t.Error("fired with 10 baseline days (min 20)")
	}
}

// Regression for the apples-to-oranges z-stat: the baseline must be the MEANS
// of trailing recentSec-sized windows, not individual 1Hz snapshots. A window
// mean has ~sqrt(n) less dispersion than single snapshots, so the old
// per-snapshot baseline muted real detections by that factor. Construction:
// baseline snapshots alternate ±0.5 (huge per-snap dispersion ⇒ old z ≈ 0.5,
// silent) while the 12 window MEANS drift only 0.000…0.011 (tiny dispersion),
// so a sustained +0.25 recent mean is a monster anomaly — and must fire.
func TestDetectImbalanceSnaps_BaselineIsWindowMeans(t *testing.T) {
	const n = 3900 // 3600s baseline (12×300s windows) + 300s recent
	snaps := snapSeries(n, 1000, func(i int) float64 {
		if i >= 3600 {
			return 0.25 // recent: sustained but NOT extreme vs per-snap ±0.5
		}
		v := 0.5
		if i%2 == 1 {
			v = -0.5
		}
		return v + 0.001*float64(i/300) // per-window drift ⇒ nonzero mean dispersion
	})
	ev, ok := DetectImbalanceSnaps(snaps, 300, 3600, 2.5)
	if !ok {
		t.Fatal("window-mean baseline must detect a sustained shift the per-snapshot baseline muted (~sqrt(n) over-conservatism)")
	}
	if ev.Z < 10 { // per-snapshot baseline gave z≈0.5; window means give z≫10
		t.Errorf("z = %+.2f, want the un-muted window-mean scale (≫ 2.5)", ev.Z)
	}
	if !strings.Contains(ev.Detail, "trailing 5m windows") ||
		!strings.Contains(ev.Detail, "60m baseline") ||
		!strings.Contains(ev.Detail, "not a prediction") {
		t.Errorf("detail must state the window-means basis: %q", ev.Detail)
	}
}
