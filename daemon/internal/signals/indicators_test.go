package signals

import (
	"math"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// barsFromCloses builds bars where only Close matters (H/L mirror close so
// close-only indicators are unaffected).
func barsFromCloses(cs ...float64) []marketdata.Bar {
	bars := make([]marketdata.Bar, len(cs))
	for i, c := range cs {
		bars[i] = marketdata.Bar{Ts: int64(i), Open: c, High: c, Low: c, Close: c, Volume: 1}
	}
	return bars
}

func within(got, want, tol float64) bool {
	return math.Abs(got-want) <= tol
}

func TestSMA(t *testing.T) {
	cases := []struct {
		name   string
		closes []float64
		n      int
		want   float64
		ok     bool
	}{
		{"last3of5", []float64{1, 2, 3, 4, 5}, 3, 4, true},
		{"exact_window", []float64{1, 2, 3, 4, 5}, 5, 3, true},
		{"insufficient", []float64{1, 2, 3, 4, 5}, 6, 0, false},
		{"zero_n", []float64{1, 2, 3}, 0, 0, false},
		{"empty", nil, 3, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := SMA(barsFromCloses(tc.closes...), tc.n)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if ok && !within(got, tc.want, 1e-12) {
				t.Fatalf("SMA = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestEMA(t *testing.T) {
	cases := []struct {
		name   string
		closes []float64
		n      int
		want   float64
		ok     bool
	}{
		// seed=SMA(1,2,3)=2, k=0.5: 4*.5+2*.5=3, then 5*.5+3*.5=4
		{"hand_vector", []float64{1, 2, 3, 4, 5}, 3, 4, true},
		{"seed_only_equals_sma", []float64{2, 4, 6}, 3, 4, true},
		{"n1_is_last_close", []float64{3, 7}, 1, 7, true},
		{"insufficient", []float64{1, 2}, 3, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := EMA(barsFromCloses(tc.closes...), tc.n)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if ok && !within(got, tc.want, 1e-12) {
				t.Fatalf("EMA = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRSI(t *testing.T) {
	cases := []struct {
		name   string
		closes []float64
		period int
		want   float64
		ok     bool
	}{
		// changes +1,+1,-1,+1: seed gain=2/3 loss=1/3; Wilder step:
		// gain=(2/3*2+1)/3=7/9, loss=2/9 -> RS=3.5 -> RSI=100-100/4.5
		{"hand_vector", []float64{10, 11, 12, 11, 12}, 3, 77.777777778, true},
		{"all_gains_100", []float64{1, 2, 3, 4}, 3, 100, true},
		{"all_losses_0", []float64{4, 3, 2, 1}, 3, 0, true},
		{"flat_reads_neutral", []float64{5, 5, 5, 5}, 3, 50, true},
		{"insufficient", []float64{1, 2, 3}, 3, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := RSI(barsFromCloses(tc.closes...), tc.period)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if ok && !within(got, tc.want, 1e-6) {
				t.Fatalf("RSI = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestMACD(t *testing.T) {
	cases := []struct {
		name                       string
		closes                     []float64
		fast, slow, signal         int
		wantMACD, wantSig, wantHst float64
		ok                         bool
	}{
		// Linear ramp: EMA2 leads EMA3 by exactly 0.5 -> hist 0.
		{"linear_ramp", []float64{1, 2, 3, 4, 5, 6}, 2, 3, 2, 0.5, 0.5, 0, true},
		{"min_bars", []float64{1, 2, 3, 4}, 2, 3, 2, 0.5, 0.5, 0, true},
		// Ramp then pullback: EMA2(...,4)=4.16667, EMA3=4 -> macd 1/6;
		// signal EMA2 of [.5,.5,.5,1/6]: .5, .5, then 1/6*2/3+.5/3=0.277778.
		{"pullback", []float64{1, 2, 3, 4, 5, 4}, 2, 3, 2, 0.1666667, 0.2777778, -0.1111111, true},
		{"insufficient", []float64{1, 2, 3}, 2, 3, 2, 0, 0, 0, false},
		{"bad_params", []float64{1, 2, 3, 4, 5}, 3, 3, 2, 0, 0, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, s, h, ok := MACD(barsFromCloses(tc.closes...), tc.fast, tc.slow, tc.signal)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if !ok {
				return
			}
			if !within(m, tc.wantMACD, 1e-6) || !within(s, tc.wantSig, 1e-6) || !within(h, tc.wantHst, 1e-6) {
				t.Fatalf("MACD = (%v, %v, %v), want (%v, %v, %v)", m, s, h, tc.wantMACD, tc.wantSig, tc.wantHst)
			}
		})
	}
}

func TestATR(t *testing.T) {
	hlc := func(rows ...[3]float64) []marketdata.Bar {
		bars := make([]marketdata.Bar, len(rows))
		for i, r := range rows {
			bars[i] = marketdata.Bar{Ts: int64(i), High: r[0], Low: r[1], Close: r[2]}
		}
		return bars
	}
	cases := []struct {
		name   string
		bars   []marketdata.Bar
		period int
		want   float64
		ok     bool
	}{
		// TR1=2, TR2=2 -> seed 2; TR3=3 -> Wilder (2*1+3)/2 = 2.5
		{"hand_vector", hlc([3]float64{10, 8, 9}, [3]float64{11, 9, 10}, [3]float64{12, 10, 11}, [3]float64{14, 11, 13}), 2, 2.5, true},
		// Gap up: TR = |high-prevClose| = 6 dominates high-low = 1.
		{"gap_true_range", hlc([3]float64{10, 8, 9}, [3]float64{15, 14, 14.5}), 1, 6, true},
		{"insufficient", hlc([3]float64{10, 8, 9}, [3]float64{11, 9, 10}), 2, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ATR(tc.bars, tc.period)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if ok && !within(got, tc.want, 1e-12) {
				t.Fatalf("ATR = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestROC(t *testing.T) {
	cases := []struct {
		name   string
		closes []float64
		n      int
		want   float64
		ok     bool
	}{
		{"ten_percent", []float64{100, 101, 102, 103, 104, 105, 106, 107, 108, 109, 110}, 10, 0.10, true},
		{"one_bar", []float64{100, 102}, 1, 0.02, true},
		{"insufficient", []float64{100, 102}, 2, 0, false},
		{"zero_base", []float64{0, 5}, 1, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ROC(barsFromCloses(tc.closes...), tc.n)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if ok && !within(got, tc.want, 1e-12) {
				t.Fatalf("ROC = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRealizedVol(t *testing.T) {
	cases := []struct {
		name   string
		closes []float64
		n      int
		want   float64
		tol    float64
		ok     bool
	}{
		// log rets ln(1.1), ln(0.9): sample stdev 0.1418956 * sqrt(365)
		{"hand_vector", []float64{100, 110, 99}, 2, 2.71091, 1e-3, true},
		// Constant growth ratio -> identical log returns -> stdev 0.
		{"constant_returns", []float64{100, 110, 121}, 2, 0, 1e-12, true},
		{"insufficient", []float64{100, 110}, 2, 0, 0, false},
		{"nonpositive_close", []float64{100, -5, 50}, 2, 0, 0, false},
		{"n_too_small", []float64{100, 110, 121}, 1, 0, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := RealizedVol(barsFromCloses(tc.closes...), tc.n)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if ok && !within(got, tc.want, tc.tol) {
				t.Fatalf("RealizedVol = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRVOL(t *testing.T) {
	vols := func(vs ...float64) []marketdata.Bar {
		bars := make([]marketdata.Bar, len(vs))
		for i, v := range vs {
			bars[i] = marketdata.Bar{Ts: int64(i), Close: 100, High: 100, Low: 100, Volume: v}
		}
		return bars
	}
	flat := make([]float64, 20)
	for i := range flat {
		flat[i] = 100
	}
	spike := make([]float64, 20)
	copy(spike, flat)
	spike[19] = 300
	zero := make([]float64, 20)
	cases := []struct {
		name string
		bars []marketdata.Bar
		want float64
		ok   bool
	}{
		{"flat_is_one", vols(flat...), 1.0, true},
		// SMA20 includes the spike bar: (19*100+300)/20 = 110 -> 300/110.
		{"spike", vols(spike...), 300.0 / 110.0, true},
		{"insufficient", vols(flat[:19]...), 0, false},
		{"zero_volume", vols(zero...), 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := RVOL(tc.bars)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if ok && !within(got, tc.want, 1e-12) {
				t.Fatalf("RVOL = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestVWAP(t *testing.T) {
	bars := []marketdata.Bar{
		{Ts: 0, High: 12, Low: 8, Close: 10, Volume: 100},  // typical 10
		{Ts: 1, High: 22, Low: 18, Close: 20, Volume: 300}, // typical 20
	}
	zeroVol := []marketdata.Bar{
		{Ts: 0, High: 12, Low: 8, Close: 10, Volume: 0},
		{Ts: 1, High: 22, Low: 18, Close: 20, Volume: 0},
	}
	cases := []struct {
		name string
		bars []marketdata.Bar
		n    int
		want float64
		ok   bool
	}{
		// (10*100 + 20*300) / 400 = 17.5
		{"hand_vector", bars, 2, 17.5, true},
		{"last_bar_only", bars, 1, 20, true},
		{"insufficient", bars, 3, 0, false},
		{"zero_volume", zeroVol, 2, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := VWAP(tc.bars, tc.n)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if ok && !within(got, tc.want, 1e-12) {
				t.Fatalf("VWAP = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestBollingerWidthPercentile(t *testing.T) {
	// need = 90 + 20 - 1 = 109 bars.
	build := func(f func(i int) float64) []marketdata.Bar {
		cs := make([]float64, 109)
		for i := range cs {
			cs[i] = f(i)
		}
		return barsFromCloses(cs...)
	}
	compression := build(func(i int) float64 { // wild history, dead-flat last 20
		if i < 89 {
			if i%2 == 0 {
				return 50
			}
			return 150
		}
		return 100
	})
	stretched := build(func(i int) float64 { // quiet history, wild last 20
		if i < 89 {
			return 100 + float64(i%2)*2
		}
		if (i-89)%2 == 0 {
			return 50
		}
		return 150
	})
	flat := build(func(int) float64 { return 100 })

	cases := []struct {
		name  string
		bars  []marketdata.Bar
		check func(t *testing.T, p float64)
		ok    bool
	}{
		{"compression_below_20", compression, func(t *testing.T, p float64) {
			if p >= 20 {
				t.Fatalf("percentile = %v, want < 20", p)
			}
		}, true},
		{"stretched_above_80", stretched, func(t *testing.T, p float64) {
			if p <= 80 {
				t.Fatalf("percentile = %v, want > 80", p)
			}
		}, true},
		// Midrank: an all-equal width history reads 50, not 100.
		{"flat_reads_50", flat, func(t *testing.T, p float64) {
			if !within(p, 50, 1e-9) {
				t.Fatalf("percentile = %v, want 50", p)
			}
		}, true},
		{"insufficient", flat[:108], nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, ok := BollingerWidthPercentile(tc.bars)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if !ok {
				return
			}
			if math.IsNaN(p) || p < 0 || p > 100 {
				t.Fatalf("percentile out of range: %v", p)
			}
			tc.check(t, p)
		})
	}
}
