package researchx

import (
	"math"
	"testing"
)

// obsWeek builds one week of matched observations for a follow_pressure rule.
// correct obs move the way the pressure sign predicts by |small|; wrong obs move
// against it by |big|. Half of each group is long-side so the week's up-rate
// stays near 0.5 and the folded naive baseline cannot absorb the win rate.
func obsWeek(week int64, nCorrect, nWrong int, small, big float64, era string) []Obs {
	var out []Obs
	id := week * 1000
	add := func(pressure, fwd float64, up bool) {
		id++
		out = append(out, Obs{
			SymbolID: id, Week: week, Ts: week * 604800,
			Vec: map[string]float64{"pressure_score": pressure, "pressure_abs": math.Abs(pressure)},
			Up:  up, FwdRet: fwd, Era: era,
		})
	}
	for i := 0; i < nCorrect; i++ {
		if i%2 == 0 {
			add(1, small, true) // dir +1, rose  -> right, +small
		} else {
			add(-1, -small, false) // dir -1, fell -> right, +small
		}
	}
	for i := 0; i < nWrong; i++ {
		if i%2 == 0 {
			add(1, -big, false) // dir +1, fell -> wrong, -big
		} else {
			add(-1, big, true) // dir -1, rose -> wrong, -big
		}
	}
	return out
}

func followAll() Rule {
	return Rule{Conds: []Cond{{Key: "pressure_abs", Op: ">=", Val: 0}}, Call: "follow_pressure"}
}

// THE POINT OF THE WHOLE CHANGE. A rule can be right most weeks and still lose
// money, because a hit rate counts how OFTEN and never how MUCH. This fixture is
// right on 8 of every 10 observations by a tenth of a percent and wrong on 2 by
// two percent, so it wins nearly every week trial while its mean signed return is
// solidly negative. The Wilson gate promotes it; the mean-return gate refuses it.
// If this test ever fails, the two criteria have stopped being different questions
// and there is no reason for SelectBy to exist.
func TestHitRateAndMeanReturnDisagree(t *testing.T) {
	var obs []Obs
	for w := int64(1); w <= 40; w++ {
		jitter := float64(w%7) * 0.00005 // keeps MeanRetSD > 0 so the t is formable
		obs = append(obs, obsWeek(w, 8, 2, 0.001+jitter, 0.02, "bull_2020_21")...)
	}
	g := GradeWeeks(obs, followAll(), 10)

	if g.Weeks != 40 {
		t.Fatalf("want 40 week trials, got %d", g.Weeks)
	}
	if g.WinWeeks != 40 {
		t.Fatalf("fixture must win every week trial, won %d", g.WinWeeks)
	}
	if g.MeanRet >= 0 {
		t.Fatalf("fixture must LOSE money on average, got mean signed return %.6f", g.MeanRet)
	}

	z := normalQuantile(1 - MaxAlpha/384) // the live corrected alpha
	wl := wilsonLower(winRate(g), g.Weeks, z)
	if wl <= 0.5 {
		t.Fatalf("Wilson gate should PASS this loser: lower bound %.4f <= 0.5", wl)
	}
	if got := MeanRetT(g, 0); got > z {
		t.Fatalf("mean-return gate should REFUSE this loser: t=%.3f > z=%.3f", got, z)
	}
}

// Pseudo-replication is this repo's recurring defect class, and the exact reason
// MeanRetT clusters on weeks rather than rows: duplicating every observation
// inside a week adds no independent information about the week's return, so the
// statistic must not move at all. A row-counted t would rise by sqrt(2) here.
func TestMeanRetTIsWeekClusteredNotRowCounted(t *testing.T) {
	var obs []Obs
	for w := int64(1); w <= 40; w++ {
		obs = append(obs, obsWeek(w, 7, 3, 0.002+float64(w%5)*0.0003, 0.001, "bull_2020_21")...)
	}
	dup := append(append([]Obs{}, obs...), func() []Obs {
		out := make([]Obs, len(obs))
		for i, o := range obs {
			o.SymbolID += 500000 // a distinct symbol, identical week and outcome
			out[i] = o
		}
		return out
	}()...)

	r := followAll()
	base, doubled := GradeWeeks(obs, r, 10), GradeWeeks(dup, r, 10)

	if base.Weeks != doubled.Weeks {
		t.Fatalf("duplication must not change the week count: %d vs %d", base.Weeks, doubled.Weeks)
	}
	if doubled.TotalObs != 2*base.TotalObs {
		t.Fatalf("fixture should have doubled the rows: %d vs %d", doubled.TotalObs, base.TotalObs)
	}
	if math.Abs(base.MeanRet-doubled.MeanRet) > 1e-12 {
		t.Fatalf("week mean return moved on duplication: %.12f vs %.12f", base.MeanRet, doubled.MeanRet)
	}
	bt, dt := MeanRetT(base, 0), MeanRetT(doubled, 0)
	if math.Abs(bt-dt) > 1e-9 {
		t.Fatalf("t statistic moved on duplicated rows (%.6f -> %.6f): it is counting rows, not weeks", bt, dt)
	}
}

func TestMeanRetTRefusesToFormWithoutEvidence(t *testing.T) {
	if got := MeanRetT(WeekGrade{Weeks: 1, MeanRet: 0.5, MeanRetSD: 0.1}, 0); got != 0 {
		t.Fatalf("one week cannot support a t, got %v", got)
	}
	if got := MeanRetT(WeekGrade{Weeks: 40, MeanRet: 0.5, MeanRetSD: 0}, 0); got != 0 {
		t.Fatalf("zero dispersion cannot support a t, got %v", got)
	}
}

// SelectBy may change the QUESTION, never the bar. MaxAlpha is a constant for
// exactly this reason, and a criterion switch that also moved the divisor would
// smuggle in the alpha widening the constant exists to prevent.
func TestSelectByCannotWidenTheGate(t *testing.T) {
	w := DiscoverConfig{PriorSearches: 8, SelectBy: SelectWilson}.withDefaults()
	m := DiscoverConfig{PriorSearches: 8, SelectBy: SelectMeanRet}.withDefaults()
	if w.Divisor() != m.Divisor() {
		t.Fatalf("divisor differs by criterion: %d vs %d", w.Divisor(), m.Divisor())
	}
	if w.CorrectedAlpha() != m.CorrectedAlpha() {
		t.Fatalf("corrected alpha differs by criterion: %v vs %v", w.CorrectedAlpha(), m.CorrectedAlpha())
	}
}

func TestSelectByFallsBackToTheLiveCriterion(t *testing.T) {
	for _, in := range []string{"", "wilson", "MEANRET", "meanreturn", "nonsense"} {
		got := DiscoverConfig{SelectBy: in}.withDefaults().SelectBy
		if in == SelectMeanRet {
			continue
		}
		if got != SelectWilson {
			t.Fatalf("SelectBy=%q resolved to %q, want %q — a typo must never silently swap the gate", in, got, SelectWilson)
		}
	}
	if got := (DiscoverConfig{SelectBy: SelectMeanRet}).withDefaults().SelectBy; got != SelectMeanRet {
		t.Fatalf("explicit meanret was not honoured, got %q", got)
	}
}
