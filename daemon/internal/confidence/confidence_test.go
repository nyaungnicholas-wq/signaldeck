package confidence

import (
	"math"
	"strings"
	"testing"
)

func episodes(n int, entry float64, worstFrac float64) []Episode {
	out := make([]Episode, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, Episode{
			EntryPx: entry,
			Lows:    []float64{entry, entry * (1 - worstFrac), entry * 0.999},
			Fwd:     0.01,
		})
	}
	return out
}

func TestAdverseExcursionWithheldBelowFloor(t *testing.T) {
	x := AdverseExcursion(episodes(MinEpisodes-1, 100, 0.05))
	if x.Valid {
		t.Errorf("want withheld below %d episodes", MinEpisodes)
	}
	if x.Note == "" {
		t.Error("a withheld excursion must carry a reason")
	}
	if x.Mean != 0 || x.P90 != 0 {
		t.Error("a withheld excursion must not report numbers")
	}
}

func TestAdverseExcursionMeasuresWorstNotFinal(t *testing.T) {
	// Every episode dips 5% then recovers to -0.1%. The excursion is the DIP.
	x := AdverseExcursion(episodes(MinEpisodes, 100, 0.05))
	if !x.Valid {
		t.Fatalf("want a valid excursion: %s", x.Note)
	}
	if math.Abs(x.Mean-0.05) > 1e-9 {
		t.Errorf("mean excursion: want 0.05 (the dip, not the close), got %v", x.Mean)
	}
	if math.Abs(x.Worst-0.05) > 1e-9 {
		t.Errorf("worst: want 0.05, got %v", x.Worst)
	}
}

// The tail must be a real observation from the sample, and it must actually
// separate from the median when the distribution is skewed.
func TestAdverseExcursionTailSeparatesFromMedian(t *testing.T) {
	var eps []Episode
	// 85 mild episodes at 1%, 15 severe at 20%. Deliberately NOT a 90/10 split:
	// at exactly 90% the nearest-rank p90 sits on the boundary and correctly
	// returns the mild value, which would make this test assert a tie-break
	// convention rather than the property it cares about.
	for i := 0; i < 85; i++ {
		eps = append(eps, Episode{EntryPx: 100, Lows: []float64{99}})
	}
	for i := 0; i < 15; i++ {
		eps = append(eps, Episode{EntryPx: 100, Lows: []float64{80}})
	}
	x := AdverseExcursion(eps)
	if !x.Valid {
		t.Fatalf("want valid: %s", x.Note)
	}
	if math.Abs(x.Median-0.01) > 1e-9 {
		t.Errorf("median should be the mild case (0.01), got %v", x.Median)
	}
	if math.Abs(x.P90-0.20) > 1e-9 {
		t.Errorf("p90 should reach the severe case (0.20), got %v", x.P90)
	}
	if x.P90 <= x.Median {
		t.Error("a skewed sample must not report an equal median and tail")
	}
}

// An unusable path is SKIPPED, never counted as a painless episode.
func TestAdverseExcursionSkipsUnusableRatherThanScoringZero(t *testing.T) {
	eps := episodes(MinEpisodes, 100, 0.10)
	// Add junk that would drag the mean toward zero if it were counted as 0%.
	for i := 0; i < 50; i++ {
		eps = append(eps, Episode{EntryPx: 0, Lows: []float64{90}})
		eps = append(eps, Episode{EntryPx: 100, Lows: nil})
		eps = append(eps, Episode{EntryPx: 100, Lows: []float64{math.NaN()}})
	}
	x := AdverseExcursion(eps)
	if !x.Valid {
		t.Fatalf("the usable episodes should still qualify: %s", x.Note)
	}
	if x.N != MinEpisodes {
		t.Errorf("want only the %d usable episodes counted, got N=%d", MinEpisodes, x.N)
	}
	if math.Abs(x.Mean-0.10) > 1e-9 {
		t.Errorf("junk episodes contaminated the mean: got %v, want 0.10", x.Mean)
	}
	if !strings.Contains(x.Note, "skipped") {
		t.Errorf("the note should disclose the skipped episodes: %s", x.Note)
	}
}

// An episode that never dipped has a REAL zero excursion.
func TestAdverseExcursionZeroIsAMeasurement(t *testing.T) {
	var eps []Episode
	for i := 0; i < MinEpisodes; i++ {
		eps = append(eps, Episode{EntryPx: 100, Lows: []float64{101, 102}})
	}
	x := AdverseExcursion(eps)
	if !x.Valid {
		t.Fatalf("want valid: %s", x.Note)
	}
	if x.Mean != 0 || x.Worst != 0 {
		t.Errorf("a setup that never dipped should measure 0, got mean=%v worst=%v", x.Mean, x.Worst)
	}
}

