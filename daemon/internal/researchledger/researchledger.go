// Package researchledger is SignalDeck's BAYESIAN RESEARCH LEDGER — the layer
// that turns individual experiments into evolving, evidence-weighted beliefs.
//
// # Why it exists
//
// The Research Lab (internal/researchlab) generates and grades micro-hypotheses
// automatically, but its verdicts are binary (shadow/promoted/rejected). Real
// research beliefs are not binary: a discovery accrues evidence FOR and AGAINST,
// gets replicated or contradicted on fresh data, survives or fails adversarial
// attacks, and its credibility should move smoothly between those events. This
// package models exactly that:
//
//	prior ──▶ evidence (Bayes factor each) ──▶ posterior ──▶ status band
//
// Every experiment, replication, and self-attack is one Evidence row carrying a
// Bayes factor; the posterior is recomputed from scratch over the full evidence
// chain (deterministic, auditable — no hidden state).
//
// # The Bayes factor (one-sided beta-binomial, plausible-edge prior)
//
// An experiment observes k directional wins in n independent trials against a
// naive baseline p0. We compare:
//
//	H0: p = p0                  (no edge)
//	H1: p ~ Uniform(p0, p0+maxEdge)   (an edge of plausible size)
//
// The bounded band matters: a diffuse prior over (p0,1) puts most of its mass on
// absurd edges (70%+ accuracy in liquid markets), which makes the marginal
// likelihood tiny and auto-rejects every REAL small edge — the Jeffreys–Lindley
// trap. maxEdge encodes "directional edges in liquid markets live within a few
// points of naive" (default 0.10; conditioned/rare-trigger hypotheses may
// justify a wider band, stated per hypothesis).
//
// BF = [ ∫_{p0}^{p0+maxEdge} p^k (1-p)^{n-k} dp / maxEdge ] / [ p0^k (1-p0)^{n-k} ]
//
// computed exactly in log space via the regularized incomplete beta function.
// Data below p0 ⇒ BF < 1 (evidence against); data above ⇒ BF > 1.
//
// # Honesty caps (model uncertainty dominates)
//
//   - Each evidence item's BF is clamped to [1/20, 20]: past "strong evidence",
//     the probability that the EXPERIMENT itself is flawed (leakage, regime
//     luck, survivorship) exceeds the statistical residual, so no single result
//     — however large its n — can push a belief to near-certainty alone.
//   - The posterior is clamped to [0.02, 0.97]: the ledger never claims
//     certainty in either direction.
//   - Status "supported" requires, ON TOP of posterior ≥ 0.85, at least
//     MinReplications independent data-window replications AND coverage of at
//     least MinRegimes volatility regimes. A single-regime discovery caps at
//     "tentative" no matter how strong its number is.
package researchledger

import (
	"math"
	"sort"
)

// Evidence kinds.
const (
	KindExperiment  = "experiment"  // first grade of a hypothesis on a data window
	KindReplication = "replication" // re-grade on a NEW, disjoint data window
	KindAttack      = "attack"      // adversarial check (failed ⇒ BF<1 penalty)
	KindManual      = "manual"      // transcribed from a human-run report (note!)
	// KindBacktest is a disjoint HISTORICAL-window grade from the backfill
	// engine: independent data the discovery never saw, but survivor-universe
	// backtest, not a live forward record.
	KindBacktest = "backtest"
)

// Status bands (derived, never stored as the source of truth).
const (
	StatusRejected  = "rejected"  // posterior < 0.10
	StatusDoubtful  = "doubtful"  // < 0.35
	StatusUncertain = "uncertain" // < 0.60
	StatusTentative = "tentative" // < 0.85, or gates unmet
	StatusSupported = "supported" // >= 0.85 AND replication+regime gates met
)

// Gates for StatusSupported beyond the posterior itself.
const (
	MinReplications = 2 // independent data-window grades (experiment+replications)
	MinRegimes      = 2 // distinct volatility regimes covered by the evidence
)

// Bayes-factor and posterior clamps (honesty caps — see package doc).
const (
	MaxBF        = 20.0
	MinBF        = 1.0 / 20.0
	MaxPosterior = 0.97
	MinPosterior = 0.02
)

