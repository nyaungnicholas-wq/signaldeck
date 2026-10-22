package ensemble

import "testing"

// A fit that pools to one block answers the same probability for every input.
// Serving refuses such a map (KnotsDiscriminate), so the fitter must refuse it
// too, or the hourly learner persists a map every pass then refuses forever
// (2026-10-11: LQD 1w, 523 "refused stale per-symbol maps" warnings).
func TestCalibrateKnotsRefusesFlatMap(t *testing.T) {
	var pairs []Pair
	day := int64(1_700_000_000)
	for i := 0; i < 40; i++ {
		// Anti-ordered: low forecasts went up, high forecasts went down, so
		// pool-adjacent-violators merges every block into one flat value.
		actual := 1.0
		if i >= 20 {
			actual = 0
		}
		pairs = append(pairs, Pair{Pred: 0.30 + 0.01*float64(i), Actual: actual, Ts: day + int64(i)*86400})
	}
	if kx, ky, ok := CalibrateKnots(pairs); ok {
		t.Fatalf("flat map was fitted: kx=%v ky=%v (serving would refuse it every pass)", kx, ky)
	}
}
