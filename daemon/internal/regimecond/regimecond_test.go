package regimecond

import (
	"math"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/regime"
)

// --- synthetic series builders -------------------------------------------------

// bar builds one OHLC bar around a close with a fixed intrabar range fraction.
// Open is set to the close; only H/L/C matter to the regime indicators.
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

// trend builds n bars whose close moves by perBar each step from start.
// perBar>0 => up, <0 => down. ts0 is the timestamp of the first bar.
func trend(n int, start, perBar, rangeFrac float64, ts0 int64) []marketdata.Bar {
	bars := make([]marketdata.Bar, n)
	c := start
	for i := 0; i < n; i++ {
		bars[i] = bar(ts0+int64(i*86400), c, rangeFrac)
		c += perBar
	}
	return bars
}

// concat joins bar segments into one series (timestamps come from the builders).
func concat(segs ...[]marketdata.Bar) []marketdata.Bar {
	var out []marketdata.Bar
	for _, s := range segs {
		out = append(out, s...)
	}
	return out
}

// upThenDown builds a clean uptrend section followed by a clean downtrend
// section, long enough that Build accumulates >= MinN observations in each of
// the uptrend and downtrend buckets.
func upThenDown() []marketdata.Bar {
	up := trend(150, 100, 0.8, 0.01, 0)
	// Continue from where the uptrend ended, then fall.
	startDown := 100 + 0.8*float64(150)
	down := trend(150, startDown, -0.8, 0.01, int64(150*86400))
	return concat(up, down)
}

// --- ForBars -------------------------------------------------------------------

func TestForBars(t *testing.T) {
	tests := []struct {
		name string
		h    marketdata.Horizon
		want int
	}{
		{"1d -> 1", marketdata.H1d, 1},
		{"1w -> 5", marketdata.H1w, 5},
		{"1h -> 0 (skip)", marketdata.H1h, 0},
		{"unknown -> 0 (skip)", marketdata.Horizon("bogus"), 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ForBars(tt.h); got != tt.want {
				t.Fatalf("ForBars(%q) = %d, want %d", tt.h, got, tt.want)
			}
		})
	}
}

// --- Build ---------------------------------------------------------------------

func TestBuildProducesBothBucketsWithSensibleSigns(t *testing.T) {
	bars := upThenDown()
	conds := Build(bars, 1)

	up, hasUp := conds["uptrend"]
	if !hasUp {
		t.Fatalf("expected an uptrend bucket, got labels: %v", keys(conds))
	}
	down, hasDown := conds["downtrend"]
	if !hasDown {
		t.Fatalf("expected a downtrend bucket, got labels: %v", keys(conds))
	}

	// Sensible signs: forward return after an uptrend bar is positive on
	// average (price kept rising); after a downtrend bar it is negative.
	if up.MeanFwd <= 0 {
		t.Errorf("uptrend MeanFwd = %.6f, want > 0", up.MeanFwd)
	}
	if up.MedianFwd <= 0 {
		t.Errorf("uptrend MedianFwd = %.6f, want > 0", up.MedianFwd)
	}
	if down.MeanFwd >= 0 {
		t.Errorf("downtrend MeanFwd = %.6f, want < 0", down.MeanFwd)
	}
	if down.MedianFwd >= 0 {
		t.Errorf("downtrend MedianFwd = %.6f, want < 0", down.MedianFwd)
	}

	// Hit rate: monotone up => every fwd return positive; monotone down => none.
	if up.HitRate <= 0.9 {
		t.Errorf("uptrend HitRate = %.3f, want ~1.0", up.HitRate)
	}
	if down.HitRate >= 0.1 {
		t.Errorf("downtrend HitRate = %.3f, want ~0.0", down.HitRate)
	}

	// Regime field is set and N is consistent.
	if up.Regime != "uptrend" || down.Regime != "downtrend" {
		t.Errorf("Regime fields wrong: up=%q down=%q", up.Regime, down.Regime)
	}
	if up.N < MinN || down.N < MinN {
		t.Errorf("N below MinN: up=%d down=%d (MinN=%d)", up.N, down.N, MinN)
	}
}

