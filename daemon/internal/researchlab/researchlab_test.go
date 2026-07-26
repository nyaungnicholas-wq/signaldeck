package researchlab

import (
	"math"
	"testing"
)

func approx(a, b, tol float64) bool { return math.Abs(a-b) < tol }

// synthetic rows: label depends deterministically on a "signal" feature plus a
// pure-"noise" feature that carries nothing. n rows, ascending ts.
func syntheticRows(n int) []Row {
	rows := make([]Row, n)
	for i := 0; i < n; i++ {
		// signal: alternating structured pattern the tree can learn.
		sig := 0.0
		y := 0.0
		if (i/3)%2 == 0 {
			sig, y = 1.0, 1.0
		} else {
			sig, y = -1.0, 0.0
		}
		// noise: deterministic but uncorrelated with y (period 7 vs y's period 6).
		noise := 0.0
		if i%7 < 3 {
			noise = 1.0
		}
		rows[i] = Row{Ts: int64(i), Y: y, Vec: map[string]float64{
			"signal": sig, "noise": noise,
		}}
	}
	return rows
}

func TestCanonicalKeys_ExcludesCheats(t *testing.T) {
	rows := []Row{{Vec: map[string]float64{"signal": 1, "pred_cal": 0.6, "gbm_prob": 0.5, "alpha": 0.1}}}
	keys := CanonicalKeys(rows)
	for _, k := range keys {
		if k == "pred_cal" || k == "gbm_prob" {
			t.Fatalf("cheat key leaked into canonical set: %v", keys)
		}
	}
	// stable sorted
	if len(keys) != 2 || keys[0] != "alpha" || keys[1] != "signal" {
		t.Fatalf("keys not stable-sorted minus cheats: %v", keys)
	}
}

func TestGenerateHypotheses_BoundedDeterministic(t *testing.T) {
	keys := []string{"a", "b", "c", "d"}
	h1 := GenerateHypotheses(keys, nil, 5)
	h2 := GenerateHypotheses(keys, nil, 5)
	if len(h1) != 5 {
		t.Fatalf("max cap not honored: %d", len(h1))
	}
	for i := range h1 {
		if h1[i].ID != h2[i].ID {
			t.Fatalf("nondeterministic generation at %d: %s vs %s", i, h1[i].ID, h2[i].ID)
		}
	}
	// IDs are unique
	seen := map[string]bool{}
	for _, h := range h1 {
		if seen[h.ID] {
			t.Fatalf("duplicate hypothesis ID %s", h.ID)
		}
		seen[h.ID] = true
	}
}

// The core anti-false-discovery guarantee: dropping the NOISE feature should NOT
// be judged a significant improvement over baseline (there is no real edge to
// find), even though its point OOS score may wobble above baseline by luck. The
// Bonferroni-corrected Wilson floor must reject it.
func TestJudge_RejectsNoiseAblation(t *testing.T) {
	rows := syntheticRows(240)
	keys := CanonicalKeys(rows)
	cfg := DefaultEvalConfig()
	// syntheticRows are spaced one second apart, so a 1s label span makes each
	// row's label resolve before the next one begins — no overlap to purge.
	// Declaring it is not optional: the evaluator now REFUSES to grade samples
	// with no stated horizon rather than compute a Lift across leaked rows.
	cfg.LabelSpan = 1
	base, err := Baseline(rows, keys, cfg)
	if err != nil {
		t.Fatalf("baseline: %v", err)
	}
	// Test dropping the noise feature under a realistic multiple-comparison load.
	h := Hypothesis{Kind: KindAblation, Drop: "noise"}
	h.ID = h.computeID()
	g, err := EvaluateHypothesis(h, rows, keys, cfg)
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	d := Judge(h, g, base, 24, 0.05)
	if d.Survives {
		t.Fatalf("noise ablation must NOT survive the corrected test (wl=%.3f base=%.3f n=%d)",
			d.WilsonLower, base.Accuracy, g.N)
	}
	if d.CorrectedAlpha >= 0.05 {
		t.Fatalf("Bonferroni correction not applied: alpha=%.4f", d.CorrectedAlpha)
	}
}

// normalQuantile sanity: standard reference points.
func TestNormalQuantile(t *testing.T) {
	if !approx(normalQuantile(0.975), 1.959964, 1e-4) {
		t.Fatalf("z_.975 wrong: %v", normalQuantile(0.975))
	}
	if !approx(normalQuantile(0.95), 1.644854, 1e-4) {
		t.Fatalf("z_.95 wrong: %v", normalQuantile(0.95))
	}
	// Bonferroni pushes the quantile UP (harder bar) as alpha shrinks.
	if normalQuantile(1-0.05/24) <= normalQuantile(1-0.05) {
		t.Fatal("corrected quantile must exceed uncorrected")
	}
}

// wilsonLower is below phat and rises toward phat as n grows.
func TestWilsonLower(t *testing.T) {
	z := 1.96
	small := wilsonLower(0.7, 20, z)
	large := wilsonLower(0.7, 2000, z)
	if !(small < 0.7 && large < 0.7 && large > small) {
		t.Fatalf("wilson monotonicity broken: small=%.3f large=%.3f", small, large)
	}
	if wilsonLower(0.7, 0, z) != 0 {
		t.Fatal("n=0 must yield 0")
	}
}

// Insufficient data must surface as an error (the honest NO EDGE outcome), not a
// fabricated grade.
func TestBaseline_InsufficientData(t *testing.T) {
	rows := syntheticRows(20) // below gbm minTrainSamples
	keys := CanonicalKeys(rows)
	if _, err := Baseline(rows, keys, DefaultEvalConfig()); err == nil {
		t.Fatal("expected insufficient-data error on tiny sample")
	}
}
