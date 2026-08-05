package researchledger

import (
	"math"
	"testing"

	"gonum.org/v1/gonum/stat"
)

// Smoke test: gonum resolves and its stats agree with known values.
func TestGonumSmoke(t *testing.T) {
	x := []float64{1, 2, 3, 4, 5, 6, 7, 8}
	y := []float64{2, 4, 6, 8, 10, 12, 14, 16}
	if got := stat.Mean(x, nil); got != 4.5 {
		t.Fatalf("mean = %v, want 4.5", got)
	}
	if got := stat.Correlation(x, y, nil); math.Abs(got-1.0) > 1e-12 {
		t.Fatalf("correlation = %v, want 1.0", got)
	}
	a, b := stat.LinearRegression(x, y, nil, false)
	if math.Abs(a) > 1e-9 || math.Abs(b-2.0) > 1e-9 {
		t.Fatalf("regression = (%v,%v), want (0,2)", a, b)
	}
}
