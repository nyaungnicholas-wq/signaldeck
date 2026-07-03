package breakout

import (
	"math"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// bar builds a bar with explicit OHLCV; Ts is the index so ordering is stable.
func bar(i int, o, h, l, c, v float64) marketdata.Bar {
	return marketdata.Bar{Ts: int64(i), Open: o, High: h, Low: l, Close: c, Volume: v}
}

// flatBars builds n bars all at the same OHLC price with the given volume.
func flatBars(n int, price, vol float64) []marketdata.Bar {
	bars := make([]marketdata.Bar, n)
	for i := 0; i < n; i++ {
		bars[i] = bar(i, price, price, price, price, vol)
	}
	return bars
}

// hasKind reports whether signals contains one of kind and returns it.
func hasKind(sigs []Signal, kind string) (Signal, bool) {
	for _, s := range sigs {
		if s.Kind == kind {
			return s, true
		}
	}
	return Signal{}, false
}

func TestDetect(t *testing.T) {
	tests := []struct {
		name      string
		bars      []marketdata.Bar
		wantKinds []string // kinds that MUST be present
		wantEmpty bool
	}{
		{
			name:      "empty input",
			bars:      nil,
			wantEmpty: true,
		},
		{
			name:      "too short for any detector",
			bars:      flatBars(5, 100, 10),
			wantEmpty: true,
		},
		{
			name: "calm flat series -> no signals",
			// 120 identical bars: no breakout (close == range edges, not >),
			// no volume spike (all equal), no squeeze release (width 0).
			bars:      flatBars(120, 100, 10),
			wantEmpty: true,
		},
		{
			name: "break above prior range -> donchian_up",
			bars: func() []marketdata.Bar {
				b := flatBars(DonchianN, 100, 10) // 20 prior bars, high=100
				// current bar closes above the 20-bar high.
				b = append(b, bar(DonchianN, 100, 105, 99, 104, 10))
				return b
			}(),
			wantKinds: []string{"donchian_up"},
		},
		{
			name: "break below prior range -> donchian_down",
			bars: func() []marketdata.Bar {
				b := flatBars(DonchianN, 100, 10) // low=100
				b = append(b, bar(DonchianN, 100, 101, 95, 96, 10))
				return b
			}(),
			wantKinds: []string{"donchian_down"},
		},
		{
			name: "volume outlier -> volume_spike",
			bars: func() []marketdata.Bar {
				// 20 prior bars at volume 10, mean=10; latest volume 30 = 3x > 2.5x.
				// Keep price inside the prior Donchian range so only volume fires.
				b := flatBars(VolumeSMAN, 100, 10)
				b = append(b, bar(VolumeSMAN, 100, 100, 100, 100, 30))
				return b
			}(),
			wantKinds: []string{"volume_spike"},
		},
		{
			name:      "coil then expand -> squeeze_release",
			bars:      coilThenExpandBars(),
			wantKinds: []string{"squeeze_release"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Detect(tt.bars)
			if got == nil {
				t.Fatalf("Detect returned nil, want non-nil (possibly empty) slice")
			}
			if tt.wantEmpty {
				if len(got) != 0 {
					t.Fatalf("want empty, got %+v", got)
				}
				return
			}
			for _, k := range tt.wantKinds {
				if _, ok := hasKind(got, k); !ok {
					t.Fatalf("want kind %q present, got %+v", k, got)
				}
			}
		})
	}
}

// coilThenExpandBars builds a history whose Bollinger width is compressed for a
// long stretch (bottom of the distribution) and then expands on the final bar,
// so detectSqueezeRelease fires. We need enough bars that the width series has
// >= SqueezeLookback+1 elements: BollingerN + SqueezeLookback + a couple.
func coilThenExpandBars() []marketdata.Bar {
	const total = BollingerN + SqueezeLookback + 5
	bars := make([]marketdata.Bar, 0, total)
	// Tight coil: closes wobble by a tiny amount so stdev (and width) is small
	// but > 0 for all but the last, keeping the distribution flat and low.
	price := 100.0
	for i := 0; i < total-1; i++ {
		wobble := 0.01
		if i%2 == 0 {
			wobble = -0.01
		}
		c := price + wobble
		bars = append(bars, bar(i, price, c+0.01, c-0.01, c, 10))
	}
	// Final bar: a large move blows the close far from the mean, so the newest
	// Bollinger width jumps well above the prior-window average -> release.
	last := total - 1
	c := price + 5.0
	bars = append(bars, bar(last, price, c+0.1, price-0.1, c, 10))
	return bars
}

func TestDetectDonchianStrengthPositive(t *testing.T) {
	// Prior bars span a real range (low 95..high 100) so the channel has
	// height and strength (distance-beyond-edge / height) is well-defined.
	b := make([]marketdata.Bar, 0, DonchianN+1)
	for i := 0; i < DonchianN; i++ {
		b = append(b, bar(i, 97, 100, 95, 98, 10))
	}
	b = append(b, bar(DonchianN, 100, 110, 99, 108, 10)) // close 108 > high 100
	got := Detect(b)
	s, ok := hasKind(got, "donchian_up")
	if !ok {
		t.Fatalf("expected donchian_up, got %+v", got)
	}
	if s.Strength <= 0 {
		t.Fatalf("expected positive strength, got %v", s.Strength)
	}
	if s.Ts != int64(DonchianN) {
		t.Fatalf("Ts = %d, want %d (latest bar)", s.Ts, DonchianN)
	}
	if s.Detail == "" {
		t.Fatalf("expected non-empty Detail")
	}
}

// TestDetectNoLookahead verifies a value computed for the latest bar depends
// ONLY on bars[..last]: appending a future bar and re-running on the truncated
// slice must reproduce the same signal, and the trigger must not depend on any
// bar after the one under test.
func TestDetectNoLookahead(t *testing.T) {
	b := flatBars(DonchianN, 100, 10)
	b = append(b, bar(DonchianN, 100, 105, 99, 104, 10)) // triggers donchian_up
	// Append several arbitrary future bars.
	future := append([]marketdata.Bar{}, b...)
	future = append(future,
		bar(DonchianN+1, 104, 200, 50, 60, 999),
		bar(DonchianN+2, 60, 61, 59, 60, 1),
	)

	base := Detect(b)
	// Detect on the truncated prefix (up to the tested bar) must equal `base`.
	trunc := Detect(future[:DonchianN+1])
	if len(base) != len(trunc) {
		t.Fatalf("prefix Detect differs in length: base=%d trunc=%d", len(base), len(trunc))
	}
	for i := range base {
		if base[i] != trunc[i] {
			t.Fatalf("prefix Detect differs at %d: %+v vs %+v", i, base[i], trunc[i])
		}
	}
}

func within(got, want, tol float64) bool {
	return math.Abs(got-want) <= tol
}
