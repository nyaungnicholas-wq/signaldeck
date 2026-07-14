package adaptive

import (
	"math"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/ensemble"
)

// mkExamples builds n labeled examples in one regime where the pressure leg
// is always RIGHT (p=0.75 on up moves, 0.25 on down) and the expectancy leg
// is always WRONG. Outcomes alternate up/down; fwd return follows direction.
func mkExamples(n int, regime string) []Example {
	out := make([]Example, 0, n)
	for i := 0; i < n; i++ {
		up := i%2 == 0
		fwd := 0.01
		pPressure, pExpect := 0.75, 0.3
		if !up {
			fwd = -0.01
			pPressure, pExpect = 0.25, 0.7
		}
		ex := Example{
			Legs:      map[string]float64{ensemble.LegPressure: pPressure, ensemble.LegExpectancy: pExpect},
			Regime:    regime,
			Up:        0,
			FwdReturn: fwd,
		}
		if up {
			ex.Up = 1
		}
		out = append(out, ex)
	}
	return out
}

func TestCompute_HitRateICAndWeights(t *testing.T) {
	w := Compute(mkExamples(40, "uptrend"), 1234)
	if w.ComputedTs != 1234 {
		t.Fatalf("computedTs = %d", w.ComputedTs)
	}
	// Both the regime cell and the "all" aggregate exist.
	for _, cellName := range []string{"uptrend", AllCell} {
		c, ok := w.Cells[cellName]
		if !ok {
			t.Fatalf("cell %q missing: %+v", cellName, w.Cells)
		}
		if c.N != 40 || c.Gated {
			t.Fatalf("%s: n=%d gated=%v, want n=40 ungated", cellName, c.N, c.Gated)
		}
		// Evidence: pressure always right, expectancy always wrong.
		if c.HitRates[ensemble.LegPressure] != 1.0 || c.HitRates[ensemble.LegExpectancy] != 0.0 {
			t.Fatalf("%s hit-rates: %+v", cellName, c.HitRates)
		}
		if c.LegN[ensemble.LegPressure] != 40 || c.LegN[ensemble.LegExpectancy] != 40 {
			t.Fatalf("%s legN: %+v", cellName, c.LegN)
		}
		// IC: pressure leg co-moves with fwd return (+1), expectancy anti (-1).
		if math.Abs(c.IC[ensemble.LegPressure]-1) > 1e-9 || math.Abs(c.IC[ensemble.LegExpectancy]+1) > 1e-9 {
			t.Fatalf("%s ic: %+v", cellName, c.IC)
		}
		// Weights: only edge over the coin flip earns weight, normalized.
		if len(c.Weights) != 1 || math.Abs(c.Weights[ensemble.LegPressure]-1.0) > 1e-9 {
			t.Fatalf("%s weights: %+v (want pressure=1.0 only)", cellName, c.Weights)
		}
	}
}

func TestCompute_HonestyGateUnder30(t *testing.T) {
	// 20 examples: perfect signal, but BELOW the sample gate — evidence is
	// reported, weights are withheld.
	w := Compute(mkExamples(20, "squeeze"), 0)
	for _, cellName := range []string{"squeeze", AllCell} {
		c := w.Cells[cellName]
		if !c.Gated || c.Weights != nil {
			t.Fatalf("%s: gated=%v weights=%v — n=20 must never yield weights", cellName, c.Gated, c.Weights)
		}
		if c.HitRates[ensemble.LegPressure] != 1.0 {
			t.Fatalf("%s: evidence must still be reported: %+v", cellName, c.HitRates)
		}
	}

	// 20 + 20 across two regimes: each regime cell stays gated, but the
	// pooled "all" cell (n=40) clears the gate.
	w = Compute(append(mkExamples(20, "uptrend"), mkExamples(20, "downtrend")...), 0)
	if !w.Cells["uptrend"].Gated || !w.Cells["downtrend"].Gated {
		t.Fatalf("thin regime cells must stay gated: %+v", w.Cells)
	}
	all := w.Cells[AllCell]
	if all.Gated || all.N != 40 || len(all.Weights) == 0 {
		t.Fatalf("pooled all cell must learn: %+v", all)
	}
}

