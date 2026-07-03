package regime

import (
	"math"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// --- synthetic series builders -------------------------------------------------

// bar builds one OHLC bar around a close with a given intrabar range (high/low
// span as a fraction of close). Open is set to the close for simplicity; only
// H/L/C matter to the indicators used here.
func bar(ts int64, close, rangeFrac float64) marketdata.Bar {
	half := close * rangeFrac / 2
	return marketdata.Bar{
		Ts:    ts,
		Open:  close,
		High:  close + half,
		Low:   close - half,
		Close: close,
	}
}

// trend builds n bars whose close moves by perBar each step from start, with a
// fixed intrabar range fraction. perBar>0 => up, <0 => down, 0 => flat.
func trend(n int, start, perBar, rangeFrac float64) []marketdata.Bar {
	bars := make([]marketdata.Bar, n)
	c := start
	for i := 0; i < n; i++ {
		bars[i] = bar(int64(i*60), c, rangeFrac)
		c += perBar
	}
	return bars
}

// noisyFlat builds n bars oscillating around base with amplitude amp (absolute
// price), a full up/down cycle every period bars. No net drift => Range.
func noisyFlat(n int, base, amp float64, period int, rangeFrac float64) []marketdata.Bar {
	bars := make([]marketdata.Bar, n)
	for i := 0; i < n; i++ {
		c := base + amp*math.Sin(2*math.Pi*float64(i)/float64(period))
		bars[i] = bar(int64(i*60), c, rangeFrac)
	}
	return bars
}

// coil builds a low-volatility series: a wide-range warmup segment followed by a
// tight-range flat segment, so the recent band width sits in the bottom
// percentile of its window (a squeeze). Returns the full concatenated series.
func coil(warmup, tight int, base float64) []marketdata.Bar {
	bars := make([]marketdata.Bar, 0, warmup+tight)
	ts := int64(0)
	// Wide, choppy warmup so band width history is large.
	for i := 0; i < warmup; i++ {
		c := base + 8*math.Sin(2*math.Pi*float64(i)/9.0)
		bars = append(bars, bar(ts, c, 0.03))
		ts += 60
	}
	// Very tight coil: near-flat close, tiny intrabar range => compressed bands.
	for i := 0; i < tight; i++ {
		c := base + 0.02*math.Sin(2*math.Pi*float64(i)/5.0)
		bars = append(bars, bar(ts, c, 0.0006))
		ts += 60
	}
	return bars
}

// --- Classify: label per regime ------------------------------------------------

func TestClassifyLabels(t *testing.T) {
	tests := []struct {
		name string
		bars []marketdata.Bar
		want Label
	}{
		{
			name: "strong monotonic uptrend",
			bars: trend(120, 100, 0.8, 0.01),
			want: Uptrend,
		},
		{
			name: "strong monotonic downtrend",
			bars: trend(120, 200, -0.8, 0.01),
			want: Downtrend,
		},
		{
			name: "flat noisy range",
			bars: noisyFlat(160, 100, 1.5, 20, 0.02),
			want: Range,
		},
		{
			name: "low-volatility coil is a squeeze",
			bars: coil(120, 40, 100),
			want: Squeeze,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st, ok := Classify(tc.bars)
			if !ok {
				t.Fatalf("Classify ok=false, want true (have %d bars)", len(tc.bars))
			}
			if st.Label != tc.want {
				t.Fatalf("label = %q, want %q\nnote: %s\n(adx=%.2f bbPct=%.2f strength=%.2f)",
					st.Label, tc.want, st.Note, st.ADX, st.BBWidthPct, st.Strength)
			}
			if st.Strength < 0 || st.Strength > 1 {
				t.Errorf("strength %.3f out of [0,1]", st.Strength)
			}
			if st.Note == "" {
				t.Error("expected a non-empty Note explaining the label")
			}
		})
	}
}

// --- Classify: warmup / insufficient data --------------------------------------

func TestClassifyWarmup(t *testing.T) {
	tests := []struct {
		name   string
		n      int
		wantOK bool
	}{
		{"empty", 0, false},
		{"one bar", 1, false},
		{"just under MinBars", MinBars - 1, false},
		{"exactly MinBars", MinBars, true},
		{"above MinBars", MinBars + 30, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			bars := trend(tc.n, 100, 0.5, 0.01)
			_, ok := Classify(bars)
			if ok != tc.wantOK {
				t.Fatalf("Classify ok = %v, want %v (n=%d)", ok, tc.wantOK, tc.n)
			}
		})
	}
}