// The core honesty property: unmeasurable fields are nil AND explained.
func TestAssessWithholdsRatherThanDefaulting(t *testing.T) {
	a := Assess(0.87, Evidence{N: 3, Accuracy: 1.0}, ConditionalReturn{}, Excursion{})
	if a.Probability != 0.87 {
		t.Errorf("probability should pass through, got %v", a.Probability)
	}
	if a.ExpectedReturn != nil || a.ExpectedDrawdown != nil || a.DrawdownTail != nil {
		t.Error("nothing was measurable — every derived field must be nil")
	}
	if a.Confidence != nil || a.Uncertainty != nil {
		t.Error("confidence must not be claimed on 3 observations")
	}
	if len(a.Withheld) < 4 {
		t.Errorf("every nil field needs a stated reason, got %v", a.Withheld)
	}
	if len(a.Reasons) == 0 {
		t.Error("an empty assessment must still explain itself")
	}
}

// A confident-looking probability from a model with no edge must score low
// confidence and say so in as many words.
func TestAssessSeparatesProbabilityFromConfidence(t *testing.T) {
	noEdge := Evidence{
		N: 500, EffectiveN: 500, Accuracy: 0.55, BaseRate: 0.55, // lift exactly 0
		CalibrationErr: 0.01, CalibrationKnown: true,
	}
	a := Assess(0.95, noEdge, ConditionalReturn{}, Excursion{})
	if a.Confidence == nil {
		t.Fatalf("with 500 effective observations confidence is measurable: %v", a.Withheld)
	}
	if *a.Confidence > 0.5 {
		t.Errorf("a zero-lift model must not score high confidence, got %v", *a.Confidence)
	}
	joined := strings.Join(a.Reasons, " | ")
	if !strings.Contains(joined, "NOT beaten its base rate") {
		t.Errorf("the no-edge case must be stated plainly: %s", joined)
	}

	// A real edge on the same sample size should score materially higher.
	edge := noEdge
	edge.Accuracy = 0.65 // +10 points of lift
	b := Assess(0.95, edge, ConditionalReturn{}, Excursion{})
	if b.Confidence == nil || *b.Confidence <= *a.Confidence {
		t.Errorf("a 10-point edge should beat a zero edge: %v vs %v", b.Confidence, a.Confidence)
	}
}

// An edge smaller than its own error bar is discounted, not celebrated.
func TestAssessDiscountsEdgeInsideItsErrorBar(t *testing.T) {
	// 40 effective observations: the Wilson half-width is wide (~0.15), so a
	// 2-point lift is well inside it. EffectiveN is what the interval reads —
	// the caller measured a design effect of 1 on this record.
	thin := Evidence{N: 40, EffectiveN: 40, Accuracy: 0.52, BaseRate: 0.50, CalibrationKnown: false}
	wide := Assess(0.6, thin, ConditionalReturn{}, Excursion{})
	if wide.Uncertainty == nil {
		t.Fatal("uncertainty should be measurable at n=40")
	}
	if *wide.Uncertainty < 0.05 {
		t.Errorf("a 40-observation interval should be wide, got %v", *wide.Uncertainty)
	}
	// Same lift, 20x the sample: the edge escapes the bar and must score higher.
	thick := Evidence{N: 800, EffectiveN: 800, Accuracy: 0.52, BaseRate: 0.50, CalibrationKnown: false}
	narrow := Assess(0.6, thick, ConditionalReturn{}, Excursion{})
	if *narrow.Confidence <= *wide.Confidence {
		t.Errorf("the same edge on 20x the data must score higher: %v vs %v",
			*narrow.Confidence, *wide.Confidence)
	}
	if *narrow.Uncertainty >= *wide.Uncertainty {
		t.Error("uncertainty must shrink with sample size")
	}
}

// Confidence stays in [0,1] under absurd inputs rather than escaping the scale.
func TestConfidenceStaysInRange(t *testing.T) {
	for _, ev := range []Evidence{
		{N: 100000, Accuracy: 1.0, BaseRate: 0.0, CalibrationErr: 0, CalibrationKnown: true},
		{N: 100, Accuracy: 0.0, BaseRate: 1.0, CalibrationErr: 5, CalibrationKnown: true},
		{N: 50, Accuracy: 0.5, BaseRate: 0.5, CalibrationErr: math.NaN(), CalibrationKnown: true},
	} {
		a := Assess(0.5, ev, ConditionalReturn{}, Excursion{})
		if a.Confidence == nil {
			continue
		}
		if *a.Confidence < 0 || *a.Confidence > 1 {
			t.Errorf("confidence %v out of [0,1] for %+v", *a.Confidence, ev)
		}
	}
}

