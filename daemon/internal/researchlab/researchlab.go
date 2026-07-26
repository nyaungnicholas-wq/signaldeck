// Package researchlab is the hypothesis-testing core of the Research Lab.
//
// THE DISCIPLINE PROBLEM this package exists to solve: if you generate many
// candidate feature rules and keep the ones with the best out-of-sample score,
// you WILL find "edges" that are pure noise — the multiple-comparisons trap that
// sinks most quant research. Everything here is built to make a false discovery
// hard:
//
//  1. Every candidate is graded by STRICT walk-forward, expanding-window
//     out-of-sample evaluation (via internal/gbm.Evaluate) — never in-sample.
//  2. Survival requires beating the incumbent baseline by a BONFERRONI-CORRECTED
//     Wilson lower bound: the more hypotheses tested in a night, the higher the
//     bar each must clear. Testing more does NOT make it easier to "find" edge.
//  3. A single night's win only earns SHADOW status. Promotion requires a STREAK
//     of consecutive wins re-measured on FRESH, accruing data — the one guard a
//     spurious subset cannot fake, because new data it never saw keeps arriving.
//  4. Nothing here mutates live predictions. A promoted hypothesis is an
//     advisory, independently-verifiable finding — adoption stays gated.
//
// The package is PURE (no I/O, no clock, no RNG): the worker assembles Rows from
// the labeled feature store and this package generates + grades hypotheses
// deterministically, so a result is reproducible and auditable.
package researchlab

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"math"
	"sort"

	"github.com/nyaungnicholas-wq/signaldeck/internal/gbm"
)

// Row is one labeled training example: a feature vector and its realized label.
// Decoupled from the store so this package stays pure and unit-testable.
type Row struct {
	Ts  int64
	Vec map[string]float64
	Y   float64 // realized 1/0 (up)
}

// Kind is the hypothesis family.
type Kind string

const (
	// KindAblation drops one feature — testing "is feature X just noise?"
	KindAblation Kind = "ablation"
	// KindInteraction adds the product of two features — testing "does the
	// interaction of X and Y carry signal the additive model misses?"
	KindInteraction Kind = "interaction"
	// KindRowGate restricts training to rows where a feature clears a
	// threshold — testing "is the edge concentrated in a sub-regime?"
	KindRowGate Kind = "row_gate"
)

// Hypothesis is one testable modification to the incumbent feature model.
type Hypothesis struct {
	ID       string   `json:"id"` // deterministic hash of the spec
	Kind     Kind     `json:"kind"`
	Drop     string   `json:"drop,omitempty"`     // ablation: feature to remove
	Interact []string `json:"interact,omitempty"` // interaction: exactly two feature names
	GateKey  string   `json:"gateKey,omitempty"`  // row_gate: feature to threshold
	GateMin  float64  `json:"gateMin,omitempty"`  // row_gate: keep rows with Vec[GateKey] >= GateMin
	Desc     string   `json:"desc"`
}

func (h Hypothesis) computeID() string {
	sig := fmt.Sprintf("%s|drop=%s|int=%v|gate=%s>=%.6f", h.Kind, h.Drop, h.Interact, h.GateKey, h.GateMin)
	sum := sha1.Sum([]byte(sig))
	return hex.EncodeToString(sum[:8])
}

