package ensemble

import (
	"math"
	"testing"
)

func sumParts(parts []AttributionPart) float64 {
	var s float64
	for _, p := range parts {
		s += p.Contribution
	}
	return s
}

func f64(v float64) *float64 { return &v }

// The contract: parts sum to WeightedProbability(c, weights) − 0.5 exactly.
func TestAttributeProbability_SumMatchesBlend(t *testing.T) {
	c := Components{
		PressureScore:     0.4,
		ExpectancyHitRate: f64(0.62),
		ForecastProb:      f64(0.58), ForecastLift: f64(0.03),
		GBMProb: f64(0.66), GBMLift: f64(0.05),
		SentimentScore: f64(-0.5),
	}
	cases := []map[string]float64{
		nil, // equal-weight fallback
		{LegPressure: 2, LegForecast: 1, LegGBM: 3, LegExpectancy: 0.5, LegSentiment: 1},
		{LegGBM: 1},                // single leg carries all mass
		{"nonexistent_leg": 4},     // no mass over available legs -> equal fallback
	}
	for i, wts := range cases {
		want, _ := WeightedProbability(c, wts)
		parts := AttributeProbability(c, wts, nil, nil)
		if got := 0.5 + sumParts(parts); math.Abs(got-want) > 1e-12 {
			t.Errorf("case %d: 0.5+Σparts = %v, want blend %v", i, got, want)
		}
	}
}

// Ensemble merge respects the blend weights: doubling a leg's weight doubles
// its share relative to a unit-weight leg.
func TestAttributeProbability_WeightedMerge(t *testing.T) {
	c := Components{
		PressureScore: 0.4, // p=0.7, delta +0.2
		GBMProb:       f64(0.9), GBMLift: f64(0.1), // delta +0.4
	}
	wts := map[string]float64{LegPressure: 1, LegGBM: 3}
	parts := AttributeProbability(c, wts, nil, nil)
	byName := map[string]float64{}
	for _, p := range parts {
		byName[p.Name] = p.Contribution
	}
	// wn = 0.25 / 0.75: pressure 0.25*0.2 = 0.05, gbm 0.75*0.4 = 0.3.
	if math.Abs(byName[LegPressure]-0.05) > 1e-12 {
		t.Errorf("pressure part = %v, want 0.05", byName[LegPressure])
	}
	if math.Abs(byName[LegGBM]-0.3) > 1e-12 {
		t.Errorf("gbm part = %v, want 0.3", byName[LegGBM])
	}
}

// Splitting pressure into comp_* parts and the GBM leg into feature parts
// preserves the sum exactly.
func TestAttributeProbability_SplitsPreserveSum(t *testing.T) {
	c := Components{
		PressureScore: 0.3,
		GBMProb:       f64(0.7), GBMLift: f64(0.02),
	}
	comps := []AttributionPart{ // contributions sum to the score 0.3
		{Name: "comp_rsi", Contribution: 0.5},
		{Name: "comp_trend", Contribution: -0.2},
	}
	gbmFeats := []AttributionPart{ // logit contribs, arbitrary scale
		{Name: "trend21", Contribution: 0.8},
		{Name: "vol21", Contribution: -0.2},
	}
	wts := map[string]float64{LegPressure: 1, LegGBM: 1}
	want, _ := WeightedProbability(c, wts)
	parts := AttributeProbability(c, wts, comps, gbmFeats)
	if got := 0.5 + sumParts(parts); math.Abs(got-want) > 1e-12 {
		t.Fatalf("split sum = %v, want %v", got, want)
	}
	kinds := map[string]string{}
	vals := map[string]float64{}
	for _, p := range parts {
		kinds[p.Name] = p.Kind
		vals[p.Name] = p.Contribution
	}
	if kinds["comp_rsi"] != KindComponent || kinds["gbm_trend21"] != KindGBMFeature {
		t.Errorf("kinds wrong: %v", kinds)
	}
	// comp split is exact: wn=0.5, part = contrib*0.5/2.
	if math.Abs(vals["comp_rsi"]-0.125) > 1e-12 {
		t.Errorf("comp_rsi = %v, want 0.125", vals["comp_rsi"])
	}
	// gbm leg contribution = 0.5*(0.7-0.5)=0.1; shares 0.8/0.6 and -0.2/0.6.
	if math.Abs(vals["gbm_trend21"]-0.1*(0.8/0.6)) > 1e-12 {
		t.Errorf("gbm_trend21 = %v", vals["gbm_trend21"])
	}
	// GBM logit total ~0 falls back to a single leg part.
	zero := []AttributionPart{{Name: "a", Contribution: 1e-15}, {Name: "b", Contribution: -1e-15}}
	parts2 := AttributeProbability(c, wts, nil, zero)
	found := false
	for _, p := range parts2 {
		if p.Name == LegGBM && p.Kind == KindLeg {
			found = true
		}
	}
	if !found {
		t.Error("near-zero gbm logit total should keep the leg whole")
	}
}

func TestTopParts(t *testing.T) {
	parts := []AttributionPart{
		{Name: "a", Contribution: 0.01},
		{Name: "b", Contribution: -0.5},
		{Name: "c", Contribution: 0.2},
	}
	top := TopParts(parts, 2)
	if len(top) != 2 || top[0].Name != "b" || top[1].Name != "c" {
		t.Errorf("TopParts = %v", top)
	}
	if len(TopParts(parts, 0)) != 3 {
		t.Error("n<=0 should return all parts")
	}
}