func TestCompute_NoEdgeNoWeights(t *testing.T) {
	// 40 examples where every leg is at or below coin-flip: no learned
	// weights may be fabricated even though the cell passes the n gate.
	var exs []Example
	for i := 0; i < 40; i++ {
		up := i%2 == 0
		p := 0.4 // always leans down; hits only the down half -> hitRate 0.5
		ex := Example{Legs: map[string]float64{ensemble.LegPressure: p}, Regime: "chop", FwdReturn: -0.01}
		if up {
			ex.Up = 1
			ex.FwdReturn = 0.01
		}
		exs = append(exs, ex)
	}
	w := Compute(exs, 0)
	c := w.Cells["chop"]
	if c.Gated {
		t.Fatalf("n=40 must not be sample-gated: %+v", c)
	}
	if c.Weights != nil {
		t.Fatalf("hitRate<=0.5 must yield NO weights, got %+v", c.Weights)
	}
}

func TestCompute_SentimentNeedsOwnSampleGate(t *testing.T) {
	// 40 examples, but sentiment is present (and perfect) in only 10 of
	// them: the sentiment-specific gate must refuse it ANY weight.
	build := func(sentimentN int) []Example {
		exs := mkExamples(40, "uptrend")
		for i := 0; i < sentimentN; i++ {
			p := 0.65
			if exs[i].Up == 0 {
				p = 0.35
			}
			exs[i].Legs[ensemble.LegSentiment] = p
		}
		return exs
	}
	w := Compute(build(10), 0)
	c := w.Cells["uptrend"]
	if c.HitRates[ensemble.LegSentiment] != 1.0 || c.LegN[ensemble.LegSentiment] != 10 {
		t.Fatalf("sentiment evidence must be measured: %+v %+v", c.HitRates, c.LegN)
	}
	if _, has := c.Weights[ensemble.LegSentiment]; has {
		t.Fatalf("sentiment with legN=10 must get NO weight: %+v", c.Weights)
	}

	// With 30+ of its own samples it earns its way in.
	w = Compute(build(30), 0)
	c = w.Cells["uptrend"]
	if _, has := c.Weights[ensemble.LegSentiment]; !has {
		t.Fatalf("sentiment with legN=30 and perfect hit-rate must be weighted: %+v", c.Weights)
	}
	// Weights stay normalized.
	var sum float64
	for _, v := range c.Weights {
		sum += v
	}
	if math.Abs(sum-1) > 1e-9 {
		t.Fatalf("weights not normalized: %+v (sum %v)", c.Weights, sum)
	}
}

func TestCompute_UnknownRegimeCountsOnlyTowardAll(t *testing.T) {
	w := Compute(mkExamples(35, ""), 0)
	if len(w.Cells) != 1 {
		t.Fatalf("unknown regime must produce only the all cell: %+v", w.Cells)
	}
	if c := w.Cells[AllCell]; c.N != 35 || c.Gated {
		t.Fatalf("all cell: %+v", c)
	}
}

func TestPick_FallbackChain(t *testing.T) {
	w := Weights{Cells: map[string]Cell{
		"uptrend": {N: 40, Weights: map[string]float64{ensemble.LegPressure: 1}},
		"squeeze": {N: 10, Gated: true},
		AllCell:   {N: 50, Weights: map[string]float64{ensemble.LegExpectancy: 1}},
	}}

	// Tier 1: the symbol's own regime cell.
	if ws, gate := Pick(w, "uptrend"); gate != GateLearnedRegime || ws[ensemble.LegPressure] != 1 {
		t.Fatalf("regime tier: %v %q", ws, gate)
	}
	// Tier 2: gated/unknown regime falls back to the all cell.
	for _, regime := range []string{"squeeze", "downtrend", ""} {
		if ws, gate := Pick(w, regime); gate != GateLearnedAll || ws[ensemble.LegExpectancy] != 1 {
			t.Fatalf("all tier (%q): %v %q", regime, ws, gate)
		}
	}
	// Tier 3: nothing learned anywhere -> static prior (nil weights).
	empty := Weights{Cells: map[string]Cell{AllCell: {N: 10, Gated: true}}}
	if ws, gate := Pick(empty, "uptrend"); gate != GateStatic || ws != nil {
		t.Fatalf("static tier: %v %q", ws, gate)
	}
	if ws, gate := Pick(Weights{}, "uptrend"); gate != GateStatic || ws != nil {
		t.Fatalf("zero value: %v %q", ws, gate)
	}
}

