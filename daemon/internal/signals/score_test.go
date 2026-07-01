package signals

import (
	"math"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// trendBars builds n bars whose close compounds by growth each bar, with a
// ±1% high/low range and constant volume — a clean synthetic trend.
func trendBars(n int, growth float64, tsStep int64) []marketdata.Bar {
	bars := make([]marketdata.Bar, n)
	c := 100.0
	for i := range bars {
		if i > 0 {
			c *= growth
		}
		bars[i] = marketdata.Bar{
			Ts:     int64(i) * tsStep,
			Open:   c,
			High:   c * 1.01,
			Low:    c * 0.99,
			Close:  c,
			Volume: 1000,
		}
	}
	return bars
}

// findComp returns the named component of a score.
func findComp(s marketdata.Score, name string) (marketdata.ScoreComponent, bool) {
	for _, c := range s.Components {
		if c.Name == name {
			return c, true
		}
	}
	return marketdata.ScoreComponent{}, false
}

// weightSum sums all component weights (vol_regime carries 0, so this is
// the weighted sum, which must renormalize to 1).
func weightSum(s marketdata.Score) float64 {
	sum := 0.0
	for _, c := range s.Components {
		sum += c.Weight
	}
	return sum
}

// assertFinite fails on any NaN/Inf anywhere in the returned scores — the
// package-level guarantee.
func assertFinite(t *testing.T, scores map[marketdata.Horizon]marketdata.Score) {
	t.Helper()
	bad := func(v float64) bool { return math.IsNaN(v) || math.IsInf(v, 0) }
	for h, s := range scores {
		if bad(s.Score) {
			t.Fatalf("%s: score is not finite: %v", h, s.Score)
		}
		for _, c := range s.Components {
			if bad(c.Value) || bad(c.Norm) || bad(c.Weight) || bad(c.Contrib) {
				t.Fatalf("%s/%s: non-finite component %+v", h, c.Name, c)
			}
		}
	}
}

func TestComputeScoresTrend(t *testing.T) {
	cases := []struct {
		name   string
		growth float64
		up     bool
	}{
		{"strong_uptrend", 1.005, true},
		{"strong_downtrend", 0.995, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			daily := trendBars(300, tc.growth, 86400)
			scores := ComputeScores(daily, nil, MicroInputs{})
			assertFinite(t, scores)

			if _, ok := scores[marketdata.H1h]; ok {
				t.Fatalf("1h horizon present with no minute bars")
			}
			for _, h := range []marketdata.Horizon{marketdata.H1d, marketdata.H1w} {
				s, ok := scores[h]
				if !ok {
					t.Fatalf("%s horizon missing", h)
				}
				if tc.up && s.Score <= 0.3 {
					t.Fatalf("%s score = %v, want > +0.3", h, s.Score)
				}
				if !tc.up && s.Score >= -0.3 {
					t.Fatalf("%s score = %v, want < -0.3", h, s.Score)
				}
				trend, ok := findComp(s, "trend_sma")
				if !ok {
					t.Fatalf("%s: trend_sma missing", h)
				}
				if tc.up && trend.Contrib <= 0 {
					t.Fatalf("%s: trend_sma contrib = %v, want > 0", h, trend.Contrib)
				}
				if !tc.up && trend.Contrib >= 0 {
					t.Fatalf("%s: trend_sma contrib = %v, want < 0", h, trend.Contrib)
				}
				// vol_regime rides along informationally, never moves the score.
				vr, ok := findComp(s, "vol_regime")
				if !ok {
					t.Fatalf("%s: vol_regime missing (300 daily bars is enough)", h)
				}
				if vr.Weight != 0 || vr.Contrib != 0 {
					t.Fatalf("%s: vol_regime weight/contrib = %v/%v, want 0/0", h, vr.Weight, vr.Contrib)
				}
				if !within(weightSum(s), 1, 1e-9) {
					t.Fatalf("%s: weights sum to %v, want 1", h, weightSum(s))
				}
				if s.Score < -1 || s.Score > 1 {
					t.Fatalf("%s: score %v outside [-1,1]", h, s.Score)
				}
			}
		})
	}
}

