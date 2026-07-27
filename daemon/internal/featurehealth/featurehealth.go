// Package featurehealth grades INDIVIDUAL features and retires the ones that
// stopped earning their place in the vector.
//
// WHY THIS EXISTS. The platform already retires MODELS automatically
// (internal/modelhealth stops a model emitting at VerdictRetired) and already
// measures features (internal/featureredundancy clusters correlated inputs and
// scores each one's IC; the store tracks feature drift). What it never did was
// act on a per-feature measurement: a feature whose information coefficient
// decayed to nothing stayed in the vector indefinitely, because the only thing
// looking at features was a report a human read.
//
// This package closes that loop with the same vocabulary modelhealth uses —
// healthy / watch / degraded / retired — so "what does retired mean" has one
// answer platform-wide.
//
// WHAT IT DELIBERATELY DOES NOT CLAIM. A feature surviving here is NOT evidence
// it predicts anything. This grades DECAY and INSTABILITY: whether a feature's
// measured relationship with forward returns has weakened or flipped, and
// whether it is a duplicate of one already kept. Out-of-sample lift remains the
// job of internal/researchlab's gate, and nothing here can promote a feature —
// it can only demote one. That asymmetry is intentional: dropping a dead input is
// cheap and reversible, adding one is a claim.
package featurehealth

import (
	"fmt"
	"math"
	"sort"
)

// Verdict mirrors modelhealth's vocabulary so one word means one thing across
// the platform.
type Verdict string

const (
	// VerdictHealthy — keep the feature in the vector.
	VerdictHealthy Verdict = "healthy"
	// VerdictWatch — weakening; still in, worth investigating.
	VerdictWatch Verdict = "watch"
	// VerdictDegraded — materially weaker or unstable; still in, flagged loudly.
	VerdictDegraded Verdict = "degraded"
	// VerdictRetired — DROP from the live vector. The record does not support it.
	VerdictRetired Verdict = "retired"
	// VerdictProvisional — not enough observations to judge either way. Kept: an
	// unjudged feature must not be silently dropped for being new.
	VerdictProvisional Verdict = "provisional"
)

const (
	// MinObservations matches modelhealth.MinObservations — one evidence floor.
	MinObservations = 30

	// RetireBelow / DegradedBelow / WatchBelow bracket the score bands, matching
	// modelhealth's thresholds so the bands mean the same thing.
	RetireBelow   = 0.35
	DegradedBelow = 0.55
	WatchBelow    = 0.70
)

// Inputs is one feature's measured state. Every field is an observation, not an
// estimate. A caller that could not measure something leaves the corresponding
// Known flag false rather than passing a flattering zero.
type Inputs struct {
	Name string

	// N is the observations behind FullIC.
	N int
	// FullIC is the feature's information coefficient over the whole record —
	// the correlation with realized forward return. Sign carries meaning.
	FullIC float64

	// RecentIC is the same measurement over the most recent window, and RecentN
	// its sample. Together with FullIC this is the DECAY signal.
	RecentIC    float64
	RecentN     int
	RecentKnown bool

	// BlockICs are per-block ICs (e.g. one per quarter) used for STABILITY: a
	// feature whose sign flips between blocks is noise that happened to
	// correlate, not a relationship.
	BlockICs []float64

	// Coverage is the fraction of rows where the feature was present at all. A
	// feature absent from most of the vector cannot carry much, however strong it
	// looks on the rows it appears in.
	Coverage float64

	// Redundant marks a feature that internal/featureredundancy clustered with a
	// kept representative — it carries no information that representative does
	// not already.
	Redundant bool
	// DriftPct is the fraction by which the feature's own distribution has moved
	// from its training distribution, when measured.
	DriftPct   float64
	DriftKnown bool
}

// Score is the graded result for one feature.
type Score struct {
	Name       string             `json:"name"`
	Overall    float64            `json:"overall"`
	Components map[string]float64 `json:"components"`
	Verdict    Verdict            `json:"verdict"`
	Reasons    []string           `json:"reasons"`
	// Keep is the actionable output: false means DROP this feature from the live
	// vector. It is a separate field from Verdict so a caller cannot accidentally
	// act on a verdict string comparison and get the polarity wrong.
	Keep bool `json:"keep"`
	N    int  `json:"n"`
}

// weights sum to 1. Strength dominates because a feature with no measurable
// relationship to forward returns has no job, however stable and well-covered
// its nothing is.
var weights = map[string]float64{
	"strength":  0.40,
	"decay":     0.25,
	"stability": 0.20,
	"coverage":  0.15,
}

// fullIC is the |IC| at which the strength term saturates. Financial features
// live at small ICs — 0.05 is a genuinely useful single input and 0.10 is strong
// — so normalizing against 0.10 rather than 1.0 is what keeps this score from
// reporting every real feature as worthless.
const fullIC = 0.10

