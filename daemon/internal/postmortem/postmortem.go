// Package postmortem is the failure-attribution engine of the Research Lab.
//
// PRINCIPLE (Chief-Quant discipline): every INCORRECT, meaningfully-convicted
// prediction is a research observation, not an embarrassment to hide. This
// package takes one resolved-and-wrong prediction plus the context we already
// have on disk (its component legs, regime changes in the window, news tone,
// macro vol state, calibration history, data thinness) and attributes the
// failure to a RANKED set of reasons drawn from a fixed taxonomy. It then
// CLUSTERS many such reports so recurring failure modes surface as signal.
//
// Everything here is PURE and deterministic (no RNG, no clock, no I/O): the
// worker assembles a Case from the store and this package classifies it. That
// keeps the attribution logic unit-testable and reproducible — a postmortem is
// a measurement, so it must be repeatable.
//
// HONESTY: we NEVER invent a cause. A reason is emitted only when the evidence
// for it is present in the Case. When nothing in the Case explains the miss,
// the primary reason is Unexplained — an honest "the market moved and none of
// our recorded signals saw it coming," which is itself the most important
// cluster to watch (it means the edge, if any, is not in our current features).
package postmortem

import "sort"

// ReasonCode is the fixed failure taxonomy. Adding a code is a deliberate
// schema decision — downstream clustering keys on these exact strings.
type ReasonCode string

const (
	// ReasonRegimeShift: a regime change (uptrend↔downtrend↔range↔squeeze)
	// occurred inside the prediction's forward window. The model scored the
	// old regime; the tape switched underneath it.
	ReasonRegimeShift ReasonCode = "regime_shift"
	// ReasonNewsShock: a strong, fresh news-sentiment signal pointing AGAINST
	// the position was present in-window — a catalyst the price model can't see.
	ReasonNewsShock ReasonCode = "news_shock"
	// ReasonModelDisagreement: the component legs were badly split (low
	// conviction dressed up as a directional call). The ensemble should not
	// have committed; low internal agreement is a leading tell of a coin-flip.
	ReasonModelDisagreement ReasonCode = "model_disagreement"
	// ReasonCalibrationError: predictions in this probability bucket have
	// historically realized far below their implied rate — the number was
	// overconfident before this trade ever resolved.
	ReasonCalibrationError ReasonCode = "calibration_error"
	// ReasonMacroEvent: a market-wide volatility spike (VIX regime elevated/
	// stressed) dominated idiosyncratic signal — beta ate alpha.
	ReasonMacroEvent ReasonCode = "macro_event"
	// ReasonDataQuality: the prediction rested on too few observations
	// (n_used below the floor) — a structurally fragile call.
	ReasonDataQuality ReasonCode = "data_quality"
	// ReasonUnexplained: none of the above evidence is present. The miss is
	// real but unattributable from our current features — the honest default,
	// and the cluster whose growth means "we are missing a feature."
	ReasonUnexplained ReasonCode = "unexplained"
)

// Reason is one attributed cause with a weight (0..1, its share of the
// explanation) and a human-readable detail. Reasons in a Report are sorted
// by descending weight.
type Reason struct {
	Code   ReasonCode `json:"code"`
	Weight float64    `json:"weight"`
	Detail string     `json:"detail"`
}