func TestComputeScoresMicro(t *testing.T) {
	daily := trendBars(300, 1.005, 86400)
	minute := trendBars(200, 1.0005, 60)

	cases := []struct {
		name  string
		micro MicroInputs
		// wantImb: horizon -> expected renormalized imbalance weight.
		wantImb  map[marketdata.Horizon]float64
		wantNorm float64
	}{
		{
			name:     "micro_ok_included",
			micro:    MicroInputs{ImbSigned: 0.5, WmidMinusMidBps: 1.2, Ok: true},
			wantImb:  map[marketdata.Horizon]float64{marketdata.H1h: 0.25, marketdata.H1d: 0.05},
			wantNorm: 0.5,
		},
		{
			name:     "micro_norm_clamped",
			micro:    MicroInputs{ImbSigned: 1.5, Ok: true},
			wantImb:  map[marketdata.Horizon]float64{marketdata.H1h: 0.25, marketdata.H1d: 0.05},
			wantNorm: 1.0,
		},
		{
			name:    "micro_not_ok_dropped",
			micro:   MicroInputs{ImbSigned: 0.9, Ok: false},
			wantImb: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			scores := ComputeScores(daily, minute, tc.micro)
			assertFinite(t, scores)
			for _, h := range marketdata.Horizons {
				s, ok := scores[h]
				if !ok {
					t.Fatalf("%s horizon missing", h)
				}
				if !within(weightSum(s), 1, 1e-9) {
					t.Fatalf("%s: weights sum to %v, want 1", h, weightSum(s))
				}
				imb, has := findComp(s, "imbalance")
				wantW, want := tc.wantImb[h]
				if has != want {
					t.Fatalf("%s: imbalance present = %v, want %v", h, has, want)
				}
				if !has {
					continue
				}
				if !within(imb.Weight, wantW, 1e-9) {
					t.Fatalf("%s: imbalance weight = %v, want %v", h, imb.Weight, wantW)
				}
				if !within(imb.Norm, tc.wantNorm, 1e-9) {
					t.Fatalf("%s: imbalance norm = %v, want %v", h, imb.Norm, tc.wantNorm)
				}
			}
			// 1w never carries imbalance, even with micro on.
			if _, has := findComp(scores[marketdata.H1w], "imbalance"); has {
				t.Fatalf("1w carries imbalance component")
			}
			// With micro dropped, 1h weights renormalize from /0.75.
			if !tc.micro.Ok {
				want := map[string]float64{
					"momentum_roc": 0.30 / 0.75,
					"rsi":          0.15 / 0.75,
					"macd":         0.15 / 0.75,
					"vwap_dist":    0.15 / 0.75,
				}
				s := scores[marketdata.H1h]
				for name, w := range want {
					c, ok := findComp(s, name)
					if !ok {
						t.Fatalf("1h: %s missing", name)
					}
					if !within(c.Weight, w, 1e-9) {
						t.Fatalf("1h: %s weight = %v, want %v", name, c.Weight, w)
					}
				}
			}
		})
	}
}

func TestComputeScoresRedistribution(t *testing.T) {
	// 50 daily bars: enough for roc/rsi/macd/vwap/rvol but NOT for
	// trend_sma (needs 200) or vol_regime (needs 109) -> weights of the
	// survivors must renormalize to 1.
	daily := trendBars(50, 1.005, 86400)
	scores := ComputeScores(daily, nil, MicroInputs{})
	assertFinite(t, scores)

	cases := []struct {
		name    string
		horizon marketdata.Horizon
		weights map[string]float64
		absent  []string
	}{
		{
			name:    "1d_without_trend",
			horizon: marketdata.H1d,
			weights: map[string]float64{
				"momentum_roc": 0.20 / 0.60,
				"rsi":          0.15 / 0.60,
				"macd":         0.15 / 0.60,
				"vwap_dist":    0.10 / 0.60,
			},
			absent: []string{"trend_sma", "vol_regime", "imbalance"},
		},
		{
			name:    "1w_without_trend",
			horizon: marketdata.H1w,
			weights: map[string]float64{
				"momentum_roc": 0.25 / 0.55,
				"rsi":          0.10 / 0.55,
				"macd":         0.15 / 0.55,
				"rvol_confirm": 0.05 / 0.55,
			},
			absent: []string{"trend_sma", "vol_regime"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, ok := scores[tc.horizon]
			if !ok {
				t.Fatalf("%s horizon missing", tc.horizon)
			}
			if len(s.Components) != len(tc.weights) {
				t.Fatalf("component count = %d, want %d (%+v)", len(s.Components), len(tc.weights), s.Components)
			}
			for name, w := range tc.weights {
				c, ok := findComp(s, name)
				if !ok {
					t.Fatalf("%s missing", name)
				}
				if !within(c.Weight, w, 1e-9) {
					t.Fatalf("%s weight = %v, want %v", name, c.Weight, w)
				}
				if !within(c.Contrib, c.Norm*c.Weight, 1e-12) {
					t.Fatalf("%s contrib = %v, want norm*weight = %v", name, c.Contrib, c.Norm*c.Weight)
				}
			}
			for _, name := range tc.absent {
				if _, ok := findComp(s, name); ok {
					t.Fatalf("%s present, want dropped", name)
				}
			}
			if !within(weightSum(s), 1, 1e-9) {
				t.Fatalf("weights sum to %v, want 1", weightSum(s))
			}
		})
	}
}