// Grade scores one feature and decides whether it stays in the vector.
func Grade(in Inputs) Score {
	s := Score{
		Name:       in.Name,
		Components: map[string]float64{},
		N:          in.N,
	}

	// Below the evidence floor nothing is judged, and the feature is KEPT. A new
	// feature must not be retired for being new — that would make the vector
	// unable to ever adopt anything.
	if in.N < MinObservations {
		s.Verdict = VerdictProvisional
		s.Keep = true
		s.Reasons = append(s.Reasons, fmt.Sprintf(
			"only %d observations (need %d) — not judged, and kept: a feature must not be retired for being new", in.N, MinObservations))
		return s
	}

	// A duplicate is retired regardless of how strong it looks: its strength is
	// its representative's strength, counted twice.
	if in.Redundant {
		s.Verdict = VerdictRetired
		s.Keep = false
		s.Components["strength"] = clamp01(math.Abs(in.FullIC) / fullIC)
		s.Reasons = append(s.Reasons,
			"clustered with a kept representative — it carries no information the representative does not already, so keeping both double-counts one input")
		return s
	}

	strength := clamp01(math.Abs(in.FullIC) / fullIC)
	s.Components["strength"] = strength

	// NOISE FLOOR, and it has to be a THRESHOLD rather than a weighted term.
	//
	// A weighted average lets a feature with no relationship at all survive on
	// the strength of its other terms: near-total coverage plus a "decay" ratio
	// computed between two numbers that are both noise scores well over the
	// retirement floor, which is how an IC of 0.0005 first passed this grader.
	// "Does it have any measurable relationship" is a yes/no question, and a no
	// must not be recoverable by being reliably present.
	//
	// The floor is one standard error of a correlation, ~1/sqrt(N): below it the
	// IC is not distinguishable from zero at the sample size that produced it.
	// One SE rather than the conventional two is deliberately lenient — the bar
	// here is "distinguishable from nothing at all", not "statistically
	// significant", which remains the out-of-sample lift gate's much stricter job.
	if noise := 1 / math.Sqrt(float64(in.N)); math.Abs(in.FullIC) < noise {
		s.Overall = 0
		s.Verdict = VerdictRetired
		s.Keep = false
		s.Reasons = append(s.Reasons, fmt.Sprintf(
			"|IC| %.4f is inside one standard error of zero (%.4f at N=%d) — not distinguishable from no relationship, so coverage and stability cannot rescue it",
			math.Abs(in.FullIC), noise, in.N))
		return s
	}

	decay, decayReason := decayScore(in)
	s.Components["decay"] = decay
	if decayReason != "" {
		s.Reasons = append(s.Reasons, decayReason)
	}

	stability, stabilityReason := stabilityScore(in.BlockICs)
	s.Components["stability"] = stability
	if stabilityReason != "" {
		s.Reasons = append(s.Reasons, stabilityReason)
	}

	coverage := clamp01(in.Coverage)
	s.Components["coverage"] = coverage
	if coverage < 0.5 {
		s.Reasons = append(s.Reasons, fmt.Sprintf(
			"present on only %.0f%% of rows — thin coverage limits what it can contribute", coverage*100))
	}

	var overall float64
	for k, w := range weights {
		overall += w * s.Components[k]
	}
	// Drift is not a weighted term: it is a PENALTY, because a feature whose own
	// distribution has moved is not merely weaker, it is measuring something else
	// than it was trained on. Only applied when actually measured.
	if in.DriftKnown && in.DriftPct > 0 {
		penalty := clamp01(in.DriftPct) * 0.20
		overall = math.Max(0, overall-penalty)
		s.Components["driftPenalty"] = penalty
		if in.DriftPct >= 0.5 {
			s.Reasons = append(s.Reasons, fmt.Sprintf(
				"distribution has moved %.0f%% from training — it may no longer measure what it was fit on", in.DriftPct*100))
		}
	}
	s.Overall = clamp01(overall)

	switch {
	case s.Overall < RetireBelow:
		s.Verdict = VerdictRetired
		s.Keep = false
		s.Reasons = append(s.Reasons, fmt.Sprintf(
			"overall %.2f is below the %.2f retirement floor — dropped from the live vector", s.Overall, RetireBelow))
	case s.Overall < DegradedBelow:
		s.Verdict = VerdictDegraded
		s.Keep = true
	case s.Overall < WatchBelow:
		s.Verdict = VerdictWatch
		s.Keep = true
	default:
		s.Verdict = VerdictHealthy
		s.Keep = true
	}
	if len(s.Reasons) == 0 {
		s.Reasons = append(s.Reasons, fmt.Sprintf(
			"|IC| %.3f over %d observations, stable and well covered", math.Abs(in.FullIC), in.N))
	}
	return s
}

