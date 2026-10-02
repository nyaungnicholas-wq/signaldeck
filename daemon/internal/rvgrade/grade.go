// Package rvgrade is the live grader for the HAR realized-variance forward
// test registered in internal/volprereg (chain kind rv-forecast-test-registration).
//
// It implements the registered DecisionRule and nothing beyond it. Every
// constant it uses is read from volprereg.Registration(), never re-typed,
// except alpha = 0.05, which exists only in the rule's prose.
//
// # Where the registered text does not decide, this file does not decide
//
// The rule leaves three things open (written up in full in
// audits/2026-10-01-rv-grader-ambiguities.md):
//
//  1. the Bonferroni divisor for "family size 24 and the looks already spent":
//     24+2 = 26, 24*2 = 48 (prereg.MultiplicityRule's family*looks), or
//     24*(1+2) = 72 (researchx.Divisor's grid*(1+prior searches));
//  2. the reference distribution of the HLN-corrected DM statistic: Student-t
//     with T-1 df (Harvey, Leybourne and Newbold's own recommendation) or N(0,1);
//  3. the bar the RV^CC control must clear to "hold": the same corrected bar,
//     the uncorrected 0.05, or the sign alone.
//
// Decide evaluates EVERY combination. A verdict is published only when all of
// them agree; otherwise the record stays ACCRUING and the reason names the
// disagreement. That is a refusal to rule, not a ruling, and it ends the day
// the owner resolves the text and the open readings collapse to one.
package rvgrade

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/nyaungnicholas-wq/signaldeck/internal/harrv"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/volprereg"
)

// The verdict strings. The four outcomes are the registration's own words;
// ACCRUING is the record's existing "floor met, not ruled" state.
const (
	Insufficient = "INSUFFICIENT"
	NoSkill      = "NO SKILL DEMONSTRATED"
	Artifact     = "ESTIMATOR ARTIFACT"
	Beats        = "BEATS THE NULLS"
	Accruing     = "ACCRUING"
)

// HeadlineHorizon is the horizon of the registered headline cell ("QLIKE,
// horizon 1, headline proxy RV^GK, HAR versus EWMA(0.94). ONE cell.").
const HeadlineHorizon = 1

// alpha is the 0.05 of the DecisionRule ("is at or above 0.05").
const alpha = 0.05

// Row is one resolved live forecast and its two outcome proxies.
type Row struct {
	Day           string  // UTC date of the call bar: the unit of observation
	HAR, RW, EWMA float64 // frozen at call time
	GK            float64 // headline proxy: mean RV^GK over t+1..t+h
	CC            float64 // control proxy: mean RV^CC over t+1..t+h; NaN when not computable
}

// Cell is one Diebold-Mariano comparison, HAR minus a null, on one loss and
// one proxy. Mean < 0 favours HAR.
type Cell struct {
	Loss     string  `json:"loss"`
	Proxy    string  `json:"proxy"`
	Null     string  `json:"null"`
	Days     int     `json:"days"`     // day-clusters tested (each >= MinSymbolsPerDay)
	ThinDays int     `json:"thinDays"` // days with rows but under MinSymbolsPerDay: not counted
	Rows     int     `json:"rows"`     // forecasts inside the counted days
	Lag      int     `json:"hacLag"`
	OK       bool    `json:"ok"` // false: fewer than 8 days or zero long-run variance
	Mean     float64 `json:"meanDiff"`
	T        float64 `json:"t"`
	PStudent float64 `json:"pStudentT"` // two-sided, df = days-1
	PNormal  float64 `json:"pNormal"`   // two-sided
}

// Grade is every cell the live table can compute at one horizon.
type Grade struct {
	Horizon   int    `json:"horizon"`
	Headline  Cell   `json:"headline"` // QLIKE, RV^GK, vs EWMA
	Control   Cell   `json:"control"`  // MSE, RV^CC, vs EWMA
	Secondary []Cell `json:"secondary"`
	// CCZero counts forecasts whose RV^CC window mean is zero (the close did
	// not move); the registration says they are excluded and COUNTED.
	CCZero int `json:"ccZeroExcluded"`
	// CCMissing counts forecasts with no RV^CC at all (a missing or
	// non-positive close in the window).
	CCMissing int `json:"ccMissing"`
}

