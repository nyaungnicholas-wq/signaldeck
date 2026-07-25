package featureredundancy

import (
	"math"
	"math/rand"
	"testing"
)

// sampleSet builds n labeled samples from a generator over an index.
func sampleSet(n int, gen func(i int, rnd *rand.Rand) (map[string]float64, float64)) []Sample {
	rnd := rand.New(rand.NewSource(42)) //nolint:gosec // deterministic test fixture
	out := make([]Sample, 0, n)
	for i := 0; i < n; i++ {
		v, f := gen(i, rnd)
		out = append(out, Sample{Vec: v, Fwd: f})
	}
	return out
}

// The headline case: three renderings of one price path plus one genuinely
// independent feature must collapse to TWO effective inputs, not four.
func TestAnalyzeCollapsesTransformsOfTheSameSignal(t *testing.T) {
	s := sampleSet(400, func(i int, rnd *rand.Rand) (map[string]float64, float64) {
		price := rnd.NormFloat64()
		return map[string]float64{
			"momentum": price,
			"rsi":      price*2 + 1,       // affine transform: |rho| = 1
			"macd":     price*-0.98 + 0.3, // inverted, still the same information
			"funding":  rnd.NormFloat64(), // genuinely independent source
		}, rnd.NormFloat64()
	})
	rep := Analyze(s, Defaults())

	if rep.FieldCount != 4 {
		t.Fatalf("FieldCount = %d, want 4", rep.FieldCount)
	}
	if rep.EffectiveCount != 2 {
		t.Fatalf("EffectiveCount = %d, want 2 (one price cluster + funding); clusters=%+v",
			rep.EffectiveCount, rep.Clusters)
	}
	if len(rep.Clusters) != 1 || len(rep.Clusters[0].Members) != 3 {
		t.Fatalf("want one 3-member cluster, got %+v", rep.Clusters)
	}
	if got := rep.RedundancyRatio; math.Abs(got-0.5) > 1e-9 {
		t.Fatalf("RedundancyRatio = %v, want 0.5", got)
	}
	// A negative correlation is still redundancy — the cluster must include the
	// inverted rendering.
	found := false
	for _, m := range rep.Clusters[0].Members {
		if m == "macd" {
			found = true
		}
	}
	if !found {
		t.Fatalf("inverted feature 'macd' not clustered: %+v", rep.Clusters[0])
	}
	if !rep.Meaningful {
		t.Fatal("Meaningful = false on 400 samples")
	}
}

// Independent features must NOT be merged — the analysis has to be able to
// report "no redundancy", or it is just an excuse to delete features.
func TestAnalyzeKeepsIndependentFeaturesSeparate(t *testing.T) {
	s := sampleSet(400, func(i int, rnd *rand.Rand) (map[string]float64, float64) {
		return map[string]float64{
			"a": rnd.NormFloat64(),
			"b": rnd.NormFloat64(),
			"c": rnd.NormFloat64(),
		}, rnd.NormFloat64()
	})
	rep := Analyze(s, Defaults())
	if rep.EffectiveCount != 3 || len(rep.Clusters) != 0 {
		t.Fatalf("independent features merged: effective=%d clusters=%+v", rep.EffectiveCount, rep.Clusters)
	}
	if rep.RedundancyRatio != 0 {
		t.Fatalf("RedundancyRatio = %v, want 0", rep.RedundancyRatio)
	}
}

// Thin overlap must be reported as UNKNOWN (never merged). Two features that
// are perfectly correlated on only a handful of shared rows have not
// demonstrated redundancy.
func TestAnalyzeRefusesToMergeOnThinOverlap(t *testing.T) {
	s := sampleSet(400, func(i int, rnd *rand.Rand) (map[string]float64, float64) {
		v := rnd.NormFloat64()
		m := map[string]float64{"always": v, "filler": rnd.NormFloat64()}
		if i < 20 { // present on only 20 rows — below MinPairObs
			m["rare"] = v * 3
		}
		return m, rnd.NormFloat64()
	})
	rep := Analyze(s, Defaults())
	for _, c := range rep.Clusters {
		for _, m := range c.Members {
			if m == "rare" {
				t.Fatalf("merged on 20 shared rows: %+v", c)
			}
		}
	}
	// It is below MinFeatureObs too, so it must show up as skipped, not silently
	// vanish.
	var skipped bool
	for _, f := range rep.Skipped {
		if f.Name == "rare" && f.N == 20 {
			skipped = true
		}
	}
	if !skipped {
		t.Fatalf("rare feature not reported in Skipped: %+v", rep.Skipped)
	}
}