func TestComputeScoresOmission(t *testing.T) {
	cases := []struct {
		name   string
		daily  []marketdata.Bar
		minute []marketdata.Bar
		want   []marketdata.Horizon
	}{
		{"no_data", nil, nil, nil},
		// 5 daily bars: even ROC(5) needs 6 -> every horizon omitted.
		{"too_few_daily", trendBars(5, 1.005, 86400), nil, nil},
		{"daily_only", trendBars(300, 1.005, 86400), nil, []marketdata.Horizon{marketdata.H1d, marketdata.H1w}},
		{"minute_only", nil, trendBars(200, 1.0005, 60), []marketdata.Horizon{marketdata.H1h}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			scores := ComputeScores(tc.daily, tc.minute, MicroInputs{})
			assertFinite(t, scores)
			if len(scores) != len(tc.want) {
				t.Fatalf("got %d horizons (%v), want %d", len(scores), horizonsOf(scores), len(tc.want))
			}
			for _, h := range tc.want {
				if _, ok := scores[h]; !ok {
					t.Fatalf("%s horizon missing", h)
				}
			}
		})
	}
}

func horizonsOf(scores map[marketdata.Horizon]marketdata.Score) []marketdata.Horizon {
	var hs []marketdata.Horizon
	for h := range scores {
		hs = append(hs, h)
	}
	return hs
}

func TestComputeScoresRvolConfirm(t *testing.T) {
	// Flat volume -> RVOL 1.0 -> confirm norm 0 regardless of momentum.
	daily := trendBars(300, 1.005, 86400)
	scores := ComputeScores(daily, nil, MicroInputs{})
	s := scores[marketdata.H1w]
	rvol, ok := findComp(s, "rvol_confirm")
	if !ok {
		t.Fatalf("rvol_confirm missing from 1w")
	}
	if !within(rvol.Value, 1.0, 1e-9) {
		t.Fatalf("rvol value = %v, want 1.0", rvol.Value)
	}
	if !within(rvol.Norm, 0, 1e-9) {
		t.Fatalf("rvol norm = %v, want 0 (flat volume confirms nothing)", rvol.Norm)
	}

	// Volume spike with positive momentum -> positive confirm norm:
	// RVOL = 3000/(19*1000+3000)*20 = 3000/1100 ≈ 2.727 -> clamp((rv-1)/2)=0.864.
	spiked := trendBars(300, 1.005, 86400)
	spiked[len(spiked)-1].Volume = 3000
	s = ComputeScores(spiked, nil, MicroInputs{})[marketdata.H1w]
	rvol, ok = findComp(s, "rvol_confirm")
	if !ok {
		t.Fatalf("rvol_confirm missing from 1w (spiked)")
	}
	wantRV := 3000.0 / 1100.0
	if !within(rvol.Value, wantRV, 1e-9) {
		t.Fatalf("rvol value = %v, want %v", rvol.Value, wantRV)
	}
	if !within(rvol.Norm, (wantRV-1)/2, 1e-9) {
		t.Fatalf("rvol norm = %v, want %v", rvol.Norm, (wantRV-1)/2)
	}

	// Same spike with negative momentum -> the confirm flips sign.
	spikedDown := trendBars(300, 0.995, 86400)
	spikedDown[len(spikedDown)-1].Volume = 3000
	s = ComputeScores(spikedDown, nil, MicroInputs{})[marketdata.H1w]
	rvol, ok = findComp(s, "rvol_confirm")
	if !ok {
		t.Fatalf("rvol_confirm missing from 1w (down spike)")
	}
	if !within(rvol.Norm, -(wantRV-1)/2, 1e-9) {
		t.Fatalf("rvol norm = %v, want %v", rvol.Norm, -(wantRV-1)/2)
	}
}

func TestComputeScoresNotes(t *testing.T) {
	// Every component must ship a non-empty plain-English note — that is
	// the honesty contract with the UI.
	daily := trendBars(300, 1.005, 86400)
	minute := trendBars(200, 1.0005, 60)
	scores := ComputeScores(daily, minute, MicroInputs{ImbSigned: 0.4, WmidMinusMidBps: 0.8, Ok: true})
	for h, s := range scores {
		if s.Horizon != h {
			t.Fatalf("score horizon = %s, keyed as %s", s.Horizon, h)
		}
		if s.SymbolID != 0 || s.Ts != 0 {
			t.Fatalf("%s: SymbolID/Ts must be left zero for the caller", h)
		}
		for _, c := range s.Components {
			if c.Note == "" {
				t.Fatalf("%s/%s: empty note", h, c.Name)
			}
			if c.Name == "" {
				t.Fatalf("%s: unnamed component %+v", h, c)
			}
		}
	}
}
