package risklens

import (
	"strings"
	"testing"
)

// TestHistoricalVaR_ThinTailWithheld is the second half of the C7 regression.
//
// BEFORE the fix, a 95% VaR was published from a 59-return window — the sample
// the package's own MinCloses=60 floor permits — where floor(0.05*59)=2 puts
// exactly THREE observations in the loss tail. Three points decided a number
// the UI rendered as "you could lose $6,000". This test asserts the number is
// now withheld, with the tail count and the required window in the reason.
func TestHistoricalVaR_ThinTailWithheld(t *testing.T) {
	rets := repeatPattern([]float64{
		-0.05, 0.02, -0.03, 0.01, 0.04, -0.02, 0.03, -0.01, 0.00, -0.06,
	}, 59)
	v, cv, gate := HistoricalVaR(rets, 0.95)
	if v != nil || cv != nil {
		t.Fatalf("published VaR from a %d-return window (tail of %d); want withheld", gate.N, gate.TailN)
	}
	if !gate.Withheld {
		t.Error("gate.Withheld must be true when the figures are nil")
	}
	if gate.TailN != 3 {
		t.Errorf("tail observations=%d, want 3 (the count the review measured)", gate.TailN)
	}
	if gate.NeedN != 180 {
		t.Errorf("NeedN=%d, want 180 return days for a 10-point tail at 95%%", gate.NeedN)
	}
	for _, want := range []string{"loss tail", "180"} {
		if !strings.Contains(gate.Reason, want) {
			t.Errorf("reason %q missing %q", gate.Reason, want)
		}
	}
}

// TestHistoricalVaR_HandComputed checks the arithmetic on a window that clears
// the gate. 200 returns, alpha=0.05 -> idx=10, so the tail is the 11 worst.
// The pattern below repeats 10x, so the ten smallest are all -0.10 and
// sorted[10] is the first -0.06:
//
//	VaR  = -sorted[10]                     = 0.06
//	CVaR = -(10*(-0.10) + (-0.06)) / 11    = 1.06/11 = 0.0963636…
func TestHistoricalVaR_HandComputed(t *testing.T) {
	pattern := []float64{
		-0.10, -0.06, -0.05, -0.04, -0.03,
		-0.02, -0.01, 0.00, 0.005, 0.01,
		0.012, 0.015, 0.02, 0.025, 0.03,
		0.035, 0.04, 0.05, 0.06, 0.08,
	}
	rets := repeatPattern(pattern, 200)
	v, cv, gate := HistoricalVaR(rets, 0.95)
	if v == nil || cv == nil {
		t.Fatalf("withheld at n=200: %s", gate.Reason)
	}
	if gate.TailN != 11 {
		t.Errorf("TailN=%d want 11", gate.TailN)
	}
	if !approx(*v, 0.06, 1e-9) {
		t.Errorf("VaR=%.6f want 0.06", *v)
	}
	if !approx(*cv, 1.06/11, 1e-9) {
		t.Errorf("CVaR=%.9f want %.9f", *cv, 1.06/11)
	}
	// CVaR must be at least as severe as VaR.
	if *cv < *v {
		t.Errorf("CVaR %.4f < VaR %.4f (must be >=)", *cv, *v)
	}
}

func TestHistoricalVaR_Edge(t *testing.T) {
	long := repeatPattern([]float64{-0.02, 0.01, 0.03}, 300)
	tests := []struct {
		name         string
		rets         []float64
		confVal      float64
		wantWithheld bool
	}{
		{"empty", nil, 0.95, true},
		{"three_points", []float64{-0.02, 0.01, 0.03}, 0.95, true},
		// Confidence clamps to 0.9999 -> alpha 1e-4 -> a 10-point tail would need
		// ~90,000 days, so even a long window is (correctly) refused.
		{"conf_clamped_high", long, 1.5, true},
		// Clamps to 0.0001 -> alpha 0.9999 -> the "tail" is nearly the whole
		// sample, which clears the floor trivially.
		{"conf_clamped_low", long, -0.5, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v, cv, gate := HistoricalVaR(tc.rets, tc.confVal)
			if gate.Withheld != tc.wantWithheld {
				t.Fatalf("withheld=%v want %v (reason %q)", gate.Withheld, tc.wantWithheld, gate.Reason)
			}
			if gate.Withheld && (v != nil || cv != nil) {
				t.Error("withheld gate must return nil figures, never 0")
			}
			if !gate.Withheld && (v == nil || cv == nil) {
				t.Error("admitted gate must return figures")
			}
		})
	}
}