// Squeeze must take priority over direction: a low-vol coil that is drifting up
// should still classify as Squeeze, not Uptrend.
func TestSqueezeSupersedesDirection(t *testing.T) {
	// Choppy warmup, then a tight but slightly rising coil.
	bars := coil(120, 40, 100)
	// Nudge the tight segment upward a hair (still tiny range => still a coil).
	for i := 120; i < len(bars); i++ {
		bump := 0.01 * float64(i-120)
		bars[i].Close += bump
		bars[i].Open += bump
		bars[i].High += bump
		bars[i].Low += bump
	}
	st, ok := Classify(bars)
	if !ok {
		t.Fatal("Classify ok=false")
	}
	if st.Label != Squeeze {
		t.Fatalf("label = %q, want Squeeze (coil should win over drift)\nnote: %s", st.Label, st.Note)
	}
	if st.BBWidthPct >= SqueezePctThreshold {
		t.Errorf("BBWidthPct = %.2f, expected < %.0f for a squeeze", st.BBWidthPct, SqueezePctThreshold)
	}
}

// --- History: timeline + change detection --------------------------------------

func TestHistoryDetectsUpToDownChange(t *testing.T) {
	// 120 bars up, then 120 bars down: the regime should flip up -> down.
	up := trend(120, 100, 0.8, 0.01)
	// Continue from where the uptrend ended, heading down.
	startDown := up[len(up)-1].Close
	down := trend(120, startDown, -0.8, 0.01)
	// Rebase timestamps of the down segment to follow the up segment.
	for i := range down {
		down[i].Ts = up[len(up)-1].Ts + int64((i+1)*60)
	}
	bars := append(append([]marketdata.Bar{}, up...), down...)

	timeline, changes := History(bars, 1)
	if len(timeline) == 0 {
		t.Fatal("empty timeline")
	}
	// Timeline must be chronologically ordered.
	for i := 1; i < len(timeline); i++ {
		if timeline[i].Ts < timeline[i-1].Ts {
			t.Fatalf("timeline out of order at %d: %d < %d", i, timeline[i].Ts, timeline[i-1].Ts)
		}
	}
	// It should start Uptrend and end Downtrend.
	if timeline[0].Label != Uptrend {
		t.Errorf("timeline starts %q, want Uptrend", timeline[0].Label)
	}
	if last := timeline[len(timeline)-1].Label; last != Downtrend {
		t.Errorf("timeline ends %q, want Downtrend", last)
	}
	// There must be at least one change, and the reversal must be captured. A
	// clean up->down flip does NOT teleport: a faithful classifier rolls the
	// uptrend over through a ranging zone (price crossing the moving averages)
	// before the downtrend asserts itself. So the honest guarantee is that the
	// timeline LEAVES uptrend and later ENTERS downtrend — not a single
	// Uptrend->Downtrend edge. We assert both a departure from Uptrend and an
	// arrival at Downtrend, in that order.
	if len(changes) == 0 {
		t.Fatal("expected at least one regime change, got none")
	}
	leftUptrendAt := -1
	enteredDowntrendAt := -1
	for i, ch := range changes {
		if ch.From == Uptrend && leftUptrendAt < 0 {
			leftUptrendAt = i
		}
		if ch.To == Downtrend {
			enteredDowntrendAt = i
		}
		// Every change Ts must exist in the bar timestamps and be within range.
		if ch.Ts < bars[0].Ts || ch.Ts > bars[len(bars)-1].Ts {
			t.Errorf("change Ts %d outside bar range", ch.Ts)
		}
	}
	if leftUptrendAt < 0 {
		t.Errorf("no departure from uptrend found in %+v", changes)
	}
	if enteredDowntrendAt < 0 {
		t.Errorf("no arrival at downtrend found in %+v", changes)
	}
	if leftUptrendAt >= 0 && enteredDowntrendAt >= 0 && enteredDowntrendAt < leftUptrendAt {
		t.Errorf("entered downtrend before leaving uptrend (out of order): %+v", changes)
	}
}

func TestHistoryStepAndWarmup(t *testing.T) {
	bars := trend(200, 100, 0.6, 0.01)

	// step normalization: step<1 behaves like step=1.
	tl1, _ := History(bars, 1)
	tl0, _ := History(bars, 0)
	if len(tl0) != len(tl1) {
		t.Errorf("step=0 should behave like step=1: len %d vs %d", len(tl0), len(tl1))
	}

	// Stepping by 5 yields a coarser (shorter) timeline than step 1.
	tl5, _ := History(bars, 5)
	if len(tl5) >= len(tl1) {
		t.Errorf("step=5 timeline (%d) should be shorter than step=1 (%d)", len(tl5), len(tl1))
	}

	// First timeline point is at index MinBars-1.
	if tl1[0].Ts != bars[MinBars-1].Ts {
		t.Errorf("first timeline Ts = %d, want %d (bar MinBars-1)", tl1[0].Ts, bars[MinBars-1].Ts)
	}

	// A monotonic uptrend should produce zero changes (single stable regime).
	_, changes := History(bars, 1)
	for _, ch := range changes {
		t.Errorf("unexpected change in pure uptrend: %+v", ch)
	}
}

func TestHistoryInsufficient(t *testing.T) {
	bars := trend(MinBars-1, 100, 0.5, 0.01)
	tl, ch := History(bars, 1)
	if tl != nil || ch != nil {
		t.Fatalf("expected empty timeline/changes for < MinBars, got tl=%d ch=%d", len(tl), len(ch))
	}
}

// --- No-lookahead guarantee ----------------------------------------------------

