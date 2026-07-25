// Package distribution replaces the binary up/down target with a forecast of
// the RETURN DISTRIBUTION — the single largest modeling gap this platform had.
//
// # The problem it fixes
//
// Every prediction here has been "P(up)". Under that target, a forecast of
// +0.2% against a realized −0.1% is scored a full error, identical in weight to
// missing a −8% crash. But a 0.3% disagreement on a name whose daily standard
// deviation is 4.6% is not a mistake — it is noise, and it is below the cost of
// trading either way. Grading noise as error is how a model gets punished for
// being right about the thing that matters and how its measured accuracy stops
// carrying information. It is also why the platform optimized a target that was
// never the target that makes money: direction is not tradeable, MAGNITUDE
// relative to cost is.
//
// # What it forecasts instead
//
// Given a conditional sample of realized forward returns and a cost threshold
// tau, Forecast reports:
//
//   - P(return > +tau) and P(return < −tau) — the probabilities that actually
//     matter, because they are the probabilities of a move worth paying to
//     capture;
//   - P(|return| <= tau) — the NO-TRADE zone, the honest majority outcome on a
//     1-day horizon, which the binary target had no way to express at all;
//   - the expected return, and the 10th/50th/90th percentiles.
//
// # Why the width is the forecastable part
//
// The center of this distribution is close to zero and stays there — that is
// the direction ceiling, proven on this platform seven independent ways. The
// WIDTH is a different matter: volatility is strongly persistent, and the
// volatility-regime predictor is the one validated-edge forecast here (74–76%
// at high conviction). So the caller conditions the sample on the CURRENT
// volatility regime, and the resulting distribution inherits skill from the
// axis that has it, rather than pretending to skill on the axis that does not.
//
// # Grading
//
// Two graders, because a distribution cannot be graded like a coin flip:
//
//   - GradeQuantiles uses PINBALL LOSS (the proper scoring rule for quantile
//     forecasts) against a CLIMATOLOGY baseline — the same forecast made from
//     the unconditional sample. Skill > 0 means conditioning added information;
//     skill <= 0 means the conditioning is decoration, and the caller is
//     expected to drop it exactly as an unlifted leg is dropped.
//   - GradeBand grades the directional call ONLY on observations that actually
//     cleared the cost band. A realized move inside ±tau is a NO-CALL, not a
//     miss. This is the label-noise fix in one line.
//
// Nothing here does I/O, and no function reads a value it was not handed, so
// there is no path by which a future bar reaches a forecast.
package distribution

import (
	"math"
	"sort"
)

// MinSample is the least conditional observations required before Forecast will
// report a distribution. Below this, quantiles are an artifact of a handful of
// points, so Forecast refuses rather than emitting a confident shape.
const MinSample = 60

// Dist is a forecast return distribution, all figures in return units
// (0.01 = 1%).
type Dist struct {
	// N is the conditional sample size the distribution was estimated from.
	N int `json:"n"`
	// Tau is the cost threshold that defines the no-trade band.
	Tau float64 `json:"tau"`
	// Mean is the expected return.
	Mean float64 `json:"mean"`
	// Q10/Q50/Q90 are the 10th, 50th (median) and 90th percentiles.
	Q10 float64 `json:"q10"`
	Q50 float64 `json:"q50"`
	Q90 float64 `json:"q90"`
	// PUp is P(return > +Tau); PDown is P(return < −Tau).
	PUp   float64 `json:"pUp"`
	PDown float64 `json:"pDown"`
	// PInside is P(|return| <= Tau) — the no-trade probability. PUp + PDown +
	// PInside == 1 by construction.
	PInside float64 `json:"pInside"`
	// Sigma is the sample standard deviation — the width, which is the part of
	// this forecast that carries skill.
	Sigma float64 `json:"sigma"`
	// Edge is PUp − PDown: the directional lean AFTER costs, in [−1, 1]. This
	// is deliberately NOT presented as a probability of being right; it is the
	// asymmetry of a cost-aware distribution.
	Edge float64 `json:"edge"`
	// ExpectedValue is the cost-adjusted expected profit of taking the larger
	// side once: E[return | side] × P(side) − Tau. Negative means the move is
	// not worth its cost even when the lean is correct — the case a win-rate
	// target can never express.
	ExpectedValue float64 `json:"expectedValue"`
}

