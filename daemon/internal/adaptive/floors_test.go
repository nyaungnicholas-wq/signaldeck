package adaptive

import (
	"math"
	"math/rand"
	"sort"
	"strings"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/ensemble"
)

const daySecs = 86400

// clusteredPanel builds the shape of THIS platform's live labeled set: `days`
// distinct UTC days, `perDay` rows on each of them, and a leg that makes ONE
// call per day which every row on that day repeats. That is what the predictor
// actually produces — it runs every 10 minutes against a daily label and ~1,000
// symbols share the day's market move — so the row count is `days*perDay` while
// the independent evidence is `days`.
//
// trueEdge is the probability the leg's daily call agrees with the day's
// realized direction: 0.5 is a pure coin flip, and every leg named in
// noiseLegs gets exactly that. cells splits each day's rows across regime
// labels so every cell spans every day.
func clusteredPanel(rnd *rand.Rand, days, perDay int, cells []string, trueEdge map[string]float64) []Example {
	var out []Example
	for d := 0; d < days; d++ {
		up := 0
		if rnd.Float64() < 0.5 {
			up = 1
		}
		fwd := 0.01
		if up == 0 {
			fwd = -0.01
		}
		// One call per (leg, cell, day) — repeated by every row that day.
		call := map[string]map[string]float64{}
		// SORTED, because this loop draws from rnd. Go randomises map iteration
		// order, so ranging trueEdge directly assigned each draw to a different
		// leg on every run — a fixed seed still produced a different panel, and
		// TestCompute_PureNoisePanelEarnsNoWeight failed whenever no leg
		// happened to reach its 0.60 winner's-curse precondition (~2 runs in 3
		// for the whole package). A test whose fixture is random is not a test.
		legNames := make([]string, 0, len(trueEdge))
		for leg := range trueEdge {
			legNames = append(legNames, leg)
		}
		sort.Strings(legNames)
		for _, cell := range cells {
			call[cell] = map[string]float64{}
			for _, leg := range legNames {
				edge := trueEdge[leg]
				agrees := rnd.Float64() < edge
				callUp := (up == 1) == agrees
				p := 0.35
				if callUp {
					p = 0.65
				}
				call[cell][leg] = p
			}
		}
		for i := 0; i < perDay; i++ {
			cell := cells[i%len(cells)]
			legs := map[string]float64{}
			for leg, p := range call[cell] {
				legs[leg] = p
			}
			out = append(out, Example{
				Legs:      legs,
				Regime:    cell,
				Ts:        int64(d) * daySecs,
				Up:        up,
				FwdReturn: fwd,
			})
		}
	}
	return out
}

// H4 — ADAPTIVE WEIGHTS WERE A WINNER'S-CURSE ESTIMATOR.
//
// Weight was proportional to max(0, hitRate-0.5) with no interval, no shrinkage
// and no multiplicity control, so the LARGEST weight went to whichever leg got
// luckiest. Here every one of the seven legs is a pure coin flip across five
// regime cells — 35 auditions of nothing — and the luckiest of them posts a
// hit rate well above 0.5 purely by sampling noise. Pre-fix that leg was
// normalised to a dominant learned weight (live: gbm at hitRate 0.5216 over 232
// rows held 93.6% of the "range" cell). Nothing here may earn any weight.
func TestCompute_PureNoisePanelEarnsNoWeight(t *testing.T) {
	rnd := rand.New(rand.NewSource(21))
	noise := map[string]float64{}
	for _, leg := range ensemble.LegNames {
		noise[leg] = 0.5 // pure coin flip: no leg has any true edge
	}
	cells := []string{"uptrend", "downtrend", "range", "squeeze", "chop"}
	exs := clusteredPanel(rnd, 60, 20, cells, noise)

	w := Compute(exs, 0)

	// The winner's curse must actually be PRESENT in the evidence, otherwise
	// this test proves nothing: some leg has to look good by luck.
	var best float64
	var bestName string
	for name, c := range w.Cells {
		for leg, hr := range c.HitRates {
			if hr > best {
				best, bestName = hr, name+"/"+leg
			}
		}
	}
	if best < 0.60 {
		t.Fatalf("panel is not exercising the winner's curse: best noise hit-rate %.4f (%s)", best, bestName)
	}
	// Every leg cleared the floors, so the panel really did audition them.
	if w.Panel.Tests < len(ensemble.LegNames) {
		t.Fatalf("panel tested only %d hypotheses — the floors, not the shrinkage, are doing the work", w.Panel.Tests)
	}
	// …and not one of them may receive weight.
	for name, c := range w.Cells {
		if len(c.Weights) > 0 {
			t.Fatalf("cell %q handed weight to pure noise (best hit-rate in panel %.4f at %s): %+v",
				name, best, bestName, c.Weights)
		}
		if c.Reason == "" {
			t.Fatalf("cell %q withheld weights without stating why", name)
		}
	}
}