// Case is the fully-assembled context for one resolved, wrong prediction.
// The worker fills this from the store; classification never reads I/O.
type Case struct {
	// Prob is the calibrated P(up) recorded AT PREDICTION TIME (no lookahead).
	Prob float64
	// Up is the realized outcome (1 up, 0 down). By construction a Case is only
	// built when the prediction was WRONG: (Prob>0.5 && Up==0) || (Prob<0.5 && Up==1).
	Up int
	// FwdReturn is the realized forward return over the horizon (signed).
	FwdReturn float64
	// Disagreement is the dispersion of the component legs around 0.5, in
	// [0,0.5]. Low dispersion with a directional call = the legs barely agreed.
	Disagreement float64
	// RegimeChanged: a regime transition occurred within the forward window.
	RegimeChanged bool
	// RegimeNote describes the transition (for the detail string), e.g.
	// "uptrend→downtrend".
	RegimeNote string
	// NewsAgainst: fresh news sentiment in-window pointed opposite the call.
	NewsAgainst bool
	// NewsMag is |mean news sentiment| in [0,1] for the in-window headlines;
	// only meaningful when NewsAgainst is true.
	NewsMag float64
	// VIXRegime is the macro vol regime 0..3 (calm/normal/elevated/stressed).
	VIXRegime int
	// CalibDeficit is (historical realized hit-rate for this prob bucket) minus
	// (the bucket's implied rate). Negative ⇒ the bucket was overconfident.
	// Zero when we have no calibration history for the bucket (unknown, not
	// evidence — never counted as a cause).
	CalibDeficit float64
	// CalibKnown is true only when CalibDeficit was actually measured.
	CalibKnown bool
	// NUsed is the sample count the prediction rested on (expectancy n, etc.).
	NUsed int
}

// Thresholds are the evidence gates. Exported so the worker and tests share one
// definition and callers can tune without editing the logic.
type Thresholds struct {
	// MinConviction: a Case with |Prob-0.5| below this is not worth a
	// postmortem — it was never a real directional call. The worker filters on
	// this BEFORE building a Case; Conviction() also reports it.
	MinConviction float64
	// DisagreementHi: leg dispersion at or below this (with a directional call)
	// triggers ReasonModelDisagreement.
	DisagreementLo float64
	// NewsMagHi: |news sentiment| at or above this counts as a shock.
	NewsMagHi float64
	// VIXStressed: VIX regime at or above this counts as a macro event.
	VIXStressed int
	// CalibDeficitHi: a calibration deficit worse (more negative) than
	// -CalibDeficitHi counts as a calibration error.
	CalibDeficitHi float64
	// MinNUsed: n_used below this counts as a data-quality failure.
	MinNUsed int
}

// DefaultThresholds are conservative, documented defaults. They are gates for
// EMITTING a reason, deliberately set so a reason appears only on real evidence.
func DefaultThresholds() Thresholds {
	return Thresholds{
		MinConviction:  0.08, // |prob-0.5| >= 0.08 ⇒ prob outside [0.42,0.58]
		DisagreementLo: 0.06, // legs within ±0.06 of 0.5 ⇒ barely agreed
		NewsMagHi:      0.40, // mean sentiment magnitude >= 0.40
		VIXStressed:    2,    // elevated or stressed
		CalibDeficitHi: 0.10, // realized >=10pp below implied
		MinNUsed:       10,
	}
}

// Report is the output of Classify: the ranked reasons plus summary fields.
type Report struct {
	Primary    ReasonCode `json:"primary"`
	Secondary  ReasonCode `json:"secondary,omitempty"`
	Reasons    []Reason   `json:"reasons"`
	Conviction float64    `json:"conviction"` // |Prob-0.5|, how big a call it was
	Magnitude  float64    `json:"magnitude"`  // |FwdReturn|, how badly it moved against us
}

