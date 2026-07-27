package researchx

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/nyaungnicholas-wq/signaldeck/internal/clusterstat"
)

// Candidate is one auto-discovered rule with its full judgment record.
type Candidate struct {
	ID          string // "AD-" + hex(sha1(canonical spec))[:8]
	Rule        Rule
	Desc        string
	Grade       WeekGrade
	CF          CFReport
	Survival    SurvivalReport
	WilsonLower float64 // lower bound at the corrected alpha below
	// NullP0 is the win rate the Wilson lower bound was actually compared
	// against: max(0.5, CF.NullMatched.WinRate). A week trial is scored a win
	// against that week's OWN folded majority max(upRate, 1-upRate), so the
	// no-skill week-win rate is not 0.5 and assuming it was understated the
	// bar. The null arm keeps the same matched observations and only
	// randomizes direction, so its realised week-win rate is the null this
	// rule faced. The max(0.5, …) floor makes the substitution strictly
	// one-directional: the bar can rise, never fall.
	NullP0 float64
	// NullWeeks is how many week-trials the null arm was measured over —
	// without it NullP0 is a number of unknown precision.
	NullWeeks int
	// Divisor is the Bonferroni divisor this candidate cleared. Recorded on the
	// row because "survived Bonferroni correction" is unauditable without the
	// number that was corrected for.
	Divisor  int
	Survives bool
	// HoldoutEra is the pre-registered final era the grid never graded on
	// ("" when no holdout was configured). The four gates above are all
	// computed on the in-sample corpus EXCLUDING this era.
	HoldoutEra string
	// HoldoutWeeks / HoldoutWilsonLower / HoldoutNullP0 record the blind
	// confirmation: the same Wilson-vs-measured-null test re-run on the
	// held-out era alone. A candidate that cleared every in-sample gate is
	// still rejected (RejectedBy=RejectHoldout) unless HoldoutWilsonLower
	// exceeds HoldoutNullP0 over at least MinHoldoutWeeks week-trials. The
	// numbers are ledgered on rejections too — "failed out of sample" is only
	// auditable with the bound it failed against.
	HoldoutWeeks       int
	HoldoutWilsonLower float64
	HoldoutNullP0      float64
	// RejectedBy names the gate that killed this rule ("" when it survived).
	// A search that returns only its winners is unauditable by construction:
	// without the rejections there is no way to tell a grid that found nothing
	// from a grid that was never run, and no way to detect the same rule being
	// re-tested until it passes.
	RejectedBy string
}

// Rejection gate names recorded in Candidate.RejectedBy.
const (
	RejectMinWeeks       = "min_weeks"
	RejectWilson         = "wilson_lower"
	RejectRegimeSurvival = "regime_survival"
	RejectFragile        = "fragile_threshold"
	RejectCounterfactual = "counterfactual"
	RejectHoldout        = "holdout"
)

// PreregHoldoutEra is the pre-registered blind era: the discovery grid never
// grades on it, and a candidate that clears every in-sample gate must clear
// the Wilson bound against its own measured null a SECOND time on this era
// alone before Survives can be true. It is a package constant, and frozen in
// the PREREGISTRATION.md hash chain, for the same reason MaxAlpha is: a
// holdout an operator can retarget after seeing the result is not a holdout.
//
// It matches histfeat.EraY2026 (2026-01-01..) by value rather than by import;
// researchx is the pure analysis core and takes no dependencies.
const PreregHoldoutEra = "y2026"

// MaxAlpha is the family-wise significance level the grid search runs at. It is
// a package CONSTANT and not a DiscoverConfig field: a false-discovery guardrail
// an operator can widen is the whole guardrail undone in one line, and raising
// alpha leaves no trace in the result.
const MaxAlpha = 0.05

// DiscoverConfig bounds the discovery grid and its false-discovery gates.
// Zero fields take the documented defaults.
type DiscoverConfig struct {
	MinWeekObs     int // default 10
	MinWeeks       int // default 30
	MinWeeksPerEra int // default 8
	MaxCandidates  int // hard cap on the grid, default 48
	// PriorSearches is how many times this grid has ALREADY been run over
	// (largely) this data — every prior night of a scheduled loop is another
	// look, and looks are what multiplicity corrects for. Zero is a CLAIM that
	// this is the first search ever conducted; a caller running on a schedule
	// that leaves it zero is correcting for one night's grid while taking many
	// nights' chances.
	PriorSearches int
	// HoldoutEra is the era name withheld from the grid entirely. Every
	// existing gate is computed on the observations NOT in this era, and a
	// candidate that clears them all must then clear the Wilson bound against
	// its own measured null on this era alone. Empty means no holdout, which
	// is the pre-2026-07-27 behaviour and is only correct for callers that are
	// not conducting a search (replays, tests). The scheduled loop passes
	// PreregHoldoutEra.
	HoldoutEra string
	// MinHoldoutWeeks is the minimum week-trials the held-out era must supply
	// before a confirmation means anything, default MinWeeksPerEra. Too few
	// and the candidate is rejected, not waved through: an unconfirmable rule
	// is not a confirmed one.
	MinHoldoutWeeks int
}