// A real, day-spread edge must still survive shrinkage — the fix has to gate
// noise, not gate everything. Pressure agrees with the day's direction 85% of
// the time over 60 days; the other six legs are coin flips.
func TestCompute_RealEdgeSurvivesShrinkage(t *testing.T) {
	rnd := rand.New(rand.NewSource(4242))
	edges := map[string]float64{}
	for _, leg := range ensemble.LegNames {
		edges[leg] = 0.5
	}
	edges[ensemble.LegPressure] = 0.85
	exs := clusteredPanel(rnd, 60, 20, []string{"uptrend", "range"}, edges)

	w := Compute(exs, 0)
	c := w.Cells[AllCell]
	if c.Gated {
		t.Fatalf("60 days x 20 rows must clear both floors: %+v", c)
	}
	if c.Weights[ensemble.LegPressure] <= 0 {
		t.Fatalf("a leg right 85%% of 60 independent days must earn weight: %+v (reason %q)", c.Weights, c.Reason)
	}
	var sum float64
	for leg, v := range c.Weights {
		sum += v
		if leg != ensemble.LegPressure {
			t.Fatalf("coin-flip leg %q earned weight %v", leg, v)
		}
	}
	if math.Abs(sum-1) > 1e-9 {
		t.Fatalf("weights not normalized: %+v (sum %v)", c.Weights, sum)
	}
	// The published edge is the SHRUNK one, and shrinkage only ever pulls
	// toward the no-skill prior — it must never exceed the raw edge.
	rawEdge := c.HitRates[ensemble.LegPressure] - 0.5
	if s := c.ShrunkEdge[ensemble.LegPressure]; s <= 0 || s > rawEdge+1e-12 {
		t.Fatalf("shrunk edge %v must be positive and <= raw edge %v", s, rawEdge)
	}
}

// H5 — SAMPLE FLOORS WERE SATISFIABLE IN ~3 INDEPENDENT DAYS.
//
// 600 rows on a SINGLE UTC day is 30x the old row floor and one market move.
// The cell must be gated, the reason must name days, and the evidence must
// still be reported (transparency was never the problem).
func TestCompute_CellNeedsDistinctDays(t *testing.T) {
	var exs []Example
	for i := 0; i < 600; i++ {
		exs = append(exs, Example{
			Legs:   map[string]float64{ensemble.LegPressure: 0.7},
			Regime: "uptrend",
			// 06:00Z + 10h of rows: 600 rows wholly inside ONE trading day.
			Ts:        6*3600 + int64(i)*60,
			Up:        1,
			FwdReturn: 0.01,
		})
	}
	w := Compute(exs, 0)
	c := w.Cells["uptrend"]
	if c.N != 600 {
		t.Fatalf("row count = %d, want 600", c.N)
	}
	if c.Days != 1 {
		t.Fatalf("distinct days = %d, want 1", c.Days)
	}
	if !c.Gated || c.Weights != nil {
		t.Fatalf("600 rows on ONE day must be gated: gated=%v weights=%+v", c.Gated, c.Weights)
	}
	if !strings.Contains(c.Reason, "day") {
		t.Fatalf("gate reason must name the day floor: %q", c.Reason)
	}
	if c.HitRates[ensemble.LegPressure] != 1.0 {
		t.Fatalf("evidence must still be reported: %+v", c.HitRates)
	}
}

