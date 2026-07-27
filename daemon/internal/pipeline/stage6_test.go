package pipeline

import (
	"context"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/ensemble"
	"github.com/nyaungnicholas-wq/signaldeck/internal/gbm"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/meanrev"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// STAGE 6 — buildFeatureVector must merge the cross-cutting micro_* and vix_*
// maps, and (only when gated) the model-leg probs.
func TestBuildFeatureVector_Stage6Features(t *testing.T) {
	sc := md.Score{Score: 0.2}
	gp, gl := 0.63, 0.04 // GBM passed its gate
	mp, ml := 0.42, 0.03 // mean-rev passed its gate
	c := ensemble.Components{
		PressureScore: 0.2,
		GBMProb:       &gp, GBMLift: &gl,
		MeanRevProb: &mp, MeanRevLift: &ml,
	}
	micro := map[string]float64{"micro_imbalance": 0.15, "micro_spread_bps": 3.2}
	vix := map[string]float64{"vix_level": 0.18, "vix_high_vol": 0}

	vec := buildFeatureVector(sc, c, 0.55, 0.55, "", nil, 0, micro, vix)

	for k, want := range map[string]float64{
		"micro_imbalance":  0.15,
		"micro_spread_bps": 3.2,
		"vix_level":        0.18,
		"vix_high_vol":     0,
		"gbm_prob":         0.63,
		"meanrev_prob":     0.42,
	} {
		if vec[k] != want {
			t.Fatalf("vec[%q] = %v, want %v", k, vec[k], want)
		}
	}
}

// A model leg whose lift did NOT clear the gate must be ABSENT from the vector
// (honesty: an edgeless leg contributed nothing, so it isn't recorded).
func TestBuildFeatureVector_UngatedLegAbsent(t *testing.T) {
	gp, gl := 0.63, -0.02 // GBM had NO edge (lift<=0)
	c := ensemble.Components{PressureScore: 0.1, GBMProb: &gp, GBMLift: &gl}
	vec := buildFeatureVector(md.Score{Score: 0.1}, c, 0.5, 0.5, "", nil, 0)
	if _, ok := vec["gbm_prob"]; ok {
		t.Fatal("an edgeless GBM leg must not appear in the feature vector")
	}
}

// The ensemble blend must INCLUDE a gated leg only when its lift>0, and the
// inclusion must actually move the blended probability.
func TestEnsemble_GatedLegsAffectBlend(t *testing.T) {
	base := ensemble.Components{PressureScore: 0.0} // pressure leg = 0.5
	pBase, _ := ensemble.RawProbability(base)

	// Add a strongly-bullish GBM leg WITH edge -> blend must move up.
	gp, gl := 0.9, 0.05
	withGBM := base
	withGBM.GBMProb, withGBM.GBMLift = &gp, &gl
	pGBM, nGBM := ensemble.RawProbability(withGBM)
	if nGBM != 2 {
		t.Fatalf("gated GBM leg should be counted, nUsed=%d", nGBM)
	}
	if pGBM <= pBase {
		t.Fatalf("bullish gated GBM leg should raise the blend: base=%.3f withGBM=%.3f", pBase, pGBM)
	}

	// Same GBM prob but NO edge -> dropped, blend unchanged.
	glNo := -0.01
	noEdge := base
	noEdge.GBMProb, noEdge.GBMLift = &gp, &glNo
	pNoEdge, nNoEdge := ensemble.RawProbability(noEdge)
	if nNoEdge != 1 || pNoEdge != pBase {
		t.Fatalf("edgeless GBM leg must be dropped: n=%d p=%.3f (base %.3f)", nNoEdge, pNoEdge, pBase)
	}
}

// GBMTrainer end-to-end: given a symbol with a LEARNABLE labeled feature store
// (feature vectors whose values determine the outcome), the trainer must store a
// GBM model forecast with positive OOS lift, and it must NOT leak the blend's own
// pred_raw into training.
func TestGBMTrainer_StoresGradedLeg(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	sym, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	if err != nil {
		t.Fatal(err)
	}

	// Seed a learnable labeled feature set: a single informative feature "x"
	// whose sign determines the outcome, plus a resolved prediction outcome for
	// each ts so LabeledFeaturesBySymbol returns them.
	h := md.H1d
	n := 300
	base := time.Now().Add(-time.Duration(n) * time.Hour).Unix()
	for i := 0; i < n; i++ {
		ts := base + int64(i)*3600
		x := -1.0
		fwd := -0.02
		if i%2 == 0 {
			x = 1.0
			fwd = 0.02
		}
		vec := map[string]float64{
			"pressure_score": x * 0.5,
			"x_feature":      x,
			"pred_raw":       0.5, // deliberately uninformative: GBM must use x
			"pred_cal":       0.5,
		}
		if err := st.InsertFeatures(ctx, sym.ID, h, ts, featureVersion, vec); err != nil {
			t.Fatal(err)
		}
		// Seed + resolve the prediction outcome for this ts so the join yields a
		// labeled row.
		if err := st.UpsertPrediction(ctx, store.Prediction{
			SymbolID: sym.ID, Horizon: h, Ts: ts, RawProb: 0.5, CalProb: 0.5, NUsed: 1,
		}); err != nil {
			t.Fatal(err)
		}
		if err := st.ResolvePrediction(ctx, sym.ID, h, ts, fwd); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := (&GBMTrainer{St: st}).Run(ctx); err != nil {
		t.Fatalf("GBMTrainer.Run: %v", err)
	}

	fcs, err := st.ModelForecasts(ctx, sym.ID)
	if err != nil {
		t.Fatal(err)
	}
	var gbmRow *store.ModelForecast
	for i := range fcs {
		if fcs[i].Model == store.ModelGBM && fcs[i].Horizon == h {
			gbmRow = &fcs[i]
		}
	}
	if gbmRow == nil {
		t.Fatal("GBMTrainer did not store a GBM leg for the learnable set")
	}
	if gbmRow.Lift <= 0 {
		t.Fatalf("GBM should show positive OOS lift on a learnable set, got %.3f (acc=%.3f base=%.3f)", gbmRow.Lift, gbmRow.Accuracy, gbmRow.BaseRate)
	}
	if gbmRow.NEval == 0 {
		t.Fatal("GBM leg should have out-of-sample eval points")
	}
}

// canonicalFeatureKeys must EXCLUDE the blend's own outputs (pred_raw/pred_cal)
// and the model legs' own past outputs (gbm_prob/meanrev_prob/alphax_prob) —
// training on them would be a self-referential shortcut.
func TestCanonicalFeatureKeys_ExcludesModelOutputs(t *testing.T) {
	rows := []store.LabeledFeature{
		{Vec: map[string]float64{
			"pressure_score": 0.1, "x": 0.2,
			"pred_raw": 0.6, "pred_cal": 0.6, "gbm_prob": 0.55, "meanrev_prob": 0.45,
			"alphax_prob": 0.61,
		}},
	}
	keys := canonicalFeatureKeys(rows)
	for _, bad := range []string{"pred_raw", "pred_cal", "gbm_prob", "meanrev_prob", "alphax_prob"} {
		for _, k := range keys {
			if k == bad {
				t.Fatalf("canonical keys must exclude %q (self-reference/leakage)", bad)
			}
		}
	}
	// The genuine features survive.
	got := map[string]bool{}
	for _, k := range keys {
		got[k] = true
	}
	if !got["pressure_score"] || !got["x"] {
		t.Fatalf("canonical keys dropped genuine features: %v", keys)
	}
}

// A7 REGRESSION — the four shortcut keys are DELETED at construction, not
// gated: buildFeatureVector must not emit them even when the components carry
// the forecast/expectancy legs, and the MODEL INPUT layout must contain no
// banned key (base or presence bit) even over historical rows that still carry
// every one of them. cmd/selfref-ablation measured what the deletion cost
// (+0.026 mean lift, zero admission-gate flips); this pins both halves so the
// defect class cannot silently return.
func TestBannedKeys_DeletedNotGated(t *testing.T) {
	// Construction side: forecast/expectancy legs present, keys still absent.
	hr, fp, fl := 0.62, 0.71, 0.05
	c := ensemble.Components{
		PressureScore:     0.2,
		ExpectancyHitRate: &hr,
		ForecastProb:      &fp,
		ForecastLift:      &fl,
	}
	vec := buildFeatureVector(md.Score{Score: 0.2}, c, 0.55, 0.55, "", nil, 0)
	for _, k := range []string{"forecast_prob", "forecast_lift", "expectancy_hit_rate", "n_used"} {
		if _, ok := vec[k]; ok {
			t.Fatalf("A7 shortcut key %q must no longer be constructed", k)
		}
	}

	// Training side: a historical-shaped row carrying every banned key must
	// yield a model layout containing none of them, base or presence bit.
	banned := []string{
		"pred_raw", "pred_cal",
		"gbm_prob", "meanrev_prob", "alphax_prob", "forecast_prob",
		"forecast_lift", "expectancy_hit_rate", "n_used",
	}
	rowVec := map[string]float64{"pressure_score": 0.1, "x_feature": 0.2}
	for _, k := range banned {
		rowVec[k] = 0.5
	}
	keys := modelFeatureKeys([]store.LabeledFeature{{Vec: rowVec}})
	got := map[string]bool{}
	for _, k := range keys {
		got[k] = true
	}
	for _, bad := range banned {
		if got[bad] || got[bad+presenceSuffix] {
			t.Fatalf("training feature list must not contain banned key %q (or its presence bit): %v", bad, keys)
		}
	}
	if !got["x_feature"] || !got["x_feature"+presenceSuffix] {
		t.Fatalf("genuine features must survive with their presence bits: %v", keys)
	}
}

// gbmSamplesFromLabeled must return samples in ASCENDING ts order (rows arrive
// newest-first) so the walk-forward grade is honest.
func TestGBMSamplesAscendingTs(t *testing.T) {
	rows := []store.LabeledFeature{ // newest first
		{Ts: 300, Vec: map[string]float64{"x": 1}, Up: 1},
		{Ts: 200, Vec: map[string]float64{"x": 0}, Up: 0},
		{Ts: 100, Vec: map[string]float64{"x": 1}, Up: 1},
	}
	samples := gbmSamplesFromLabeled(rows, []string{"x"})
	if len(samples) != 3 {
		t.Fatalf("want 3 samples, got %d", len(samples))
	}
	if samples[0].Ts != 100 || samples[2].Ts != 300 {
		t.Fatalf("samples not ascending by ts: %d..%d", samples[0].Ts, samples[2].Ts)
	}
}

// meanRevSamplesFromLabeled must skip rows lacking the pressure input and keep
// ascending order.
//
// It reads pressure_score, NOT pred_raw. pred_raw is the blend output and the
// blend contains this very leg, so reading it closed a self-reference loop —
// the leg learning to invert a number that already contained itself. A row with
// only pred_raw must now be skipped, which is what the middle row asserts.
func TestMeanRevSamplesSkipMissingPressure(t *testing.T) {
	rows := []store.LabeledFeature{
		{Ts: 300, Vec: map[string]float64{"pressure_score": 0.4}, Up: 0, FwdReturn: -0.01},
		{Ts: 200, Vec: map[string]float64{"pred_raw": 0.9}, Up: 1, FwdReturn: 0.01}, // blend output only — must be skipped
		{Ts: 100, Vec: map[string]float64{"pressure_score": -0.4}, Up: 1, FwdReturn: 0.01},
	}
	samples := meanRevSamplesFromLabeled(rows)
	if len(samples) != 2 {
		t.Fatalf("want 2 mean-rev samples (one skipped), got %d", len(samples))
	}
	if samples[0].Ts != 100 || samples[1].Ts != 300 {
		t.Fatalf("mean-rev samples not ascending: %d then %d", samples[0].Ts, samples[1].Ts)
	}
}

// Sanity: the pipeline's gbm/meanrev wiring uses the engines' own no-leakage
// grading — a quick guard that the exported engines are the ones we tested.
func TestPipelineUsesGradedEngines(t *testing.T) {
	// GBM Run refuses tiny data (ok=false) — the pipeline relies on this to skip.
	if _, _, ok := gbm.Run([]gbm.Sample{{Ts: 1, Feat: []float64{1}, Y: 1}}, []float64{1}, 5, gbm.Defaults()); ok {
		t.Fatal("gbm.Run should refuse a tiny set")
	}
	// meanrev Run likewise.
	if _, _, ok := meanrev.Run([]meanrev.Sample{{Ts: 1, RawProb: 0.8}}, 0.8, 5, meanrev.DefaultStrength, 0.001); ok {
		t.Fatal("meanrev.Run should refuse a tiny set")
	}
}