// The representative must be the cluster member with the strongest |IC|, so
// pruning to representatives keeps the most informative rendering.
func TestRepresentativeIsHighestAbsIC(t *testing.T) {
	s := sampleSet(400, func(i int, rnd *rand.Rand) (map[string]float64, float64) {
		base := rnd.NormFloat64()
		fwd := base + 0.05*rnd.NormFloat64()
		return map[string]float64{
			"weak":   base + 3*rnd.NormFloat64(), // still >0.9 correlated? no — noisy
			"strong": base,
			"mid":    base * 1.0001,
		}, fwd
	})
	rep := Analyze(s, Defaults())
	for _, c := range rep.Clusters {
		if len(c.Members) < 2 {
			continue
		}
		var best string
		var bestIC float64
		for _, m := range c.Members {
			for _, f := range rep.Features {
				if f.Name == m && math.Abs(f.IC) > bestIC {
					bestIC, best = math.Abs(f.IC), m
				}
			}
		}
		if c.Representative != best {
			t.Fatalf("representative %q != highest-|IC| member %q", c.Representative, best)
		}
	}
}

// Determinism: the report must not depend on Go's map iteration order.
func TestAnalyzeIsDeterministic(t *testing.T) {
	s := sampleSet(300, func(i int, rnd *rand.Rand) (map[string]float64, float64) {
		p := rnd.NormFloat64()
		return map[string]float64{
			"x": p, "y": p * 2, "z": p*-1 + 1, "w": rnd.NormFloat64(), "v": rnd.NormFloat64(),
		}, rnd.NormFloat64()
	})
	first := Analyze(s, Defaults())
	for i := 0; i < 20; i++ {
		got := Analyze(s, Defaults())
		if got.EffectiveCount != first.EffectiveCount || len(got.Clusters) != len(first.Clusters) {
			t.Fatalf("nondeterministic shape on run %d", i)
		}
		for j := range got.Clusters {
			if got.Clusters[j].Representative != first.Clusters[j].Representative {
				t.Fatalf("nondeterministic representative on run %d", i)
			}
		}
		for j := range got.Representatives {
			if got.Representatives[j] != first.Representatives[j] {
				t.Fatalf("nondeterministic representatives on run %d", i)
			}
		}
	}
}

// The allow-list is what keeps a model's own output from being analyzed as if it
// were an input.
func TestAllowListExcludesNonInputs(t *testing.T) {
	s := sampleSet(300, func(i int, rnd *rand.Rand) (map[string]float64, float64) {
		p := rnd.NormFloat64()
		return map[string]float64{"rsi": p, "pred_raw": p, "funding": rnd.NormFloat64()}, rnd.NormFloat64()
	})
	cfg := Defaults()
	cfg.Allow = []string{"rsi", "funding"}
	rep := Analyze(s, cfg)
	for _, f := range rep.Features {
		if f.Name == "pred_raw" {
			t.Fatal("model output analyzed as an input despite the allow-list")
		}
	}
	if rep.FieldCount != 2 {
		t.Fatalf("FieldCount = %d, want 2", rep.FieldCount)
	}
}

// Degenerate inputs must produce an honest empty report, never a panic or a
// fabricated conclusion.
func TestAnalyzeDegenerateInputs(t *testing.T) {
	if rep := Analyze(nil, Defaults()); rep.Meaningful || rep.FieldCount != 0 {
		t.Fatalf("nil samples produced %+v", rep)
	}
	// A constant feature carries no information; correlation is defined as 0 and
	// it must not be merged with anything.
	s := sampleSet(300, func(i int, rnd *rand.Rand) (map[string]float64, float64) {
		return map[string]float64{"const": 1, "real": rnd.NormFloat64()}, rnd.NormFloat64()
	})
	rep := Analyze(s, Defaults())
	if rep.EffectiveCount != 2 {
		t.Fatalf("constant feature merged: %+v", rep.Clusters)
	}
	for _, f := range rep.Features {
		if f.Name == "const" && f.IC != 0 {
			t.Fatalf("constant feature IC = %v, want 0", f.IC)
		}
	}
}

func TestPearsonKnownValues(t *testing.T) {
	xs := []float64{1, 2, 3, 4, 5}
	if got := pearson(xs, []float64{2, 4, 6, 8, 10}); math.Abs(got-1) > 1e-12 {
		t.Fatalf("perfect positive = %v", got)
	}
	if got := pearson(xs, []float64{10, 8, 6, 4, 2}); math.Abs(got+1) > 1e-12 {
		t.Fatalf("perfect negative = %v", got)
	}
	if got := pearson([]float64{1}, []float64{1}); got != 0 {
		t.Fatalf("single point = %v, want 0", got)
	}
}