// The per-leg floor has to count days too, or H3's fix just moves the hole: a
// leg present in thousands of rows across three days is still three market
// moves. Here `sentiment` is perfect on 3 days (120 rows — four times the old
// per-leg row floor) inside a cell that spans 40 days.
func TestCompute_LegNeedsOwnDistinctDays(t *testing.T) {
	var exs []Example
	for d := 0; d < 40; d++ {
		up := d % 2
		fwd := 0.01
		if up == 0 {
			fwd = -0.01
		}
		for i := 0; i < 40; i++ {
			legs := map[string]float64{ensemble.LegPressure: 0.65}
			if up == 0 {
				legs[ensemble.LegPressure] = 0.35
			}
			if d < 3 { // sentiment: perfect, but only on three days
				legs[ensemble.LegSentiment] = 0.65
				if up == 0 {
					legs[ensemble.LegSentiment] = 0.35
				}
			}
			exs = append(exs, Example{
				Legs: legs, Regime: "uptrend", Ts: int64(d) * daySecs,
				Up: up, FwdReturn: fwd,
			})
		}
	}
	w := Compute(exs, 0)
	c := w.Cells["uptrend"]
	if c.Gated {
		t.Fatalf("40-day cell must clear the cell floors: %+v", c)
	}
	if c.LegN[ensemble.LegSentiment] != 120 || c.LegDays[ensemble.LegSentiment] != 3 {
		t.Fatalf("sentiment evidence: legN=%d legDays=%d, want 120 rows over 3 days",
			c.LegN[ensemble.LegSentiment], c.LegDays[ensemble.LegSentiment])
	}
	if _, has := c.Weights[ensemble.LegSentiment]; has {
		t.Fatalf("a leg with 3 distinct days must earn NO weight: %+v", c.Weights)
	}
	// It was never even auditioned, so it must not inflate the Bonferroni
	// divisor or the empirical-Bayes variance estimate either.
	if _, tested := c.EdgeLower[ensemble.LegSentiment]; tested {
		t.Fatalf("a floor-excluded leg must not enter the panel: %+v", c.EdgeLower)
	}
}

// Two legs with the SAME raw hit rate but different amounts of independent
// evidence must NOT get the same weight: the thinner one is shrunk harder,
// because shrinkage is a function of its own standard error.
func TestCompute_ShrinkageFavoursMoreDays(t *testing.T) {
	// `pressure` is present on all 50 days, `forecast` on 25 of them; both are
	// right on exactly 80% of the days they call.
	var exs []Example
	for d := 0; d < 50; d++ {
		up := 1
		if d%2 == 0 {
			up = 0
		}
		fwd := 0.01
		if up == 0 {
			fwd = -0.01
		}
		right := 0.65
		wrong := 0.35
		if up == 0 {
			right, wrong = 0.35, 0.65
		}
		pp := right
		if d%5 == 0 { // wrong on 1 day in 5 -> 80%
			pp = wrong
		}
		for i := 0; i < 10; i++ {
			legs := map[string]float64{ensemble.LegPressure: pp}
			if d < 25 {
				fp := right
				if d%5 == 0 {
					fp = wrong
				}
				legs[ensemble.LegForecast] = fp
			}
			exs = append(exs, Example{
				Legs: legs, Regime: "uptrend", Ts: int64(d) * daySecs,
				Up: up, FwdReturn: fwd,
			})
		}
	}
	w := Compute(exs, 0)
	c := w.Cells["uptrend"]
	hp, hf := c.HitRates[ensemble.LegPressure], c.HitRates[ensemble.LegForecast]
	if math.Abs(hp-hf) > 1e-9 {
		t.Fatalf("test setup broken: hit rates differ (%v vs %v)", hp, hf)
	}
	if c.LegDays[ensemble.LegPressure] != 50 || c.LegDays[ensemble.LegForecast] != 25 {
		t.Fatalf("legDays: %+v", c.LegDays)
	}
	if c.ShrunkEdge[ensemble.LegPressure] <= c.ShrunkEdge[ensemble.LegForecast] {
		t.Fatalf("identical raw edge, half the days: forecast must shrink harder — %+v", c.ShrunkEdge)
	}
}

// House invariant: a gate withholds with a STATED reason. Every path that
// produces nil Weights must say why.
func TestCompute_WithheldWeightsAlwaysCarryAReason(t *testing.T) {
	rnd := rand.New(rand.NewSource(7))
	noise := map[string]float64{ensemble.LegPressure: 0.5, ensemble.LegExpectancy: 0.5}
	for _, exs := range [][]Example{
		clusteredPanel(rnd, 2, 20, []string{"uptrend"}, noise),  // fails the day floor
		clusteredPanel(rnd, 40, 20, []string{"uptrend"}, noise), // clears floors, no edge
	} {
		w := Compute(exs, 0)
		for name, c := range w.Cells {
			if len(c.Weights) == 0 && c.Reason == "" {
				t.Fatalf("cell %q withheld weights with no reason: %+v", name, c)
			}
		}
	}
}
