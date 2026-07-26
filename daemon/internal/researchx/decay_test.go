package researchx

import (
	"math"
	"testing"

	rl "github.com/nyaungnicholas-wq/signaldeck/internal/researchledger"
)

const day = int64(86400)

// grade builds a data-backed grade row. N matters: an evidence row with no
// observation is an ASSERTION, and rl.Posterior refuses an assertion's
// FOR-weight (rl.AssertedMaxBF), so a fixture without it would silently grade
// as a flat chain.
func grade(kind string, ts int64, bf float64) rl.Evidence {
	return rl.Evidence{Kind: kind, Ts: ts, K: 60, N: 100, P0: 0.5, BF: bf}
}

// A belief that rose and then gave the ground back must report the PEAK it
// reached and when — a chain reported only at its current value hides the
// decay that is the whole reason to track it.
func TestDecayFindsThePeakAndTheWeakening(t *testing.T) {
	chain := []rl.Evidence{
		grade(rl.KindExperiment, 10*day, 5),
		grade(rl.KindReplication, 20*day, 4),
		grade(rl.KindReplication, 30*day, rl.MinBF),
		grade(rl.KindReplication, 40*day, rl.MinBF),
	}
	rep := Decay(0.3, chain, 41*day)
	if rep.PeakTs != 20*day {
		t.Errorf("peak at ts=%d, want %d (the second grade)", rep.PeakTs, 20*day)
	}
	// prior 0.3 → odds 3/7, ×5×4 = 8.571 → 0.8955.
	if want := (3.0 / 7 * 5 * 4) / (1 + 3.0/7*5*4); math.Abs(rep.Peak-want) > 1e-9 {
		t.Errorf("peak = %.4f, want %.4f", rep.Peak, want)
	}
	if rep.Current >= rep.Peak {
		t.Errorf("current %.4f not below peak %.4f", rep.Current, rep.Peak)
	}
	if !rep.EdgeWeakening {
		t.Errorf("a chain that fell %.3f below its peak did not report weakening", rep.Peak-rep.Current)
	}
	if rep.LastGradeTs != 40*day || rep.Stale {
		t.Errorf("lastGrade=%d stale=%v, want %d/false", rep.LastGradeTs, rep.Stale, 40*day)
	}
}

// A belief that only ever lost ground peaked at BIRTH: reporting its peak as
// the best it managed after the fall would flatter it.
func TestDecayPeaksAtBirthWhenItOnlyFell(t *testing.T) {
	chain := []rl.Evidence{
		grade(rl.KindExperiment, 10*day, rl.MinBF),
		grade(rl.KindReplication, 20*day, 0.5),
	}
	rep := Decay(0.5, chain, 21*day)
	if rep.PeakTs != 0 {
		t.Errorf("peakTs = %d, want 0 (peaked at the prior)", rep.PeakTs)
	}
	if math.Abs(rep.Peak-0.5) > 1e-9 {
		t.Errorf("peak = %.4f, want the 0.5 prior", rep.Peak)
	}
	// EdgeWeakening is meaningless below coin-flip: a belief that was never
	// above 0.5 has no edge to weaken.
	if rep.EdgeWeakening {
		t.Error("reported weakening for a belief that never exceeded 0.5")
	}
}

// A hypothesis nobody has graded in 60 days has stopped being tested, and one
// nobody ever graded is stale by definition — reporting either as healthy is
// how a dead belief keeps its number on a page.
func TestDecayStaleness(t *testing.T) {
	if rep := Decay(0.5, nil, 1000*day); !rep.Stale || rep.LastGradeTs != 0 {
		t.Errorf("never-graded chain: stale=%v lastGrade=%d, want true/0", rep.Stale, rep.LastGradeTs)
	}
	chain := []rl.Evidence{grade(rl.KindReplication, 100*day, 3)}
	if rep := Decay(0.5, chain, 100*day+staleAfterSecs); rep.Stale {
		t.Error("a grade exactly at the 60-day boundary reported stale")
	}
	if rep := Decay(0.5, chain, 100*day+staleAfterSecs+1); !rep.Stale {
		t.Error("a grade one second past 60 days did not report stale")
	}
}

// Attacks move the posterior but are not GRADES: an attack does not reset the
// staleness clock, or a hypothesis could look actively tested while nothing had
// re-measured it.
func TestDecayAttacksDoNotCountAsGrades(t *testing.T) {
	chain := []rl.Evidence{
		grade(rl.KindReplication, 10*day, 3),
		{Kind: rl.KindAttack, Ts: 100 * day, BF: rl.PenaltySingleRegime, Note: "single-regime: calm only"},
	}
	rep := Decay(0.5, chain, 100*day)
	if rep.LastGradeTs != 10*day {
		t.Errorf("lastGradeTs = %d, want %d — an attack is not a re-measurement", rep.LastGradeTs, 10*day)
	}
}

// Decay replays each prefix through EffectiveChain, exactly as the ledger would
// have reported it at the time. Without that, repeated static attacks would
// compound across the replay and invent a decline the ledger never published.
func TestDecayReplaysThroughEffectiveChain(t *testing.T) {
	static := func(ts int64) rl.Evidence {
		return rl.Evidence{Kind: rl.KindAttack, Ts: ts, BF: rl.PenaltySingleRegime,
			Note: "single-regime: calm only"}
	}
	chain := []rl.Evidence{
		grade(rl.KindExperiment, 10*day, 2),
		static(10 * day),
		grade(rl.KindReplication, 20*day, 2),
		static(20 * day),
		grade(rl.KindReplication, 30*day, 2),
		static(30 * day),
	}
	rep := Decay(0.5, chain, 31*day)
	// One single-regime penalty, levied once: odds = 2*2*2*0.6 = 4.8. Three
	// levies would give 1.728 → 0.633, comfortably below the posterior clamp,
	// so this distinguishes the two behaviours rather than testing the clamp.
	want := (2.0 * 2 * 2 * rl.PenaltySingleRegime) / (1 + 2.0*2*2*rl.PenaltySingleRegime)
	if math.Abs(rep.Current-want) > 1e-9 {
		t.Errorf("current = %.6f, want %.6f (static attack levied once, not three times)", rep.Current, want)
	}
	if rep.EdgeWeakening {
		t.Error("three consecutive confirming replications reported as weakening")
	}
}

// An asserted row cannot manufacture a peak: the decay replay has to inherit
// the ledger's rule, or a hand-typed BF would show up as a historical high the
// belief has now "fallen from".
func TestDecayIgnoresAssertedPromotions(t *testing.T) {
	chain := []rl.Evidence{
		{Kind: rl.KindManual, Ts: 10 * day, N: 0, BF: rl.MaxBF, Note: "STATED JUDGMENT"},
		grade(rl.KindReplication, 20*day, 0.5),
	}
	rep := Decay(0.5, chain, 21*day)
	if rep.Peak > 0.5+1e-9 {
		t.Errorf("peak = %.4f, want <= 0.5 — an n=0 assertion set a historical high", rep.Peak)
	}
}