// DefaultMaxEdge is the plausible-edge band for an UNCONDITIONED directional
// hypothesis in liquid markets. Conditioned (rare-trigger) hypotheses may state
// a wider band on the Hypothesis.
const DefaultMaxEdge = 0.10

// WeekTrialMaxEdge is the plausible-edge band on the WEEK-TRIAL scale: when a
// grader scores one Bernoulli trial per calendar week (win = that week's
// cross-sectional win rate beats the week's own naive baseline), the null win
// probability is ≤0.5 (the folded baseline makes it conservative) and a real
// edge shows up as winning well more than half the weeks — so the band is far
// wider than a per-observation edge band.
const WeekTrialMaxEdge = 0.35

// DiscoveryMaxBF caps the FIRST (experiment) grade of an open hypothesis. The
// first grade typically re-measures the very data window the discovery was
// made on — the prior was formed looking at it, so letting it carry full
// evidence weight would be a disguised double-count. Fresh-window replications
// carry the full MaxBF cap.
const DiscoveryMaxBF = 5.0

// Hypothesis is one named research belief. Prior and MaxEdge are fixed at
// creation (changing a prior after seeing data is forbidden); Posterior/Status
// and the counters are DERIVED from the evidence chain via Recompute.
type Hypothesis struct {
	ID             string   `json:"id"`     // e.g. "H008"
	Family         string   `json:"family"` // signal family: momentum|meanrev|...
	Statement      string   `json:"statement"`
	Horizon        string   `json:"horizon"` // "" when not horizon-specific
	Prior          float64  `json:"prior"`
	MaxEdge        float64  `json:"maxEdge"`
	Posterior      float64  `json:"posterior"`
	Status         string   `json:"status"`
	Replications   int      `json:"replications"`
	Contradictions int      `json:"contradictions"`
	Regimes        int      `json:"regimes"`
	OpenQuestions  []string `json:"openQuestions"`
	// Decay tracking (derived, written by the research engine's decay sweep):
	// the highest posterior the chain ever reached and when, plus the newest
	// grade timestamp — a belief whose Current sits well below Peak is decaying,
	// one with a stale LastGradeTs has stopped being tested.
	PeakPosterior float64 `json:"peakPosterior"`
	PeakTs        int64   `json:"peakTs"`
	LastGradeTs   int64   `json:"lastGradeTs"`
	// Spec is the machine-gradable rule JSON for engine-graded hypotheses;
	// "" = prose-only.
	Spec string `json:"spec,omitempty"`
}

// Evidence is one entry in a hypothesis's chain. For experiment/replication
// rows K/N/P0 hold the graded binomial observation and BF is computed from
// them; attack and manual rows carry a stated BF with the reasoning in Note.
type Evidence struct {
	HypID string  `json:"hypId"`
	Ts    int64   `json:"ts"`
	Kind  string  `json:"kind"`
	K     int     `json:"k"`
	N     int     `json:"n"`
	P0    float64 `json:"p0"`
	BF    float64 `json:"bf"`
	Note  string  `json:"note"`
	// Data window the evidence graded (unix seconds); replications must be
	// disjoint from all prior windows — the caller enforces via cursor.
	WindowFrom int64 `json:"windowFrom"`
	WindowTo   int64 `json:"windowTo"`
}

// BayesFactorAbove grades H1 "p ~ U(p0, p0+maxEdge)" against H0 "p = p0" for k
// wins in n trials — the one-sided "this signal beats naive" comparison. The
// result is clamped to [MinBF, MaxBF].
//
// The band integral is evaluated for EVERY input — including k/n beyond the
// band, where the exact marginal keeps the answer n-aware (3/3 wins is mild
// evidence, 700/1000 is decisive). Only when the integral underflows entirely
// (huge n, data far outside the band) does the fallback decide by which SIDE
// of the band the data sit on.
func BayesFactorAbove(k, n int, p0, maxEdge float64) float64 {
	if n <= 0 || p0 <= 0 || p0 >= 1 || maxEdge <= 0 {
		return 1
	}
	hi := math.Min(1, p0+maxEdge)
	bf, ok := bfBandLoHi(k, n, p0, hi, p0)
	if !ok {
		// Underflow: decisively outside the band. Above it = capped evidence
		// for; below the null = floored evidence against.
		if float64(k)/float64(n) >= hi {
			return MaxBF
		}
		return MinBF
	}
	return clampBF(bf)
}