// CanonicalKeys returns the STABLE, sorted union of feature keys across rows,
// excluding keys that would let a model cheat (the blend's own probabilities and
// each model leg's prior output — training on those learns "copy the ensemble"
// instead of finding independent structure). Mirrors the live GBM trainer's
// exclusion set so hypotheses are graded on the same honest footing.
func CanonicalKeys(rows []Row) []string {
	set := map[string]struct{}{}
	for _, r := range rows {
		for k := range r.Vec {
			if excludedKey(k) {
				continue
			}
			set[k] = struct{}{}
		}
	}
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func excludedKey(k string) bool {
	switch k {
	case "pred_raw", "pred_cal", "gbm_prob", "meanrev_prob", "alphax_prob":
		return true
	}
	return false
}

// GenerateHypotheses produces a deterministic, BOUNDED candidate set over the
// given feature keys, prioritized by the dominant failure cluster so the search
// spends its (multiple-comparison-limited) budget where the misses actually are.
//
// clusterPriority maps a primary failure-reason code to a weight; higher-weight
// clusters pull related hypothesis kinds to the front. It may be nil (uniform).
// max caps the total candidates — a HARD limit, because every extra hypothesis
// raises the Bonferroni bar for ALL of them, so a bloated search is
// self-defeating, not free.
func GenerateHypotheses(keys []string, clusterPriority map[string]float64, max int) []Hypothesis {
	if max <= 0 {
		max = 24
	}
	// Deterministic key order already (CanonicalKeys sorts). Ablation for every
	// feature; interactions among the first interactCap keys (bounded pairwise);
	// row-gates only when the dominant cluster suggests a sub-regime hides edge.
	var hyps []Hypothesis
	for _, k := range keys {
		h := Hypothesis{Kind: KindAblation, Drop: k, Desc: "drop feature " + k + " (test if it is noise)"}
		h.ID = h.computeID()
		hyps = append(hyps, h)
	}
	const interactCap = 6
	top := keys
	if len(top) > interactCap {
		top = top[:interactCap]
	}
	for i := 0; i < len(top); i++ {
		for j := i + 1; j < len(top); j++ {
			h := Hypothesis{Kind: KindInteraction, Interact: []string{top[i], top[j]},
				Desc: "add interaction " + top[i] + "×" + top[j]}
			h.ID = h.computeID()
			hyps = append(hyps, h)
		}
	}
	// Cluster-driven row gates: if regime/macro failures dominate, test whether
	// edge concentrates where a regime/vol feature is elevated.
	if clusterPriority != nil {
		for _, k := range keys {
			if !gateCandidateKey(k) {
				continue
			}
			h := Hypothesis{Kind: KindRowGate, GateKey: k, GateMin: 0.5,
				Desc: "restrict to rows where " + k + " >= 0.5 (test sub-regime edge)"}
			h.ID = h.computeID()
			hyps = append(hyps, h)
		}
	}

	// Prioritize by cluster relevance, then by ID for a stable order, then cap.
	sort.SliceStable(hyps, func(i, j int) bool {
		pi, pj := hypPriority(hyps[i], clusterPriority), hypPriority(hyps[j], clusterPriority)
		if pi != pj {
			return pi > pj
		}
		return hyps[i].ID < hyps[j].ID
	})
	if len(hyps) > max {
		hyps = hyps[:max]
	}
	return hyps
}

func gateCandidateKey(k string) bool {
	switch k {
	case "vix_high_vol", "vix_regime", "regime_up", "vix_level":
		return true
	}
	return false
}

// hypPriority scores a hypothesis by how much the dominant failure clusters
// implicate its kind — a light, honest heuristic to order the bounded search.
func hypPriority(h Hypothesis, cp map[string]float64) float64 {
	if cp == nil {
		return 0
	}
	switch h.Kind {
	case KindRowGate:
		return cp["regime_shift"] + cp["macro_event"]
	case KindInteraction:
		return cp["model_disagreement"] + cp["unexplained"]
	case KindAblation:
		return cp["calibration_error"] + cp["data_quality"]
	}
	return 0
}

// Grade is re-exported for callers so they don't import gbm directly.
type Grade = gbm.Grade

// EvalConfig holds the walk-forward + model settings, fixed so grades stay
// comparable across hypotheses and across nights.
type EvalConfig struct {
	Folds  int
	Params gbm.Params
	// LabelSpan is how long after its timestamp a row's label resolves, in
	// seconds. gbm.Evaluate needs it to purge training rows whose label lands
	// inside the test block; without it the grade is refused rather than
	// computed across overlapping labels. Zero means the caller has not
	// declared it, which is itself a defect the evaluator will surface.
	LabelSpan int64
}

// DefaultEvalConfig uses the same shallow, small GBM the live trainer uses (the
// feature-store training sets are modest; a deep ensemble would memorize noise).
func DefaultEvalConfig() EvalConfig {
	return EvalConfig{Folds: 5, Params: gbm.Defaults()}
}

// Baseline grades the incumbent full-feature model — the bar every hypothesis
// must beat. Returns ErrInsufficientData transparently when there is too little
// labeled data to grade honestly (the correct "NO EDGE DETECTED" outcome).
func Baseline(rows []Row, keys []string, cfg EvalConfig) (Grade, error) {
	samples := toSamples(rows, keys, nil, cfg.LabelSpan)
	return gbm.Evaluate(samples, cfg.Folds, cfg.Params)
}

// EvaluateHypothesis grades one hypothesis by walk-forward OOS, applying its
// feature transform to the SAME rows the baseline used (so the comparison is
// apples-to-apples). Returns the OOS Grade or a gbm error (insufficient data).
func EvaluateHypothesis(h Hypothesis, rows []Row, keys []string, cfg EvalConfig) (Grade, error) {
	switch h.Kind {
	case KindAblation:
		kept := without(keys, h.Drop)
		return gbm.Evaluate(toSamples(rows, kept, nil, cfg.LabelSpan), cfg.Folds, cfg.Params)
	case KindInteraction:
		if len(h.Interact) != 2 {
			return Grade{}, fmt.Errorf("interaction needs exactly 2 features")
		}
		a, b := h.Interact[0], h.Interact[1]
		return gbm.Evaluate(toSamples(rows, keys, func(v map[string]float64) []float64 {
			return []float64{v[a] * v[b]}
		}, cfg.LabelSpan), cfg.Folds, cfg.Params)
	case KindRowGate:
		gated := make([]Row, 0, len(rows))
		for _, r := range rows {
			if r.Vec[h.GateKey] >= h.GateMin {
				gated = append(gated, r)
			}
		}
		return gbm.Evaluate(toSamples(gated, keys, nil, cfg.LabelSpan), cfg.Folds, cfg.Params)
	}
	return Grade{}, fmt.Errorf("unknown hypothesis kind %q", h.Kind)
}

// toSamples builds gbm samples from rows over an ordered key list, optionally
// appending extra engineered features (e.g. an interaction product).
func toSamples(rows []Row, keys []string, extra func(map[string]float64) []float64, labelSpan int64) []gbm.Sample {
	out := make([]gbm.Sample, 0, len(rows))
	for _, r := range rows {
		feat := make([]float64, 0, len(keys)+1)
		for _, k := range keys {
			feat = append(feat, r.Vec[k])
		}
		if extra != nil {
			feat = append(feat, extra(r.Vec)...)
		}
		out = append(out, gbm.Sample{Ts: r.Ts, Feat: feat, Y: r.Y, LabelEnd: r.Ts + labelSpan})
	}
	return out
}

func without(keys []string, drop string) []string {
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		if k != drop {
			out = append(out, k)
		}
	}
	return out
}

