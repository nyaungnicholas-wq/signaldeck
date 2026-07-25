// Package researchx is the pure analysis core of the research discovery
// engine: machine-gradable rules (Rule) over labeled weekly research
// observations (Obs), graded with EXACTLY the live research ledger's
// week-trial discipline, then stress-tested by the adversarial layers the
// house doctrine demands — counterfactual ablations (does each condition add
// value?), regime-survival gating (does the edge exist in more than one
// era?), threshold-fragility probing (does ±10% destroy it?), bounded
// auto-discovery with Bonferroni-corrected judging, posterior decay replay,
// and the evidence graph.
//
// House purity: no I/O, no clock, no RNG. The null-matched baseline arm uses
// a stable FNV-64a hash of (SymbolID, Week) as its deterministic coin.
package researchx

import (
	"encoding/binary"
	"hash/fnv"
	"math"
	"sort"
)

// Obs is one labeled symbol-week research observation (one decoded row of the
// research_weeks table).
type Obs struct {
	SymbolID int64
	Week     int64
	Ts       int64
	Vec      map[string]float64
	Up       bool
	FwdRet   float64
	Era      string
	HighVol  bool
}

// Cond is one threshold condition of a machine-gradable rule.
type Cond struct {
	Key string  `json:"key"`
	Op  string  `json:"op"` // ">=" | "<="
	Val float64 `json:"val"`
	// Pct: the threshold applies to the WITHIN-WEEK cross-sectional percentile
	// rank of Key (0..1) — computed among that week's obs that HAVE the key —
	// not to the raw value.
	Pct bool `json:"pct"`
}

// Rule is a machine-gradable weekly hypothesis: a directional call gated by
// zero or more conditions. Per-obs direction by Call:
//
//	inverse_pressure  -sign(vec["pressure_score"])  (0 pressure ⇒ no trade)
//	follow_pressure   +sign(vec["pressure_score"])
//	long              +1
//	short             -1
//
// A rule matches an obs when ALL conds hold (Pct conds evaluated against that
// week's cross-sectional ranks). An unknown Op or Call matches/trades nothing
// — an unintelligible rule must grade empty, never wrong.
type Rule struct {
	Conds []Cond `json:"conds"`
	Call  string `json:"call"`
}

// WeekTrial is one calendar week scored as a single Bernoulli trial.
type WeekTrial struct {
	Week    int64
	N       int
	Wins    int
	Ups     int
	HighVol int
	Win     bool
}

// EraGrade aggregates week trials within one era.
type EraGrade struct {
	Era      string
	Weeks    int
	WinWeeks int
	Obs      int
}

// WeekGrade is a full week-trial grade of a rule over an observation set.
type WeekGrade struct {
	Weeks      int
	WinWeeks   int
	TotalObs   int
	HighVolObs int
	Trials     []WeekTrial // chronological
	ByEra      []EraGrade  // chronological era order (order of first trial)
}

// GradeWeeks grades a rule with the week-trial discipline exactly as the live
// ledger grader: each calendar week's matched directional obs form ONE
// Bernoulli trial; win = the week's cross-sectional win rate STRICTLY beats
// its own folded naive baseline max(upRate, 1-upRate); weeks with fewer than
// minWeekObs obs are dropped (a near-empty week is not a measurable
// cross-section). Directionless obs (Dir==0) are excluded before clustering.
func GradeWeeks(obs []Obs, r Rule, minWeekObs int) WeekGrade {
	return gradeArm(obs, r.Conds, callDir(r.Call), minWeekObs)
}

// CFArm is one arm of a counterfactual comparison.
type CFArm struct {
	Name    string
	Grade   WeekGrade
	WinRate float64 // WinWeeks/Weeks; 0 when no graded weeks
}

// CFReport is the counterfactual decomposition of a rule: the full rule
// against its drop-one-condition ablations, the unconditioned base call, and
// a null arm that keeps the matched obs but randomizes direction
// deterministically.
type CFReport struct {
	Full      CFArm
	Ablations []CFArm // drop-one-cond, same call ("without <condKey>")
	Base      CFArm   // no conds, same call
	// NullMatched grades the SAME matched obs as Full, but with direction =
	// deterministic hash parity of (SymbolID, Week) — the random-baseline arm.
	NullMatched CFArm
	AddsValue   bool    // Full beats EVERY ablation AND Base AND NullMatched
	Margin      float64 // Full winrate − best competing arm winrate
}