// CCTarget is the RV^CC outcome of a forecast called at bar callTs: the mean
// of ln(C_i/C_{i-1})^2 over the h bars after the call bar ("RV^CC =
// ln(C_t/C_{t-1})^2", the Control field). NaN when the call bar is absent, the
// window is incomplete, or a close in it is non-positive -- cc_series and
// control_section in tools/rv_forecast_backtest.py, which index the same raw
// daily bars and apply no other guard.
func CCTarget(bars []md.Bar, callTs int64, h int) float64 {
	t := -1
	for i, b := range bars {
		if b.Ts == callTs {
			t = i
			break
		}
	}
	if t < 0 || h < 1 || t+h >= len(bars) {
		return math.NaN()
	}
	sum := 0.0
	for i := t + 1; i <= t+h; i++ {
		prev, cur := bars[i-1].Close, bars[i].Close
		if prev <= 0 || cur <= 0 {
			return math.NaN()
		}
		r := math.Log(cur / prev)
		sum += r * r
	}
	return sum / float64(h)
}

type loss func(actual, forecast float64) (float64, bool)

func qlike(a, f float64) (float64, bool) { return harrv.QLIKE(a, f) }

// mse mirrors tools/rv_forecast_backtest.py: the control drops a window whose
// RV^CC mean is not positive before any loss is taken (cc > 0 is checked by
// the caller), so plain harrv.MSE is the loss.
func mse(a, f float64) (float64, bool) { return harrv.MSE(a, f) }

// GradeHorizon computes the headline, the control and the secondary cells.
func GradeHorizon(rows []Row, h int) Grade {
	g := Grade{Horizon: h}
	gk := func(r Row) float64 { return r.GK }
	cc := func(r Row) float64 {
		if math.IsNaN(r.CC) || r.CC <= 0 {
			return math.NaN()
		}
		return r.CC
	}
	har := func(r Row) float64 { return r.HAR }
	rw := func(r Row) float64 { return r.RW }
	ew := func(r Row) float64 { return r.EWMA }

	g.Headline = cell(rows, h, "QLIKE", "RV^GK", "EWMA(0.94)", qlike, gk, har, ew)
	g.Control = cell(rows, h, "MSE", "RV^CC", "EWMA(0.94)", mse, cc, har, ew)
	g.Secondary = []Cell{
		cell(rows, h, "QLIKE", "RV^GK", "random walk", qlike, gk, har, rw),
		cell(rows, h, "MSE", "RV^GK", "EWMA(0.94)", mse, gk, har, ew),
		cell(rows, h, "MSE", "RV^GK", "random walk", mse, gk, har, rw),
		cell(rows, h, "MSE", "RV^CC", "random walk", mse, cc, har, rw),
	}
	for _, r := range rows {
		switch {
		case math.IsNaN(r.CC):
			g.CCMissing++
		case r.CC <= 0:
			g.CCZero++
		}
	}
	return g
}

// cell collapses (symbol, day) to one mean loss per day per model, drops days
// under MinSymbolsPerDay, and runs DM on the daily differentials -- the
// registered unit of observation. Same arithmetic as aggregate() and
// control_section() in tools/rv_forecast_backtest.py.
func cell(rows []Row, h int, lossName, proxy, null string, L loss,
	actual, model, base func(Row) float64) Cell {
	spec := volprereg.Registration()
	c := Cell{Loss: lossName, Proxy: proxy, Null: null}

	type acc struct {
		m, b float64
		n    int
	}
	byDay := map[string]*acc{}
	for _, r := range rows {
		a := actual(r)
		if math.IsNaN(a) {
			continue
		}
		lm, ok1 := L(a, model(r))
		lb, ok2 := L(a, base(r))
		if !ok1 || !ok2 {
			continue
		}
		s := byDay[r.Day]
		if s == nil {
			s = &acc{}
			byDay[r.Day] = s
		}
		s.m += lm
		s.b += lb
		s.n++
	}
	days := make([]string, 0, len(byDay))
	for d, s := range byDay {
		if s.n < spec.MinSymbolsPerDay {
			c.ThinDays++
			continue
		}
		days = append(days, d)
	}
	sort.Strings(days)
	diff := make([]float64, len(days))
	for i, d := range days {
		s := byDay[d]
		diff[i] = s.m/float64(s.n) - s.b/float64(s.n)
		c.Rows += s.n
	}
	c.Days = len(days)

	// "lag max(floor(4*(T/100)^(2/9)), h)", clamped to T-1 exactly as both
	// DM implementations clamp it.
	c.Lag = max(harrv.NeweyWestLag(len(diff)), h)
	if n := len(diff); n > 0 && c.Lag > n-1 {
		c.Lag = n - 1
	}
	dm := harrv.DieboldMariano(diff, c.Lag)
	c.Mean = dm.Mean
	if dm.OK {
		c.OK = true
		c.T = dm.T
		c.PStudent = StudentTTwoSidedP(dm.T, dm.N-1)
		c.PNormal = math.Erfc(math.Abs(dm.T) / math.Sqrt2)
	}
	return c
}

