package researchx

import (
	"math"
	"testing"
)

// ob is a compact obs builder for the grading tests.
func ob(sym, week int64, up bool, vec map[string]float64) Obs {
	if vec == nil {
		vec = map[string]float64{}
	}
	return Obs{SymbolID: sym, Week: week, Ts: week * 604800, Vec: vec, Up: up, Era: "e1"}
}

// week builds one calendar week of n obs where the first `ups` are up moves and
// the rule's call is correct on the first `wins` of them.
func week(w int64, n, ups int, press float64) []Obs {
	var out []Obs
	for i := 0; i < n; i++ {
		out = append(out, ob(int64(i+1), w, i < ups, map[string]float64{"pressure_score": press}))
	}
	return out
}

// ── the week-trial discipline ───────────────────────────────────────────────

// A week wins only when its cross-sectional win rate STRICTLY beats its own
// FOLDED naive baseline max(upRate, 1-upRate). That folding is what absorbs the
// week's common market move; without it, a week where everything went up would
// hand a long-only rule a free win.
func TestGradeWeeksFoldedBaselineDeniesTheFreeWin(t *testing.T) {
	// 20 obs, 19 up. A "long" call is right 19/20 = 0.95 — but the folded
	// baseline is also 0.95, and the test is strict, so the week does NOT win.
	obs := week(1, 20, 19, -1)
	g := GradeWeeks(obs, Rule{Call: "long"}, 10)
	if g.Weeks != 1 {
		t.Fatalf("weeks = %d, want 1", g.Weeks)
	}
	if g.WinWeeks != 0 {
		t.Errorf("a week whose winrate only MATCHES its own up-rate counted as a win: %+v", g.Trials[0])
	}
	// One more correct call than the baseline allows does win.
	obs2 := week(2, 20, 19, -1)
	obs2[19].Up = true // 20/20 up; long is right 20/20 > folded baseline 1.0? no
	if got := GradeWeeks(obs2, Rule{Call: "long"}, 10); got.WinWeeks != 0 {
		t.Errorf("a degenerate all-up week must not win: %+v", got.Trials)
	}
}

func TestGradeWeeksDropsThinWeeks(t *testing.T) {
	var obs []Obs
	obs = append(obs, week(1, 20, 12, -1)...)
	obs = append(obs, week(2, 3, 2, -1)...) // below minWeekObs
	g := GradeWeeks(obs, Rule{Call: "inverse_pressure"}, 10)
	if g.Weeks != 1 {
		t.Errorf("weeks = %d, want 1 — a 3-obs week is not a measurable cross-section", g.Weeks)
	}
	if g.TotalObs != 20 {
		t.Errorf("TotalObs = %d, want 20 (dropped week's obs must not be counted)", g.TotalObs)
	}
}

// An obs the rule declines to trade (pressure exactly 0 under a pressure call)
// must be excluded before clustering — counting it as a loss would penalize a
// rule for the trades it correctly refused.
func TestGradeWeeksExcludesDirectionlessObs(t *testing.T) {
	var obs []Obs
	for i := 0; i < 12; i++ {
		obs = append(obs, ob(int64(i+1), 1, i < 8, map[string]float64{"pressure_score": -1}))
	}
	for i := 0; i < 40; i++ {
		obs = append(obs, ob(int64(100+i), 1, false, map[string]float64{"pressure_score": 0}))
	}
	g := GradeWeeks(obs, Rule{Call: "inverse_pressure"}, 10)
	if g.TotalObs != 12 {
		t.Errorf("graded %d obs, want 12 — zero-pressure obs are no-trades, not losses", g.TotalObs)
	}
}

// An unintelligible rule must grade EMPTY, never wrong: a typo in a call or an
// operator cannot be allowed to produce a confident number.
func TestUnknownCallAndOpGradeEmpty(t *testing.T) {
	obs := week(1, 20, 12, -1)
	if g := GradeWeeks(obs, Rule{Call: "teleport"}, 10); g.Weeks != 0 || g.TotalObs != 0 {
		t.Errorf("unknown call graded %d weeks / %d obs, want 0/0", g.Weeks, g.TotalObs)
	}
	bad := Rule{Call: "long", Conds: []Cond{{Key: "pressure_score", Op: "~=", Val: 0}}}
	if g := GradeWeeks(obs, bad, 10); g.Weeks != 0 {
		t.Errorf("unknown operator graded %d weeks, want 0", g.Weeks)
	}
}

// A missing feature fails its condition. Zero-filling it instead would let a
// rule trade on data it never saw.
func TestMissingFeatureFailsTheCondition(t *testing.T) {
	obs := week(1, 20, 12, -1) // no "ext_score" key anywhere
	r := Rule{Call: "long", Conds: []Cond{{Key: "ext_score", Op: ">=", Val: -1e9}}}
	if g := GradeWeeks(obs, r, 10); g.Weeks != 0 {
		t.Errorf("a rule on an absent key graded %d weeks, want 0", g.Weeks)
	}
}

