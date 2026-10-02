package rvgrade

import (
	"math"
	"testing"
)

func TestStudentTTwoSidedPMatchesScipy(t *testing.T) {
	tests := []struct {
		t, df float64
		want  float64
	}{
		{0.0, 59, 1.0},
		{-1.0, 59, 0.3213942797694449},
		{2.0, 59, 0.05011041298824439},
		{-2.5, 10, 0.03144684423660882},
		{3.3, 59, 0.0016433069479587059},
		{-4.1, 119, 7.586401120145286e-05},
		{5.0, 7, 0.0015652779531728249},
		{-8.79, 1397, 4.30496926229526e-18},
		{1.96, 1000000, 0.04999606758526985},
		{0.3, 2, 0.7924856608401776},
	}
	for _, tt := range tests {
		got := StudentTTwoSidedP(tt.t, int(tt.df))
		if math.IsNaN(tt.want) {
			if !math.IsNaN(got) {
				t.Errorf("StudentTTwoSidedP(%v, %v) = %v, want NaN", tt.t, tt.df, got)
			}
			continue
		}
		var err float64
		if math.Abs(tt.want) < 1e-12 {
			err = math.Abs(got - tt.want)
			if err > 1e-15 {
				t.Errorf("StudentTTwoSidedP(%v, %v) = %v, want %v (abs err %v > 1e-15)", tt.t, tt.df, got, tt.want, err)
			}
		} else {
			err = math.Abs((got - tt.want) / tt.want)
			if err > 1e-9 {
				t.Errorf("StudentTTwoSidedP(%v, %v) = %v, want %v (rel err %v > 1e-9)", tt.t, tt.df, got, tt.want, err)
			}
		}
	}
	if got := StudentTTwoSidedP(0, 0); !math.IsNaN(got) {
		t.Errorf("StudentTTwoSidedP(0,0) = %v, want NaN", got)
	}
	if got := StudentTTwoSidedP(math.Inf(1), 5); got != 0 {
		t.Errorf("StudentTTwoSidedP(+Inf,5) = %v, want 0", got)
	}
	if !t.Failed() {
		t.Log("STUDENTT OK")
	}
}