// Classify attributes one wrong prediction to a ranked set of reasons.
// Weights are relative shares; they are normalized to sum to 1 across the
// emitted reasons so clustering can average them meaningfully. When no
// evidence is present the sole reason is Unexplained (weight 1).
func Classify(c Case, t Thresholds) Report {
	conviction := c.Prob - 0.5
	if conviction < 0 {
		conviction = -conviction
	}
	mag := c.FwdReturn
	if mag < 0 {
		mag = -mag
	}

	var rs []Reason
	// Each raw weight is the evidence STRENGTH, so a stronger signal claims a
	// larger share of the explanation. All are gated on real evidence.

	if c.RegimeChanged {
		note := "regime changed within the forward window"
		if c.RegimeNote != "" {
			note = "regime " + c.RegimeNote + " within the forward window"
		}
		rs = append(rs, Reason{ReasonRegimeShift, 1.0, note})
	}
	if c.NewsAgainst && c.NewsMag >= t.NewsMagHi {
		rs = append(rs, Reason{ReasonNewsShock, c.NewsMag,
			"fresh news sentiment pointed against the call"})
	}
	if c.CalibKnown && c.CalibDeficit <= -t.CalibDeficitHi {
		// deeper deficit ⇒ stronger cause; scale into a comparable band.
		rs = append(rs, Reason{ReasonCalibrationError, 0.5 + (-c.CalibDeficit),
			"this probability bucket has been historically overconfident"})
	}
	if c.VIXRegime >= t.VIXStressed {
		rs = append(rs, Reason{ReasonMacroEvent, 0.3 + 0.2*float64(c.VIXRegime),
			"market-wide volatility regime was elevated/stressed"})
	}
	// Disagreement only counts when the call was directional (it explains why a
	// low-conviction call missed): weak internal agreement + a committed call.
	if conviction >= t.MinConviction && c.Disagreement <= t.DisagreementLo {
		rs = append(rs, Reason{ReasonModelDisagreement, 0.4,
			"component legs barely agreed — thin internal conviction"})
	}
	if c.NUsed > 0 && c.NUsed < t.MinNUsed {
		rs = append(rs, Reason{ReasonDataQuality, 0.35,
			"prediction rested on too few observations"})
	}

	if len(rs) == 0 {
		rs = append(rs, Reason{ReasonUnexplained, 1.0,
			"no recorded signal explains this miss — likely a missing feature"})
	}

	// Normalize weights to sum to 1, then sort by descending weight with a
	// stable code tie-break so output is fully deterministic.
	var sum float64
	for _, r := range rs {
		sum += r.Weight
	}
	if sum > 0 {
		for i := range rs {
			rs[i].Weight /= sum
		}
	}
	sort.SliceStable(rs, func(i, j int) bool {
		if rs[i].Weight != rs[j].Weight {
			return rs[i].Weight > rs[j].Weight
		}
		return rs[i].Code < rs[j].Code
	})

	rep := Report{
		Primary:    rs[0].Code,
		Reasons:    rs,
		Conviction: conviction,
		Magnitude:  mag,
	}
	if len(rs) > 1 {
		rep.Secondary = rs[1].Code
	}
	return rep
}

// Cluster is an aggregated failure mode: how often a reason was the PRIMARY
// cause, its mean explanatory weight, and the mean severity of those misses.
type Cluster struct {
	Code       ReasonCode `json:"code"`
	Count      int        `json:"count"`
	Share      float64    `json:"share"`      // Count / total reports
	MeanWeight float64    `json:"meanWeight"` // mean primary weight
	MeanMag    float64    `json:"meanMag"`    // mean |FwdReturn| of these misses
}

// ClusterReports groups reports by their PRIMARY reason and returns the
// clusters sorted by descending count (the biggest recurring failure mode
// first) with a stable code tie-break. This is the "discover patterns" step
// the Research Lab consumes.
func ClusterReports(reports []Report) []Cluster {
	if len(reports) == 0 {
		return nil
	}
	type acc struct {
		count            int
		sumWeight, sumMag float64
	}
	m := map[ReasonCode]*acc{}
	for _, r := range reports {
		a := m[r.Primary]
		if a == nil {
			a = &acc{}
			m[r.Primary] = a
		}
		a.count++
		a.sumMag += r.Magnitude
		// primary weight = first reason's normalized weight.
		if len(r.Reasons) > 0 {
			a.sumWeight += r.Reasons[0].Weight
		}
	}
	total := float64(len(reports))
	out := make([]Cluster, 0, len(m))
	for code, a := range m {
		out = append(out, Cluster{
			Code:       code,
			Count:      a.count,
			Share:      float64(a.count) / total,
			MeanWeight: a.sumWeight / float64(a.count),
			MeanMag:    a.sumMag / float64(a.count),
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Code < out[j].Code
	})
	return out
}

// IsWrong reports whether a calibrated prob and realized outcome constitute a
// wrong directional call. A prob of exactly 0.5 is never wrong (no call was made).
func IsWrong(prob float64, up int) bool {
	if prob > 0.5 {
		return up == 0
	}
	if prob < 0.5 {
		return up == 1
	}
	return false
}
