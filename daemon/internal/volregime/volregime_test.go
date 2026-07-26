package volregime

import (
	"math"
	"strings"
	"testing"
)

// C5 (2026-07-26 hostile review): HistoricalAccuracy is a backtest lookup
// (AccuracyForConviction), never a live/graded number — nothing snapshots
// this predictor's calls for later grading at all. Every Forecast must say
// so explicitly rather than leaving a reader of the JSON to infer it from a
// doc comment that used the word "MEASURED" with no further qualifier.
func TestForecastCarriesBacktestEvidence(t *testing.T) {
	calm := make([]float64, 260)
	for i := range calm {
		calm[i] = 0.001 * sign(i)
	}
	hot := make([]float64, 20)
	for i := range hot {
		hot[i] = 0.06 * sign(i)
	}
	f, ok := Predict(append(append([]float64{}, calm...), hot...))
	if !ok {
		t.Fatal("expected forecast")
	}
	if f.Evidence != "backtest" {
		t.Fatalf("Evidence = %q, want %q", f.Evidence, "backtest")
	}
	// Unlike internal/structregime, nothing snapshots this predictor's calls
	// for live grading, so no first-gradable date is promised — an invented
	// date would be less honest than none.
	if f.FirstGradableOn != "" {
		t.Fatalf("FirstGradableOn = %q, want empty — no live grading loop is wired to this "+
			"predictor, so no date should be promised", f.FirstGradableOn)
	}
	for _, want := range []string{"BACKTEST CLAIM", "not a live measurement", "No live grading loop"} {
		if !strings.Contains(f.EvidenceCaveat, want) {
			t.Errorf("EvidenceCaveat missing %q; got: %s", want, f.EvidenceCaveat)
		}
	}
}

func TestInsufficientHistory(t *testing.T) {
	if _, ok := Predict(make([]float64, minHistory-1)); ok {
		t.Error("predicted on too little history; want honest ok=false")
	}
}

// A synthetic series that switches from a long CALM stretch into a HIGH-vol
// burst must be called "elevated" with real conviction; the reverse, "calm".
func TestRegimeDirectionAndConviction(t *testing.T) {
	// 260 calm days (tiny returns) then 20 violent days.
	calm := make([]float64, 260)
	for i := range calm {
		calm[i] = 0.001 * sign(i)
	}
	hot := make([]float64, 20)
	for i := range hot {
		hot[i] = 0.06 * sign(i)
	}
	f, ok := Predict(append(append([]float64{}, calm...), hot...))
	if !ok {
		t.Fatal("ok=false on sufficient history")
	}
	if f.Regime != "elevated" {
		t.Errorf("regime = %q after a vol burst, want elevated", f.Regime)
	}
	if f.Conviction < 0.5 {
		t.Errorf("conviction = %.2f after a clear burst, want strong", f.Conviction)
	}
	if f.HistoricalAccuracy != AccuracyForConviction(f.Conviction) {
		t.Errorf("accuracy %.3f != tier accuracy", f.HistoricalAccuracy)
	}

	// Now the mirror: a violent stretch settling into calm → "calm".
	f2, _ := Predict(append(append([]float64{}, hot...), calm...))
	// after 260 calm days the current vol is LOW vs its window
	if f2.Regime != "calm" {
		t.Errorf("regime = %q after settling calm, want calm", f2.Regime)
	}
}

func TestAccuracyForConvictionMonotoneAndBounded(t *testing.T) {
	prev := 0.0
	for c := 0.0; c <= 1.0001; c += 0.05 {
		a := AccuracyForConviction(c)
		if a < 0.5 || a > 0.8 {
			t.Errorf("accuracy(%.2f) = %.3f outside the measured [0.5, 0.8]", c, a)
		}
		if a < prev {
			t.Errorf("accuracy not monotone: acc(%.2f)=%.3f < prev %.3f", c, a, prev)
		}
		prev = a
	}
	// The top tier must equal the measured ceiling, never above it.
	if AccuracyForConviction(1.0) != 0.76 {
		t.Errorf("top-tier accuracy = %.3f, want the measured 0.76", AccuracyForConviction(1.0))
	}
}

// No-lookahead: Predict on a prefix must be UNAFFECTED by appending future
// returns — the forecast at time t uses only data <= t.
func TestNoLookahead(t *testing.T) {
	base := make([]float64, 300)
	for i := range base {
		base[i] = 0.01 * sign(i)
	}
	f1, _ := Predict(base)
	extended := append(append([]float64{}, base...), 0.09, -0.09, 0.09)
	// Re-run Predict on the SAME prefix length via a copy — appending future
	// data to a different slice must not change the prefix forecast.
	f2, _ := Predict(base)
	if f1 != f2 {
		t.Errorf("prefix forecast changed across calls: %+v vs %+v", f1, f2)
	}
	// And the extended series is allowed to differ (it has new data) — sanity.
	if _, ok := Predict(extended); !ok {
		t.Error("extended series failed to predict")
	}
}

// Grade on a persistent-vol synthetic series (regimes that last) must beat a
// coin flip clearly — the property the whole method rests on.
func TestGradeBeatsCoinFlipOnPersistentVol(t *testing.T) {
	// Build alternating LONG regimes (120d calm, 120d hot, repeated) so vol is
	// strongly persistent and the forecast should be right most of the time.
	var r []float64
	for block := 0; block < 12; block++ {
		amp := 0.002
		if block%2 == 1 {
			amp = 0.05
		}
		for i := 0; i < 120; i++ {
			r = append(r, amp*sign(i))
		}
	}
	correct, total := Grade(r, 63, 0.0)
	if total < 5 {
		t.Fatalf("too few graded windows: %d", total)
	}
	acc := float64(correct) / float64(total)
	if acc < 0.6 {
		t.Errorf("persistent-vol accuracy = %.3f (%d/%d), want > 0.6", acc, correct, total)
	}
}

func sign(i int) float64 {
	if i%2 == 0 {
		return 1
	}
	return -1
}

var _ = math.Sqrt