// BayesFactorBelow is the mirror: H1 "p ~ U(p0-maxEdge, p0)" vs H0 "p = p0" —
// for hypotheses that predict the rate is BELOW the baseline (e.g. "adding leg X
// makes the blend WORSE than the partner alone").
func BayesFactorBelow(k, n int, p0, maxEdge float64) float64 {
	if n <= 0 || p0 <= 0 || p0 >= 1 || maxEdge <= 0 {
		return 1
	}
	lo := math.Max(0, p0-maxEdge)
	bf, ok := bfBandLoHi(k, n, lo, p0, p0)
	if !ok {
		if float64(k)/float64(n) <= lo {
			return MaxBF
		}
		return MinBF
	}
	return clampBF(bf)
}

// bfBandLoHi computes marginal-likelihood(p ~ U(lo,hi)) / likelihood(p=null),
// exactly, in log space:
//
//	ln num = lnB(k+1, n-k+1) + ln[I_hi(k+1,n-k+1) - I_lo(k+1,n-k+1)] - ln(hi-lo)
//	ln den = k ln(null) + (n-k) ln(1-null)
//
// ok=false when the band integral underflows entirely (the data's posterior
// mass sits decisively outside [lo,hi]) — the caller decides which side and
// returns the corresponding clamp. Evaluating the integral for EVERY input
// (rather than early-returning when k/n exits the band) keeps small samples
// n-aware: 3/3 wins beyond the band is mild evidence (~1.3), not the cap.
func bfBandLoHi(k, n int, lo, hi, null float64) (float64, bool) {
	a, b := float64(k+1), float64(n-k+1)
	dI := regIncBeta(hi, a, b) - regIncBeta(lo, a, b)
	if dI < 1e-300 {
		return 0, false
	}
	lnNum := lbeta(a, b) + math.Log(dI) - math.Log(hi-lo)
	lnDen := float64(k)*math.Log(null) + float64(n-k)*math.Log(1-null)
	return math.Exp(lnNum - lnDen), true
}

// Posterior recomputes a hypothesis's posterior from its full evidence chain:
// posterior odds = prior odds × ∏ BF, clamped. Deterministic and auditable —
// the chain IS the belief. Callers that maintain attack evidence should pass
// the chain through EffectiveChain first so static concerns are not re-levied
// per grade.
func Posterior(prior float64, evidence []Evidence) float64 {
	if prior <= 0 || math.IsNaN(prior) {
		prior = MinPosterior
	}
	if prior >= 1 {
		prior = MaxPosterior
	}
	odds := prior / (1 - prior)
	for _, e := range evidence {
		bf := e.BF
		if bf <= 0 || math.IsNaN(bf) {
			continue // malformed row contributes nothing rather than -Inf/NaN
		}
		odds *= clampBF(bf)
		// Running cap: a long chain of capped BFs must saturate, not overflow
		// to +Inf (whose posterior would be NaN). 1e12 is already far past the
		// posterior clamp.
		if odds > 1e12 {
			odds = 1e12
		}
	}
	p := odds / (1 + odds)
	if math.IsNaN(p) {
		return MinPosterior // defensive: never let a NaN reach Status
	}
	return math.Min(MaxPosterior, math.Max(MinPosterior, p))
}

// Counters derives (replications, contradictions) from an evidence chain.
// Replications count KindReplication and KindBacktest rows: the first
// (experiment) grade of an open hypothesis typically re-measures the discovery
// window the prior was formed on, so it must not satisfy the independence gate
// — only fresh disjoint-window grades do, and backtest rows ARE disjoint
// historical-window grades (the API distinguishes live vs backtest separately).
// Contradictions count experiment, replication, AND backtest rows whose BF
// argued against (< 1); malformed BF<=0 rows are skipped exactly as Posterior
// skips them.
func Counters(evidence []Evidence) (replications, contradictions int) {
	for _, e := range evidence {
		if e.BF <= 0 || math.IsNaN(e.BF) {
			continue
		}
		switch e.Kind {
		case KindReplication, KindBacktest:
			replications++
			if e.BF < 1 {
				contradictions++
			}
		case KindExperiment:
			if e.BF < 1 {
				contradictions++
			}
		}
	}
	return
}