func (c DiscoverConfig) withDefaults() DiscoverConfig {
	if c.MinWeekObs <= 0 {
		c.MinWeekObs = 10
	}
	if c.MinWeeks <= 0 {
		c.MinWeeks = 30
	}
	if c.MinWeeksPerEra <= 0 {
		c.MinWeeksPerEra = 8
	}
	if c.MaxCandidates <= 0 {
		c.MaxCandidates = 48
	}
	if c.PriorSearches < 0 {
		c.PriorSearches = 0
	}
	if c.MinHoldoutWeeks <= 0 {
		c.MinHoldoutWeeks = c.MinWeeksPerEra
	}
	return c
}

// Divisor is the Bonferroni divisor: every rule in the grid, once per search
// conducted over this data including this one. It is derived, never supplied —
// widening the grid and re-running the search both RAISE it.
func (c DiscoverConfig) Divisor() int {
	d := c.withDefaults()
	n := len(discoverGrid(d.MaxCandidates)) * (1 + d.PriorSearches)
	if n < 1 {
		n = 1
	}
	return n
}

// CorrectedAlpha is the per-rule significance level actually applied.
func (c DiscoverConfig) CorrectedAlpha() float64 { return MaxAlpha / float64(c.Divisor()) }

// discoverAtoms is the fixed condition vocabulary. The first discoverAnchors
// entries (pressure_abs, ext_score) are the only atoms allowed to anchor a
// pair — the grid stays bounded and every pair asks "does X sharpen a
// pressure/extension setup?", not "do any two features correlate?".
var discoverAtoms = []Cond{
	{Key: "pressure_abs", Op: ">=", Val: 0.75, Pct: true},
	{Key: "pressure_abs", Op: ">=", Val: 0.90, Pct: true},
	{Key: "ext_score", Op: ">=", Val: 0.70},
	{Key: "rsi_pct", Op: ">=", Val: 0.80},
	{Key: "rsi_pct", Op: "<=", Val: 0.20},
	{Key: "vol_pct", Op: ">=", Val: 0.80},
	{Key: "vol_anomaly", Op: ">=", Val: 0.85, Pct: true},
	{Key: "price_accel", Op: ">=", Val: 0.85, Pct: true},
	{Key: "price_accel", Op: "<=", Val: 0.15, Pct: true},
	{Key: "consec_dir", Op: ">=", Val: 0.40},
	{Key: "consec_dir", Op: "<=", Val: -0.40},
	{Key: "vix_high_vol", Op: ">=", Val: 1},
}

const discoverAnchors = 3

var discoverCalls = []string{"inverse_pressure", "follow_pressure"}

// discoverGrid enumerates the bounded deterministic grid: every single atom ×
// call, then every distinct-key anchored pair × call, truncated at maxRules
// (singles enumerate first, so the cap trims the pair tail).
func discoverGrid(maxRules int) []Rule {
	var rules []Rule
	add := func(r Rule) bool {
		if len(rules) >= maxRules {
			return false
		}
		rules = append(rules, r)
		return true
	}
	for _, a := range discoverAtoms {
		for _, call := range discoverCalls {
			if !add(Rule{Conds: []Cond{a}, Call: call}) {
				return rules
			}
		}
	}
	for i := 0; i < discoverAnchors; i++ {
		for j := i + 1; j < len(discoverAtoms); j++ {
			if discoverAtoms[i].Key == discoverAtoms[j].Key {
				continue
			}
			for _, call := range discoverCalls {
				if !add(Rule{Conds: []Cond{discoverAtoms[i], discoverAtoms[j]}, Call: call}) {
					return rules
				}
			}
		}
	}
	return rules
}

