// Package modelhealth scores a model's fitness to keep predicting, and decides
// when it must stop.
//
// WHY THIS EXISTS
// ---------------
// SignalDeck already proves the need empirically. Its directional ensemble
// accumulated a live record whose day-clustered interval sat entirely BELOW the
// majority-class baseline — significantly negative skill — and it kept shipping
// predictions the whole time, because nothing in the system had the authority to
// switch a model off. An accuracy number that no process acts on is decoration.
//
// The figures are deliberately not typed here. data/accuracy_registry.json is
// the one source and GET /api/accuracy serves it; a number copied into a comment
// is a number that goes stale silently, which is how FC1 happened.
//
// So health here is not a dashboard metric. It is a GATE with one job: a model
// whose live record no longer supports its claims stops emitting, automatically,
// without waiting for a human to notice.
//
// SCORING
// -------
// Five components, each in [0,1], combined by weight. They answer genuinely
// different questions, which is why no single one can carry the decision:
//
//	skill        — does it beat its own naive baseline? (the only one that can
//	               force retirement on its own; negative skill is disqualifying
//	               no matter how good the rest looks)
//	calibration  — when it says 70%, does it happen 70% of the time?
//	drift        — is recent accuracy holding against its longer record?
//	freshness    — how long since the model was retrained?
//	stability    — are its inputs still behaving like the ones it learned on?
//
// EVIDENCE FIRST
// --------------
// Below MinObservations the verdict is Provisional, never Healthy and never
// Retired. A thin record cannot promote a model OR condemn one, and saying so
// is more useful than a confident number computed from nothing.
package modelhealth

import "math"

// Verdict is the operational decision derived from the score.
type Verdict string

const (
	// VerdictHealthy — keep predicting.
	VerdictHealthy Verdict = "healthy"
	// VerdictWatch — degraded but still emitting; worth investigating.
	VerdictWatch Verdict = "watch"
	// VerdictDegraded — emitting under an explicit warning label.
	VerdictDegraded Verdict = "degraded"
	// VerdictRetired — STOP emitting. The live record does not support it.
	VerdictRetired Verdict = "retired"
	// VerdictProvisional — not enough evidence to judge either way.
	VerdictProvisional Verdict = "provisional"
	// VerdictUnattributable — STOP emitting, but claim nothing about the
	// record. The grader's revision gate found contributing rows written by
	// builds this repository does not contain, so the live record cannot be
	// tied to any released code. That disqualifies the evidence in BOTH
	// directions: it is not a FAILED verdict (no claim survives the gate) and
	// it is not a clean bill of health either. A model nobody can reproduce
	// does not get to keep publishing while the question is open.
	VerdictUnattributable Verdict = "unattributable"
)

// Inputs is one model's measured state. Every field is an observation, never
// an estimate — a caller that cannot measure something leaves it zero and says
// so via Observations.
type Inputs struct {
	// Observations is the count of INDEPENDENT resolved predictions behind
	// Accuracy. Pseudo-replicated counts (many intraday rows resolving against
	// one forward move) must be deduped before they reach this struct.
	Observations int

	Accuracy     float64 // realized directional accuracy over the record
	BaselineAcc  float64 // best naive constant predictor (majority class)
	RecentAcc    float64 // accuracy over the most recent window
	RecentN      int     // observations behind RecentAcc
	BrierSkill   float64 // 1 - Brier/Brier_baserate; >0 beats the base rate
	CalibrationErr float64 // mean |predicted - realized| across bins

	AgeDays    float64 // days since last retrain
	MaxAgeDays float64 // age at which freshness reaches zero (0 = default)
	// FeatureDriftPct is the fraction of features whose distribution moved
	// materially. A POINTER, because "not measured" and "no drift" are different
	// answers and a float64 cannot tell them apart.
	//
	// It used to be a plain float64, and every failure path in the caller
	// returned 0 — which scores stability at a perfect 1.0. That is precisely
	// the defect drift.go was written to close ("nothing ever computed it, so it
	// was always zero and stability always scored a perfect 1.0"), reinstated
	// through the error path of the fix. nil now withholds the component
	// instead of awarding full marks for an unanswered question.
	FeatureDriftPct *float64
}

// Score is the graded result.
type Score struct {
	Overall     float64            `json:"overall"`
	Components  map[string]float64 `json:"components"`
	Verdict     Verdict            `json:"verdict"`
	Reasons     []string           `json:"reasons"`
	Emitting    bool               `json:"emitting"`
	Observations int               `json:"observations"`
}

const (
	// MinObservations is the independent-observation floor below which no
	// verdict is claimed. Matches the platform's existing gate so one number
	// governs "is this evidence" everywhere.
	MinObservations = 30

	// RetireBelow is the overall score at which a model is switched off.
	RetireBelow = 0.35
	// DegradedBelow / WatchBelow bracket the warning bands.
	DegradedBelow = 0.55
	WatchBelow    = 0.70

	defaultMaxAgeDays = 90
)