// The full object, when everything IS measurable.
func TestAssessFullyPopulated(t *testing.T) {
	ev := Evidence{N: 400, EffectiveN: 380, Accuracy: 0.62, BaseRate: 0.52, CalibrationErr: 0.02, CalibrationKnown: true}
	ret := ConditionalReturn{Mean: 0.013, N: 120, Valid: true}
	ex := AdverseExcursion(episodes(MinEpisodes, 100, 0.04))

	a := Assess(0.71, ev, ret, ex)
	for name, p := range map[string]*float64{
		"expectedReturn":   a.ExpectedReturn,
		"expectedDrawdown": a.ExpectedDrawdown,
		"drawdownTail":     a.DrawdownTail,
		"confidence":       a.Confidence,
		"uncertainty":      a.Uncertainty,
	} {
		if p == nil {
			t.Errorf("%s should be populated when measurable", name)
		}
	}
	if len(a.Withheld) != 0 {
		t.Errorf("nothing should be withheld here, got %v", a.Withheld)
	}
	if *a.ExpectedReturn != 0.013 {
		t.Errorf("expected return: want 0.013, got %v", *a.ExpectedReturn)
	}
	if math.Abs(*a.ExpectedDrawdown-0.04) > 1e-9 {
		t.Errorf("expected drawdown: want 0.04, got %v", *a.ExpectedDrawdown)
	}
}

// Uncertainty is evaluated at the day-clustered EFFECTIVE sample size, and is
// WITHHELD — with a stated reason — when the caller measured none: an interval
// at the raw row count would assert independence the observations do not have,
// which is the A1 defect this field used to ship.
func TestUncertaintyReadsEffectiveNOrWithholds(t *testing.T) {
	base := Evidence{N: 2500, Accuracy: 0.5, BaseRate: 0.5}

	// No effective N measured: BOTH uncertainty and confidence are withheld, each
	// with a stated reason.
	//
	// This assertion was inverted deliberately on 2026-07-27, and the reason is
	// written here rather than in a commit message. It used to require that
	// confidence stayed scorable with no measured effective size — but a quarter
	// of the confidence composite IS a sample term, and with no certified sample
	// size that term was being computed from the raw row count: the very quantity
	// the sentence above calls "independence the observations do not have". The
	// result was a payload reading "we cannot say how uncertain this is, and we
	// are 0.97 confident", which is the package's honesty rule inverted. Nothing
	// was loosened to make a number appear; a number stopped appearing.
	a := Assess(0.5, base, ConditionalReturn{}, Excursion{})
	if a.Uncertainty != nil {
		t.Fatal("uncertainty must be withheld when no effective sample size was measured")
	}
	if a.Confidence != nil {
		t.Fatalf("confidence must be withheld too: its sample term has no honest denominator "+
			"without an effective size, got %v", *a.Confidence)
	}
	named, namedConf := false, false
	for _, w := range a.Withheld {
		if strings.HasPrefix(w, "uncertainty:") {
			named = true
		}
		if strings.HasPrefix(w, "confidence:") {
			namedConf = true
		}
	}
	if !namedConf {
		t.Errorf("the withheld confidence must be named with its reason, got %v", a.Withheld)
	}
	if !named {
		t.Errorf("the withheld uncertainty must be named with its reason, got %v", a.Withheld)
	}

	// A heavily clustered record (effective N far below N) must publish a WIDER
	// interval than a lightly clustered one — the whole point of the correction.
	// Both sit above MinEpisodes effective observations — the comparison being
	// made here is interval WIDTH, not whether the floor is cleared.
	clustered, independent := base, base
	clustered.EffectiveN = 40
	independent.EffectiveN = 2500
	wide := Assess(0.5, clustered, ConditionalReturn{}, Excursion{})
	narrow := Assess(0.5, independent, ConditionalReturn{}, Excursion{})
	if wide.Uncertainty == nil || narrow.Uncertainty == nil {
		t.Fatal("both intervals should be computable")
	}
	if *narrow.Uncertainty >= *wide.Uncertainty {
		t.Errorf("interval must narrow with effective n: %v (effN=2500) vs %v (effN=25)",
			*narrow.Uncertainty, *wide.Uncertainty)
	}

	// A domain-violating accuracy is withheld, not clamped into a number.
	for _, acc := range []float64{-0.1, 1.1, math.NaN()} {
		ev := base
		ev.EffectiveN = 100
		ev.Accuracy = acc
		if got := Assess(0.5, ev, ConditionalReturn{}, Excursion{}); got.Uncertainty != nil {
			t.Errorf("accuracy %v should withhold the uncertainty", acc)
		}
	}
}