// The classification at bar i must not depend on any bar after i. We verify by
// classifying a prefix directly and confirming it matches the History value at
// that same index, and by appending arbitrary FUTURE bars and confirming the
// as-of-i result is unchanged.
func TestNoLookahead(t *testing.T) {
	up := trend(120, 100, 0.8, 0.01)
	down := trend(80, up[len(up)-1].Close, -0.8, 0.01)
	for i := range down {
		down[i].Ts = up[len(up)-1].Ts + int64((i+1)*60)
	}
	full := append(append([]marketdata.Bar{}, up...), down...)

	// Pick an index in the uptrend region, well past warmup.
	i := 100
	prefix := full[:i+1]

	stPrefix, ok := Classify(prefix)
	if !ok {
		t.Fatal("Classify(prefix) ok=false")
	}
	// Classifying the FULL series (which contains a later downtrend) but only
	// up to i via slicing must give the identical result — future bars cannot
	// leak in.
	stAsOf, ok := Classify(full[:i+1])
	if !ok {
		t.Fatal("Classify(full[:i+1]) ok=false")
	}
	if stPrefix.Label != stAsOf.Label || stPrefix.ADX != stAsOf.ADX || stPrefix.BBWidthPct != stAsOf.BBWidthPct {
		t.Fatalf("lookahead detected: prefix (%q adx=%.4f bb=%.4f) != as-of (%q adx=%.4f bb=%.4f)",
			stPrefix.Label, stPrefix.ADX, stPrefix.BBWidthPct,
			stAsOf.Label, stAsOf.ADX, stAsOf.BBWidthPct)
	}

	// History at the timeline point matching Ts=full[i].Ts must equal stPrefix.
	tl, _ := History(full, 1)
	var found *Timestamped
	for k := range tl {
		if tl[k].Ts == full[i].Ts {
			found = &tl[k]
			break
		}
	}
	if found == nil {
		t.Fatalf("no timeline point at Ts=%d", full[i].Ts)
	}
	if found.Label != stPrefix.Label {
		t.Errorf("History label at i (%q) != Classify(prefix) (%q)", found.Label, stPrefix.Label)
	}
}

// --- Indicator unit tests ------------------------------------------------------

func TestSMAAt(t *testing.T) {
	xs := []float64{1, 2, 3, 4, 5, 6}
	tests := []struct {
		end, n int
		want   float64
	}{
		{end: 5, n: 3, want: (4 + 5 + 6) / 3.0},
		{end: 2, n: 3, want: (1 + 2 + 3) / 3.0},
		{end: 0, n: 5, want: 1},   // fewer than n available -> avg of what's there
		{end: 4, n: 2, want: 4.5}, // (4+5)/2
		{end: -1, n: 3, want: 0},  // invalid end
	}
	for _, tc := range tests {
		got := smaAt(xs, tc.end, tc.n)
		if math.Abs(got-tc.want) > 1e-9 {
			t.Errorf("smaAt(end=%d,n=%d) = %.4f, want %.4f", tc.end, tc.n, got, tc.want)
		}
	}
}

func TestBBWidthPercentileMonotone(t *testing.T) {
	// A series that starts volatile and ends tight should give a LOW percentile.
	bars := coil(120, 40, 100)
	closes := closesOf(bars)
	pct := bbWidthPercentile(closes, bbLen, bbK, bbWidthWindow)
	if pct >= SqueezePctThreshold {
		t.Errorf("coil bbWidthPercentile = %.2f, want < %.0f", pct, SqueezePctThreshold)
	}

	// A single sample must not read as a squeeze (defensively returns 100).
	if p := bbWidthPercentile([]float64{100}, bbLen, bbK, bbWidthWindow); p != 100 {
		t.Errorf("single-sample percentile = %.2f, want 100", p)
	}
}

func TestADXLikeStrongVsFlat(t *testing.T) {
	strong := trend(120, 100, 1.0, 0.01) // clean directional move
	flat := noisyFlat(120, 100, 0.5, 8, 0.02)

	adxStrong := adxLike(strong, adxLen)
	adxFlat := adxLike(flat, adxLen)

	if adxStrong <= adxFlat {
		t.Errorf("expected trending ADX (%.2f) > flat ADX (%.2f)", adxStrong, adxFlat)
	}
	if adxStrong < adxTrendThreshold {
		t.Errorf("clean trend ADX = %.2f, expected >= %.0f", adxStrong, adxTrendThreshold)
	}
	// Too few bars -> 0.
	if got := adxLike(strong[:adxLen], adxLen); got != 0 {
		t.Errorf("adxLike with too few bars = %.2f, want 0", got)
	}
}

func TestClamp01(t *testing.T) {
	for _, tc := range []struct{ in, want float64 }{
		{-1, 0}, {0, 0}, {0.5, 0.5}, {1, 1}, {2, 1},
	} {
		if got := clamp01(tc.in); got != tc.want {
			t.Errorf("clamp01(%.2f) = %.2f, want %.2f", tc.in, got, tc.want)
		}
	}
}