// Reading is one combination of the three open readings and what the rule
// says under it.
type Reading struct {
	Divisor    int    `json:"divisor"`
	Dist       string `json:"dist"`       // "student-t" | "normal"
	ControlBar string `json:"controlBar"` // "corrected" | "uncorrected" | "sign"
	Outcome    string `json:"outcome"`
}

// Outcomes a reading can reach that are NOT registered verdicts. They exist so
// the gap is visible rather than silently mapped onto a neighbour.
const (
	gapWrongSign  = "UNREGISTERED: significant in EWMA's favour"
	gapNoControl  = "UNDEFINED: control statistic not computable"
	gapNoHeadline = "UNDEFINED: headline statistic not computable"
)

// Divisors are the three readings of "Bonferroni-corrected across family size
// 24 and the looks already spent".
func Divisors() []int {
	s := volprereg.Registration()
	return []int{
		s.FamilySize + s.LooksSpent,
		s.FamilySize * s.LooksSpent,
		s.FamilySize * (1 + s.LooksSpent),
	}
}

// Ruling is the registered verdict for the study, decided by the headline
// cell at horizon 1 and its RV^CC control.
type Ruling struct {
	Verdict  string    `json:"verdict"`
	Reason   string    `json:"verdictReason"`
	Readings []Reading `json:"readings,omitempty"`
}

// Decide applies the DecisionRule to the horizon-1 grade.
func Decide(g Grade) Ruling {
	spec := volprereg.Registration()
	head, ctl := g.Headline, g.Control
	if head.Days < spec.MinDistinctDays {
		return Ruling{Verdict: Insufficient, Reason: fmt.Sprintf(
			"%d of the %d distinct trading days required (days with fewer than %d symbols "+
				"are not counted); no verdict either way.",
			head.Days, spec.MinDistinctDays, spec.MinSymbolsPerDay)}
	}

	var readings []Reading
	seen := map[string]bool{}
	var order []string
	for _, div := range Divisors() {
		bar := alpha / float64(div)
		for _, dist := range []string{"student-t", "normal"} {
			p := func(c Cell) float64 {
				if dist == "normal" {
					return c.PNormal
				}
				return c.PStudent
			}
			for _, cb := range []string{"corrected", "uncorrected", "sign"} {
				var out string
				switch {
				case !head.OK:
					out = gapNoHeadline
				case p(head) >= bar:
					out = NoSkill
				case head.Mean >= 0:
					out = gapWrongSign
				case !ctl.OK:
					out = gapNoControl
				default:
					holds := ctl.Mean < 0
					switch cb {
					case "corrected":
						holds = holds && p(ctl) < bar
					case "uncorrected":
						holds = holds && p(ctl) < alpha
					}
					out = Artifact
					if holds {
						out = Beats
					}
				}
				readings = append(readings, Reading{div, dist, cb, out})
				if !seen[out] {
					seen[out] = true
					order = append(order, out)
				}
			}
		}
	}

	stats := fmt.Sprintf("Headline (QLIKE, RV^GK, HAR vs EWMA, %d days): mean differential %+.4g, "+
		"DM t %.3f, two-sided p %.3g (Student-t) / %.3g (normal), HAC lag %d. "+
		"RV^CC control (MSE, %d days): mean %+.4g, t %.3f, p %.3g / %.3g. "+
		"Corrected bar 0.05/%v.",
		head.Days, head.Mean, head.T, head.PStudent, head.PNormal, head.Lag,
		ctl.Days, ctl.Mean, ctl.T, ctl.PStudent, ctl.PNormal, Divisors())

	if len(order) == 1 && (order[0] == NoSkill || order[0] == Artifact || order[0] == Beats) {
		return Ruling{Verdict: order[0], Readings: readings,
			Reason: order[0] + " under every open reading of the registered rule. " + stats}
	}
	return Ruling{Verdict: Accruing, Readings: readings, Reason: fmt.Sprintf(
		"%d distinct trading days, at or above the %d-day floor, but the registered rule does not "+
			"yet yield one verdict: its open readings give %s. Held, not ruled, until the "+
			"rule text is resolved (audits/2026-10-01-rv-grader-ambiguities.md). %s",
		head.Days, spec.MinDistinctDays, strings.Join(order, " / "), stats)}
}
