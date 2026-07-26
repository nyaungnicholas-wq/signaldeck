package risklens

import (
	"math"
	"strings"
	"testing"
)

// The C7 follow-up: HistoricalVaR now withholds below its tail floor, which
// promoted ParametricVaR from "smooth complement" to "the only VaR on screen".
// The parametric figure is the one that UNDERSTATES tails, and it shipped from
// any sample with n>=2. These tests are the gate that closes that hole.

// TestParametricVaR_ThinSampleWithheld is the sample-size half of the gate.
//
// BEFORE: a five-return series produced a published Gaussian VaR. The scale
// estimate behind it has a relative standard error of 1/sqrt(2(n-1)) = 35% at
// n=5 — the number is noise wearing a decimal point. Assert it is withheld and
// that the reason names the floor.
func TestParametricVaR_ThinSampleWithheld(t *testing.T) {
	rets := []float64{-0.02, -0.01, 0, 0.01, 0.02}
	v, gate := ParametricVaR(rets, 0.95)
	if v != nil {
		t.Fatalf("published a Gaussian VaR of %.6f from %d returns; want withheld", *v, len(rets))
	}
	if !gate.Withheld {
		t.Error("gate.Withheld must be true when the figure is nil")
	}
	if gate.MinN != MinParametricVaRObservations {
		t.Errorf("gate.MinN=%d want %d", gate.MinN, MinParametricVaRObservations)
	}
	if !strings.Contains(gate.Reason, "49") {
		t.Errorf("reason %q must name the %d-observation floor", gate.Reason, MinParametricVaRObservations)
	}
}

// TestParametricVaR_NonNormalWithheld is the assumption half of the gate, and
// the one that matters most: the Gaussian VaR's ONLY claim is that returns are
// normal. A sample that rejects normality at 1% (Jarque-Bera) falsifies that
// claim, so the number must not ship — publishing it is exactly the
// understatement the review measured. The reason must name the assumption.
func TestParametricVaR_NonNormalWithheld(t *testing.T) {
	// 200 quiet days plus a handful of crash days: heavily fat-tailed, the
	// shape of every real equity return series.
	rets := make([]float64, 0, 205)
	for i := 0; i < 200; i++ {
		if i%2 == 0 {
			rets = append(rets, 0.001)
		} else {
			rets = append(rets, -0.001)
		}
	}
	rets = append(rets, -0.09, 0.08, -0.11, 0.10, -0.12)

	v, gate := ParametricVaR(rets, 0.95)
	if v != nil {
		t.Fatalf("published a Gaussian VaR of %.6f on a sample that rejects normality; want withheld", *v)
	}
	if !gate.Withheld {
		t.Error("gate.Withheld must be true when the figure is nil")
	}
	if gate.NormalityRejected != true {
		t.Error("NormalityRejected must be true for a fat-tailed sample")
	}
	if gate.ExcessKurtosis <= 1 {
		t.Errorf("excess kurtosis=%.3f, expected a clearly fat-tailed sample", gate.ExcessKurtosis)
	}
	if !strings.Contains(strings.ToLower(gate.Reason), "normal") {
		t.Errorf("reason %q must name the normality assumption it failed", gate.Reason)
	}
}

// TestParametricVaR_PublishesWhenNormalAndLarge proves the gate is not a
// blanket refusal: a large, genuinely Gaussian-shaped sample still gets a
// number, and the arithmetic is unchanged (-(mean + z*sd)).
func TestParametricVaR_PublishesWhenNormalAndLarge(t *testing.T) {
	rets := gaussianish(600)
	v, gate := ParametricVaR(rets, 0.95)
	if v == nil {
		t.Fatalf("withheld on a %d-return near-normal sample: %s", len(rets), gate.Reason)
	}
	mean, std := meanStd(rets)
	want := -(mean + normInvCDF(0.05)*std)
	if math.Abs(*v-want) > 1e-12 {
		t.Errorf("ParametricVaR=%.10f want %.10f", *v, want)
	}
	if gate.Withheld {
		t.Error("gate.Withheld must be false when a figure is published")
	}
	if gate.Assumes == "" {
		t.Error("a published Gaussian VaR must state its normality assumption in gate.Assumes")
	}
}