// A week straddling an era boundary takes the era of its LATEST obs, so era
// grades never double-count a week.
func TestWeekStraddlingErasTakesTheLatestEra(t *testing.T) {
	obs := week(5, 20, 12, -1)
	for i := range obs {
		obs[i].Era = "early"
	}
	obs[19].Era, obs[19].Ts = "late", obs[19].Ts+100
	g := GradeWeeks(obs, Rule{Call: "inverse_pressure"}, 10)
	if len(g.ByEra) != 1 || g.ByEra[0].Era != "late" {
		t.Errorf("ByEra = %+v, want one entry for the latest era", g.ByEra)
	}
}

// ── percentile conditions ───────────────────────────────────────────────────

// Pct conditions rank WITHIN the week, among the obs that have the key. A rank
// computed across the whole sample would leak later weeks into earlier ones.
func TestWeekPctRanksAreWithinWeekAndTiedAware(t *testing.T) {
	obs := []Obs{
		ob(1, 1, true, map[string]float64{"x": 1}),
		ob(2, 1, true, map[string]float64{"x": 2}),
		ob(3, 1, true, map[string]float64{"x": 3}),
		ob(4, 1, true, map[string]float64{"x": 3}), // tie with obs 3
		ob(5, 2, true, map[string]float64{"x": 100}),
		ob(6, 2, true, map[string]float64{}), // lacks the key
	}
	r := weekPctRanks(obs, "x")
	if math.Abs(r[0]-0.125) > 1e-12 {
		t.Errorf("rank of the week's minimum = %.4f, want 0.125", r[0])
	}
	// Ties share the midpoint of the block they occupy: 2 tied values at the top
	// of 4 → (2 + 0.5*2)/4 = 0.75 each.
	if math.Abs(r[2]-0.75) > 1e-12 || math.Abs(r[3]-0.75) > 1e-12 {
		t.Errorf("tied ranks = %.4f / %.4f, want 0.75 each", r[2], r[3])
	}
	// Week 2's single keyed obs ranks 0.5 within ITS OWN week, not 1.0 across
	// the sample — the point of within-week ranking.
	if math.Abs(r[4]-0.5) > 1e-12 {
		t.Errorf("lone obs in week 2 ranked %.4f, want 0.5 (within-week)", r[4])
	}
	if !math.IsNaN(r[5]) {
		t.Errorf("obs lacking the key ranked %.4f, want NaN", r[5])
	}
}

// ── the null-matched arm ────────────────────────────────────────────────────

// The null arm must trade the SAME obs as the real arm with a coin for
// direction. If it traded a different set, the comparison would measure
// selection rather than direction, and every rule would look like it adds value.
func TestNullMatchedArmTradesTheSameObs(t *testing.T) {
	var obs []Obs
	for w := int64(1); w <= 40; w++ {
		for i := 0; i < 20; i++ {
			p := -1.0
			if i%3 == 0 {
				p = 0 // a no-trade obs
			}
			obs = append(obs, ob(int64(i+1), w, i%2 == 0, map[string]float64{"pressure_score": p}))
		}
	}
	cf := Counterfactual(obs, Rule{Call: "inverse_pressure"}, 10, 30)
	if cf.Full.Grade.TotalObs != cf.NullMatched.Grade.TotalObs {
		t.Errorf("null arm graded %d obs, full arm %d — the arms must be matched",
			cf.NullMatched.Grade.TotalObs, cf.Full.Grade.TotalObs)
	}
	// And it is deterministic: no RNG anywhere in this package.
	if again := Counterfactual(obs, Rule{Call: "inverse_pressure"}, 10, 30); again.NullMatched.WinRate != cf.NullMatched.WinRate {
		t.Error("null arm is not deterministic across runs")
	}
}

// AddsValue requires beating EVERY competing arm and having enough weeks. A
// rule with too little history cannot claim incremental value however good it
// looks.
func TestCounterfactualNeedsEnoughWeeks(t *testing.T) {
	var obs []Obs
	for w := int64(1); w <= 5; w++ {
		obs = append(obs, week(w, 20, 4, -1)...) // inverse call wins these weeks
	}
	cf := Counterfactual(obs, Rule{Call: "inverse_pressure"}, 10, 30)
	if cf.AddsValue {
		t.Errorf("5 weeks claimed incremental value (margin %.3f) — MinWeeks not enforced", cf.Margin)
	}
}

// Ablations are drop-ONE-condition arms, one per condition, named for the key
// they drop — the record of what each condition was worth.
func TestCounterfactualAblatesEachCondition(t *testing.T) {
	r := Rule{Call: "long", Conds: []Cond{
		{Key: "rsi_pct", Op: ">=", Val: 0.8},
		{Key: "vol_pct", Op: ">=", Val: 0.8},
	}}
	cf := Counterfactual(nil, r, 10, 30)
	if len(cf.Ablations) != 2 {
		t.Fatalf("%d ablation arms, want one per condition", len(cf.Ablations))
	}
	if cf.Ablations[0].Name != "without rsi_pct" || cf.Ablations[1].Name != "without vol_pct" {
		t.Errorf("ablation arms = %q / %q", cf.Ablations[0].Name, cf.Ablations[1].Name)
	}
}

