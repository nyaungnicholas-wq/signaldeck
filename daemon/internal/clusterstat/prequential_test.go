package clusterstat

import (
	"math"
	"testing"
)

func floatEq(a, b, tol float64) bool {
	return math.Abs(a-b) < tol
}

func TestPrequentialBaselineEmpty(t *testing.T) {
	got := PrequentialBaseline(nil)
	if !floatEq(got, 0.5, 1e-9) {
		t.Errorf("empty slice: got %v, want 0.5", got)
	}
}

func TestPrequentialBaselineSingleDay(t *testing.T) {
	days := []DayLabel{{N: 10, Ups: 7}}
	got := PrequentialBaseline(days)
	if !floatEq(got, 0.5, 1e-9) {
		t.Errorf("single day: got %v, want 0.5 (no prior -> coin flip)", got)
	}
}

func TestPrequentialBaselineTwoDaysUpPrior(t *testing.T) {
	days := []DayLabel{
		{N: 10, Ups: 8},
		{N: 10, Ups: 6},
	}
	got := PrequentialBaseline(days)
	if !floatEq(got, 0.55, 1e-9) {
		t.Errorf("two days up prior: got %v, want 0.55", got)
	}
}

func TestPrequentialBaselineTiedPrior(t *testing.T) {
	days := []DayLabel{
		{N: 10, Ups: 5},
		{N: 10, Ups: 9},
	}
	got := PrequentialBaseline(days)
	if !floatEq(got, 0.5, 1e-9) {
		t.Errorf("tied prior: got %v, want 0.5", got)
	}
}

func TestPrequentialBaselineDownPrior(t *testing.T) {
	days := []DayLabel{
		{N: 10, Ups: 2},
		{N: 10, Ups: 3},
	}
	got := PrequentialBaseline(days)
	if !floatEq(got, 0.6, 1e-9) {
		t.Errorf("down-majority prior: got %v, want 0.6", got)
	}
}

func TestPrequentialBaselineHighImbalanceRegression(t *testing.T) {
	days := []DayLabel{
		{N: 100, Ups: 96},
		{N: 100, Ups: 96},
		{N: 100, Ups: 96},
		{N: 100, Ups: 96},
		{N: 100, Ups: 96},
	}
	got := PrequentialBaseline(days)
	if got >= 0.96 {
		t.Errorf("oracle baseline regressed: got %v, must be < 0.96", got)
	}
	if got <= 0.5 {
		t.Errorf("oracle baseline regressed: got %v, must be > 0.5", got)
	}
}

func TestPrequentialBaselineDeterminism(t *testing.T) {
	days := []DayLabel{
		{N: 10, Ups: 8},
		{N: 10, Ups: 6},
	}
	got1 := PrequentialBaseline(days)
	got2 := PrequentialBaseline(days)
	if !floatEq(got1, got2, 1e-9) {
		t.Errorf("determinism failed: first=%v, second=%v", got1, got2)
	}
}

func TestDayLabelsFromMismatchedLengths(t *testing.T) {
	got := DayLabelsFrom([]int64{100, 200}, []float64{0.5})
	if got != nil {
		t.Errorf("mismatched lengths: expected nil, got %v", got)
	}
}

func TestDayLabelsFromReverseOrder(t *testing.T) {
	// Two days, but fed in reverse chronological order. Both stamps sit at
	// 12:00Z so each lands mid trading day rather than near the fold.
	// Day 0: 43200, actual 0.6 -> up
	// Day 1: 129600, actual 0.4 -> down
	ts := []int64{86400 + 43200, 43200}
	actuals := []float64{0.4, 0.6}
	got := DayLabelsFrom(ts, actuals)
	if len(got) != 2 {
		t.Fatalf("expected 2 day labels, got %d", len(got))
	}
	if got[0].Day != 0 || got[0].N != 1 || got[0].Ups != 1 {
		t.Errorf("first day label (day 0): got %+v, want {Day:0, N:1, Ups:1}", got[0])
	}
	if got[1].Day != 1 || got[1].N != 1 || got[1].Ups != 0 {
		t.Errorf("second day label (day 1): got %+v, want {Day:1, N:1, Ups:0}", got[1])
	}
}

func TestDayLabelsFromSameDayCollapse(t *testing.T) {
	// Three timestamps in the same trading day (day 0): 06:00Z, 12:00Z and
	// 23:59:59Z all fold together once the boundary is off UTC midnight.
	// Actuals: 0.5 (up), 0.49 (down), 0.6 (up) -> two ups, one down.
	ts := []int64{21600, 43200, 86399}
	actuals := []float64{0.5, 0.49, 0.6}
	got := DayLabelsFrom(ts, actuals)
	if len(got) != 1 {
		t.Fatalf("expected 1 day label, got %d", len(got))
	}
	if got[0].Day != 0 || got[0].N != 3 || got[0].Ups != 2 {
		t.Errorf("collapsed day: got %+v, want {Day:0, N:3, Ups:2}", got[0])
	}
}

func TestDayLabelsFromEmpty(t *testing.T) {
	got := DayLabelsFrom(nil, nil)
	if got == nil {
		t.Fatal("expected non-nil slice for empty input")
	}
	if DistinctDays(got) != 0 {
		t.Errorf("expected 0 distinct days, got %d", DistinctDays(got))
	}
}