// staticAttacks are the attack names describing the WHOLE data history rather
// than one grade window — EffectiveChain dedupes each to its latest row.
var staticAttacks = map[string]bool{
	"single-regime": true,
	"survivorship":  true,
}

// EffectiveChain filters an evidence chain for posterior computation: per-grade
// attacks (time-split, suspicious-edge) qualify THEIR window's experiment and
// all pass through, but STATIC-STATE attacks — "single-regime" and
// "survivorship", facts about the whole data history rather than one window —
// are each deduplicated to their LATEST row, so an unchanged concern is levied
// once, not once per grade. (Re-levying a static 0.6 per replication would
// make three calm-regime replications of a TRUE edge drift the posterior down
// — penalty compounding the house doctrine does not intend.)
func EffectiveChain(evidence []Evidence) []Evidence {
	last := map[string]int{}
	for i, e := range evidence {
		if e.Kind == KindAttack {
			if name := attackName(e.Note); staticAttacks[name] {
				last[name] = i
			}
		}
	}
	if len(last) == 0 {
		return evidence
	}
	out := make([]Evidence, 0, len(evidence))
	for i, e := range evidence {
		if e.Kind == KindAttack {
			if name := attackName(e.Note); staticAttacks[name] && i != last[name] {
				continue
			}
		}
		out = append(out, e)
	}
	return out
}

// attackName extracts an attack's name (the note prefix before the first ':').
func attackName(note string) string {
	for i := 0; i < len(note); i++ {
		if note[i] == ':' {
			return note[:i]
		}
	}
	return note
}

// Status derives the band from the posterior plus the hard supported-gates.
func Status(posterior float64, replications, regimes int) string {
	if math.IsNaN(posterior) {
		return StatusUncertain // a malformed posterior must never read as supported
	}
	switch {
	case posterior < 0.10:
		return StatusRejected
	case posterior < 0.35:
		return StatusDoubtful
	case posterior < 0.60:
		return StatusUncertain
	case posterior < 0.85:
		return StatusTentative
	}
	if replications < MinReplications || regimes < MinRegimes {
		return StatusTentative // strong number, unmet independence gates
	}
	return StatusSupported
}

// ── Meta-analysis: the engine learning about RESEARCH, not markets ──

// FamilyStat aggregates the ledger per signal family.
type FamilyStat struct {
	Family       string  `json:"family"`
	N            int     `json:"n"`
	AvgPosterior float64 `json:"avgPosterior"`
	Supported    int     `json:"supported"`
	Tentative    int     `json:"tentative"`
	Rejected     int     `json:"rejected"`
}

// Meta computes per-family statistics over the current hypothesis set, sorted
// by descending average posterior — "which signal families keep surviving?".
func Meta(hyps []Hypothesis) []FamilyStat {
	byFam := map[string]*FamilyStat{}
	for _, h := range hyps {
		fs, ok := byFam[h.Family]
		if !ok {
			fs = &FamilyStat{Family: h.Family}
			byFam[h.Family] = fs
		}
		fs.N++
		fs.AvgPosterior += h.Posterior
		switch h.Status {
		case StatusSupported:
			fs.Supported++
		case StatusTentative:
			fs.Tentative++
		case StatusRejected:
			fs.Rejected++
		}
	}
	out := make([]FamilyStat, 0, len(byFam))
	for _, fs := range byFam {
		fs.AvgPosterior /= float64(fs.N)
		out = append(out, *fs)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].AvgPosterior != out[j].AvgPosterior {
			return out[i].AvgPosterior > out[j].AvgPosterior
		}
		return out[i].Family < out[j].Family
	})
	return out
}

// AttackStat counts how often each named attack argued against a hypothesis —
// "which failure mode kills the most research?".
type AttackStat struct {
	Attack string `json:"attack"`
	Failed int    `json:"failed"`
	Run    int    `json:"run"`
}

// AttackLethality aggregates attack evidence by note prefix (the attack name up
// to the first ':'), most lethal first.
func AttackLethality(evidence []Evidence) []AttackStat {
	agg := map[string]*AttackStat{}
	for _, e := range evidence {
		if e.Kind != KindAttack {
			continue
		}
		name := e.Note
		for i := 0; i < len(name); i++ {
			if name[i] == ':' {
				name = name[:i]
				break
			}
		}
		as, ok := agg[name]
		if !ok {
			as = &AttackStat{Attack: name}
			agg[name] = as
		}
		as.Run++
		if e.BF < 1 {
			as.Failed++
		}
	}
	out := make([]AttackStat, 0, len(agg))
	for _, as := range agg {
		out = append(out, *as)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Failed != out[j].Failed {
			return out[i].Failed > out[j].Failed
		}
		return out[i].Attack < out[j].Attack
	})
	return out
}

