package candles

import (
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// engulfMotif tiles a 6-bar dip-then-rise motif whose 2nd bar is a bullish
// engulfing at a local low, followed by a steady rise — so the forward 5-bar
// return after each engulfing is reliably positive (bias-aligned). base rises
// each motif, so the series drifts up.
func engulfSeries(motifs int) []marketdata.Bar {
	var bars []marketdata.Bar
	ts := int64(0)
	L := 100.0
	push := func(o, h, l, c float64) {
		bars = append(bars, mkBar(ts, o, h, l, c))
		ts++
	}
	for m := 0; m < motifs; m++ {
		push(L+2, L+2.5, L-0.5, L)   // b0: bearish
		push(L-1, L+4, L-1.5, L+3.5) // b1: bullish engulfing at the low
		push(L+3, L+5, L+2.5, L+4.5) // b2..b5: steady rise
		push(L+4, L+6, L+3.5, L+5.5) //
		push(L+5, L+7, L+4.5, L+6.5) //
		push(L+6, L+8, L+5.5, L+7.5) //
		L += 7                       // next motif continues the advance
	}
	return bars
}

func find(stats []EdgeStat, name string) (EdgeStat, bool) {
	for _, s := range stats {
		if s.Pattern == name {
			return s, true
		}
	}
	return EdgeStat{}, false
}

func TestMeasureEdgesHitRateAndSample(t *testing.T) {
	bars := engulfSeries(30) // ~30 engulfing firings, each followed by a rise
	stats := MeasureEdges(bars, 5)
	be, ok := find(stats, "bullish_engulfing")
	if !ok {
		t.Fatalf("bullish_engulfing edge missing; got %v", stats)
	}
	if be.N < MinPatternN {
		t.Fatalf("N=%d < MinPatternN=%d — gate should have withheld or sample too thin", be.N, MinPatternN)
	}
	if be.Horizon != 5 {
		t.Fatalf("horizon=%d, want 5", be.Horizon)
	}
	if be.HitRate < 0.9 {
		t.Fatalf("hitRate=%.2f, want ~1.0 for an always-up-followed engulfing", be.HitRate)
	}
	if be.MeanFwd <= 0 {
		t.Fatalf("meanFwd=%.4f, want positive", be.MeanFwd)
	}
	if be.Bias != +1 {
		t.Fatalf("bias=%d, want +1", be.Bias)
	}
}

func TestMeasureEdgesGatesThinSamples(t *testing.T) {
	bars := engulfSeries(3) // only ~3 firings — below MinPatternN
	stats := MeasureEdges(bars, 5)
	if _, ok := find(stats, "bullish_engulfing"); ok {
		t.Fatalf("thin sample (<%d) must be withheld, got %v", MinPatternN, stats)
	}
}

func TestMeasureEdgesExcludesNeutralPatterns(t *testing.T) {
	// A long series peppered with dojis: neutral (bias 0) patterns must never
	// appear in the edge table (no direction to grade).
	var bars []marketdata.Bar
	for i := 0; i < 200; i++ {
		bars = append(bars, mkBar(int64(i), 100, 105, 95, 100.1)) // doji every bar
	}
	stats := MeasureEdges(bars, 5)
	for _, s := range stats {
		if s.Bias == 0 {
			t.Fatalf("neutral pattern %q leaked into edge stats", s.Pattern)
		}
	}
	if _, ok := find(stats, "doji"); ok {
		t.Fatal("doji (neutral) must be excluded from measured edge")
	}
}

func TestMeasureEdgesNoLookaheadShortInput(t *testing.T) {
	// Fewer bars than the horizon → no outcome exists → empty, no panic.
	bars := engulfSeries(1)[:3]
	if stats := MeasureEdges(bars, 5); len(stats) != 0 {
		t.Fatalf("too-short input produced %v, want none", stats)
	}
}

func TestMeasureEdgesDefaultHorizon(t *testing.T) {
	bars := engulfSeries(30)
	stats := MeasureEdges(bars, 0) // 0 → DefaultHorizon
	be, ok := find(stats, "bullish_engulfing")
	if !ok {
		t.Fatalf("expected an edge with default horizon; got %v", stats)
	}
	if be.Horizon != DefaultHorizon {
		t.Fatalf("horizon=%d, want DefaultHorizon=%d", be.Horizon, DefaultHorizon)
	}
}
