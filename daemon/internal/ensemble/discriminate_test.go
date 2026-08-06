package ensemble

import "testing"

func TestKnotsDiscriminate(t *testing.T) {
	tests := []struct {
		name  string
		knots []float64
		want  bool
	}{
		{"nil slice is rejected", nil, false},
		{"single knot cannot discriminate", []float64{0.5}, false},
		{"flat map publishes one identical probability for every symbol", []float64{0.4635, 0.4635, 0.4635, 0.4635, 0.4635}, false},
		{"real spread discriminates", []float64{0.30, 0.45, 0.55, 0.70}, true},
		{"spread exactly at discriminationEps is rejected (strictly greater than)", []float64{0.5, 0.5 + 1e-4}, false},
		{"spread just above discriminationEps is accepted", []float64{0.5, 0.5 + 2e-4}, true},
		{"order independence uses range not direction", []float64{0.7, 0.3}, true},
		{"tiny real spread below discriminationEps is rejected", []float64{0.5, 0.5 + 1e-9}, false},
	}

	for _, tt := range tests {
		if got := KnotsDiscriminate(tt.knots); got != tt.want {
			t.Errorf("%s: KnotsDiscriminate(%v) = %v, want %v", tt.name, tt.knots, got, tt.want)
		}
	}
}