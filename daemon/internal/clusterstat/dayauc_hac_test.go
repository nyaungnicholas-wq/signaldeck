package clusterstat

import (
	"math"
	"math/rand"
	"testing"
)

// Lag 0 must be exactly the independence interval it generalises.
func TestDayClusteredAUCLagZeroIsTheOldInterval(t *testing.T) {
	daily := []float64{0.52, 0.55, 0.49, 0.61, 0.50, 0.58, 0.47, 0.56, 0.53, 0.60, 0.51, 0.57}
	m0, lo0, hi0, ok0 := DayClusteredAUC(daily)
	m1, lo1, hi1, ok1 := DayClusteredAUCLag(daily, 0)
	if !ok0 || !ok1 || m0 != m1 || math.Abs(lo0-lo1) > 1e-15 || math.Abs(hi0-hi1) > 1e-15 {
		t.Fatalf("lag 0 (%v %v %v) != old (%v %v %v)", m1, lo1, hi1, m0, lo0, hi0)
	}
}

// A 1w-style series: each value is the mean of the next 5 "session" shocks, so
// neighbours share 4 of 5 inputs. The overlap-corrected interval must be clearly
// wider, and a mean whose independence bound clears 0.5 must stop clearing it.
func TestDayClusteredAUCLagWidensOnOverlappingLabels(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	shocks := make([]float64, 40)
	for i := range shocks {
		shocks[i] = 0.06 * rng.NormFloat64()
	}
	daily := make([]float64, 35)
	for i := range daily {
		s := 0.0
		for k := 0; k < 5; k++ {
			s += shocks[i+k]
		}
		daily[i] = 0.515 + s/5
	}
	_, lo0, hi0, _ := DayClusteredAUC(daily)
	_, lo4, hi4, ok := DayClusteredAUCLag(daily, 4)
	if !ok {
		t.Fatal("35 days must be measured")
	}
	if (hi4 - lo4) < 1.3*(hi0-lo0) {
		t.Fatalf("overlap-corrected width %.4f not clearly wider than independence width %.4f", hi4-lo4, hi0-lo0)
	}
}

func TestDayClusteredAUCLagRespectsTheFloorAndClamps(t *testing.T) {
	if _, _, _, ok := DayClusteredAUCLag(make([]float64, MinDaysForInterval-1), 4); ok {
		t.Fatal("below the day floor must be unmeasured")
	}
	daily := []float64{0.5, 0.6, 0.4, 0.55, 0.45, 0.52, 0.48, 0.51, 0.49, 0.5}
	if _, _, _, ok := DayClusteredAUCLag(daily, 99); !ok {
		t.Fatal("a lag longer than the series must clamp, not fail")
	}
	if _, _, _, ok := DayClusteredAUCLag(daily, -3); !ok {
		t.Fatal("a negative lag must be treated as 0")
	}
}

func TestVetoOnDayClusteredAUCLagUsesTheCorrectedInterval(t *testing.T) {
	daily := make([]float64, 20)
	for i := range daily {
		daily[i] = 0.40 + 0.01*float64(i%3)
	}
	veto, mean, measured := VetoOnDayClusteredAUCLag(daily, 4)
	if !measured || !veto || mean > 0.45 {
		t.Fatalf("a clearly backwards leg must still be vetoed: veto=%v mean=%v measured=%v", veto, mean, measured)
	}
}