// weights sum to 1. Skill dominates because a model that does not beat its
// baseline has no job, however well-calibrated or fresh it is.
var weights = map[string]float64{
	"skill":       0.40,
	"calibration": 0.20,
	"drift":       0.20,
	"freshness":   0.10,
	"stability":   0.10,
}

func clamp01(v float64) float64 { return math.Max(0, math.Min(1, v)) }

// Ptr wraps a measured value for the nilable Inputs fields. Mirrors
// fleetmon.Ptr, and exists for the same reason: the difference between a
// measurement of zero and no measurement at all has to survive into the struct.
func Ptr(v float64) *float64 { return &v }

// Grade scores the inputs and returns the operational verdict.
func Grade(in Inputs) Score {
	comp := map[string]float64{}
	var reasons []string

	// SKILL — edge over the naive baseline, not over 50%. Equities close up
	// more than half the time, so grading against a coin flip manufactures
	// skill that is not there. +5pp of real edge is treated as a full score;
	// nothing sustainable does much better.
	edge := in.Accuracy - in.BaselineAcc
	comp["skill"] = clamp01(0.5 + edge/0.10)
	if edge < 0 {
		reasons = append(reasons, "accuracy is BELOW the naive baseline — no measured edge")
	}

	// CALIBRATION — combine reliability with Brier skill. A model can be
	// directionally useless yet well-calibrated, so this cannot stand alone.
	cal := clamp01(1 - in.CalibrationErr/0.20)
	if in.BrierSkill < 0 {
		cal *= 0.5
		reasons = append(reasons, "Brier skill negative — worse than forecasting the base rate")
	}
	comp["calibration"] = cal

	// DRIFT — recent vs full-record accuracy. Absent a usable recent window,
	// score neutral rather than inventing a trend from noise.
	if in.RecentN >= MinObservations {
		delta := in.RecentAcc - in.Accuracy
		comp["drift"] = clamp01(0.5 + delta/0.10)
		if delta < -0.05 {
			reasons = append(reasons, "recent accuracy has decayed materially vs its own record")
		}
	} else {
		comp["drift"] = 0.5
	}

	// FRESHNESS — a stale model is not wrong, just unverified against the
	// current regime, so this is weighted lightly.
	maxAge := in.MaxAgeDays
	if maxAge <= 0 {
		maxAge = defaultMaxAgeDays
	}
	comp["freshness"] = clamp01(1 - in.AgeDays/maxAge)
	if in.AgeDays > maxAge {
		reasons = append(reasons, "model is past its retrain horizon")
	}

	// STABILITY — inputs drifting away from the training distribution.
	// WITHHELD, not scored, when drift could not be measured: a component
	// missing from the map is visibly absent to every consumer (they all read it
	// with the comma-ok idiom), whereas a 1.0 is indistinguishable from a
	// genuinely stable model.
	if in.FeatureDriftPct != nil {
		comp["stability"] = clamp01(1 - *in.FeatureDriftPct)
		if *in.FeatureDriftPct > 0.30 {
			reasons = append(reasons, "a third or more of features have shifted distribution")
		}
	} else {
		reasons = append(reasons,
			"feature drift could not be measured — stability is WITHHELD from this "+
				"grade rather than scored, so the overall figure is a weighted average "+
				"of the components that were actually measured")
	}

	// Renormalise over the components actually present. Summing absent ones as
	// zero would penalise a model for a measurement the platform failed to take,
	// which is the mirror of the bug above and just as dishonest. With every
	// component present the divisor is 1 and this is arithmetically identical to
	// what it replaced.
	overall, wsum := 0.0, 0.0
	for k, w := range weights {
		v, ok := comp[k]
		if !ok {
			continue
		}
		overall += v * w
		wsum += w
	}
	if wsum > 0 {
		overall /= wsum
	}

	s := Score{
		Overall:      overall,
		Components:   comp,
		Reasons:      reasons,
		Observations: in.Observations,
	}

	// Evidence gate first: a thin record neither promotes nor condemns.
	if in.Observations < MinObservations {
		s.Verdict = VerdictProvisional
		s.Emitting = true
		s.Reasons = append(s.Reasons,
			"insufficient independent observations to judge — emitting as experimental")
		return s
	}

	// Negative measured edge is disqualifying on its own. This is the rule
	// that would have caught the directional ensemble: a strong score elsewhere
	// must not rescue a model that loses to guessing the majority class.
	if edge < 0 {
		s.Verdict = VerdictRetired
		s.Emitting = false
		return s
	}

	switch {
	case overall < RetireBelow:
		s.Verdict, s.Emitting = VerdictRetired, false
	case overall < DegradedBelow:
		s.Verdict, s.Emitting = VerdictDegraded, true
	case overall < WatchBelow:
		s.Verdict, s.Emitting = VerdictWatch, true
	default:
		s.Verdict, s.Emitting = VerdictHealthy, true
	}
	return s
}
