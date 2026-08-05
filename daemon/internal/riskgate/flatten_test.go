package riskgate

import (
	"math"
	"strings"
	"testing"
)

func healthy() Book {
	return Book{Equity: 100_000, Cash: 10_000, CurrentDrawdown: 0.05,
		DrawdownKnown: true, OpenPositions: 3}
}

func TestFlattenFiresAtAndPastTheThreshold(t *testing.T) {
	lim := Limits{}
	thr := FlattenLimit(lim)
	for _, dd := range []float64{thr, thr + 0.01, 0.90} {
		b := healthy()
		b.CurrentDrawdown = dd
		d := ShouldFlatten(b, lim)
		if !d.Flatten {
			t.Fatalf("drawdown %.2f at threshold %.2f must flatten", dd, thr)
		}
		if d.Reason == "" {
			t.Fatal("a forced liquidation with no stated cause is indistinguishable from a bug")
		}
	}
}

func TestFlattenHoldsBelowTheThreshold(t *testing.T) {
	lim := Limits{}
	b := healthy()
	b.CurrentDrawdown = FlattenLimit(lim) - 0.0001
	if d := ShouldFlatten(b, lim); d.Flatten {
		t.Fatalf("just below the rung must not liquidate: %+v", d)
	}
}

// The asymmetry that justifies this rung failing the opposite way to Admit().
func TestUnknownDrawdownDoesNotFlatten(t *testing.T) {
	lim := Limits{}
	b := healthy()
	b.CurrentDrawdown = 0.99
	b.DrawdownKnown = false
	if d := ShouldFlatten(b, lim); d.Flatten {
		t.Fatal("an unmeasured book must never be liquidated — the halt already covers it")
	}
	b.DrawdownKnown = true
	b.CurrentDrawdown = math.NaN()
	if d := ShouldFlatten(b, lim); d.Flatten {
		t.Fatal("NaN drawdown must never be liquidated")
	}
}

// This test exists because an earlier draft of flatten.go claimed the opposite
// and was wrong. Admit() does NOT halt on an unknown drawdown — it allows and
// records the breaker as unarmed. Pinning the real behaviour keeps anyone from
// reintroducing "the halt covers it" as a justification for failing open.
func TestUnknownDrawdownAllowsEntriesButSaysTheBreakerIsUnarmed(t *testing.T) {
	b := healthy()
	b.DrawdownKnown = false
	a := Admit(b, Limits{})
	if !a.Allow {
		t.Fatal("a young book with no equity history must still be allowed to start")
	}
	var said bool
	for _, r := range a.Reasons {
		if strings.Contains(r, "unarmed") {
			said = true
		}
	}
	if !said {
		t.Fatal("an unarmed breaker must be stated, never silently read as a clean pass")
	}
	for _, br := range a.Breaches {
		if br == "max-drawdown" {
			t.Fatal("an unmeasured drawdown is not a drawdown breach")
		}
	}
}

func TestUnusableEquityDoesNotFlatten(t *testing.T) {
	for _, eq := range []float64{0, -1, math.NaN(), math.Inf(1)} {
		b := healthy()
		b.Equity = eq
		b.CurrentDrawdown = 0.99
		if d := ShouldFlatten(b, Limits{}); d.Flatten {
			t.Fatalf("equity %v is not a book worth liquidating against", eq)
		}
	}
}

func TestNoOpenPositionsIsNotAFlatten(t *testing.T) {
	b := healthy()
	b.CurrentDrawdown = 0.99
	b.OpenPositions = 0
	if d := ShouldFlatten(b, Limits{}); d.Flatten {
		t.Fatal("a liquidation that closes nothing must not be reported as one")
	}
}

// The ladder must not invert: flatten may never sit tighter than suspend.
func TestFlattenNeverPrecedesTheSuspendRung(t *testing.T) {
	lim := Limits{MaxDrawdown: 0.40}
	if got := FlattenLimit(lim); got < lim.MaxDrawdown {
		t.Fatalf("flatten %.2f is tighter than suspend %.2f — the book would be "+
			"liquidated while still opening positions", got, lim.MaxDrawdown)
	}
	// Env misconfiguration is floored, not honoured.
	t.Setenv("SIGNALDECK_RISK_FLATTEN_DRAWDOWN", "0.01")
	suspend := (Limits{}).withDefaults().MaxDrawdown
	if got := FlattenLimit(Limits{}); got < suspend {
		t.Fatalf("a misconfigured flatten rung must be floored at the suspend rung "+
			"(%.4f), got %.4f", suspend, got)
	}
}

func TestDecisionEchoesWhatItSaw(t *testing.T) {
	b := healthy()
	b.CurrentDrawdown = 0.77
	d := ShouldFlatten(b, Limits{})
	if d.Measured != 0.77 || d.Threshold <= 0 {
		t.Fatalf("the ledger must record what the rule saw, got %+v", d)
	}
}
