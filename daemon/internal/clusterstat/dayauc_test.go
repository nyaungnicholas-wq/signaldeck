package clusterstat

import (
	"math"
	"testing"
)

// THE REAL MEASUREMENT. These are the 16 within-day cross-sectional AUCs for the
// pressure leg on the live 1d stock record (weekday rows, one per symbol-day,
// 2026-07-14..2026-08-06). The n-weighted mean of per-symbol AUCs on the same
// data is 0.3614 and vetoed the leg outright; this estimator cannot distinguish
// it from chance. If that conclusion ever changes, it must change here first.
var livePressureDailyAUC = []float64{
	0.4546, 0.4928, 0.6056, 0.4984, 0.4847, 0.3611, 0.5695, 0.5214,
	0.3494, 0.6810, 0.2588, 0.3937, 0.3909, 0.5257, 0.4423, 0.3870,
}

func TestLivePressureIsIndistinguishableFromChance(t *testing.T) {
	mean, lo, hi, ok := DayClusteredAUC(livePressureDailyAUC)
	if !ok {
		t.Fatalf("16 days did not produce an interval")
	}
	if math.Abs(mean-0.4636) > 0.0005 {
		t.Errorf("mean daily AUC = %.4f, want ~0.4636", mean)
	}
	if !(lo < 0.5 && hi > 0.5) {
		t.Errorf("interval [%.4f, %.4f] does not straddle 0.5 — the live record does", lo, hi)
	}
	// And therefore it must NOT be vetoed.
	veto, _, measured := VetoOnDayClusteredAUC(livePressureDailyAUC)
	if !measured {
		t.Fatal("16 days read as unmeasured")
	}
	if veto {
		t.Errorf("the live pressure leg was vetoed on an interval [%.4f, %.4f] containing chance; "+
			"that is the estimator artifact this change exists to remove", lo, hi)
	}
}

// A leg that is GENUINELY backwards must still be vetoed. The veto is not being
// weakened, it is being made conditional on evidence.
func TestGenuinelyBackwardsLegIsStillVetoed(t *testing.T) {
	daily := make([]float64, 16)
	for i := range daily {
		daily[i] = 0.34 + 0.01*float64(i%3) // tightly clustered well below chance
	}
	veto, mean, measured := VetoOnDayClusteredAUC(daily)
	if !measured {
		t.Fatal("16 days read as unmeasured")
	}
	if !veto {
		_, lo, hi, _ := DayClusteredAUC(daily)
		t.Errorf("a leg at mean %.4f, interval [%.4f, %.4f] entirely below chance was not vetoed",
			mean, lo, hi)
	}
}

// A genuinely GOOD leg is never vetoed.
func TestPredictiveLegIsNotVetoed(t *testing.T) {
	daily := make([]float64, 16)
	for i := range daily {
		daily[i] = 0.62 + 0.01*float64(i%3)
	}
	if veto, _, _ := VetoOnDayClusteredAUC(daily); veto {
		t.Error("a leg well above chance was vetoed")
	}
}

// Too few days must read as UNMEASURED and never veto: absence of evidence is
// not evidence of backwardness, exactly as it is not evidence of edge.
func TestThinWindowIsUnmeasuredAndNeverVetoes(t *testing.T) {
	for _, n := range []int{0, 1, 2, MinDaysForInterval - 1} {
		daily := make([]float64, n)
		for i := range daily {
			daily[i] = 0.20 // catastrophically backwards, but on too few days
		}
		veto, _, measured := VetoOnDayClusteredAUC(daily)
		if measured {
			t.Errorf("%d days reported as measured", n)
		}
		if veto {
			t.Errorf("%d days produced a veto; a thin window must never bench a leg", n)
		}
	}
}

// The interval must widen as days get noisier — a guard against a stub that
// returns a constant width.
func TestIntervalRespondsToDispersion(t *testing.T) {
	tight := make([]float64, 16)
	wide := make([]float64, 16)
	for i := range tight {
		tight[i] = 0.50
		wide[i] = 0.50 + 0.20*float64(i%2*2-1)
	}
	_, tl, th, _ := DayClusteredAUC(tight)
	_, wl, wh, _ := DayClusteredAUC(wide)
	if (th - tl) >= (wh - wl) {
		t.Errorf("identical days gave a wider interval (%.4f) than dispersed days (%.4f)",
			th-tl, wh-wl)
	}
}