func TestBuildMinNFilter(t *testing.T) {
	// A series long enough to classify but arranged so one label appears only a
	// handful of times. We build a long uptrend (many uptrend labels) with a tiny
	// downtrend tail that yields fewer than MinN downtrend observations.
	up := trend(200, 100, 0.8, 0.01, 0)
	// Short down tail: only a few bars, and only bars with fwdBars ahead count.
	startDown := 100 + 0.8*float64(200)
	down := trend(3, startDown, -0.8, 0.01, int64(200*86400))
	bars := concat(up, down)

	conds := Build(bars, 1)

	// Uptrend is the dominant, well-populated bucket and must be present.
	if _, ok := conds["uptrend"]; !ok {
		t.Fatalf("expected uptrend bucket to survive MinN, labels: %v", keys(conds))
	}
	// Every reported bucket must satisfy N >= MinN.
	for label, c := range conds {
		if c.N < MinN {
			t.Errorf("bucket %q has N=%d < MinN=%d; should have been filtered", label, c.N, MinN)
		}
	}
}

func TestBuildNoLookaheadRegimeMatchesClassify(t *testing.T) {
	// Confirm the feature (label) at each bar is exactly regime.Classify(bars[:i+1])
	// and the outcome uses only the future close — i.e. Build's own labelling is
	// lookahead-free. We recompute independently and compare bucket counts.
	bars := upThenDown()
	fwdBars := 1

	want := map[string]int{}
	for i := regime.MinBars - 1; i <= len(bars)-fwdBars-1; i++ {
		if bars[i].Close <= 0 {
			continue
		}
		st, ok := regime.Classify(bars[:i+1])
		if !ok {
			continue
		}
		want[string(st.Label)]++
	}

	got := Build(bars, fwdBars)
	for label, n := range want {
		if n < MinN {
			continue // filtered out; not expected in got
		}
		c, ok := got[label]
		if !ok {
			t.Errorf("label %q expected in output (n=%d) but missing", label, n)
			continue
		}
		if c.N != n {
			t.Errorf("label %q N=%d, want %d (independent recompute)", label, c.N, n)
		}
	}
}

func TestBuildDeterministic(t *testing.T) {
	bars := upThenDown()
	a := Build(bars, 1)
	b := Build(bars, 1)
	if len(a) != len(b) {
		t.Fatalf("nondeterministic bucket set: %d vs %d", len(a), len(b))
	}
	for label, ca := range a {
		cb, ok := b[label]
		if !ok {
			t.Fatalf("label %q present in first run, missing in second", label)
		}
		if ca != cb {
			t.Errorf("label %q differs across runs: %+v vs %+v", label, ca, cb)
		}
	}
}

func TestBuildEdgeCases(t *testing.T) {
	tests := []struct {
		name    string
		bars    []marketdata.Bar
		fwdBars int
	}{
		{"nil bars", nil, 1},
		{"empty bars", []marketdata.Bar{}, 1},
		{"too few bars", trend(10, 100, 0.5, 0.01, 0), 1},
		{"exactly MinBars, no room for fwd", trend(regime.MinBars, 100, 0.5, 0.01, 0), 1},
		{"fwdBars zero", upThenDown(), 0},
		{"fwdBars negative", upThenDown(), -3},
		{"fwdBars larger than series", trend(regime.MinBars+2, 100, 0.5, 0.01, 0), 1000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Build(tt.bars, tt.fwdBars) // must not panic
			if got == nil {
				t.Fatalf("Build returned nil map; want empty non-nil map")
			}
			if len(got) != 0 {
				t.Errorf("Build = %d buckets, want 0 for edge case", len(got))
			}
		})
	}
}

func TestBuildSkipsNonPositiveBase(t *testing.T) {
	// Inject a zero close mid-series; Build must skip that base bar without
	// panicking or producing a division blow-up (would be -Inf/NaN).
	bars := upThenDown()
	bars[100].Close = 0
	got := Build(bars, 1) // must not panic
	for label, c := range got {
		if math.IsNaN(c.MeanFwd) || math.IsInf(c.MeanFwd, 0) {
			t.Errorf("bucket %q MeanFwd is non-finite: %v", label, c.MeanFwd)
		}
		if math.IsNaN(c.MedianFwd) || math.IsInf(c.MedianFwd, 0) {
			t.Errorf("bucket %q MedianFwd is non-finite: %v", label, c.MedianFwd)
		}
	}
}