// decayScore compares the recent window against the full record.
//
// The ratio is of ABSOLUTE ICs, but a SIGN FLIP is scored at zero rather than as
// a strong negative ratio: a feature that used to predict up and now predicts
// down has not weakened, it has stopped being the thing it was, and averaging
// across the flip would report the two halves as cancelling noise.
//
// When the recent window was not measured the term returns a NEUTRAL 0.5 rather
// than either extreme, and says so — the platform should not reward a feature for
// being unmeasured, nor punish it.
func decayScore(in Inputs) (float64, string) {
	if !in.RecentKnown || in.RecentN <= 0 {
		return 0.5, "recent-window IC not measured — decay scored neutral rather than assumed"
	}
	full := math.Abs(in.FullIC)
	if full <= 1e-12 {
		// Never had anything to decay from. Strength already scores that; decay
		// is neutral here rather than a free pass.
		return 0.5, ""
	}
	if in.FullIC*in.RecentIC < 0 {
		return 0, fmt.Sprintf(
			"IC flipped sign (%.3f over the record, %.3f recently) — the relationship reversed rather than weakened",
			in.FullIC, in.RecentIC)
	}
	ratio := math.Abs(in.RecentIC) / full
	score := clamp01(ratio)
	if ratio < 0.5 {
		return score, fmt.Sprintf(
			"recent |IC| %.3f is less than half the full-record %.3f — decaying", math.Abs(in.RecentIC), full)
	}
	return score, ""
}

// stabilityScore measures sign consistency across blocks: the fraction of blocks
// agreeing with the majority direction, rescaled so that a coin-flip split (0.5)
// scores 0 and unanimity scores 1.
//
// Rescaling matters. Raw agreement can never fall below 0.5 by construction, so
// an unrescaled score would report pure noise as "50% stable" and let it pass the
// retirement floor on that alone.
func stabilityScore(blocks []float64) (float64, string) {
	usable := make([]float64, 0, len(blocks))
	for _, b := range blocks {
		if !math.IsNaN(b) && !math.IsInf(b, 0) && b != 0 {
			usable = append(usable, b)
		}
	}
	if len(usable) < 3 {
		return 0.5, "fewer than 3 usable blocks — stability scored neutral rather than assumed"
	}
	pos, neg := 0, 0
	for _, b := range usable {
		if b > 0 {
			pos++
		} else {
			neg++
		}
	}
	major := pos
	if neg > pos {
		major = neg
	}
	agreement := float64(major) / float64(len(usable))
	score := clamp01((agreement - 0.5) * 2)
	if score < 0.4 {
		return score, fmt.Sprintf(
			"sign is inconsistent across blocks (%d up / %d down) — this is noise that correlated, not a relationship", pos, neg)
	}
	return score, ""
}

// Report is the fleet-wide feature verdict: what the live vector should contain
// after this pass, and what it should drop.
type Report struct {
	Scores []Score `json:"scores"`
	// Keep and Retire are the actionable sets, sorted by name.
	Keep   []string `json:"keep"`
	Retire []string `json:"retire"`
	// RetiredPct is the share of judged features being dropped. A large value is
	// itself a warning: it more likely means the LABEL is broken than that every
	// feature died at once.
	RetiredPct float64 `json:"retiredPct"`
	Note       string  `json:"note"`
}

// MaxRetirePct is the share of features above which a wholesale retirement is
// treated as suspect and NOT applied.
//
// If more than half the vector grades out at once, the likeliest explanation is
// that the forward-return label feeding every IC is broken — a resolution bug, a
// timestamp misalignment — not that every input independently died. Emptying the
// vector on that basis would turn one upstream defect into a total outage, so the
// pass reports the verdicts and keeps everything.
const MaxRetirePct = 0.5

// Analyze grades every feature and returns the resulting keep/retire sets.
func Analyze(ins []Inputs) Report {
	r := Report{}
	judged := 0
	retired := 0
	for _, in := range ins {
		s := Grade(in)
		r.Scores = append(r.Scores, s)
		if s.Verdict != VerdictProvisional {
			judged++
		}
		if !s.Keep {
			retired++
		}
	}
	if judged > 0 {
		r.RetiredPct = float64(retired) / float64(judged)
	}

	if r.RetiredPct > MaxRetirePct {
		// Report, do not act.
		for i := range r.Scores {
			r.Keep = append(r.Keep, r.Scores[i].Name)
		}
		sort.Strings(r.Keep)
		r.Note = fmt.Sprintf(
			"%.0f%% of judged features graded out at once, past the %.0f%% ceiling — retirement NOT applied. A whole vector dying together points at the forward-return label, not at every feature independently; the verdicts stand as a report until that is ruled out.",
			r.RetiredPct*100, MaxRetirePct*100)
		return r
	}

	for _, s := range r.Scores {
		if s.Keep {
			r.Keep = append(r.Keep, s.Name)
		} else {
			r.Retire = append(r.Retire, s.Name)
		}
	}
	sort.Strings(r.Keep)
	sort.Strings(r.Retire)
	r.Note = "per-feature decay/stability grading; surviving here is NOT evidence a feature predicts anything — that remains the out-of-sample lift gate's job"
	return r
}

func clamp01(v float64) float64 {
	if math.IsNaN(v) {
		return 0
	}
	return math.Max(0, math.Min(1, v))
}