// Counterfactual grades a rule against its ablation/base/null arms.
// AddsValue requires Full.Weeks >= minWeeks and a strictly positive winrate
// margin over every competing arm.
func Counterfactual(obs []Obs, r Rule, minWeekObs, minWeeks int) CFReport {
	dir := callDir(r.Call)
	rep := CFReport{Full: cfArm("full", gradeArm(obs, r.Conds, dir, minWeekObs))}
	for i, c := range r.Conds {
		rest := make([]Cond, 0, len(r.Conds)-1)
		rest = append(rest, r.Conds[:i]...)
		rest = append(rest, r.Conds[i+1:]...)
		rep.Ablations = append(rep.Ablations, cfArm("without "+c.Key, gradeArm(obs, rest, dir, minWeekObs)))
	}
	rep.Base = cfArm("base", gradeArm(obs, nil, dir, minWeekObs))
	rep.NullMatched = cfArm("null-matched", gradeArm(obs, r.Conds, nullDir(dir), minWeekObs))

	best := math.Inf(-1)
	for _, a := range rep.Ablations {
		best = math.Max(best, a.WinRate)
	}
	best = math.Max(best, rep.Base.WinRate)
	best = math.Max(best, rep.NullMatched.WinRate)
	rep.Margin = rep.Full.WinRate - best
	rep.AddsValue = rep.Full.Grade.Weeks >= minWeeks && rep.Margin > 0
	return rep
}

// SurvivalReport gates a grade on cross-regime (era) robustness.
type SurvivalReport struct {
	Eras         []EraGrade
	PositiveEras int // eras with >=minWeeksPerEra weeks AND winRate > 0.5
	GradedEras   int // eras with >=minWeeksPerEra weeks
	TotalWeeks   int
	// Survives: PositiveEras >= 2 AND TotalWeeks >= 30 AND no graded era has
	// winRate < 0.35 (catastrophic regime failure).
	Survives bool
}

// RegimeSurvival judges a grade's era decomposition. Eras below minWeeksPerEra
// weeks are ungraded: too thin to acquit OR convict.
func RegimeSurvival(g WeekGrade, minWeeksPerEra int) SurvivalReport {
	rep := SurvivalReport{Eras: g.ByEra, TotalWeeks: g.Weeks}
	catastrophic := false
	for _, e := range g.ByEra {
		if e.Weeks < minWeeksPerEra {
			continue
		}
		rep.GradedEras++
		wr := float64(e.WinWeeks) / float64(e.Weeks)
		if wr > 0.5 {
			rep.PositiveEras++
		}
		if wr < 0.35 {
			catastrophic = true
		}
	}
	rep.Survives = rep.PositiveEras >= 2 && rep.TotalWeeks >= 30 && !catastrophic
	return rep
}

// FragileThreshold perturbs every numeric threshold ±10% (both directions, one
// cond at a time), re-grades, and reports the WORST retained edge fraction
// (perturbed edge / original edge, where edge = week win rate − 0.5).
// fragile = original edge > 0 AND worst retained < 0.5 of the original. With
// no conds or no positive original edge there is nothing to perturb or
// destroy: worstRetained = 1, fragile = false.
func FragileThreshold(obs []Obs, r Rule, minWeekObs int) (worstRetained float64, fragile bool) {
	orig := GradeWeeks(obs, r, minWeekObs)
	edge := winRate(orig) - 0.5
	if len(r.Conds) == 0 || edge <= 0 {
		return 1, false
	}
	dir := callDir(r.Call)
	worstRetained = math.Inf(1)
	for i := range r.Conds {
		for _, f := range []float64{1.1, 0.9} {
			pert := make([]Cond, len(r.Conds))
			copy(pert, r.Conds)
			pert[i].Val *= f
			retained := (winRate(gradeArm(obs, pert, dir, minWeekObs)) - 0.5) / edge
			worstRetained = math.Min(worstRetained, retained)
		}
	}
	return worstRetained, worstRetained < 0.5
}

// ── grading internals ────────────────────────────────────────────────────

type dirFunc func(Obs) int

func callDir(call string) dirFunc {
	switch call {
	case "inverse_pressure":
		return func(o Obs) int { return -sign(o.Vec["pressure_score"]) }
	case "follow_pressure":
		return func(o Obs) int { return sign(o.Vec["pressure_score"]) }
	case "long":
		return func(Obs) int { return 1 }
	case "short":
		return func(Obs) int { return -1 }
	}
	return func(Obs) int { return 0 }
}

func sign(v float64) int {
	switch {
	case v > 0:
		return 1
	case v < 0:
		return -1
	}
	return 0
}

// nullDir replaces an arm's direction with the parity of a stable hash of
// (SymbolID, Week) — a deterministic coin on the SAME obs set as the real arm
// (obs the real call declines to trade stay excluded).
func nullDir(real dirFunc) dirFunc {
	return func(o Obs) int {
		if real(o) == 0 {
			return 0
		}
		if hash64(o.SymbolID, o.Week)&1 == 0 {
			return 1
		}
		return -1
	}
}

func hash64(a, b int64) uint64 {
	h := fnv.New64a()
	var buf [16]byte
	binary.BigEndian.PutUint64(buf[:8], uint64(a))
	binary.BigEndian.PutUint64(buf[8:], uint64(b))
	h.Write(buf[:])
	return h.Sum64()
}