func TestFromVector_RebuildsLegsAndRegime(t *testing.T) {
	vec := map[string]float64{
		"pressure_score":      0.6,
		"expectancy_hit_rate": 0.62,
		"forecast_prob":       0.71,
		"forecast_lift":       0.05,
		"sentiment_score":     1.0,
		"sentiment_n":         5,
		"regime_uptrend":      1,
		"rank_pct":            83,
		"pred_raw":            0.7,
	}
	legs, regime := FromVector(vec)
	if regime != "uptrend" {
		t.Fatalf("regime = %q", regime)
	}
	want := ensemble.LegProbabilities(ensemble.Components{
		PressureScore:     0.6,
		ExpectancyHitRate: fp(0.62),
		ForecastProb:      fp(0.71),
		ForecastLift:      fp(0.05),
		SentimentScore:    fp(1.0),
	})
	if len(legs) != len(want) {
		t.Fatalf("legs = %v, want %v", legs, want)
	}
	for k, v := range want {
		if legs[k] != v {
			t.Fatalf("leg %s = %v, want %v", k, legs[k], v)
		}
	}

	// Edgeless forecast must be dropped by the SAME rule the live blend uses,
	// and a vector without a regime one-hot yields regime "".
	legs, regime = FromVector(map[string]float64{
		"pressure_score": 0, "forecast_prob": 0.9, "forecast_lift": -0.01,
	})
	if regime != "" || len(legs) != 1 {
		t.Fatalf("edgeless forecast/no regime: legs=%v regime=%q", legs, regime)
	}
}

// The stored model-leg probs (gbm_prob / meanrev_prob / alphax_prob) are
// written to the vector ONLY when their OOS gate passed, so FromVector must
// reconstruct each as a live leg — attribution then grades exactly the legs
// the blend used, alphax included.
func TestFromVector_ReconstructsModelLegs(t *testing.T) {
	legs, _ := FromVector(map[string]float64{
		"pressure_score": 0.2,
		"gbm_prob":       0.61,
		"meanrev_prob":   0.44,
		"alphax_prob":    0.67,
	})
	posLift := 1.0
	want := ensemble.LegProbabilities(ensemble.Components{
		PressureScore: 0.2,
		GBMProb:       fp(0.61), GBMLift: &posLift,
		MeanRevProb: fp(0.44), MeanRevLift: &posLift,
		AlphaXProb: fp(0.67), AlphaXLift: &posLift,
	})
	if len(legs) != len(want) || len(legs) != 4 {
		t.Fatalf("legs = %v, want %v", legs, want)
	}
	for k, v := range want {
		if legs[k] != v {
			t.Fatalf("leg %s = %v, want %v", k, legs[k], v)
		}
	}
	if legs[ensemble.LegAlphaX] != 0.67 {
		t.Fatalf("alphax leg = %v, want 0.67", legs[ensemble.LegAlphaX])
	}
}

func TestMaxWeightShift(t *testing.T) {
	a := Weights{Cells: map[string]Cell{
		AllCell: {Weights: map[string]float64{ensemble.LegPressure: 0.6, ensemble.LegExpectancy: 0.4}},
	}}
	b := Weights{Cells: map[string]Cell{
		AllCell:   {Weights: map[string]float64{ensemble.LegPressure: 0.5, ensemble.LegExpectancy: 0.5}},
		"uptrend": {Weights: map[string]float64{ensemble.LegForecast: 0.3}},
	}}
	// Largest move: the brand-new uptrend/forecast weight (0 -> 0.3).
	if got := MaxWeightShift(a, b); math.Abs(got-0.3) > 1e-9 {
		t.Fatalf("shift = %v, want 0.3", got)
	}
	if got := MaxWeightShift(a, a); got != 0 {
		t.Fatalf("identical sets must shift 0, got %v", got)
	}
	if got := MaxWeightShift(Weights{}, Weights{}); got != 0 {
		t.Fatalf("empty sets: %v", got)
	}
}