// Forecast estimates the return distribution from a conditional sample of
// realized forward returns. Returns ok=false when the sample is too thin
// (below MinSample), when tau is negative, or when the sample is degenerate.
// The input slice is never mutated.
func Forecast(sample []float64, tau float64) (Dist, bool) {
	if len(sample) < MinSample || tau < 0 {
		return Dist{}, false
	}
	xs := append([]float64(nil), sample...)
	for _, v := range xs {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return Dist{}, false
		}
	}
	sort.Float64s(xs)
	n := float64(len(xs))

	d := Dist{N: len(xs), Tau: tau}
	for _, v := range xs {
		d.Mean += v
	}
	d.Mean /= n
	var ss float64
	for _, v := range xs {
		ss += (v - d.Mean) * (v - d.Mean)
	}
	d.Sigma = math.Sqrt(ss / (n - 1))
	d.Q10, d.Q50, d.Q90 = quantile(xs, 0.10), quantile(xs, 0.50), quantile(xs, 0.90)

	var up, down int
	for _, v := range xs {
		switch {
		case v > tau:
			up++
		case v < -tau:
			down++
		}
	}
	d.PUp = float64(up) / n
	d.PDown = float64(down) / n
	d.PInside = 1 - d.PUp - d.PDown
	d.Edge = d.PUp - d.PDown

	// Expected profit of taking the leaning side ONCE, net of the round-trip
	// cost: side × E[return] − tau, where the side is chosen by the cost-aware
	// lean. This is the arithmetic that makes a 45%-win / +6%-winner strategy
	// read as profitable and a 70%-win / +0.5%-winner strategy read as a loser
	// — the comparison a win-rate target cannot express. A zero lean means no
	// trade, so no cost is paid and the value is zero, not −tau.
	switch {
	case d.Edge > 0:
		d.ExpectedValue = d.Mean - tau
	case d.Edge < 0:
		d.ExpectedValue = -d.Mean - tau
	default:
		d.ExpectedValue = 0
	}
	return d, true
}

// quantile returns the q-th quantile of an ALREADY-SORTED slice using linear
// interpolation between order statistics (the type-7 definition, matching numpy
// and R defaults so an external re-check reproduces these numbers exactly).
func quantile(sorted []float64, q float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	if q <= 0 {
		return sorted[0]
	}
	if q >= 1 {
		return sorted[len(sorted)-1]
	}
	pos := q * float64(len(sorted)-1)
	lo := int(math.Floor(pos))
	hi := int(math.Ceil(pos))
	if lo == hi {
		return sorted[lo]
	}
	frac := pos - float64(lo)
	return sorted[lo]*(1-frac) + sorted[hi]*frac
}

// ─────────────────────────────────────────────────────────────────────────
// Grading

// QPair is one graded quantile forecast: the conditional forecast made at the
// time, the CLIMATOLOGY forecast (unconditional, the null this must beat), and
// the return that was actually realized afterwards.
type QPair struct {
	Cond     Dist
	Clim     Dist
	Realized float64
}

// QuantileGrade is the out-of-sample verdict on a set of distribution
// forecasts.
type QuantileGrade struct {
	N int `json:"n"`
	// Loss is the mean pinball loss of the conditional forecast across the
	// 10/50/90 quantiles. Lower is better.
	Loss float64 `json:"loss"`
	// BaselineLoss is the same figure for the climatology forecast.
	BaselineLoss float64 `json:"baselineLoss"`
	// Skill is 1 − Loss/BaselineLoss. > 0 means conditioning added information.
	// <= 0 means it did not, and the leg should be dropped like any unlifted
	// leg — this is the gate, not a diagnostic.
	Skill float64 `json:"skill"`
	// Coverage80 is the fraction of realized returns that landed inside
	// [Q10, Q90]. A calibrated forecast scores ~0.80; materially below means
	// the forecast is overconfident about the width, which is the failure mode
	// that matters when the width is the part carrying skill.
	Coverage80 float64 `json:"coverage80"`
	// Meaningful is false when there were too few pairs to conclude anything.
	Meaningful bool `json:"meaningful"`
}

