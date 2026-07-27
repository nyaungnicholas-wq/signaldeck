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
//   - An ASSERTED row (n=0 — no observation the integral above could grade)
//     may withhold belief but never manufacture it. See AssertedMaxBF.
package researchledger

import (
	"encoding/json"
	"fmt"
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
	// KindEconomic grades the hypothesis's TRADABLE FORM net of costs — the
	// position that would have to make the money, not the statistic. See the
	// tradability gate below for why this is a distinct kind.
	KindEconomic = "economic"
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

// ── The tradability gate ─────────────────────────────────────────────────
//
// A hypothesis may not reach StatusSupported on a statistic alone, however well
// estimated. It must first STATE the position that would have to earn the money
// (TradableForm) and have that position graded net of costs (EconomicTest).
//
// This gate exists because the ledger produced two independent demonstrations of
// the same failure, eight days apart, and neither was catchable by collecting
// more data:
//
//   - H018 (63d SPY-correlation regime persists) sat at 73.1% with a tight
//     out-of-sample interval for over a week. Its tradable form — cointegration
//     pairs trading — turned out to be indistinguishable from picking pairs at
//     random inside the same sector, because the quantity that persists is
//     shared market beta and a dollar-neutral spread is built precisely to
//     cancel it. The statistic was correct. It was also empty.
//   - The 1-session news-sentiment IC reached an interval excluding zero even
//     after a Bonferroni correction, while its cost-net aligned quintile book
//     returned NEGATIVE. Detectable and unprofitable at once.
//
// In both cases the tradable form settled in one run what no amount of further
// sampling would have settled, because the missing thing was never sample size.
// A hypothesis without a stated position is not yet a claim about markets — it
// is a claim about a number.
//
// EconomicTest holds a description or tag of the run that graded the position
// (for H018: the 2026-07-25 pairs test). Whether that run PASSED is deliberately
// not part of the gate — a failing economic test enters the chain as ordinary
// evidence with BF < 1 and moves the posterior down on its own. Counting it
// twice would double-punish it.

// EconomicGrades counts evidence rows that graded a hypothesis's TRADABLE FORM
// rather than its statistic. Cost-aware backtests of the actual position enter
// the chain under this kind.
func EconomicGrades(evidence []Evidence) int {
	n := 0
	for _, e := range evidence {
		if e.Kind == KindEconomic {
			n++
		}
	}
	return n
}

// ── The machine-evidence floor ───────────────────────────────────────────
//
// A posterior is a summary of evidence, and nothing in the arithmetic records
// WHO produced that evidence. A 0.95 derived from one hand-typed `manual`
// assertion renders identically to a 0.95 derived from thirty machine grades on
// disjoint windows. On 2026-07-27 that was the ledger's actual state: H005,
// H011, H012 and H015–H018 sat at 0.896–0.97 on one or two transcribed rows
// each, while the only hypotheses that ever met a grader (H002, H002-R1, H008,
// with 20–30 backtest and attack rows) were rejected at 0.02. The band was
// reading as a claim about markets when it was a claim about typing.
//
// So a hypothesis whose chain contains ZERO machine-produced rows may not
// report a band above "uncertain", whatever its posterior. This can only lower
// a status, never raise one: it does not touch the posterior, and manual rows
// keep their full downward weight — a hand-entered attack still demotes.
//
// MachineKinds are the evidence kinds a grader produces. Everything except
// KindManual (which is, by definition, transcribed by a human) qualifies.
var MachineKinds = map[string]bool{
	KindExperiment:  true,
	KindReplication: true,
	KindAttack:      true,
	KindBacktest:    true,
	KindEconomic:    true,
}

// MachineGrades counts evidence rows produced by a grader rather than typed in
// by hand. Zero means the hypothesis has never been machine-tested.
func MachineGrades(evidence []Evidence) int {
	n := 0
	for _, e := range evidence {
		if MachineKinds[e.Kind] {
			n++
		}
	}
	return n
}

// EvidenceMix counts an evidence chain by kind, so the UI can render WHAT a
// posterior is made of next to the posterior itself.
func EvidenceMix(evidence []Evidence) map[string]int {
	mix := map[string]int{}
	for _, e := range evidence {
		mix[e.Kind]++
	}
	return mix
}

// Gates bundles the hard supported-gates beyond the posterior. The zero value
// is the honest default for a hypothesis nobody has tried to trade and nothing
// has ever graded: no machine evidence, no replications, no regimes, no stated
// position.
type Gates struct {
	// MachineGrades is the number of grader-produced rows in the chain; 0
	// caps the band at StatusUncertain regardless of posterior.
	MachineGrades int
	Replications  int
	Regimes       int
	// TradableForm is the position that would have to make the money —
	// "" means the hypothesis has never been stated as a trade.
	TradableForm string
	// EconomicTest names the run that graded that position net of costs;
	// "" means the position was stated but never tested.
	EconomicTest string
}

// UnmetGate returns the first supported-gate this hypothesis fails, in plain
// words, or "" when every gate is met. The page shows this instead of leaving a
// high posterior looking arbitrarily withheld.
func (g Gates) UnmetGate() string {
	switch {
	case g.MachineGrades == 0:
		return "no machine-graded evidence — the posterior rests on hand-entered rows only"
	case g.Replications < MinReplications:
		return "needs independent replication on a fresh data window"
	case g.Regimes < MinRegimes:
		return "measured in only one volatility regime"
	case g.TradableForm == "":
		return "no tradable form stated — the position that would earn the money is unspecified"
	case g.EconomicTest == "":
		return "tradable form stated but never graded net of costs"
	}
	return ""
}

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
	// TradableForm is the position that would have to earn the money if this
	// belief is true, and EconomicTest names the run that graded that position
	// net of costs. Both are required before a hypothesis may reach
	// StatusSupported — see the tradability gate.
	TradableForm string `json:"tradableForm,omitempty"`
	EconomicTest string `json:"economicTest,omitempty"`
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

// ── Asserted evidence: the hand-entered Bayes factor ─────────────────────
//
// A row with N == 0 carries no graded observation, so its Bayes factor was
// DECLARED — nothing above computed it and nothing can re-derive it. Two
// legitimate row shapes are of that kind (attack penalties and transcribed
// judgments), and one illegitimate one was found in the live ledger on
// 2026-07-25: of 86 evidence rows, 58 carried n=0, and two published posteriors
// were the product of a single typed number each —
//
//	H006 (0.50 prior → published 0.75, "tentative") on one manual row at BF=3;
//	H003 (0.50 prior → published 0.048, "REJECTED") on one manual row at
//	exactly MinBF, the floor of the honesty clamp.
//
// AssertedMaxBF is the correction, and its asymmetry is the whole point: an
// assertion may WITHHOLD belief but may never MANUFACTURE it. A declared
// penalty states a known weakness and moves the posterior conservatively — that
// is the honest alternative to ignoring the weakness, and the entire attack
// battery depends on it, so penalties keep full force down to MinBF. A declared
// BF > 1 is positive evidence claimed on zero trials; there is no n for it, no
// integral behind it, and no way to audit it. It gets 1.
//
// This never deletes a row. The declared number is stored, marshaled and
// reported verbatim (see Evidence.MarshalJSON and Breakdown); only the weight
// it carries into the posterior is refused.
const AssertedMaxBF = 1.0

// Band sides, for reproducing a stored BF from its own observation.
const (
	SideAbove = "above" // BayesFactorAbove: the hypothesis predicts a rate above p0
	SideBelow = "below" // BayesFactorBelow: it predicts a rate below p0
)

// DataBacked reports whether this row's Bayes factor came from a graded
// binomial observation the beta-binomial integral could produce, rather than
// from a human. N is the discriminator because it is the only field that
// cannot be true of an assertion: zero trials, zero evidence.
func (e Evidence) DataBacked() bool { return e.N > 0 }

// AppliedBF is the Bayes factor the posterior actually uses for this row: the
// stored value re-clamped to [MinBF, MaxBF], and additionally capped at
// AssertedMaxBF when the row carries no observation. A malformed BF reports 1
// because that is what Posterior does with it — skips it, leaving the odds
// unchanged — and a payload that said otherwise would misdescribe the chain.
func (e Evidence) AppliedBF() float64 {
	if e.BF <= 0 || math.IsNaN(e.BF) {
		return 1
	}
	bf := clampBF(e.BF)
	if !e.DataBacked() && bf > AssertedMaxBF {
		return AssertedMaxBF
	}
	return bf
}

// MarshalJSON emits the stored row verbatim plus two derived fields, because
// /api/research-ledger marshals []Evidence straight into the payload: without
// them a reader cannot tell a measurement from an assertion, which is the
// condition that let a typed 3 become a published 0.75.
func (e Evidence) MarshalJSON() ([]byte, error) {
	type row Evidence // shed MarshalJSON to avoid infinite recursion
	return json.Marshal(struct {
		row
		Asserted  bool    `json:"asserted"`
		AppliedBF float64 `json:"appliedBf"`
	}{row(e), !e.DataBacked(), e.AppliedBF()})
}

// ReproducesBF re-derives a data-backed row's Bayes factor from its own
// (K, N, P0) under maxEdge and reports which band side matches, or ok=false if
// neither does. A row whose stored BF matches neither integral is a number
// somebody typed into a field documented as computed.
//
// maxEdge and the band side are NOT stored on the row, which is why this takes
// the band as an argument and why the check did not exist before: a stored BF
// was unverifiable by construction. Verified over the live DB on 2026-07-25 all
// 28 data-backed rows reproduced (grade rows under WeekTrialMaxEdge, transcribed
// rows under their hypothesis's own MaxEdge) — the audit passes today, and now
// it is an audit rather than an assumption.
func ReproducesBF(e Evidence, maxEdge float64) (side string, ok bool) {
	if !e.DataBacked() {
		return "", false
	}
	const tol = 1e-9
	null := storedNull(e)
	if above, ok := BayesFactorAbove(e.K, e.N, null, maxEdge); ok &&
		math.Abs(above-e.BF) <= tol*math.Max(1, e.BF) {
		return SideAbove, true
	}
	if below, ok := BayesFactorBelow(e.K, e.N, null, maxEdge); ok &&
		math.Abs(below-e.BF) <= tol*math.Max(1, e.BF) {
		return SideBelow, true
	}
	return "", false
}

// PosteriorParts separates what the data proved from what a human declared, so
// a refused assertion is disclosed rather than silently absorbed.
type PosteriorParts struct {
	// Posterior is the headline: data-backed BFs plus asserted PENALTIES.
	Posterior float64 `json:"posterior"`
	// DataOnly drops every asserted row, penalties included — the belief the
	// observations alone support.
	DataOnly float64 `json:"dataOnly"`
	// Declared is what the chain would read if every stored BF were taken at
	// face value. It is the number the ledger used to publish.
	Declared     float64 `json:"declared"`
	DataRows     int     `json:"dataRows"`
	AssertedRows int     `json:"assertedRows"`
	// RefusedRows counts asserted rows that argued FOR the hypothesis and were
	// capped to AssertedMaxBF.
	RefusedRows int `json:"refusedRows"`
	// Note states the refusal in words; "" when nothing was refused, because a
	// caveat printed on every hypothesis is a caveat nobody reads.
	Note string `json:"note,omitempty"`
}

// Breakdown computes the three posteriors side by side over one evidence chain.
// Pass the chain through EffectiveChain first if the caller maintains attack
// evidence — Breakdown does not dedupe, exactly like Posterior.
func Breakdown(prior float64, evidence []Evidence) PosteriorParts {
	p := PosteriorParts{}
	data := make([]Evidence, 0, len(evidence))
	for _, e := range evidence {
		if e.DataBacked() {
			p.DataRows++
			data = append(data, e)
			continue
		}
		p.AssertedRows++
		if clampBF(e.BF) > AssertedMaxBF {
			p.RefusedRows++
		}
	}
	p.Posterior = Posterior(prior, evidence)
	p.DataOnly = Posterior(prior, data)
	// Declared takes every stored BF at face value — the arithmetic the ledger
	// published before assertions were made second-class.
	p.Declared = chainPosterior(prior, evidence, func(e Evidence) float64 { return clampBF(e.BF) })
	if p.RefusedRows > 0 {
		p.Note = "posterior excludes the FOR-weight of asserted evidence: " +
			"rows with n=0 carry no observation the beta-binomial integral can grade, " +
			"so they may argue against a belief but not for it"
	}
	return p
}

// MeasuredNull is the no-skill rate a Bayes factor is graded against, together
// with the number of trials that rate was itself measured over. It exists so a
// null cannot be a literal: the fields are unexported, so the only ways to
// obtain one are the constructors below, each of which demands a sample.
//
// The motivating defect: the week-trial no-skill rate is NOT 0.5 (a week is
// won only by STRICTLY beating that week's own folded majority
// max(upRate,1-upRate)), yet several grading sites passed the bare 0.5 anyway
// while researchx.Discover measured the same statistic on the matched null
// arm. Two paths in one repository judged the same statistic against two
// different nulls. An unmeasured null is now unrepresentable rather than
// merely discouraged.
type MeasuredNull struct {
	p0     float64
	trials int    // observations/weeks the null rate was measured over
	source string // where the rate came from, for the evidence note
}

// P0 is the measured no-skill rate; Trials is its sample size; Source names
// its provenance. Measured reports whether the null has a sample at all — a
// null with zero trials is not a null, and grading against it must not happen.
func (m MeasuredNull) P0() float64    { return m.p0 }
func (m MeasuredNull) Trials() int    { return m.trials }
func (m MeasuredNull) Source() string { return m.source }
func (m MeasuredNull) Measured() bool { return m.trials > 0 && m.p0 > 0 && m.p0 < 1 }
func (m MeasuredNull) String() string {
	return fmt.Sprintf("%.4f (measured over %d %s trials)", m.p0, m.trials, m.source)
}

// NullFromArm builds the null from a GRADED arm — winTrials wins out of
// trials, e.g. researchx CFReport.NullMatched (the same matched observations
// with direction randomized). The rate is floored at 0.5 so substituting a
// measured null for the old 0.5 literal can only make a hypothesis harder to
// support, never easier: every posterior can fall or stay, never rise.
//
// An arm with zero trials yields a non-Measured null, and BayesFactorAbove
// then returns ok=false — the caller must write no evidence row rather than
// fall back to an assumption.
func NullFromArm(winTrials, trials int, source string) MeasuredNull {
	if trials <= 0 {
		return MeasuredNull{source: source}
	}
	return MeasuredNull{
		p0:     math.Max(0.5, float64(winTrials)/float64(trials)),
		trials: trials,
		source: source,
	}
}

// TranscribedNull carries a null that an external, cited measurement produced
// over `trials` observations (the seeded/manual chapters). It still requires a
// sample size — a rate with no observations behind it is refused here too.
func TranscribedNull(p0 float64, trials int, source string) MeasuredNull {
	if trials <= 0 {
		return MeasuredNull{source: source}
	}
	return MeasuredNull{p0: p0, trials: trials, source: source}
}

// storedNull reconstructs the null of an already-written row for the
// reproduction audit. Package-private on purpose: it is the one place a rate
// legitimately arrives without a fresh measurement, because it is re-deriving
// a number that was measured when the row was written.
func storedNull(e Evidence) MeasuredNull {
	return MeasuredNull{p0: e.P0, trials: e.N, source: "stored-row"}
}

// BayesFactorAbove grades H1 "p ~ U(p0, p0+maxEdge)" against H0 "p = null.P0()"
// for k wins in n trials — the one-sided "this signal beats naive" comparison.
// The result is clamped to [MinBF, MaxBF]. ok=false means the null was never
// measured; there is then no Bayes factor and the caller must write no row.
//
// The band integral is evaluated for EVERY input — including k/n beyond the
// band, where the exact marginal keeps the answer n-aware (3/3 wins is mild
// evidence, 700/1000 is decisive). Only when the integral underflows entirely
// (huge n, data far outside the band) does the fallback decide by which SIDE
// of the band the data sit on.
func BayesFactorAbove(k, n int, null MeasuredNull, maxEdge float64) (float64, bool) {
	if !null.Measured() {
		return 0, false
	}
	p0 := null.p0
	if n <= 0 || maxEdge <= 0 {
		return 1, true
	}
	hi := math.Min(1, p0+maxEdge)
	bf, ok := bfBandLoHi(k, n, p0, hi, p0)
	if !ok {
		// Underflow: decisively outside the band. Above it = capped evidence
		// for; below the null = floored evidence against.
		if float64(k)/float64(n) >= hi {
			return MaxBF, true
		}
		return MinBF, true
	}
	return clampBF(bf), true
}

// BayesFactorBelow is the mirror: H1 "p ~ U(p0-maxEdge, p0)" vs H0 "p = p0" —
// for hypotheses that predict the rate is BELOW the baseline (e.g. "adding leg X
// makes the blend WORSE than the partner alone").
func BayesFactorBelow(k, n int, null MeasuredNull, maxEdge float64) (float64, bool) {
	if !null.Measured() {
		return 0, false
	}
	p0 := null.p0
	if n <= 0 || maxEdge <= 0 {
		return 1, true
	}
	lo := math.Max(0, p0-maxEdge)
	bf, ok := bfBandLoHi(k, n, lo, p0, p0)
	if !ok {
		if float64(k)/float64(n) <= lo {
			return MaxBF, true
		}
		return MinBF, true
	}
	return clampBF(bf), true
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
	// AppliedBF, not the stored BF: an asserted row (n=0) may argue against a
	// belief at full force but may not argue for it. See AssertedMaxBF.
	return chainPosterior(prior, evidence, Evidence.AppliedBF)
}

// chainPosterior multiplies a chain's Bayes factors into the prior odds, taking
// each row's weight from bfOf so the honest posterior and the as-declared one
// share the same arithmetic and can only differ where intended.
func chainPosterior(prior float64, evidence []Evidence, bfOf func(Evidence) float64) float64 {
	odds := prior / (1 - prior)
	for _, e := range evidence {
		if e.BF <= 0 || math.IsNaN(e.BF) {
			continue // malformed row contributes nothing rather than -Inf/NaN
		}
		odds *= bfOf(e)
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

// Status derives the band from the posterior plus the independence gates.
//
// It cannot return StatusSupported, because it has no way to know whether the
// hypothesis was ever stated as a position — see the tradability gate above.
// It also cannot exceed StatusUncertain, because a caller passing no Gates has
// no machine evidence to report — which is correct for its users: a prior with
// an empty chain is exactly the manual-only case the floor exists for.
// Seeding paths (prior only, no evidence) use it; every promotion path must use
// StatusWithGates, which is the only function that can promote.
func Status(posterior float64, replications, regimes int) string {
	return StatusWithGates(posterior, Gates{Replications: replications, Regimes: regimes})
}

// StatusWithGates derives the band from the posterior plus every hard
// supported-gate: independent replication, regime coverage, a stated tradable
// form, and an economic grade of that form.
func StatusWithGates(posterior float64, g Gates) string {
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
		if g.MachineGrades == 0 {
			return StatusUncertain // manual-only chain: see the machine-evidence floor
		}
		return StatusTentative
	}
	if g.MachineGrades == 0 {
		return StatusUncertain
	}
	if g.UnmetGate() != "" {
		return StatusTentative // strong number, unmet gate
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

// ── Ledger liveness: is this posterior still being TESTED? ──
//
// The band answers "how strong is the number"; it cannot answer "when was that
// number last put at risk". Those are different questions and the ledger had
// only one word for both. The state it exists to name was live:
// research_ledger_evidence held 55 attack + 15 backtest + 16 manual rows, every
// one dated 2026-07-16/17, and ZERO rows of kind experiment or replication — so
// no hypothesis had ever been graded on a fresh window, while five published
// posteriors of 0.95-0.97 under the word "tentative". "Tentative, posterior
// 0.952" and "seeded once, never re-tested" were the same sentence.
//
// This measures and refuses to describe. It moves no posterior, no threshold
// and no null; it can only make a hypothesis read WEAKER than its band does.
const (
	LivenessLive         = "live"
	LivenessUnreplicated = "UNREPLICATED"
	LivenessStale        = "STALE"
)

// LivenessUnreplicatedMin is the posterior at which never having been replicated
// stops being a stage of research and starts being a claim the chain does not
// support. Below it the band already reads weak; at or above it the number is
// the loudest thing on the surface and must carry how it was earned.
const LivenessUnreplicatedMin = 0.9

// LivenessStaleDays is how many days without ANY new evidence row, while the
// research corpus itself has moved on, mean the hypothesis stopped being graded
// rather than having nothing new to grade.
const LivenessStaleDays = 14

// LedgerLiveness is the verdict on a hypothesis's TESTING, beside the verdict
// on its truth.
type LedgerLiveness struct {
	State   string `json:"state"`
	Healthy bool   `json:"healthy"`
	Detail  string `json:"detail"`
}

// LivenessOf derives the testing verdict. replications/experiments are ROW
// counts of KindReplication / KindExperiment evidence (not the stored
// replications counter, which also credits backtest rows); evidenceAgeDays is
// the age of the newest evidence row of ANY kind, -1 when the chain is empty;
// corpusGrew says the research corpus holds data newer than that newest row —
// the precondition that makes silence diagnostic, exactly as corpus size is for
// LoopEngineHealth. With nothing new to grade, silence is honest.
//
// UNREPLICATED outranks STALE: a 0.95 that no fresh window ever touched is the
// stronger indictment, and reporting the milder one would understate it.
func LivenessOf(posterior float64, replications, experiments, evidenceAgeDays int, corpusGrew bool) LedgerLiveness {
	switch {
	case posterior >= LivenessUnreplicatedMin && replications == 0:
		return LedgerLiveness{State: LivenessUnreplicated, Detail: fmt.Sprintf(
			"posterior %.3f with ZERO replication rows (%d experiment rows) — this "+
				"number was never re-graded on a fresh, disjoint window, so it is an "+
				"initial claim, not a confirmed one",
			posterior, experiments)}
	case corpusGrew && evidenceAgeDays >= LivenessStaleDays:
		age := "never" // an empty chain on a corpus that has data is the extreme case
		if evidenceAgeDays >= 0 {
			age = fmt.Sprintf("%d days ago", evidenceAgeDays)
		}
		return LedgerLiveness{State: LivenessStale, Detail: fmt.Sprintf(
			"newest evidence of any kind is %s while the research corpus holds newer "+
				"data — this hypothesis stopped being graded, it did not run out of "+
				"data to grade", age)}
	}
	return LedgerLiveness{State: LivenessLive, Healthy: true, Detail: fmt.Sprintf(
		"%d replication and %d experiment rows; newest evidence %s",
		replications, experiments, agePhrase(evidenceAgeDays))}
}

func agePhrase(days int) string {
	if days < 0 {
		return "never"
	}
	return fmt.Sprintf("%d days old", days)
}

// Verdict is the single word a surface may render for a hypothesis. When
// testing is unhealthy it REPLACES the band: a hypothesis whose posterior was
// never replicated must not be renderable as "tentative", because "tentative"
// describes a number under test and this one is not.
func Verdict(status string, l LedgerLiveness) string {
	if !l.Healthy {
		return l.State
	}
	return status
}