// ── Attack penalties (documented judgment priors, not fitted numbers) ──

// Attack BF penalties: a failed attack enters the evidence chain as a BF<1 row.
// These are stated judgment priors — the honest alternative to silently
// ignoring known weaknesses — and are deliberately mild compared to data
// evidence (data can still overwhelm them with enough replication).
const (
	// PenaltySingleRegime: every observation sits in one volatility regime; the
	// discovery has never been stress-tested.
	PenaltySingleRegime = 0.6
	// PenaltyTimeSplitFail: the edge's SIGN flips between the sample's halves —
	// a strong hint of a lucky period.
	PenaltyTimeSplitFail = 0.4
	// PenaltySuspiciousEdge: the observed edge exceeds the hypothesis's own
	// plausible band — more likely leakage or a grading bug than genuine skill.
	PenaltySuspiciousEdge = 0.2
	// PenaltySurvivorship: the backfilled universe is today's survivor set —
	// delisted losers are absent, so historical grades lean optimistic.
	PenaltySurvivorship = 0.65
	// PenaltyFragileThreshold: perturbing the rule's thresholds ±10% destroys
	// most of the edge — a knife-edge fit, not a robust phenomenon.
	PenaltyFragileThreshold = 0.5
	// PenaltyNoIncrementalValue: the full rule fails to beat its own ablations
	// and baselines — the added conditions carry no incremental value.
	PenaltyNoIncrementalValue = 0.3
)

func clampBF(bf float64) float64 {
	return math.Min(MaxBF, math.Max(MinBF, bf))
}

// ── numerics: log-beta + regularized incomplete beta (pure Go, no deps) ──

func lbeta(a, b float64) float64 {
	la, _ := math.Lgamma(a)
	lb, _ := math.Lgamma(b)
	lab, _ := math.Lgamma(a + b)
	return la + lb - lab
}

// regIncBeta returns I_x(a,b), the regularized incomplete beta function,
// via the standard continued-fraction expansion (Lentz's algorithm), using the
// symmetry transform for the fast-converging side.
func regIncBeta(x, a, b float64) float64 {
	if x <= 0 {
		return 0
	}
	if x >= 1 {
		return 1
	}
	lbt := a*math.Log(x) + b*math.Log(1-x) - lbeta(a, b)
	bt := math.Exp(lbt)
	if x < (a+1)/(a+b+2) {
		return bt * betacf(x, a, b) / a
	}
	return 1 - bt*betacf(1-x, b, a)/b
}

// betacf evaluates the continued fraction for the incomplete beta function
// (Numerical Recipes 6.4, modified Lentz).
func betacf(x, a, b float64) float64 {
	const (
		maxIter = 300
		eps     = 3e-14
		fpmin   = 1e-300
	)
	qab, qap, qam := a+b, a+1, a-1
	c := 1.0
	d := 1 - qab*x/qap
	if math.Abs(d) < fpmin {
		d = fpmin
	}
	d = 1 / d
	h := d
	for m := 1; m <= maxIter; m++ {
		m2 := float64(2 * m)
		mf := float64(m)
		aa := mf * (b - mf) * x / ((qam + m2) * (a + m2))
		d = 1 + aa*d
		if math.Abs(d) < fpmin {
			d = fpmin
		}
		c = 1 + aa/c
		if math.Abs(c) < fpmin {
			c = fpmin
		}
		d = 1 / d
		h *= d * c
		aa = -(a + mf) * (qab + mf) * x / ((a + m2) * (qap + m2))
		d = 1 + aa*d
		if math.Abs(d) < fpmin {
			d = fpmin
		}
		c = 1 + aa/c
		if math.Abs(c) < fpmin {
			c = fpmin
		}
		d = 1 / d
		del := d * c
		h *= del
		if math.Abs(del-1) < eps {
			break
		}
	}
	return h
}
