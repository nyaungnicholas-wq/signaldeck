package researchlab

import (
	"math"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/gbm"
)

func approx(a, b, tol float64) bool { return math.Abs(a-b) < tol }

// synthetic rows: label depends deterministically on a "signal" feature plus a
// pure-"noise" feature that carries nothing. n rows, ascending ts, one row per
// UTC DAY — the gate now evaluates its bound at the day-clustered effective
// sample size, so a fixture whose rows all land on one day would (correctly)
// get every bound withheld and test nothing.
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
		rows[i] = Row{Ts: int64(i) * 86400, Y: y, Vec: map[string]float64{
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
	// syntheticRows are spaced one day apart, so a 1-day label span makes each
	// row's label resolve before the next one begins — no overlap to purge.
	// Declaring it is not optional: the evaluator now REFUSES to grade samples
	// with no stated horizon rather than compute a Lift across leaked rows.
	cfg.LabelSpan = 86400
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
	d := Judge(h, g, base, Multiplicity{Batch: 24})
	if d.Survives {
		t.Fatalf("noise ablation must NOT survive the corrected test (wl=%.3f base=%.3f n=%d)",
			d.WilsonLower, base.Accuracy, g.N)
	}
	if d.CorrectedAlpha >= MaxNominalAlpha {
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

// The gate's lower bound is DAY-CLUSTERED: reconcilable per-day tallies yield a
// bound at effective N, below the point estimate; a grade without them gets no
// bound and cannot survive however good its raw numbers look — falling back to
// raw N is the A1 defect this gate was the last to shed.
func TestClusteredLowerBound(t *testing.T) {
	g := Grade{N: 200, Accuracy: 0.7, Lift: 0.2}
	for d := int64(0); d < 20; d++ {
		g.DayTallies = append(g.DayTallies, gbm.DayTally{Day: d, N: 10, Hits: 7})
	}
	lo, deff, effN, ok := clusteredLower(g, 1.96)
	if !ok || !(lo > 0 && lo < 0.7) {
		t.Fatalf("clustered lower bound wrong: lo=%.3f ok=%v", lo, ok)
	}
	if deff < 1 || effN <= 0 || effN > 200 {
		t.Fatalf("deff=%.3f effN=%.1f — effective N must be N/deff with deff >= 1", deff, effN)
	}

	// No tallies: no bound, and Judge refuses to promote.
	bare := Grade{N: 200, Accuracy: 0.99, Lift: 0.5}
	if _, _, _, ok := clusteredLower(bare, 1.96); ok {
		t.Fatal("a grade without per-day tallies must not get a bound")
	}
	d := Judge(Hypothesis{Kind: KindAblation}, bare, Grade{Accuracy: 0.5, N: 200}, Multiplicity{Batch: 1})
	if d.Survives || d.Bound != "withheld" {
		t.Fatalf("a tally-less grade must be withheld, got bound=%q survives=%v", d.Bound, d.Survives)
	}

	// Irreconcilable tallies are a caller bug, not a sample.
	bad := g
	bad.N = 150
	if _, _, _, ok := clusteredLower(bad, 1.96); ok {
		t.Fatal("tallies that do not reconcile with N must not get a bound")
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
