package featurehealth

import (
	"math"
	"testing"
)

// build makes n samples where `key` correlates with Fwd by `sign` over the given
// index range, and is flat elsewhere.
func build(n int, key string, flipAfter int) []Sample {
	out := make([]Sample, 0, n)
	for i := 0; i < n; i++ {
		x := float64(i%10) - 4.5
		fwd := x * 0.001
		if flipAfter >= 0 && i >= flipAfter {
			fwd = -fwd // the relationship reverses partway through
		}
		out = append(out, Sample{
			Ts:  int64(i) * 86400,
			Vec: map[string]float64{key: x},
			Fwd: fwd,
		})
	}
	return out
}

// The recent window must be the NEWEST rows regardless of input order — a decay
// check that reads the oldest rows as "recent" reports every dead feature as fresh.
func TestFromSamplesRecentWindowIsChronological(t *testing.T) {
	// Positive relationship for the first 300 rows, reversed for the last 100.
	samples := build(400, "f", 300)

	cfg := DefaultSampleConfig()
	cfg.Allow = []string{"f"}
	ins := FromSamples(samples, cfg)
	if len(ins) != 1 {
		t.Fatalf("want one feature, got %d", len(ins))
	}
	in := ins[0]
	if !in.RecentKnown {
		t.Fatal("the recent window should be measurable at 400 rows")
	}
	// The newest quarter is the reversed span, so the recent IC must have the
	// OPPOSITE sign to the full-record IC.
	if in.FullIC*in.RecentIC >= 0 {
		t.Errorf("recent window did not pick up the reversal: full=%.4f recent=%.4f",
			in.FullIC, in.RecentIC)
	}

	// Reversing the input order must not change the answer.
	shuffled := make([]Sample, len(samples))
	for i := range samples {
		shuffled[i] = samples[len(samples)-1-i]
	}
	got := FromSamples(shuffled, cfg)[0]
	if math.Abs(got.RecentIC-in.RecentIC) > 1e-9 {
		t.Errorf("input order changed the recent IC: %.6f vs %.6f", got.RecentIC, in.RecentIC)
	}
}

// Absent keys are skipped, never imputed as zero — a fabricated observation would
// pull every correlation toward nothing.
func TestFromSamplesSkipsAbsentRatherThanImputing(t *testing.T) {
	var samples []Sample
	for i := 0; i < 200; i++ {
		vec := map[string]float64{"always": float64(i % 7)}
		if i%2 == 0 {
			vec["sometimes"] = float64(i % 7)
		}
		samples = append(samples, Sample{Ts: int64(i) * 86400, Vec: vec, Fwd: float64(i%7) * 0.001})
	}
	cfg := DefaultSampleConfig()
	cfg.Allow = []string{"always", "sometimes"}
	ins := FromSamples(samples, cfg)

	byName := map[string]Inputs{}
	for _, in := range ins {
		byName[in.Name] = in
	}
	if got := byName["always"]; got.N != 200 || math.Abs(got.Coverage-1) > 1e-9 {
		t.Errorf("always: want N=200 coverage=1, got N=%d coverage=%.3f", got.N, got.Coverage)
	}
	if got := byName["sometimes"]; got.N != 100 || math.Abs(got.Coverage-0.5) > 1e-9 {
		t.Errorf("sometimes: want N=100 coverage=0.5, got N=%d coverage=%.3f", got.N, got.Coverage)
	}
	// Both correlate identically on the rows where they exist; imputing zeros for
	// the absent half would have weakened "sometimes".
	if math.Abs(byName["always"].FullIC-byName["sometimes"].FullIC) > 1e-9 {
		t.Errorf("absent rows contaminated the IC: %.6f vs %.6f",
			byName["always"].FullIC, byName["sometimes"].FullIC)
	}
}

// Blocks are contiguous spans; a block too thin to compute an IC is OMITTED rather
// than reported as zero, since "no relationship" and "could not look" differ.
func TestBlockICsAreContiguousAndOmitThinBlocks(t *testing.T) {
	samples := build(500, "f", -1)
	got := blockICs(samples, "f", 5)
	if len(got) != 5 {
		t.Fatalf("want 5 usable blocks over 500 rows, got %d", len(got))
	}
	for i, ic := range got {
		if ic <= 0 {
			t.Errorf("block %d should carry the positive relationship, got %.4f", i, ic)
		}
	}
	// 40 rows over 5 blocks = 8 per block, under the 20-pair floor: nothing usable.
	if thin := blockICs(build(40, "f", -1), "f", 5); len(thin) != 0 {
		t.Errorf("thin blocks must be omitted, got %v", thin)
	}
}

// Drift and redundancy are passed through only when the caller supplied them.
func TestFromSamplesCarriesRedundancyAndDriftOnlyWhenGiven(t *testing.T) {
	samples := build(200, "f", -1)
	cfg := DefaultSampleConfig()
	cfg.Allow = []string{"f"}

	plain := FromSamples(samples, cfg)[0]
	if plain.DriftKnown {
		t.Error("drift must read as unmeasured when no map was supplied")
	}
	if plain.Redundant {
		t.Error("redundancy must default to false")
	}

	cfg.Drift = map[string]float64{"f": 0.4}
	cfg.Redundant = map[string]bool{"f": true}
	marked := FromSamples(samples, cfg)[0]
	if !marked.DriftKnown || marked.DriftPct != 0.4 {
		t.Errorf("drift not carried: known=%v pct=%v", marked.DriftKnown, marked.DriftPct)
	}
	if !marked.Redundant {
		t.Error("redundancy flag not carried")
	}
}

// End to end: a feature whose relationship reverses in the newest window must be
// graded worse than one that holds, using only the derivation above.
func TestDerivedInputsFeedGradingCoherently(t *testing.T) {
	cfg := DefaultSampleConfig()
	cfg.Allow = []string{"f"}

	steady := Grade(FromSamples(build(600, "f", -1), cfg)[0])
	flipped := Grade(FromSamples(build(600, "f", 450), cfg)[0])

	if steady.Overall <= flipped.Overall {
		t.Errorf("a steady feature must outscore a reversed one: %.3f vs %.3f",
			steady.Overall, flipped.Overall)
	}
	if !steady.Keep {
		t.Errorf("the steady feature should be kept: %v", steady.Reasons)
	}
}

func TestPearsonGuardsConstantsAndLength(t *testing.T) {
	if got := pearson([]float64{1, 1, 1}, []float64{1, 2, 3}); got != 0 {
		t.Errorf("a constant series has no correlation, got %v", got)
	}
	if got := pearson([]float64{1, 2}, []float64{1}); got != 0 {
		t.Errorf("mismatched lengths must return 0, got %v", got)
	}
	if got := pearson([]float64{1, 2, 3}, []float64{2, 4, 6}); math.Abs(got-1) > 1e-9 {
		t.Errorf("a perfect linear relationship should be 1, got %v", got)
	}
}
