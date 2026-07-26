package researchlab

import "testing"

// The failure these tests exist to prevent:
//
// The nightly loop re-graded its SHADOW POOL and corrected by `len(shadows)` —
// the size of the pool tonight. That pool shrinks as members are promoted or
// rejected, so a hypothesis that survived a 24-way correction on night 1 faced a
// 3-way correction by night 8 while being asked the SAME question of the same
// data. Every extra look is an extra chance for noise to clear the bar, so the
// correction has to grow with the looks, and the divisor was doing the opposite.
//
// The divisor is also no longer a caller-supplied number. It is derived from
// counts of tests actually conducted, and the significance level is a package
// constant, because an anti-p-hacking guardrail an operator can widen is not a
// guardrail.

func TestDivisorGrowsWithRepeatedTesting(t *testing.T) {
	// Eight nights of the real shape: a 24-hypothesis batch on night 1, then a
	// pool that shrinks to 3 as members are killed off.
	batches := []int{24, 12, 8, 6, 5, 4, 3, 3}
	prior := 0
	last := 0
	for night, batch := range batches {
		m := Multiplicity{Batch: batch, PriorTests: prior}
		d := m.Divisor()
		if night > 0 && d <= last {
			t.Errorf("night %d: divisor %d did not exceed night %d's %d — "+
				"re-testing a shrinking pool must TIGHTEN the bar", night+1, d, night, last)
		}
		last = d
		prior += batch
	}
	// And the eventual bar is materially harder than the first night's.
	if last <= batches[0] {
		t.Errorf("final divisor %d <= first-night divisor %d", last, batches[0])
	}
}

func TestDivisorNeverBelowOne(t *testing.T) {
	for _, m := range []Multiplicity{{}, {Batch: -5}, {Batch: 0, PriorTests: -3}} {
		if got := m.Divisor(); got < 1 {
			t.Errorf("Multiplicity%+v.Divisor() = %d, want >= 1", m, got)
		}
	}
}

// A pure-noise candidate must not be admitted no matter how many nights it is
// re-offered: the accumulating divisor has to make the bar rise faster than
// repeated looks lower it.
func TestRepeatedTestingNeverPromotesNoise(t *testing.T) {
	rows := syntheticRows(240)
	keys := CanonicalKeys(rows)
	cfg := DefaultEvalConfig()
	cfg.LabelSpan = 1
	base, err := Baseline(rows, keys, cfg)
	if err != nil {
		t.Fatalf("baseline: %v", err)
	}
	h := Hypothesis{Kind: KindAblation, Drop: "noise"}
	h.ID = h.computeID()
	g, err := EvaluateHypothesis(h, rows, keys, cfg)
	if err != nil {
		t.Fatalf("eval: %v", err)
	}

	prior, lastWilson := 0, 1.0
	for night := 1; night <= 30; night++ {
		batch := 24
		if night > 1 {
			batch = 1 // the pool has shrunk to this one survivor candidate
		}
		d := Judge(h, g, base, Multiplicity{Batch: batch, PriorTests: prior})
		if d.Survives {
			t.Fatalf("night %d: noise ablation survived (divisor=%d wl=%.4f base=%.4f)",
				night, d.Divisor, d.WilsonLower, base.Accuracy)
		}
		if night > 1 && d.WilsonLower >= lastWilson {
			t.Fatalf("night %d: Wilson floor %.4f did not fall below night %d's %.4f — "+
				"the bar got easier under re-testing", night, d.WilsonLower, night-1, lastWilson)
		}
		lastWilson = d.WilsonLower
		prior += batch
	}
}

// The nominal level is a constant, not an argument: there is no call shape that
// runs the lab looser than MaxNominalAlpha.
func TestCorrectedAlphaIsNotOperatorSupplied(t *testing.T) {
	d := Judge(Hypothesis{Kind: KindAblation}, Grade{Accuracy: 0.6, N: 500, Lift: 0.1},
		Grade{Accuracy: 0.5, N: 500}, Multiplicity{Batch: 24})
	if want := MaxNominalAlpha / 24; d.CorrectedAlpha != want {
		t.Errorf("corrected alpha = %g, want %g", d.CorrectedAlpha, want)
	}
	if d.Divisor != 24 {
		t.Errorf("divisor = %d, want 24", d.Divisor)
	}
}