func TestBuildWeekHorizon(t *testing.T) {
	// fwdBars=5 (a week). Uptrend forward returns compound over 5 bars, so the
	// mean is larger in magnitude than the 1-bar horizon but still positive.
	bars := upThenDown()
	conds := Build(bars, 5)
	up, ok := conds["uptrend"]
	if !ok {
		t.Fatalf("expected uptrend bucket at 5-bar horizon, labels: %v", keys(conds))
	}
	if up.MeanFwd <= 0 {
		t.Errorf("uptrend 5-bar MeanFwd = %.6f, want > 0", up.MeanFwd)
	}
	down, ok := conds["downtrend"]
	if !ok {
		t.Fatalf("expected downtrend bucket at 5-bar horizon, labels: %v", keys(conds))
	}
	if down.MeanFwd >= 0 {
		t.Errorf("downtrend 5-bar MeanFwd = %.6f, want < 0", down.MeanFwd)
	}
}

// --- Current -------------------------------------------------------------------

func TestCurrent(t *testing.T) {
	tests := []struct {
		name     string
		bars     []marketdata.Bar
		wantOK   bool
		wantOnev []string // acceptable labels (nil => any)
	}{
		{
			name:     "latest section is a downtrend",
			bars:     upThenDown(),
			wantOK:   true,
			wantOnev: []string{"downtrend"},
		},
		{
			name:     "latest section is an uptrend",
			bars:     trend(150, 100, 0.8, 0.01, 0),
			wantOK:   true,
			wantOnev: []string{"uptrend"},
		},
		{
			name:   "too few bars",
			bars:   trend(10, 100, 0.5, 0.01, 0),
			wantOK: false,
		},
		{
			name:   "nil bars",
			bars:   nil,
			wantOK: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			label, ok := Current(tt.bars)
			if ok != tt.wantOK {
				t.Fatalf("Current ok = %v, want %v (label=%q)", ok, tt.wantOK, label)
			}
			if !ok {
				if label != "" {
					t.Errorf("Current label = %q, want empty when ok=false", label)
				}
				return
			}
			if tt.wantOnev != nil && !contains(tt.wantOnev, label) {
				t.Errorf("Current label = %q, want one of %v", label, tt.wantOnev)
			}
		})
	}
}

func TestCurrentMatchesClassify(t *testing.T) {
	// Current must be exactly regime.Classify on the full series.
	bars := upThenDown()
	label, ok := Current(bars)
	st, cok := regime.Classify(bars)
	if ok != cok {
		t.Fatalf("Current ok=%v but Classify ok=%v", ok, cok)
	}
	if ok && label != string(st.Label) {
		t.Errorf("Current label = %q, Classify label = %q", label, st.Label)
	}
}

// --- helpers -------------------------------------------------------------------

// mean / median / hitRate unit checks (small, exact fixtures).
func TestStatHelpers(t *testing.T) {
	tests := []struct {
		name       string
		xs         []float64
		wantMean   float64
		wantMedian float64
		wantHit    float64
	}{
		{"empty", nil, 0, 0, 0},
		{"single positive", []float64{0.5}, 0.5, 0.5, 1},
		{"single negative", []float64{-0.5}, -0.5, -0.5, 0},
		{"odd length", []float64{-1, 0, 1}, 0, 0, 1.0 / 3.0},
		{"even length", []float64{-2, -1, 1, 2}, 0, 0, 0.5},
		{"all zero (no hits, > 0 strict)", []float64{0, 0, 0}, 0, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mean(tt.xs); !approx(got, tt.wantMean) {
				t.Errorf("mean = %v, want %v", got, tt.wantMean)
			}
			if got := median(tt.xs); !approx(got, tt.wantMedian) {
				t.Errorf("median = %v, want %v", got, tt.wantMedian)
			}
			if got := hitRate(tt.xs); !approx(got, tt.wantHit) {
				t.Errorf("hitRate = %v, want %v", got, tt.wantHit)
			}
		})
	}
}

func TestMedianDoesNotMutateInput(t *testing.T) {
	xs := []float64{3, 1, 2}
	orig := []float64{3, 1, 2}
	_ = median(xs)
	for i := range xs {
		if xs[i] != orig[i] {
			t.Fatalf("median mutated input at %d: %v != %v", i, xs[i], orig[i])
		}
	}
}

func keys(m map[string]Cond) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func approx(a, b float64) bool {
	return math.Abs(a-b) < 1e-9
}
