// Evidence-gate thresholds are PINNED here on purpose.
//
// The three structural predictors have thousands of forecasts outstanding and
// the first of them becomes gradable on 2026-08-07. Between now and then the
// only way any of them can appear to earn a verdict is if a threshold moves —
// and a threshold moved while waiting for evidence is indistinguishable, after
// the fact, from a threshold that was always that low. That is the single
// failure this platform exists to prevent, and it is most tempting precisely
// when the wait is nearly over.
//
// So these constants are asserted, not merely used. Loosening one is allowed —
// it is the author's call — but it cannot happen quietly: the test fails, and
// changing it is a deliberate edit that shows up in a diff next to this
// paragraph. Tightening a gate fails too, for the same reason in reverse; a
// gate that moves with the data it is judging is not a gate.
package api

import (
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/canary"
	"github.com/nyaungnicholas-wq/signaldeck/internal/modelhealth"
	rl "github.com/nyaungnicholas-wq/signaldeck/internal/researchledger"
)

func TestEvidenceGatesArePinned(t *testing.T) {
	cases := []struct {
		name string
		got  any
		want any
		why  string
	}{
		{
			"api.minIndependentN", minIndependentN, 30,
			"independent observations before ANY live skill claim is unlocked",
		},
		{
			"modelhealth.MinObservations", modelhealth.MinObservations, 30,
			"observations before a model may be graded or retired in either direction",
		},
		{
			"canary.MinObservations", canary.MinObservations, 30,
			"observations before a challenger version may take production",
		},
		{
			"canary.MinWindowDays", canary.MinWindowDays, 14,
			"days a challenger must run — long enough to have seen more than one market state",
		},
		{
			"canary.MinMarginPp", canary.MinMarginPp, 0.5,
			"percentage points a challenger must beat the incumbent by, so noise cannot promote",
		},
		{
			"researchledger.MinReplications", rl.MinReplications, 2,
			"independent disjoint-window grades before a belief may reach supported",
		},
		{
			"researchledger.MinRegimes", rl.MinRegimes, 2,
			"volatility regimes a belief must survive — a single-regime discovery caps at tentative",
		},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %v, pinned at %v.\n"+
				"This gate controls %s.\n"+
				"If the change is deliberate, update this test in the same commit and say why. "+
				"If it is not, revert it: the first structural forecasts become gradable 2026-08-07, "+
				"and a gate that moves while evidence is pending proves nothing.",
				c.name, c.got, c.want, c.why)
		}
	}
}

// The tradability gate is part of the same promise: a hypothesis cannot reach
// "supported" without a position that was graded net of costs. Asserting the
// band directly means deleting the gate breaks a test rather than silently
// promoting eight hypotheses that are sitting at posterior 0.90+.
func TestSupportedStillRequiresAGradedPosition(t *testing.T) {
	strong := 0.97
	g := rl.Gates{MachineGrades: 3, Replications: rl.MinReplications, Regimes: rl.MinRegimes}
	if got := rl.StatusWithGates(strong, g); got == rl.StatusSupported {
		t.Fatal("a posterior of 0.97 reached 'supported' with no tradable form stated")
	}
	g.TradableForm = "a stated position"
	if got := rl.StatusWithGates(strong, g); got == rl.StatusSupported {
		t.Fatal("'supported' granted for a position that was never graded net of costs")
	}
	g.EconomicTest = "graded, and it failed"
	if got := rl.StatusWithGates(strong, g); got != rl.StatusSupported {
		t.Fatalf("every gate met but status = %q", got)
	}
	// ...and the machine-evidence floor binds on top of all of it: a chain of
	// hand-entered `manual` rows cannot report a confident band at any
	// posterior, however many other gates are met.
	manual := g
	manual.MachineGrades = 0
	if got := rl.StatusWithGates(strong, manual); got != rl.StatusUncertain {
		t.Fatalf("manual-only chain at 0.97 = %q, want %q", got, rl.StatusUncertain)
	}
}
