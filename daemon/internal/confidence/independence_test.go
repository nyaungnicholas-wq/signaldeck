// The evidence floor and the sample term must count what the record HOLDS, not
// how many rows it has.
//
// This package already knows the distinction — Evidence.EffectiveN exists
// precisely because "rows on one day share one market move, and an interval
// over raw rows was measured ~3.8x too narrow" — and then spent it on the
// uncertainty alone. Both the "is there enough to be confident about" gate and
// the sample term of the confidence score went on counting raw rows, so a record
// whose 300 rows carry the information of 20 scored a full sample term and
// cleared a floor written in independent observations.
//
// Measured on the live 1d directional record (2026-07-27): 13,065 rows over 24
// distinct days, design effect 14.69, effective n 889. The saturation point of
// the sample term is 300, so today both routes saturate and no published number
// moves — which is exactly why this is worth pinning now rather than after a
// thinner surface reaches the gate first.
package confidence

import "testing"

// A record with plenty of rows but few independent observations must not clear
// the evidence floor. The floor is written in observations; rows are not
// observations.
func TestEvidenceFloorCountsEffectiveObservations(t *testing.T) {
	ev := Evidence{
		N:                600, // far past MinEpisodes on raw rows
		EffectiveN:       12,  // ...but a design effect of 50 leaves twelve
		Accuracy:         0.62,
		BaseRate:         0.50,
		CalibrationErr:   0.01,
		CalibrationKnown: true,
	}
	a := Assess(0.62, ev, ConditionalReturn{}, Excursion{})
	if a.Confidence != nil {
		t.Fatalf("confidence %v published on 12 effective observations (600 raw); "+
			"the floor is counting rows", *a.Confidence)
	}
	if a.Uncertainty != nil {
		t.Fatalf("uncertainty %v published below the evidence floor", *a.Uncertainty)
	}
	if !withheldMentions(a, "effective") {
		t.Fatalf("withheld does not say the shortfall is in EFFECTIVE observations: %v", a.Withheld)
	}
}

// The sample term of the confidence score saturates on independent
// observations, never on rows. Two records with identical accuracy and
// calibration but different clustering must not score the same.
func TestSampleTermUsesEffectiveNotRawRows(t *testing.T) {
	// The edge is deliberately large (20 points) so that it saturates the edge
	// term in BOTH records and sits outside both error bars. That isolates the
	// sample term: if the two scores still differ, it is the denominator that
	// differed, not the edge-inside-its-error-bar halving.
	base := Evidence{Accuracy: 0.70, BaseRate: 0.50, CalibrationErr: 0.01, CalibrationKnown: true}

	clean := base
	clean.N, clean.EffectiveN = 300, 300 // genuinely independent
	dirty := base
	dirty.N, dirty.EffectiveN = 300, 60 // same rows, design effect 5

	cleanA := Assess(0.70, clean, ConditionalReturn{}, Excursion{})
	dirtyA := Assess(0.70, dirty, ConditionalReturn{}, Excursion{})
	if cleanA.Confidence == nil || dirtyA.Confidence == nil {
		t.Fatal("both records clear the floor and must carry a confidence score")
	}
	if *dirtyA.Confidence >= *cleanA.Confidence {
		t.Fatalf("clustered record scored %v, at least as high as the independent one (%v) — "+
			"the sample term is counting rows", *dirtyA.Confidence, *cleanA.Confidence)
	}
}

// A record whose effective size was never measured has no certified sample size,
// so it gets no confidence score either. Publishing one while withholding the
// uncertainty would score the model on a sample size the platform just refused
// to vouch for.
func TestUnmeasuredEffectiveSizeWithholdsConfidenceToo(t *testing.T) {
	ev := Evidence{N: 5000, EffectiveN: 0, Accuracy: 0.60, BaseRate: 0.50,
		CalibrationErr: 0.01, CalibrationKnown: true}
	a := Assess(0.60, ev, ConditionalReturn{}, Excursion{})
	if a.Confidence != nil {
		t.Fatalf("confidence %v published on an unmeasured effective sample size", *a.Confidence)
	}
	if a.Uncertainty != nil {
		t.Fatalf("uncertainty %v published on an unmeasured effective sample size", *a.Uncertainty)
	}
}

// The live-shaped record still reports, and reports the same as before: this
// change is a floor and a denominator, not a new refusal on real data.
func TestLiveShapedRecordStillAssessed(t *testing.T) {
	// 1d directional record measured 2026-07-27.
	ev := Evidence{N: 13065, EffectiveN: 889.4, Accuracy: 0.4812, BaseRate: 0.5460,
		CalibrationErr: 0.04, CalibrationKnown: true}
	a := Assess(0.52, ev, ConditionalReturn{}, Excursion{})
	if a.Confidence == nil || a.Uncertainty == nil {
		t.Fatalf("the live record must still be assessable: %v", a.Withheld)
	}
	// No demonstrated edge, so the edge term is zero and only the sample term
	// contributes: 0.25/0.75 with calibration unweighted... calibration IS known
	// here, so 0.25 sample + 0.25 cal.
	if *a.Confidence > 0.6 {
		t.Fatalf("a below-null record scored confidence %v", *a.Confidence)
	}
}

func withheldMentions(a Assessment, sub string) bool {
	for _, w := range a.Withheld {
		if len(w) >= len(sub) && contains(w, sub) {
			return true
		}
	}
	return false
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