// TestParametricVaR_DegenerateStillWithheld keeps the old contract: nil, never
// 0. A 0 renders as "this portfolio cannot lose money".
func TestParametricVaR_DegenerateStillWithheld(t *testing.T) {
	if v, _ := ParametricVaR(nil, 0.95); v != nil {
		t.Errorf("nil series -> %.6f want withheld", *v)
	}
	if v, _ := ParametricVaR([]float64{0.01}, 0.95); v != nil {
		t.Errorf("single return -> %.6f want withheld", *v)
	}
}

// TestSummary_StatesTheNormalityAssumption: the review's third ask — say what
// it assumes WHERE IT IS DISPLAYED. The plain-English summary is the surface a
// non-quant reads, so the word "normal" has to appear next to the figure, and
// a withheld parametric VaR must be said out loud rather than silently absent.
func TestSummary_StatesTheNormalityAssumption(t *testing.T) {
	rets := gaussianish(600)
	closes := closesFromReturns(100, rets)
	holdings := []Holding{{Symbol: "AAA", Weight: 1}}
	series := []Series{{Symbol: "AAA", Closes: closes}}

	report, err := Analyze(holdings, series, Series{}, 0.95, 100000)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if report.ParamVaRPct == nil {
		t.Fatalf("expected a published parametric VaR on a near-normal %d-day window: %s",
			len(rets), report.ParamVaRGate.Reason)
	}
	s := Summary(report)
	low := strings.ToLower(s)
	if !strings.Contains(low, "normal") {
		t.Errorf("summary must name the normality assumption beside the figure; got %q", s)
	}
	if !strings.Contains(low, "understate") {
		t.Errorf("summary must say the normal model understates fat tails; got %q", s)
	}
}

// TestSummary_SaysWhyParametricVaRWasWithheld: silence is not disclosure. When
// the parametric figure is gated the summary must say so, otherwise a reader
// sees a report with one VaR and assumes the other simply was not computed.
func TestSummary_SaysWhyParametricVaRWasWithheld(t *testing.T) {
	// Fat-tailed daily returns over a long window: historical VaR clears its
	// tail floor, parametric is rejected for non-normality.
	rets := make([]float64, 0, 400)
	for i := 0; i < 400; i++ {
		switch {
		case i%97 == 0:
			rets = append(rets, -0.10)
		case i%2 == 0:
			rets = append(rets, 0.0015)
		default:
			rets = append(rets, -0.0012)
		}
	}
	closes := closesFromReturns(100, rets)
	report, err := Analyze(
		[]Holding{{Symbol: "AAA", Weight: 1}},
		[]Series{{Symbol: "AAA", Closes: closes}},
		Series{}, 0.95, 100000)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if report.ParamVaRPct != nil {
		t.Fatalf("expected the parametric VaR withheld on a fat-tailed sample, got %.6f", *report.ParamVaRPct)
	}
	s := Summary(report)
	if !strings.Contains(strings.ToLower(s), "normal") {
		t.Errorf("summary must explain the withheld normal-model VaR; got %q", s)
	}
}

// gaussianish builds n deterministic returns whose shape is close to normal:
// the inverse-normal CDF evaluated on an evenly spaced grid, scaled to a ~1.5%
// daily vol. Deterministic so the test never flakes (no RNG in this repo's
// production or test paths).
func gaussianish(n int) []float64 {
	out := make([]float64, n)
	for i := 0; i < n; i++ {
		p := (float64(i) + 0.5) / float64(n)
		out[i] = normInvCDF(p) * 0.015
	}
	return out
}