// Decision is the verdict on one hypothesis for a night.
type Decision struct {
	Hypothesis Hypothesis
	Grade      Grade
	Baseline   Grade
	// CorrectedAlpha is the Bonferroni-adjusted significance level applied
	// (nominal alpha / number of hypotheses tested).
	CorrectedAlpha float64
	// WilsonLower is the corrected-alpha lower bound on the candidate's OOS
	// accuracy — the honest floor of its skill.
	WilsonLower float64
	// Survives is true iff the candidate's Wilson lower bound (at the corrected
	// alpha) exceeds the baseline's point accuracy AND its lift is positive.
	Survives bool
}

// Judge applies the Bonferroni-corrected Wilson-lower-bound test. A candidate
// survives ONLY when even the pessimistic floor of its OOS accuracy — corrected
// for having tested nTested hypotheses tonight — still beats the incumbent
// baseline's accuracy, and its lift over its own base rate is positive.
//
// This is the anti-false-discovery gate: it is deliberately HARD to pass, and
// gets harder the more hypotheses are tested.
func Judge(h Hypothesis, g, baseline Grade, nTested int, nominalAlpha float64) Decision {
	if nTested < 1 {
		nTested = 1
	}
	corrected := nominalAlpha / float64(nTested)
	z := normalQuantile(1 - corrected) // one-sided
	wl := wilsonLower(g.Accuracy, g.N, z)
	survives := g.N > 0 && g.Lift > 0 && wl > baseline.Accuracy
	return Decision{
		Hypothesis: h, Grade: g, Baseline: baseline,
		CorrectedAlpha: corrected, WilsonLower: wl, Survives: survives,
	}
}

// wilsonLower returns the lower bound of the Wilson score interval for a
// proportion phat over n trials at the given z. Returns 0 for n<=0.
func wilsonLower(phat float64, n int, z float64) float64 {
	if n <= 0 {
		return 0
	}
	nf := float64(n)
	z2 := z * z
	denom := 1 + z2/nf
	center := phat + z2/(2*nf)
	margin := z * math.Sqrt(phat*(1-phat)/nf+z2/(4*nf*nf))
	lb := (center - margin) / denom
	if lb < 0 {
		return 0
	}
	return lb
}

// normalQuantile is the inverse standard-normal CDF (probit) via Acklam's
// rational approximation — pure and deterministic (no RNG, no clock). Accurate
// to ~1e-9, plenty for a significance threshold.
func normalQuantile(p float64) float64 {
	if p <= 0 {
		return math.Inf(-1)
	}
	if p >= 1 {
		return math.Inf(1)
	}
	// coefficients
	a := []float64{-3.969683028665376e+01, 2.209460984245205e+02, -2.759285104469687e+02, 1.383577518672690e+02, -3.066479806614716e+01, 2.506628277459239e+00}
	b := []float64{-5.447609879822406e+01, 1.615858368580409e+02, -1.556989798598866e+02, 6.680131188771972e+01, -1.328068155288572e+01}
	c := []float64{-7.784894002430293e-03, -3.223964580411365e-01, -2.400758277161838e+00, -2.549732539343734e+00, 4.374664141464968e+00, 2.938163982698783e+00}
	d := []float64{7.784695709041462e-03, 3.224671290700398e-01, 2.445134137142996e+00, 3.754408661907416e+00}
	const plow = 0.02425
	phigh := 1 - plow
	switch {
	case p < plow:
		q := math.Sqrt(-2 * math.Log(p))
		return (((((c[0]*q+c[1])*q+c[2])*q+c[3])*q+c[4])*q + c[5]) /
			((((d[0]*q+d[1])*q+d[2])*q+d[3])*q + 1)
	case p <= phigh:
		q := p - 0.5
		r := q * q
		return (((((a[0]*r+a[1])*r+a[2])*r+a[3])*r+a[4])*r + a[5]) * q /
			(((((b[0]*r+b[1])*r+b[2])*r+b[3])*r+b[4])*r + 1)
	default:
		q := math.Sqrt(-2 * math.Log(1-p))
		return -(((((c[0]*q+c[1])*q+c[2])*q+c[3])*q+c[4])*q + c[5]) /
			((((d[0]*q+d[1])*q+d[2])*q+d[3])*q + 1)
	}
}