// Discover runs the bounded deterministic grid over obs and returns EVERY
// judged candidate, sorted by ID — survivors carry Survives=true and an empty
// RejectedBy, rejections carry Survives=false and the name of the gate that
// killed them. Callers that want survivors only filter on Survives; the
// rejections exist so the search is auditable and so a re-test of an
// already-killed rule is detectable. Every candidate is judged: the
// week-trial winrate's Bonferroni-corrected Wilson lower bound must exceed the
// MEASURED null-matched week-win rate floored at 0.5 (Candidate.NullP0),
// AND the grade must survive RegimeSurvival, AND the thresholds must not be
// fragile, AND (multi-cond rules only) the counterfactual must show incremental
// value. Grades below MinWeeks are never judged — too little history to claim
// anything. Pure and deterministic: the same obs always yield the same
// survivors.
//
// The correction is DERIVED (cfg.Divisor): the grid size times the number of
// searches conducted over this data. Widening the grid and re-running the
// search both make every individual rule harder to clear, which is the only
// arrangement under which "search more" is not a way to manufacture a finding.
func Discover(obs []Obs, cfg DiscoverConfig) []Candidate {
	cfg = cfg.withDefaults()
	grid := discoverGrid(cfg.MaxCandidates)
	if len(grid) == 0 {
		return nil
	}
	divisor := cfg.Divisor()
	z := normalQuantile(1 - cfg.CorrectedAlpha())
	// THE BLIND ERA. The grid — and therefore every gate below — sees only
	// inSample. holdout is never graded until a candidate has already survived
	// on data it was fitted to, so "shadow" now requires one result the search
	// could not have selected for. Whole weeks move together (a week's era is
	// the era of its latest obs, as gradeArm defines it) so no week's
	// cross-sectional percentile ranks are computed over half a cross-section.
	inSample, holdout := splitHoldout(obs, cfg.HoldoutEra)
	var out []Candidate
	cand := func(r Rule, gate string, g WeekGrade, wl, p0 float64, nw int, sv SurvivalReport, cf CFReport, h holdoutResult) Candidate {
		return Candidate{
			ID: ruleID(r), Rule: r, Desc: ruleDesc(r), Grade: g, CF: cf,
			Survival: sv, WilsonLower: wl, NullP0: p0, NullWeeks: nw,
			HoldoutEra: cfg.HoldoutEra, HoldoutWeeks: h.weeks,
			HoldoutWilsonLower: h.wilsonLower, HoldoutNullP0: h.nullP0,
			Divisor: divisor, Survives: gate == "", RejectedBy: gate,
		}
	}
	reject := func(r Rule, gate string, g WeekGrade, wl, p0 float64, nw int, sv SurvivalReport, cf CFReport) {
		out = append(out, cand(r, gate, g, wl, p0, nw, sv, cf, holdoutResult{}))
	}
	for _, r := range grid {
		g := GradeWeeks(inSample, r, cfg.MinWeekObs)
		if g.Weeks < cfg.MinWeeks {
			reject(r, RejectMinWeeks, g, 0, 0, 0, SurvivalReport{}, CFReport{})
			continue
		}
		// MEASURED null, not an assumed one. The counterfactual is computed
		// here rather than after the Wilson gate so the null arm it already
		// grades — same matched obs, direction randomized — can supply the
		// rate this rule is judged against. Floored at 0.5 so no rule ever
		// becomes easier to clear than under the old literal.
		cf := Counterfactual(inSample, r, cfg.MinWeekObs, cfg.MinWeeks)
		p0 := math.Max(0.5, cf.NullMatched.WinRate)
		nullWeeks := cf.NullMatched.Grade.Weeks
		wl := wilsonLower(winRate(g), g.Weeks, z)
		if wl <= p0 {
			reject(r, RejectWilson, g, wl, p0, nullWeeks, SurvivalReport{}, cf)
			continue
		}
		sv := RegimeSurvival(g, cfg.MinWeeksPerEra, p0)
		if !sv.Survives {
			reject(r, RejectRegimeSurvival, g, wl, p0, nullWeeks, sv, cf)
			continue
		}
		if _, fragile := FragileThreshold(inSample, r, cfg.MinWeekObs, p0); fragile {
			reject(r, RejectFragile, g, wl, p0, nullWeeks, sv, cf)
			continue
		}
		if len(r.Conds) > 1 && !cf.AddsValue {
			reject(r, RejectCounterfactual, g, wl, p0, nullWeeks, sv, cf)
			continue
		}
		// LAST GATE, AND ONLY OUT OF SAMPLE. Everything above was measured on
		// data the grid searched; this is not. A candidate confirms only by
		// clearing the same corrected Wilson bound against the null it
		// measures on the blind era itself.
		h := judgeHoldout(holdout, r, cfg, z)
		if !h.ok {
			out = append(out, cand(r, RejectHoldout, g, wl, p0, nullWeeks, sv, cf, h))
			continue
		}
		out = append(out, cand(r, "", g, wl, p0, nullWeeks, sv, cf, h))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// splitHoldout partitions obs into (in-sample, held-out) by WEEK, assigning
// each week to the era of its latest observation — the same convention
// gradeArm uses when a week straddles an era boundary. Splitting by week and
// not by row keeps every week's cross-section intact on exactly one side, so
// within-week percentile conditions mean the same thing in both arms. An
// empty era name yields (obs, nil): no holdout, no split.
func splitHoldout(obs []Obs, era string) (inSample, holdout []Obs) {
	if era == "" {
		return obs, nil
	}
	type mark struct {
		maxTs int64
		era   string
	}
	weekEra := map[int64]mark{}
	for _, o := range obs {
		m, ok := weekEra[o.Week]
		if !ok || o.Ts > m.maxTs {
			weekEra[o.Week] = mark{maxTs: o.Ts, era: o.Era}
		}
	}
	for _, o := range obs {
		if weekEra[o.Week].era == era {
			holdout = append(holdout, o)
		} else {
			inSample = append(inSample, o)
		}
	}
	return inSample, holdout
}

// holdoutResult is the blind-era confirmation of one candidate.
type holdoutResult struct {
	weeks       int
	wilsonLower float64
	nullP0      float64
	ok          bool
}

// judgeHoldout re-runs the Wilson-vs-measured-null test on the held-out era
// alone, at the SAME corrected alpha. Insufficient held-out week-trials is a
// failure, not a pass: a rule that cannot be checked out of sample has not
// been checked. With no holdout configured (empty obs) it reports ok=true and
// zero weeks, which is the pre-holdout behaviour and is visible as such on the
// ledgered row.
func judgeHoldout(holdout []Obs, r Rule, cfg DiscoverConfig, z float64) holdoutResult {
	if len(holdout) == 0 && cfg.HoldoutEra == "" {
		return holdoutResult{ok: true}
	}
	hg := GradeWeeks(holdout, r, cfg.MinWeekObs)
	res := holdoutResult{weeks: hg.Weeks}
	if hg.Weeks < cfg.MinHoldoutWeeks {
		return res
	}
	// The null is measured on the blind era's own matched observations —
	// importing the in-sample null would reintroduce exactly the dependence
	// this gate exists to break. Floored at 0.5 as everywhere else, so the
	// substitution can only raise the bar.
	hcf := Counterfactual(holdout, r, cfg.MinWeekObs, cfg.MinHoldoutWeeks)
	res.nullP0 = math.Max(0.5, hcf.NullMatched.WinRate)
	res.wilsonLower = wilsonLower(winRate(hg), hg.Weeks, z)
	res.ok = res.wilsonLower > res.nullP0
	return res
}

// ruleID derives the stable candidate ID: "AD-" + hex(sha1(canonical
// spec))[:8]. Conds are sorted in the canonical form so equivalent rules hash
// identically regardless of cond order.
func ruleID(r Rule) string {
	sum := sha1.Sum([]byte(canonicalSpec(r)))
	return "AD-" + hex.EncodeToString(sum[:])[:8]
}

func canonicalSpec(r Rule) string {
	parts := make([]string, len(r.Conds))
	for i, c := range r.Conds {
		pct := ""
		if c.Pct {
			pct = "~pct"
		}
		parts[i] = c.Key + pct + c.Op + strconv.FormatFloat(c.Val, 'g', -1, 64)
	}
	sort.Strings(parts)
	return r.Call + "|" + strings.Join(parts, "&")
}

func ruleDesc(r Rule) string {
	if len(r.Conds) == 0 {
		return fmt.Sprintf("Auto-discovered weekly rule: %s unconditioned (week-trial graded)", r.Call)
	}
	parts := make([]string, len(r.Conds))
	for i, c := range r.Conds {
		k := c.Key
		if c.Pct {
			k += " (week-pct)"
		}
		parts[i] = fmt.Sprintf("%s %s %g", k, c.Op, c.Val)
	}
	return fmt.Sprintf("Auto-discovered weekly rule: %s when %s (week-trial graded)", r.Call, strings.Join(parts, " and "))
}

// wilsonLower returns the lower bound of the Wilson score interval for a
// proportion phat over n trials at the given z. Returns 0 for n<=0.
//
// It delegates to clusterstat.WilsonEffAt — the ONE Wilson implementation in
// the tree — with effN = n. The raw count is the right unit at this gate's one
// call site: n is g.Weeks, already one trial per WEEK, which is the clustered
// unit of this discovery loop, not a symbol-day row count.
func wilsonLower(phat float64, n int, z float64) float64 {
	if n <= 0 {
		return 0
	}
	return clusterstat.WilsonEffAt(phat, float64(n), z).Lo
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