func TestPearson(t *testing.T) {
	if _, ok := pearson([]float64{1, 2}, []float64{1, 2}); ok {
		t.Fatal("n<3 must be undefined")
	}
	if _, ok := pearson([]float64{1, 1, 1}, []float64{1, 2, 3}); ok {
		t.Fatal("zero variance must be undefined")
	}
	if r, ok := pearson([]float64{1, 2, 3, 4}, []float64{2, 4, 6, 8}); !ok || math.Abs(r-1) > 1e-9 {
		t.Fatalf("perfect positive: %v %v", r, ok)
	}
	if r, ok := pearson([]float64{1, 2, 3, 4}, []float64{8, 6, 4, 2}); !ok || math.Abs(r+1) > 1e-9 {
		t.Fatalf("perfect negative: %v %v", r, ok)
	}
}

func fp(v float64) *float64 { return &v }

// H3 (adversarial finding) — EVERY leg needs MinCellSamples of ITS OWN
// examples, not just sentiment. The proven failure: a 40-sample cell where
// alphax appeared in only 3 examples (all lucky hits) handed it the dominant
// learned weight (0.77) while the legs with real evidence split the rest.
// Now the thin leg is excluded outright and the evidenced legs share the
// weight near-equally.
func TestCompute_PerLegSampleGateExcludesThinLeg(t *testing.T) {
	// 40 examples: pressure and expectancy are present in ALL 40 with a
	// modest, identical edge (right 22/40 = 0.55); alphax is present in only
	// 3 — and perfect in all 3.
	var exs []Example
	for i := 0; i < 40; i++ {
		up := i%2 == 0
		fwd := 0.01
		if !up {
			fwd = -0.01
		}
		right, wrong := 0.7, 0.3
		if !up {
			right, wrong = 0.3, 0.7
		}
		p := right // first 22 examples: correct call…
		if i >= 22 {
			p = wrong // …last 18: wrong call → hitRate 0.55
		}
		ex := Example{
			Legs:   map[string]float64{ensemble.LegPressure: p, ensemble.LegExpectancy: p},
			Regime: "uptrend", FwdReturn: fwd,
		}
		if up {
			ex.Up = 1
		}
		if i < 3 { // alphax: 3 lucky, perfect examples
			ex.Legs[ensemble.LegAlphaX] = right
		}
		exs = append(exs, ex)
	}

	w := Compute(exs, 0)
	c := w.Cells["uptrend"]
	if c.Gated || c.N != 40 {
		t.Fatalf("cell must clear the cell-level gate: %+v", c)
	}
	// The evidence is still measured and reported (transparency)…
	if c.HitRates[ensemble.LegAlphaX] != 1.0 || c.LegN[ensemble.LegAlphaX] != 3 {
		t.Fatalf("alphax evidence must be reported: hitRates=%+v legN=%+v", c.HitRates, c.LegN)
	}
	// …but 3 lucky examples must never buy a learned weight.
	if _, has := c.Weights[ensemble.LegAlphaX]; has {
		t.Fatalf("alphax with legN=3 must get NO weight (was 0.77 pre-fix): %+v", c.Weights)
	}
	// The evidenced legs split the weight near-equally (identical hit-rates
	// → exactly equal), and the set stays normalized.
	pw, ew := c.Weights[ensemble.LegPressure], c.Weights[ensemble.LegExpectancy]
	if pw <= 0 || math.Abs(pw-ew) > 1e-9 {
		t.Fatalf("evidenced legs must share weight near-equally: %+v", c.Weights)
	}
	var sum float64
	for _, v := range c.Weights {
		sum += v
	}
	if math.Abs(sum-1) > 1e-9 {
		t.Fatalf("weights not normalized: %+v (sum %v)", c.Weights, sum)
	}
}
