package expectancy

import (
	"math"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// TestEvidenceFloorsRaised pins the two gates. minSamples=5 let a five-row cell
// emit a production probability; 30 matches the platform-wide observation floor
// (modelhealth.MinObservations, ensemble.MinCalibrationPairs).
func TestEvidenceFloorsRaised(t *testing.T) {
	if minSamples < 30 {
		t.Errorf("minSamples = %d, want >= 30", minSamples)
	}
	if minDistinctDays < 5 {
		t.Errorf("minDistinctDays = %d, want >= 5", minDistinctDays)
	}
}

// TestShrinkHitRatePullsTowardBase is the label-noise fix. A thin cell with an
// extreme raw rate must not publish that rate.
func TestShrinkHitRatePullsTowardBase(t *testing.T) {
	const base = 0.52

	// A perfect 30/30 must NOT publish 1.0.
	got := shrinkHitRate(30, 30, base)
	if got >= 1.0 {
		t.Errorf("shrinkHitRate(30,30,%v) = %v, want strictly below 1.0", base, got)
	}
	want := (30.0 + expectancyPriorStrength*base) / (30.0 + expectancyPriorStrength)
	if math.Abs(got-want) > 1e-12 {
		t.Errorf("shrinkHitRate(30,30) = %v, want %v", got, want)
	}

	// A 0/30 must NOT publish 0.0.
	if z := shrinkHitRate(0, 30, base); z <= 0 {
		t.Errorf("shrinkHitRate(0,30,%v) = %v, want strictly above 0", base, z)
	}

	// Shrinkage must always land between the raw rate and the base rate.
	for _, c := range []struct{ hits, n int }{{29, 30}, {1, 30}, {60, 100}, {5, 40}} {
		raw := float64(c.hits) / float64(c.n)
		s := shrinkHitRate(c.hits, c.n, base)
		lo, hi := math.Min(raw, base), math.Max(raw, base)
		if s < lo-1e-12 || s > hi+1e-12 {
			t.Errorf("shrinkHitRate(%d,%d,%v) = %v, outside [%v,%v]", c.hits, c.n, base, s, lo, hi)
		}
	}

	// More evidence must move the result closer to the raw rate: 900/1000 should
	// sit nearer 0.9 than 27/30 does.
	near := shrinkHitRate(900, 1000, base)
	far := shrinkHitRate(27, 30, base)
	if math.Abs(near-0.9) >= math.Abs(far-0.9) {
		t.Errorf("shrinkage does not weaken with evidence: n=1000 gave %v, n=30 gave %v", near, far)
	}

	// Degenerate input must not panic or produce NaN.
	if v := shrinkHitRate(0, 0, base); !finiteFloat(v) {
		t.Errorf("shrinkHitRate(0,0) = %v, want a finite fallback", v)
	}
}

// TestBuildRespectsSampleFloor: nothing thinner than the floor may be emitted.
func TestBuildRespectsSampleFloor(t *testing.T) {
	n := 400
	closes := make([]float64, n)
	vols := make([]float64, n)
	p := 100.0
	s := uint64(20260804)
	for i := range closes {
		s = s*6364136223846793005 + 1442695040888963407
		u := float64((s>>11)&((1<<53)-1)) / float64(uint64(1)<<53)
		p *= 1 + (u-0.5)*0.03
		closes[i], vols[i] = p, 1e6*(0.5+u)
	}
	out := Build(mkBars(closes, vols), nil)
	for h, rows := range out {
		for _, r := range rows {
			if r.N < minSamples {
				t.Errorf("horizon %v state %q emitted with N=%d, below floor %d", h, r.StateKey, r.N, minSamples)
			}
			// Shrinkage must have removed saturation from every published rate.
			if r.HitRate <= 0 || r.HitRate >= 1 {
				t.Errorf("horizon %v state %q published a saturated HitRate %v", h, r.StateKey, r.HitRate)
			}
		}
	}
}

// TestMinuteWalkGatedByDistinctDays is the point of the day floor: the 1h walk
// samples every 15 minute-bars, so a single session can manufacture hundreds of
// rows that carry the information of ONE day. Rows are not observations.
func TestMinuteWalkGatedByDistinctDays(t *testing.T) {
	// 1200 consecutive minute bars = ~20 hours, spanning at most two UTC days.
	const n = 1200
	closes := make([]float64, n)
	vols := make([]float64, n)
	ts := make([]int64, n)
	p := 100.0
	s := uint64(7)
	base := int64(1785000000) // fixed instant; bars are 60s apart
	for i := range closes {
		s = s*6364136223846793005 + 1442695040888963407
		u := float64((s>>11)&((1<<53)-1)) / float64(uint64(1)<<53)
		p *= 1 + (u-0.5)*0.004
		closes[i], vols[i] = p, 1e5*(0.5+u)
		ts[i] = base + int64(i)*60
	}
	minute := mkBars(closes, vols)
	for i := range minute {
		minute[i].Ts = ts[i]
	}
	out := Build(nil, minute)
	if rows, ok := out[marketdata.H1h]; ok && len(rows) > 0 {
		t.Errorf("1h emitted %d state(s) from bars spanning <= 2 UTC days; the distinct-day floor (%d) must drop them",
			len(rows), minDistinctDays)
	}
}

func finiteFloat(x float64) bool { return !math.IsNaN(x) && !math.IsInf(x, 0) }