// ── regime survival ─────────────────────────────────────────────────────────

func TestRegimeSurvivalGates(t *testing.T) {
	mk := func(eras []EraGrade, weeks int) SurvivalReport {
		return RegimeSurvival(WeekGrade{Weeks: weeks, ByEra: eras}, 8)
	}
	twoGood := []EraGrade{{Era: "a", Weeks: 20, WinWeeks: 14}, {Era: "b", Weeks: 20, WinWeeks: 13}}
	if r := mk(twoGood, 40); !r.Survives || r.PositiveEras != 2 || r.GradedEras != 2 {
		t.Errorf("two positive eras over 40 weeks did not survive: %+v", r)
	}
	// One era is not cross-regime evidence, however strong.
	if r := mk([]EraGrade{{Era: "a", Weeks: 40, WinWeeks: 39}}, 40); r.Survives {
		t.Errorf("single-era grade survived: %+v", r)
	}
	// Too few total weeks.
	if r := mk(twoGood, 29); r.Survives {
		t.Errorf("29 total weeks survived: %+v", r)
	}
	// A catastrophic era disqualifies even with two positive ones.
	cat := append(append([]EraGrade{}, twoGood...), EraGrade{Era: "c", Weeks: 20, WinWeeks: 4})
	if r := mk(cat, 60); r.Survives {
		t.Errorf("a 20%%-winrate era did not disqualify: %+v", r)
	}
	// A thin era is UNGRADED — too little to acquit or convict — so it neither
	// counts as positive nor as catastrophic.
	thin := append(append([]EraGrade{}, twoGood...), EraGrade{Era: "c", Weeks: 3, WinWeeks: 0})
	r := mk(thin, 43)
	if !r.Survives || r.GradedEras != 2 {
		t.Errorf("a 3-week era changed the verdict: %+v", r)
	}
}

// ── threshold fragility ─────────────────────────────────────────────────────

// With nothing to perturb, or no positive edge to destroy, the probe must
// report "not fragile" rather than manufacture a verdict.
func TestFragileThresholdNothingToPerturb(t *testing.T) {
	obs := week(1, 20, 12, -1)
	if w, f := FragileThreshold(obs, Rule{Call: "long"}, 10); w != 1 || f {
		t.Errorf("unconditioned rule: worst=%.2f fragile=%v, want 1/false", w, f)
	}
	r := Rule{Call: "long", Conds: []Cond{{Key: "pressure_score", Op: "<=", Val: 0}}}
	if w, f := FragileThreshold(obs, r, 10); w != 1 || f {
		t.Errorf("no positive edge: worst=%.2f fragile=%v, want 1/false", w, f)
	}
}

// A knife-edge threshold — where ±10% wipes out the edge — must be reported
// fragile. This is the check that separates a phenomenon from a fitted number.
func TestFragileThresholdCatchesAKnifeEdge(t *testing.T) {
	// The edge lives ONLY in ext_score ∈ [1.0, 1.1): raising the threshold 10%
	// keeps just the losers above it, lowering it 10% admits the losers below.
	// Pressure alternates sign so the week's folded baseline stays at 0.5 and a
	// real win rate can clear it.
	var obs []Obs
	for w := int64(1); w <= 40; w++ {
		for i := 0; i < 36; i++ {
			var ext float64
			var win bool
			switch {
			case i < 16:
				ext, win = 1.05, true // the edge: inside the band
			case i < 26:
				ext, win = 1.50, false // survives a +10% threshold, and loses
			default:
				ext, win = 0.95, false // admitted by a -10% threshold, and loses
			}
			press := 1.0
			if i%2 == 0 {
				press = -1.0
			}
			up := press < 0 // the direction the inverse call predicts
			if !win {
				up = !up
			}
			obs = append(obs, ob(int64(i+1), w, up, map[string]float64{
				"pressure_score": press, "ext_score": ext,
			}))
		}
	}
	r := Rule{Call: "inverse_pressure", Conds: []Cond{{Key: "ext_score", Op: ">=", Val: 1.0}}}
	if g := GradeWeeks(obs, r, 5); g.WinWeeks != g.Weeks || g.Weeks == 0 {
		t.Fatalf("fixture broken: unperturbed rule won %d/%d weeks, want all", g.WinWeeks, g.Weeks)
	}
	worst, fragile := FragileThreshold(obs, r, 5)
	if !fragile {
		t.Errorf("knife-edge threshold reported robust (worst retained %.3f)", worst)
	}
	if worst >= 0.5 {
		t.Errorf("worst retained edge = %.3f, want well under 0.5", worst)
	}
}