// matcher resolves a cond set against an obs slice: Pct conds are evaluated
// on within-week cross-sectional percentile ranks (computed once per key),
// raw conds on the vec value; a missing key or rank fails the cond.
func matcher(obs []Obs, conds []Cond) func(i int) bool {
	var ranks map[string][]float64
	for _, c := range conds {
		if !c.Pct {
			continue
		}
		if ranks == nil {
			ranks = map[string][]float64{}
		}
		if _, ok := ranks[c.Key]; !ok {
			ranks[c.Key] = weekPctRanks(obs, c.Key)
		}
	}
	return func(i int) bool {
		for _, c := range conds {
			var v float64
			if c.Pct {
				v = ranks[c.Key][i]
				if math.IsNaN(v) {
					return false
				}
			} else {
				var ok bool
				v, ok = obs[i].Vec[c.Key]
				if !ok {
					return false
				}
			}
			switch c.Op {
			case ">=":
				if v < c.Val {
					return false
				}
			case "<=":
				if v > c.Val {
					return false
				}
			default:
				return false
			}
		}
		return true
	}
}

// weekPctRanks computes, per obs index, the within-week cross-sectional
// percentile rank of key: (strictly-less + 0.5*equal) / count among the
// week's obs that HAVE the key; NaN when the obs lacks the key.
func weekPctRanks(obs []Obs, key string) []float64 {
	out := make([]float64, len(obs))
	byWeek := map[int64][]int{}
	for i, o := range obs {
		out[i] = math.NaN()
		if _, ok := o.Vec[key]; ok {
			byWeek[o.Week] = append(byWeek[o.Week], i)
		}
	}
	for _, idxs := range byWeek {
		vals := make([]float64, len(idxs))
		for j, i := range idxs {
			vals[j] = obs[i].Vec[key]
		}
		sort.Float64s(vals)
		n := float64(len(vals))
		for _, i := range idxs {
			v := obs[i].Vec[key]
			less := sort.SearchFloat64s(vals, v)
			eq := sort.Search(len(vals), func(k int) bool { return vals[k] > v }) - less
			out[i] = (float64(less) + 0.5*float64(eq)) / n
		}
	}
	return out
}

type weekAgg struct {
	week             int64
	n, wins, ups, hi int
	maxTs            int64
	era              string
}

// winsTrial is the week-trial outcome: the cross-sectional win rate strictly
// beats the week's own folded naive baseline.
func (wa *weekAgg) winsTrial() bool {
	upRate := float64(wa.ups) / float64(wa.n)
	p0 := math.Max(upRate, 1-upRate)
	return float64(wa.wins)/float64(wa.n) > p0
}

func gradeArm(obs []Obs, conds []Cond, dir dirFunc, minWeekObs int) WeekGrade {
	match := matcher(obs, conds)
	byWeek := map[int64]*weekAgg{}
	for i, o := range obs {
		if !match(i) {
			continue
		}
		d := dir(o)
		if d == 0 {
			continue
		}
		wa := byWeek[o.Week]
		if wa == nil {
			wa = &weekAgg{week: o.Week, maxTs: o.Ts, era: o.Era}
			byWeek[o.Week] = wa
		}
		wa.n++
		if (d > 0) == o.Up {
			wa.wins++
		}
		if o.Up {
			wa.ups++
		}
		if o.HighVol {
			wa.hi++
		}
		// A week straddling an era boundary takes the era of its latest obs.
		if o.Ts > wa.maxTs {
			wa.maxTs, wa.era = o.Ts, o.Era
		}
	}
	weeks := make([]*weekAgg, 0, len(byWeek))
	for _, wa := range byWeek {
		if wa.n < minWeekObs {
			continue
		}
		weeks = append(weeks, wa)
	}
	sort.Slice(weeks, func(i, j int) bool { return weeks[i].week < weeks[j].week })

	var g WeekGrade
	eraIdx := map[string]int{}
	for _, wa := range weeks {
		win := wa.winsTrial()
		g.Weeks++
		if win {
			g.WinWeeks++
		}
		g.TotalObs += wa.n
		g.HighVolObs += wa.hi
		g.Trials = append(g.Trials, WeekTrial{
			Week: wa.week, N: wa.n, Wins: wa.wins, Ups: wa.ups, HighVol: wa.hi, Win: win,
		})
		k, ok := eraIdx[wa.era]
		if !ok {
			k = len(g.ByEra)
			eraIdx[wa.era] = k
			g.ByEra = append(g.ByEra, EraGrade{Era: wa.era})
		}
		g.ByEra[k].Weeks++
		if win {
			g.ByEra[k].WinWeeks++
		}
		g.ByEra[k].Obs += wa.n
	}
	return g
}

func cfArm(name string, g WeekGrade) CFArm {
	return CFArm{Name: name, Grade: g, WinRate: winRate(g)}
}

func winRate(g WeekGrade) float64 {
	if g.Weeks == 0 {
		return 0
	}
	return float64(g.WinWeeks) / float64(g.Weeks)
}
