package structregime

import "math"

// Instance is one historical, walk-forward occurrence of a regime signal on a
// single symbol: what was called, at what conviction, and how it resolved.
// Produced NON-OVERLAPPING (step == horizon), so instances never share forward
// windows — the same discipline the accuracy tables were measured with.
type Instance struct {
	Ts         int64   `json:"ts"`
	Regime     string  `json:"regime"`
	Conviction float64 `json:"conviction"`
	Actual     string  `json:"actual"`
	Correct    bool    `json:"correct"`
}

// Input is one named model input for the "why it fired" surface.
type Input struct {
	Name  string  `json:"name"`
	Value float64 `json:"value"`
	Note  string  `json:"note"`
}

// TrendHistory replays the trend predictor over one symbol's daily closes
// (ts aligned to closes) and returns every non-overlapping instance with its
// resolution, oldest first. Wild-move windows are skipped exactly like the
// live predictor would refuse them.
func TrendHistory(ts []int64, closes []float64) []Instance {
	n := len(closes)
	if len(ts) != n || n < minHistory+horizon {
		return nil
	}
	sma := rollMean(closes, 200)
	dist := make([]float64, n)
	absd := make([]float64, n)
	for i := range closes {
		if sma[i] > 0 {
			dist[i] = closes[i]/sma[i] - 1
			absd[i] = math.Abs(dist[i])
		} else {
			dist[i] = math.NaN()
			absd[i] = math.NaN()
		}
	}
	var out []Instance
	for t := minHistory; t+horizon < n; t += horizon {
		d0, d1 := dist[t], dist[t+horizon]
		if !finite(d0) || !finite(d1) || d0 == 0 || d1 == 0 {
			continue
		}
		if wildClose(closes[:t+1], minHistory) {
			continue
		}
		conv := frac(absd[max(0, t-window):t], math.Abs(d0))
		pred, act := "downtrend", "downtrend"
		if d0 > 0 {
			pred = "uptrend"
		}
		if d1 > 0 {
			act = "uptrend"
		}
		out = append(out, Instance{Ts: ts[t], Regime: pred, Conviction: conv,
			Actual: act, Correct: pred == act})
	}
	return out
}

// LiquidityHistory replays the liquidity predictor, same contract as
// TrendHistory.
func LiquidityHistory(ts []int64, closes, volumes []float64) []Instance {
	n := len(closes)
	if len(ts) != n || len(volumes) != n || n < minHistory+horizon {
		return nil
	}
	dv := make([]float64, n)
	for i := range closes {
		x := closes[i] * volumes[i]
		if x > 0 {
			dv[i] = math.Log(x)
		} else {
			dv[i] = math.NaN()
		}
	}
	m := rollMeanNaN(dv, horizon)
	var out []Instance
	for t := minHistory; t+horizon < n; t += horizon {
		if !finite(m[t]) || wildClose(closes[:t+1], minHistory) {
			continue
		}
		med := medianOf(m[max(0, t-window) : t+1])
		var s float64
		cnt := 0
		for _, x := range dv[t+1 : t+1+horizon] {
			if finite(x) {
				s += x
				cnt++
			}
		}
		if cnt < horizon || !finite(med) || s/float64(cnt) == med {
			continue
		}
		rank := frac(m[max(0, t-window):t], m[t])
		pred, act := "quiet", "quiet"
		if rank > 0.5 {
			pred = "active"
		}
		if s/float64(cnt) > med {
			act = "active"
		}
		out = append(out, Instance{Ts: ts[t], Regime: pred,
			Conviction: math.Abs(rank-0.5) * 2, Actual: act, Correct: pred == act})
	}
	return out
}

