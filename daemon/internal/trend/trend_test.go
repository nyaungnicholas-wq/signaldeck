package trend

import (
	"math"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// zigzag builds an oscillating series with a period-8 triangle wave riding a
// linear drift, so it has clean, well-separated swing highs (apex) and swing
// lows (trough) that lie on parallel drifting lines. drift>0 → uptrend,
// drift<0 → downtrend, drift==0 → range.
func zigzag(n int, drift, amp float64) []marketdata.Bar {
	bars := make([]marketdata.Bar, n)
	for i := 0; i < n; i++ {
		phase := i % 8
		var tri float64
		if phase <= 4 {
			tri = float64(phase) / 4.0
		} else {
			tri = float64(8-phase) / 4.0
		}
		// Wicks centred on close so each apex/trough is a STRICT high/low pivot
		// (no shared open/close level between neighbouring bars).
		c := 100.0 + drift*float64(i) + amp*tri
		bars[i] = marketdata.Bar{Ts: int64(i) * 86400, Open: c, High: c + 1, Low: c - 1, Close: c}
	}
	return bars
}

func lineOfKind(res Result, kind string) (Line, bool) {
	for _, l := range res.Trendlines {
		if l.Kind == kind {
			return l, true
		}
	}
	return Line{}, false
}

func TestClassifyUptrend(t *testing.T) {
	res, ok := Analyze(zigzag(48, 0.5, 8))
	if !ok {
		t.Fatal("Analyze ok=false on a 48-bar window")
	}
	if res.Class != Uptrend {
		t.Fatalf("class=%q, want uptrend (slope=%.5f)", res.Class, res.SlopePctPerBar)
	}
	if res.SlopePctPerBar <= 0 {
		t.Fatalf("slopePctPerBar=%.5f, want positive", res.SlopePctPerBar)
	}
}

func TestClassifyDowntrend(t *testing.T) {
	res, ok := Analyze(zigzag(48, -0.5, 8))
	if !ok {
		t.Fatal("Analyze ok=false")
	}
	if res.Class != Downtrend {
		t.Fatalf("class=%q, want downtrend (slope=%.5f)", res.Class, res.SlopePctPerBar)
	}
	if res.SlopePctPerBar >= 0 {
		t.Fatalf("slopePctPerBar=%.5f, want negative", res.SlopePctPerBar)
	}
}

func TestClassifyRange(t *testing.T) {
	res, ok := Analyze(zigzag(48, 0, 8))
	if !ok {
		t.Fatal("Analyze ok=false")
	}
	if res.Class != Range {
		t.Fatalf("class=%q, want range (slope=%.5f)", res.Class, res.SlopePctPerBar)
	}
	if math.Abs(res.SlopePctPerBar) > slopeMinPctPerBar {
		t.Fatalf("slopePctPerBar=%.5f, want ~flat", res.SlopePctPerBar)
	}
}

func TestAscendingSeriesSupportLineTwoTouches(t *testing.T) {
	res, ok := Analyze(zigzag(48, 0.5, 8))
	if !ok {
		t.Fatal("Analyze ok=false")
	}
	sup, ok := lineOfKind(res, "support")
	if !ok {
		t.Fatalf("no support line fitted; trendlines=%v", res.Trendlines)
	}
	if sup.TouchCount < 2 {
		t.Fatalf("support touchCount=%d, want >= 2", sup.TouchCount)
	}
	// An ascending support line must slope upward in price.
	if sup.ToPrice <= sup.FromPrice {
		t.Fatalf("support line not ascending: from=%.2f to=%.2f", sup.FromPrice, sup.ToPrice)
	}
	if sup.ToTs <= sup.FromTs {
		t.Fatalf("support endpoints out of order: fromTs=%d toTs=%d", sup.FromTs, sup.ToTs)
	}
}

func TestChannelDetectedOnParallelZigzag(t *testing.T) {
	res, ok := Analyze(zigzag(48, 0.5, 8))
	if !ok {
		t.Fatal("Analyze ok=false")
	}
	if !res.Channel {
		t.Fatal("expected a channel on the parallel-drift zigzag")
	}
	// Both a support and a resistance line should be present for a channel.
	if _, ok := lineOfKind(res, "support"); !ok {
		t.Fatal("channel without a support line")
	}
	if _, ok := lineOfKind(res, "resistance"); !ok {
		t.Fatal("channel without a resistance line")
	}
}

func TestThinHistoryNotOK(t *testing.T) {
	if _, ok := Analyze(zigzag(MinBars-1, 0.5, 8)); ok {
		t.Fatalf("Analyze should return ok=false below MinBars=%d", MinBars)
	}
}

func TestNoLookaheadRecentBarsNotPivots(t *testing.T) {
	// The final swingWindow bars can never be confirmed pivots — no support/
	// resistance endpoint should sit in that trailing zone.
	bars := zigzag(48, 0.5, 8)
	res, _ := Analyze(bars)
	cutoff := bars[len(bars)-1-swingWindow].Ts
	for _, l := range res.Trendlines {
		if l.ToTs > cutoff {
			t.Fatalf("%s endpoint ts=%d is inside the unconfirmable trailing window (> %d)", l.Kind, l.ToTs, cutoff)
		}
	}
}
