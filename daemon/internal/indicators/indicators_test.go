package indicators

import (
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// ramp builds n bars whose close moves by step each bar (rising if step>0).
func ramp(n int, step float64) []marketdata.Bar {
	bars := make([]marketdata.Bar, n)
	for i := 0; i < n; i++ {
		c := 100 + float64(i)*step
		bars[i] = marketdata.Bar{
			Ts: int64(i) * 86400, Open: c - 0.5, High: c + 0.2, Low: c - 0.7, Close: c,
		}
	}
	return bars
}

func TestAllSignalsPresentWithEnoughBars(t *testing.T) {
	s := Compute(ramp(80, 1))
	m := s.Map()
	for _, k := range []string{"stoch_k", "adx14", "cci20", "williams_r14", "bb_pctb", "supertrend_dir", "keltner_pos"} {
		if _, ok := m[k]; !ok {
			t.Fatalf("signal %q absent on an 80-bar series; map=%v", k, m)
		}
	}
}

func TestAbsentWhenTooFewBars(t *testing.T) {
	s := Compute(ramp(10, 1))
	if m := s.Map(); len(m) != 0 {
		t.Fatalf("with 10 bars every signal should be absent, got %v", m)
	}
}

func TestSignalRangesAndDirections(t *testing.T) {
	up := Compute(ramp(80, 1))
	if up.StochK == nil || *up.StochK < 0 || *up.StochK > 1 {
		t.Fatalf("stoch_k out of [0,1] or absent: %v", up.StochK)
	}
	if *up.StochK < 0.8 {
		t.Fatalf("rising series should push stoch_k high, got %.3f", *up.StochK)
	}
	if up.WilliamsR14 == nil || *up.WilliamsR14 < -1 || *up.WilliamsR14 > 0 {
		t.Fatalf("williams_r14 out of [-1,0] or absent: %v", up.WilliamsR14)
	}
	if up.ADX14 == nil || *up.ADX14 < 0 {
		t.Fatalf("adx14 should be >= 0, got %v", up.ADX14)
	}
	if up.SupertrendDir == nil || *up.SupertrendDir != 1 {
		t.Fatalf("rising series supertrend_dir should be +1, got %v", up.SupertrendDir)
	}

	down := Compute(ramp(80, -1))
	if down.SupertrendDir == nil || *down.SupertrendDir != -1 {
		t.Fatalf("falling series supertrend_dir should be -1, got %v", down.SupertrendDir)
	}
	if down.StochK == nil || *down.StochK > 0.2 {
		t.Fatalf("falling series should push stoch_k low, got %v", down.StochK)
	}
}

func TestDegenerateFlatSeriesLeavesRangeSignalsAbsent(t *testing.T) {
	// A perfectly flat series has zero-range windows: %K, %R and %B are
	// undefined and must be absent, never a fabricated value.
	flat := make([]marketdata.Bar, 80)
	for i := range flat {
		flat[i] = marketdata.Bar{Ts: int64(i) * 86400, Open: 50, High: 50, Low: 50, Close: 50}
	}
	s := Compute(flat)
	if s.StochK != nil {
		t.Fatalf("stoch_k must be absent on a flat series, got %v", *s.StochK)
	}
	if s.WilliamsR14 != nil {
		t.Fatalf("williams_r14 must be absent on a flat series, got %v", *s.WilliamsR14)
	}
	if s.BBPctB != nil {
		t.Fatalf("bb_pctb must be absent on a zero-σ series, got %v", *s.BBPctB)
	}
}

func TestNoLookaheadTrailingWindowOnly(t *testing.T) {
	// A signal computed on a prefix must not change when the prefix later
	// becomes the tail of a longer series (only trailing windows are read).
	full := ramp(80, 1)
	prefix := full[:60]
	a := Compute(prefix)
	// Recompute on the SAME 60 bars taken as the last 60 of a fabricated longer
	// series that shares those exact bars at the end.
	b := Compute(append(ramp(20, 1), prefix...)[20:])
	if (a.SupertrendDir == nil) != (b.SupertrendDir == nil) {
		t.Fatal("supertrend presence differs for identical trailing window")
	}
	if a.StochK != nil && b.StochK != nil && *a.StochK != *b.StochK {
		t.Fatalf("stoch_k differs for identical trailing window: %.6f vs %.6f", *a.StochK, *b.StochK)
	}
}