// Vol21History replays the monthly vol predictor over returns (ts aligned to
// rets — i.e. ts[i] is the day rets[i] resolved).
func Vol21History(ts []int64, rets []float64) []Instance {
	n := len(rets)
	if len(ts) != n || n < minHistory+horizon {
		return nil
	}
	ev := ewmaVol(rets)
	rv := make([]float64, n)
	for i := range rv {
		rv[i] = math.NaN()
	}
	for i := horizon; i < n; i++ {
		var sum, sq float64
		cnt := 0
		for _, x := range rets[i-horizon+1 : i+1] {
			if finite(x) {
				sum += x
				sq += x * x
				cnt++
			}
		}
		if cnt == horizon {
			mean := sum / float64(cnt)
			rv[i] = math.Sqrt(sq/float64(cnt) - mean*mean)
		}
	}
	var out []Instance
	for t := minHistory; t+horizon < n; t += horizon {
		if wildRet(rets[:t+1], minHistory) {
			continue
		}
		med := medianOf(rv[max(0, t-window) : t+1])
		fwd := rv[t+horizon]
		if !finite(med) || !finite(fwd) || fwd == med {
			continue
		}
		rank := frac(ev[max(0, t-window):t], ev[t])
		pred, act := "calm", "calm"
		if rank > 0.5 {
			pred = "elevated"
		}
		if fwd > med {
			act = "elevated"
		}
		out = append(out, Instance{Ts: ts[t], Regime: pred,
			Conviction: math.Abs(rank-0.5) * 2, Actual: act, Correct: pred == act})
	}
	return out
}

// ExplainTrend returns the trend forecast plus the raw inputs behind it —
// the "why it fired" surface. ok=false mirrors PredictTrend's refusals.
func ExplainTrend(closes []float64) (Forecast, []Input, bool) {
	f, ok := PredictTrend(closes)
	if !ok {
		return Forecast{}, nil, false
	}
	n := len(closes)
	sma := rollMean(closes, 200)
	dist := closes[n-1]/sma[n-1] - 1
	return f, []Input{
		{Name: "close", Value: closes[n-1], Note: "latest daily close"},
		{Name: "sma200", Value: sma[n-1], Note: "200-day simple moving average"},
		{Name: "distance_pct", Value: dist * 100, Note: "close vs SMA200; the SIGN is the call"},
		{Name: "distance_rank", Value: f.Conviction, Note: "|distance| percentile in its trailing 200d — the conviction; extremes persist best"},
	}, true
}

// ExplainLiquidity returns the liquidity forecast plus its raw inputs.
func ExplainLiquidity(closes, volumes []float64) (Forecast, []Input, bool) {
	f, ok := PredictLiquidity(closes, volumes)
	if !ok {
		return Forecast{}, nil, false
	}
	n := len(closes)
	var dollar float64
	cnt := 0
	for i := n - horizon; i < n; i++ {
		if closes[i] > 0 && volumes[i] > 0 {
			dollar += closes[i] * volumes[i]
			cnt++
		}
	}
	if cnt > 0 {
		dollar /= float64(cnt)
	}
	return f, []Input{
		{Name: "avg_dollar_volume_21d", Value: dollar, Note: "mean daily close×volume over the last 21 sessions"},
		{Name: "rank", Value: f.Rank, Note: "that mean's percentile in its trailing 200d distribution; >0.5 ⇒ active"},
		{Name: "conviction", Value: f.Conviction, Note: "2×|rank−0.5| — distance from the median is the conviction"},
	}, true
}

// ExplainVol21 returns the monthly vol forecast plus its raw inputs.
func ExplainVol21(rets []float64) (Forecast, []Input, bool) {
	f, ok := PredictVol21(rets)
	if !ok {
		return Forecast{}, nil, false
	}
	ev := ewmaVol(rets)
	cur := ev[len(ev)-1]
	return f, []Input{
		{Name: "ewma_vol_daily_pct", Value: cur * 100, Note: "RiskMetrics EWMA vol (λ=0.94), daily"},
		{Name: "ewma_vol_annualized_pct", Value: cur * math.Sqrt(252) * 100, Note: "same, annualized ×√252"},
		{Name: "rank", Value: f.Rank, Note: "current EWMA vol's percentile in its trailing 200d; >0.5 ⇒ elevated"},
		{Name: "conviction", Value: f.Conviction, Note: "2×|rank−0.5|"},
	}, true
}
