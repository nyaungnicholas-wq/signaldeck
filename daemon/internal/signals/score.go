package signals

import (
	"fmt"

	"github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// MicroInputs carries crypto order-book microstructure into the score. The
// zero value (Ok=false) is correct for stocks or whenever no fresh snapshot
// exists — the imbalance component is then dropped and its weight
// redistributed.
type MicroInputs struct {
	// ImbSigned is the signed book imbalance in [-1,+1]; positive = bid-heavy.
	ImbSigned float64
	// WmidMinusMidBps is the weighted-mid minus mid in basis points
	// (explanation only; it does not enter the score).
	WmidMinusMidBps float64
	// Ok reports whether microstructure data is available and fresh.
	Ok bool
}

// clamp1 clamps v into [-1, +1].
func clamp1(v float64) float64 {
	if v > 1 {
		return 1
	}
	if v < -1 {
		return -1
	}
	return v
}

// sign is -1/0/+1.
func sign(v float64) float64 {
	switch {
	case v > 0:
		return 1
	case v < 0:
		return -1
	}
	return 0
}

// rel words a strict comparison; ties read "below" to match the norm's
// else-branch (equality is not bullish).
func rel(a, b float64) string {
	if a > b {
		return "above"
	}
	return "below"
}

// candidate is one weighted component before redistribution.
type candidate struct {
	comp   marketdata.ScoreComponent
	weight float64
	ok     bool
}

// trendComponent is the 3-part daily trend vote: close vs SMA200 (±0.5),
// SMA50 vs SMA200 (±0.25), close vs SMA20 (±0.25). Value holds the vote sum
// itself — the comparisons ARE the indicator, there is no rawer scalar.
func trendComponent(daily []marketdata.Bar) (marketdata.ScoreComponent, bool) {
	sma200, ok200 := SMA(daily, 200)
	sma50, ok50 := SMA(daily, 50)
	sma20, ok20 := SMA(daily, 20)
	if !ok200 || !ok50 || !ok20 {
		return marketdata.ScoreComponent{}, false
	}
	c := daily[len(daily)-1].Close
	norm := 0.0
	if c > sma200 {
		norm += 0.5
	} else {
		norm -= 0.5
	}
	if sma50 > sma200 {
		norm += 0.25
	} else {
		norm -= 0.25
	}
	if c > sma20 {
		norm += 0.25
	} else {
		norm -= 0.25
	}
	note := fmt.Sprintf("close %.2f %s SMA200 %.2f; SMA50 %.2f %s SMA200; close %s SMA20 %.2f",
		c, rel(c, sma200), sma200, sma50, rel(sma50, sma200), rel(c, sma20), sma20)
	return marketdata.ScoreComponent{Name: "trend_sma", Value: norm, Norm: norm, Note: note}, true
}

// momentumComponent normalizes ROC(n) by a per-horizon full-signal scale.
func momentumComponent(bars []marketdata.Bar, n int, scale float64, unit string) (marketdata.ScoreComponent, bool) {
	roc, ok := ROC(bars, n)
	if !ok {
		return marketdata.ScoreComponent{}, false
	}
	norm := clamp1(roc / scale)
	note := fmt.Sprintf("%+.2f%% over last %d %s (full signal at ±%.2f%%)",
		roc*100, n, unit, scale*100)
	return marketdata.ScoreComponent{Name: "momentum_roc", Value: roc, Norm: norm, Note: note}, true
}

// rsiComponent maps RSI(14) linearly so 50→0 and 25/75→∓1.
func rsiComponent(bars []marketdata.Bar) (marketdata.ScoreComponent, bool) {
	r, ok := RSI(bars, 14)
	if !ok {
		return marketdata.ScoreComponent{}, false
	}
	norm := clamp1((r - 50) / 25)
	note := fmt.Sprintf("RSI(14) = %.1f (50 neutral, normalized %+.2f)", r, norm)
	return marketdata.ScoreComponent{Name: "rsi", Value: r, Norm: norm, Note: note}, true
}

// macdComponent normalizes the MACD(12,26,9) histogram by 0.5%% of the last
// close, so ±1 means the histogram is ≥0.5%% of price — a strong divergence.
func macdComponent(bars []marketdata.Bar) (marketdata.ScoreComponent, bool) {
	_, _, hist, ok := MACD(bars, 12, 26, 9)
	if !ok {
		return marketdata.ScoreComponent{}, false
	}
	c := bars[len(bars)-1].Close
	if c <= 0 {
		return marketdata.ScoreComponent{}, false
	}
	norm := clamp1(hist / (0.005 * c))
	note := fmt.Sprintf("MACD(12,26,9) histogram %+.4f = %+.3f%% of close %.2f",
		hist, hist/c*100, c)
	return marketdata.ScoreComponent{Name: "macd", Value: hist, Norm: norm, Note: note}, true
}

// vwapComponent measures the close's distance from the trailing 20-bar VWAP
// in ATR(14) units. A flat series (ATR 0) has no meaningful distance and is
// dropped.
func vwapComponent(bars []marketdata.Bar) (marketdata.ScoreComponent, bool) {
	vwap, okV := VWAP(bars, 20)
	atr, okA := ATR(bars, 14)
	if !okV || !okA || atr <= 0 {
		return marketdata.ScoreComponent{}, false
	}
	c := bars[len(bars)-1].Close
	dist := (c - vwap) / atr
	note := fmt.Sprintf("close %.2f is %+.2f ATR(14=%.2f) from VWAP20 %.2f", c, dist, atr, vwap)
	return marketdata.ScoreComponent{Name: "vwap_dist", Value: c - vwap, Norm: clamp1(dist), Note: note}, true
}

// rvolConfirmComponent is volume as a CONFIRMER: above-average volume pushes
// in the direction momentum already points, below-average volume pushes
// against it. Without a momentum reading the sign is undefined, so the
// component is dropped alongside it.
func rvolConfirmComponent(daily []marketdata.Bar, momNorm float64, momOK bool) (marketdata.ScoreComponent, bool) {
	rv, ok := RVOL(daily)
	if !ok || !momOK {
		return marketdata.ScoreComponent{}, false
	}
	s := sign(momNorm)
	norm := clamp1((rv-1)/2) * s
	dir := "flat"
	switch {
	case s > 0:
		dir = "positive"
	case s < 0:
		dir = "negative"
	}
	note := fmt.Sprintf("volume %.2fx its 20-day average with %s momentum", rv, dir)
	return marketdata.ScoreComponent{Name: "rvol_confirm", Value: rv, Norm: norm, Note: note}, true
}

// imbalanceComponent turns the signed book imbalance into a component; the
// caller must gate on micro.Ok.
func imbalanceComponent(micro MicroInputs) marketdata.ScoreComponent {
	norm := clamp1(micro.ImbSigned)
	side := "balanced"
	switch {
	case micro.ImbSigned > 0:
		side = "bid-heavy"
	case micro.ImbSigned < 0:
		side = "ask-heavy"
	}
	note := fmt.Sprintf("order book imbalance %+.2f (%s), weighted mid %+.1f bps from mid",
		micro.ImbSigned, side, micro.WmidMinusMidBps)
	return marketdata.ScoreComponent{Name: "imbalance", Value: micro.ImbSigned, Norm: norm, Note: note}
}

// volRegimeComponent is informational only (Weight and Contrib stay 0):
// where today's Bollinger width sits in its 90-bar history. Compression and
// stretch are worth SEEING, but width direction does not predict price
// direction, so it never moves the score.
func volRegimeComponent(bars []marketdata.Bar) (marketdata.ScoreComponent, bool) {
	p, ok := BollingerWidthPercentile(bars)
	if !ok {
		return marketdata.ScoreComponent{}, false
	}
	label := "normal volatility"
	switch {
	case p < 20:
		label = "volatility compression"
	case p > 80:
		label = "stretched volatility"
	}
	note := fmt.Sprintf("Bollinger width at %.0fth percentile of last 90 bars — %s", p, label)
	return marketdata.ScoreComponent{Name: "vol_regime", Value: p, Norm: 0, Weight: 0, Contrib: 0, Note: note}, true
}

// assemble drops unavailable candidates, renormalizes the surviving weights
// to sum 1, sums contribs into the score and appends the weight-0 vol_regime
// component when computable. ok=false when NO weighted component survives —
// a horizon with only informational content has no honest score, so the
// caller omits it entirely.
func assemble(h marketdata.Horizon, cands []candidate, volReg marketdata.ScoreComponent, haveVolReg bool) (marketdata.Score, bool) {
	total := 0.0
	kept := make([]candidate, 0, len(cands))
	for _, c := range cands {
		if c.ok {
			kept = append(kept, c)
			total += c.weight
		}
	}
	if total <= 0 {
		return marketdata.Score{}, false
	}
	comps := make([]marketdata.ScoreComponent, 0, len(kept)+1)
	score := 0.0
	for _, c := range kept {
		c.comp.Weight = c.weight / total
		c.comp.Contrib = c.comp.Norm * c.comp.Weight
		score += c.comp.Contrib
		comps = append(comps, c.comp)
	}
	if haveVolReg {
		comps = append(comps, volReg)
	}
	return marketdata.Score{Horizon: h, Score: clamp1(score), Components: comps}, true
}

// ComputeScores builds one Pressure Score per horizon (1h, 1d, 1w) with full
// component decomposition. Bars must be ASCENDING by Ts; minute bars feed
// the 1h horizon, daily bars feed 1d/1w. Components without enough bars are
// dropped and the remaining weights renormalized to sum 1; a horizon whose
// weighted components ALL lack data is omitted from the map. SymbolID and Ts
// are left zero for the caller to fill. The vol_regime component is computed
// on the horizon's own timeframe (minute for 1h, daily for 1d/1w).
func ComputeScores(daily, minute []marketdata.Bar, micro MicroInputs) map[marketdata.Horizon]marketdata.Score {
	out := make(map[marketdata.Horizon]marketdata.Score, len(marketdata.Horizons))

	// Daily-timeframe components shared by the 1d and 1w horizons.
	trendC, trendOK := trendComponent(daily)
	rsiD, rsiDOK := rsiComponent(daily)
	macdD, macdDOK := macdComponent(daily)
	vwapD, vwapDOK := vwapComponent(daily)
	volD, volDOK := volRegimeComponent(daily)

	// 1h — minute-bar signals plus (crypto only) order-book imbalance.
	{
		momM, momMOK := momentumComponent(minute, 60, 0.005, "minute bars")
		rsiM, rsiMOK := rsiComponent(minute)
		macdM, macdMOK := macdComponent(minute)
		vwapM, vwapMOK := vwapComponent(minute)
		cands := []candidate{
			{momM, 0.30, momMOK},
			{rsiM, 0.15, rsiMOK},
			{macdM, 0.15, macdMOK},
			{vwapM, 0.15, vwapMOK},
		}
		if micro.Ok {
			cands = append(cands, candidate{imbalanceComponent(micro), 0.25, true})
		}
		volM, volMOK := volRegimeComponent(minute)
		if s, ok := assemble(marketdata.H1h, cands, volM, volMOK); ok {
			out[marketdata.H1h] = s
		}
	}

	// 1d — daily trend plus short daily momentum.
	{
		mom, momOK := momentumComponent(daily, 5, 0.02, "daily bars")
		cands := []candidate{
			{trendC, 0.35, trendOK},
			{mom, 0.20, momOK},
			{rsiD, 0.15, rsiDOK},
			{macdD, 0.15, macdDOK},
			{vwapD, 0.10, vwapDOK},
		}
		if micro.Ok {
			cands = append(cands, candidate{imbalanceComponent(micro), 0.05, true})
		}
		if s, ok := assemble(marketdata.H1d, cands, volD, volDOK); ok {
			out[marketdata.H1d] = s
		}
	}

	// 1w — trend-dominated, with volume as a momentum confirmer.
	{
		mom, momOK := momentumComponent(daily, 20, 0.05, "daily bars")
		rvol, rvolOK := rvolConfirmComponent(daily, mom.Norm, momOK)
		cands := []candidate{
			{trendC, 0.45, trendOK},
			{mom, 0.25, momOK},
			{rsiD, 0.10, rsiDOK},
			{macdD, 0.15, macdDOK},
			{rvol, 0.05, rvolOK},
		}
		if s, ok := assemble(marketdata.H1w, cands, volD, volDOK); ok {
			out[marketdata.H1w] = s
		}
	}

	return out
}
