package pipeline

import "testing"

// A PER-SYMBOL column must carry a PER-SYMBOL measurement.
//
// The expectancy trainer wrote the pooled fleetAUC into every contributing
// symbol's row. Measured on the live 1d record that produced 654 rows whose AUC
// spanned 0.430 to 0.439 — indistinguishable to three decimals across 654
// different symbols, which no real per-symbol measurement is.
//
// It matters because rankGate pairs that column with the SYMBOL'S OWN NEval to
// build a Wilson lower bound on that symbol's ranking. A fleet AUC with a
// per-symbol n makes the bound a statement about nothing: every symbol inherits
// the fleet verdict while appearing to have been judged on its own evidence.
//
// This pins the property that distinguishes the two, so a future refactor that
// re-pools the column fails here rather than silently in the gate.
func TestPerSymbolAUCsMustActuallyVary(t *testing.T) {
	// The shape the bug produced: one value, repeated.
	pooled := []float64{0.4304, 0.4304, 0.4304, 0.4304, 0.4304}
	if spread(pooled) > 1e-9 {
		t.Fatal("fixture is wrong: the pooled shape must have zero spread")
	}
	if !looksPooled(pooled) {
		t.Error("a constant series was not detected as pooled")
	}

	// The shape a real per-symbol measurement has: dispersion. These are real
	// pressure per-symbol AUCs from the same table, which was never pooled.
	real := []float64{0.0820, 0.3139, 0.4870, 0.5512, 0.7580}
	if looksPooled(real) {
		t.Errorf("genuine per-symbol AUCs (spread %.4f) were detected as pooled", spread(real))
	}
}

func spread(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	mn, mx := v[0], v[0]
	for _, x := range v {
		if x < mn {
			mn = x
		}
		if x > mx {
			mx = x
		}
	}
	return mx - mn
}

// looksPooled reports the signature of a fleet aggregate stamped into a
// per-symbol column: essentially no variation across symbols.
func looksPooled(v []float64) bool { return len(v) > 1 && spread(v) < 0.01 }