// minGradePairs is the least resolved pairs before a quantile grade is treated
// as evidence rather than noise. Matches the platform-wide independent-obs
// floor.
const minGradePairs = 30

// GradeQuantiles scores conditional distribution forecasts against the
// climatology null using pinball loss. Skill > 0 is the admission gate.
func GradeQuantiles(pairs []QPair) QuantileGrade {
	g := QuantileGrade{N: len(pairs)}
	if len(pairs) == 0 {
		return g
	}
	qs := []float64{0.10, 0.50, 0.90}
	var inside int
	for _, p := range pairs {
		cond := []float64{p.Cond.Q10, p.Cond.Q50, p.Cond.Q90}
		clim := []float64{p.Clim.Q10, p.Clim.Q50, p.Clim.Q90}
		for i, q := range qs {
			g.Loss += pinball(cond[i], p.Realized, q)
			g.BaselineLoss += pinball(clim[i], p.Realized, q)
		}
		if p.Realized >= p.Cond.Q10 && p.Realized <= p.Cond.Q90 {
			inside++
		}
	}
	denom := float64(len(pairs) * len(qs))
	g.Loss /= denom
	g.BaselineLoss /= denom
	if g.BaselineLoss > 0 {
		g.Skill = 1 - g.Loss/g.BaselineLoss
	}
	g.Coverage80 = float64(inside) / float64(len(pairs))
	g.Meaningful = len(pairs) >= minGradePairs
	return g
}

// pinball is the quantile (pinball) loss: the proper scoring rule for a
// quantile forecast. Under-forecasting the q-th quantile costs q per unit of
// shortfall; over-forecasting costs (1−q). Minimizing it over a sample is
// uniquely achieved by the true q-th quantile, which is what makes it proper
// and what makes Skill above meaningful.
func pinball(pred, actual, q float64) float64 {
	if actual >= pred {
		return q * (actual - pred)
	}
	return (1 - q) * (pred - actual)
}

// BandGrade is the directional record measured ONLY where the realized move
// cleared the cost band.
type BandGrade struct {
	// N is every observation offered.
	N int `json:"n"`
	// Gradeable is how many cleared ±tau and could therefore be right or wrong.
	Gradeable int `json:"gradeable"`
	// NoCall is N − Gradeable: moves inside the band. Under the old binary
	// target every one of these was scored as a win or a loss; here they are
	// scored as neither, which is the whole point.
	NoCall int `json:"noCall"`
	// Correct among Gradeable.
	Correct int `json:"correct"`
	// Accuracy is Correct/Gradeable, or 0 when nothing was gradeable.
	Accuracy float64 `json:"accuracy"`
	// NoCallRate is NoCall/N — how much of the old accuracy number was being
	// computed on noise.
	NoCallRate float64 `json:"noCallRate"`
	// Meaningful is false below the gradeable-sample floor.
	Meaningful bool `json:"meaningful"`
}

// BandPair is one directional call and what happened. Edge is the forecast
// lean (Dist.Edge, or any signed conviction); Realized is the forward return.
type BandPair struct {
	Edge     float64
	Realized float64
}

// GradeBand scores directional calls with the cost band applied to the LABEL: a
// realized |return| <= tau is a no-call and enters neither numerator nor
// denominator. A zero Edge is also a no-call — declining to lean is not a
// wrong answer.
func GradeBand(pairs []BandPair, tau float64) BandGrade {
	g := BandGrade{N: len(pairs)}
	for _, p := range pairs {
		if math.Abs(p.Realized) <= tau || p.Edge == 0 {
			g.NoCall++
			continue
		}
		g.Gradeable++
		if (p.Edge > 0) == (p.Realized > 0) {
			g.Correct++
		}
	}
	if g.Gradeable > 0 {
		g.Accuracy = float64(g.Correct) / float64(g.Gradeable)
	}
	if g.N > 0 {
		g.NoCallRate = float64(g.NoCall) / float64(g.N)
	}
	g.Meaningful = g.Gradeable >= minGradePairs
	return g
}
