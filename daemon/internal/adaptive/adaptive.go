// Package adaptive closes SignalDeck's learning loop: it measures, per market
// regime, how well each ensemble component leg actually predicted resolved
// outcomes (directional hit-rate + information coefficient), turns measured
// edge into blend weights, and persists them versioned — so the ensemble's
// weights are LEARNED from the system's own graded history instead of
// asserted.
//
// # Honesty doctrine (non-negotiable)
//
// No component receives weight without measured evidence:
//
//   - A regime cell yields learned weights only when it holds at least
//     MinCellSamples labeled rows AND those rows span at least MinCellDays
//     DISTINCT UTC DAYS; below that the caller falls back to the "all" cell,
//     and below that to the static equal prior (RawProbability's existing
//     behavior).
//   - EVERY leg additionally needs MinCellSamples rows and MinCellDays days of
//     ITS OWN in the cell before it can receive ANY weight. Legs can be much
//     rarer than the cell (a gated model leg only exists where its OOS gate
//     passed), and without the per-leg floor 3 lucky alphax examples inside a
//     40-row cell were enough to hand it a 0.77 learned weight (adversarial
//     finding H3).
//   - Weight is proportional to the SHRUNK edge over a coin flip, and only for
//     legs whose raw edge clears a Bonferroni-adjusted lower bound across the
//     whole leg x cell panel (H4). A leg that cannot beat 50/50 by more than
//     its own noise gets exactly zero weight, and if NO leg does, the cell
//     yields no learned weights at all (static prior again, never a fabricated
//     tilt). See derivePanelWeights for why each of the three corrections
//     exists.
//
// # Rows are not observations
//
// The predictor runs every ~10 minutes against DAILY labels and roughly a
// thousand symbols share each day's market move, so the labeled set is ~12 rows
// per symbol-day and only a few dozen distinct days wide. Every floor and every
// standard error in this package therefore counts DISTINCT UTC DAYS. Counting
// rows is what let a leg buy a dominant weight with three days of luck.
//
// Every cell carries its row counts, its DAY counts, per-leg hit-rates, ICs,
// shrunk edges and lower bounds, so the UI can show exactly WHY a weight is
// what it is — and a cell that yields no weights always states its reason.
package adaptive

import (
	"fmt"
	"math"
	"sort"

	"github.com/nyaungnicholas-wq/signaldeck/internal/ensemble"
)

// MinCellSamples is the minimum number of labeled ROWS a regime cell — and,
// per leg, EACH LEG within it — needs before learned weights are trusted. It
// bounds the raw evidence only; MinCellDays is the floor that actually binds.
const MinCellSamples = 30

// MinCellDays is the minimum number of DISTINCT UTC DAYS a regime cell — and,
// per leg, EACH LEG within it — must span before learned weights are trusted.
//
// Rows are not observations. The predictor runs every 10 minutes against DAILY
// labels and roughly a thousand symbols share each day's market move, so the
// SUPERSEDED-SNAPSHOT figures below are the dated 2026-07-25 measurement that
// sized this floor, not the current record:
// live labeled set is 158,204 rows over 13,058 symbol-days over 23 DISTINCT
// DAYS — 12.1 rows per symbol-day, and one measured case of 153 rows inside a
// single symbol-day. Under the old row-only floor a cell cleared n>=30 on
// about three independent days of evidence, and the "per-leg floor" added for
// H3 counted the same inflated unit.
//
// 20 days generalises metalabel.MinTakenDays, which is the only floor in the
// repo that already counted the right unit and states the reason: same-day
// rows are not independent evidence and a gate measured on a handful of days
// is mostly learning WHICH DAYS were good. At 20 days a hit rate's standard
// error under the no-skill null is 0.5/sqrt(20) = 0.112, so a leg must beat a
// coin flip by ~11 points before one standard error separates it from noise —
// which is the honest scale of what 20 market days can show.
const MinCellDays = 20

// AllCell is the key of the aggregate cell that pools every labeled example
// regardless of regime — the first fallback when a regime cell is too thin.
const AllCell = "all"

// MetaKey is the versioned meta key the computed weights are persisted under.
const MetaKey = "adaptive_weights:v1"

// Example is one labeled training row: the component legs' up-probabilities
// exactly as the ensemble saw them at prediction time, the regime label the
// symbol was in, and the realized outcome.
type Example struct {
	Legs   map[string]float64 // leg name -> P(up) leg, only legs present
	Regime string             // regime label ("" = unknown)
	// Ts is the prediction's unix timestamp. Its UTC day is the independence
	// unit every floor in this package counts: an example with Ts==0 lands on
	// day 0 with every other unstamped example, so a caller that forgets to
	// stamp gets ONE day of evidence and the honesty gate refuses — the
	// fail-safe direction.
	Ts        int64
	Up        int     // realized 1/0
	FwdReturn float64 // realized forward return
}

// Cell is the measured evidence + derived weights for one regime cell.
type Cell struct {
	// N is the number of labeled ROWS in the cell. It is not a sample size.
	N int `json:"n"`
	// Days is the number of DISTINCT UTC DAYS the cell's rows span — the
	// honest sample size, and the unit every floor here counts.
	Days int `json:"days"`
	// Weights are the learned normalized blend weights per leg. nil when the
	// honesty gate refused (Gated) or when no leg's edge survived shrinkage
	// and the panel-wide multiplicity correction. Reason always says which.
	Weights map[string]float64 `json:"weights,omitempty"`
	// HitRates is each leg's directional hit-rate (sign(p-0.5) vs realized
	// direction) over the examples where the leg was present and directional.
	HitRates map[string]float64 `json:"hitRates,omitempty"`
	// IC is each leg's Pearson correlation between leg probability and
	// realized forward return over the examples where the leg was present.
	IC map[string]float64 `json:"ic,omitempty"`
	// LegN is the per-leg ROW count (a leg can be rarer than the cell,
	// e.g. sentiment only exists where fresh headlines existed).
	LegN map[string]int `json:"legN,omitempty"`
	// LegDays is the per-leg count of DISTINCT UTC DAYS on which the leg made
	// a directional call — the denominator its standard error is computed
	// from, and the per-leg floor MinCellDays is checked against.
	LegDays map[string]int `json:"legDays,omitempty"`
	// ShrunkEdge is each tested leg's hit-rate edge AFTER empirical-Bayes
	// shrinkage toward the no-skill prior (0.5). It is what the weight is
	// proportional to; the raw edge never is.
	ShrunkEdge map[string]float64 `json:"shrunkEdge,omitempty"`
	// EdgeLower is each tested leg's one-sided lower confidence bound on its
	// raw edge at the Bonferroni-adjusted level for the whole leg x cell
	// panel. A leg earns weight only when this is above zero — i.e. when it
	// beat the coin flip by more than its own noise, allowing for how many
	// legs were auditioned.
	EdgeLower map[string]float64 `json:"edgeLower,omitempty"`
	// Gated is true when the cell itself failed a floor (rows or distinct
	// days), so its learned weights are withheld (Weights==nil).
	Gated bool `json:"gated"`
	// Reason states plainly why Weights is nil, and is never empty when it is.
	Reason string `json:"reason,omitempty"`
}

// Panel records the multiplicity/shrinkage context one attribution pass ran
// under, so a reader can reconstruct why a given edge did or did not earn
// weight without re-deriving it.
type Panel struct {
	// Tests is the number of (cell, leg) hypotheses that cleared the floors
	// and were auditioned — the Bonferroni divisor.
	Tests int `json:"tests"`
	// Alpha is the family-wise one-sided error rate the panel was held to.
	Alpha float64 `json:"alpha"`
	// Z is the normal quantile the per-leg lower bound used (alpha/Tests).
	Z float64 `json:"z"`
	// Tau2 is the empirical-Bayes estimate of the BETWEEN-leg variance of
	// true edges, about the no-skill point. Zero means the observed spread of
	// hit rates is fully explained by sampling noise, so every edge shrinks to
	// nothing — which is the honest reading of a panel of coin flips.
	Tau2 float64 `json:"tau2"`
}

// Weights is the versioned, persisted output of one attribution pass.
type Weights struct {
	ComputedTs int64           `json:"computedTs"`
	Cells      map[string]Cell `json:"cells"`
	Panel      Panel           `json:"panel"`
}

// Compute runs per-regime component attribution over labeled examples and
// derives gated weights. Every distinct regime label seen becomes a cell,
// plus the AllCell aggregate over everything (examples with an unknown
// regime count only toward AllCell).
func Compute(examples []Example, nowTs int64) Weights {
	byCell := map[string][]Example{}
	for _, ex := range examples {
		byCell[AllCell] = append(byCell[AllCell], ex)
		if ex.Regime != "" {
			byCell[ex.Regime] = append(byCell[ex.Regime], ex)
		}
	}
	out := Weights{ComputedTs: nowTs, Cells: map[string]Cell{}}
	for name, exs := range byCell {
		out.Cells[name] = measureCell(exs)
	}
	// Weights are derived across the WHOLE leg x cell panel at once, not cell
	// by cell: shrinkage needs the panel to estimate how much of the observed
	// spread in hit rates is real, and the multiplicity correction needs to
	// know how many hypotheses were auditioned. Deriving per cell in isolation
	// is what made the old estimator a winner's-curse machine.
	out.Panel = derivePanelWeights(out.Cells)
	return out
}

// utcDay maps a unix timestamp to its UTC day index. This is the house unit:
// one observation per (symbol, UTC-day), never a raw row.
func utcDay(ts int64) int64 { return ts / 86400 }

// DistinctDays counts the distinct UTC days a set of examples spans — the
// honest sample size for anything measured over them.
func DistinctDays(exs []Example) int {
	days := make(map[int64]struct{}, len(exs))
	for _, ex := range exs {
		days[utcDay(ex.Ts)] = struct{}{}
	}
	return len(days)
}

// measureCell measures one cell's per-leg evidence and applies the CELL-level
// floors. It derives no weights: that happens across the panel.
func measureCell(exs []Example) Cell {
	c := Cell{
		N:        len(exs),
		Days:     DistinctDays(exs),
		HitRates: map[string]float64{},
		IC:       map[string]float64{},
		LegN:     map[string]int{},
		LegDays:  map[string]int{},
	}
	// Per-leg evidence.
	for _, leg := range ensemble.LegNames {
		var n, dirN, hits int
		var ps, fwds []float64
		dirDays := map[int64]struct{}{}
		for _, ex := range exs {
			p, ok := ex.Legs[leg]
			if !ok {
				continue
			}
			n++
			ps = append(ps, p)
			fwds = append(fwds, ex.FwdReturn)
			// Directional hit-rate: p==0.5 makes no call, so it neither
			// helps nor hurts.
			if p == 0.5 {
				continue
			}
			dirN++
			// Days are counted over DIRECTIONAL rows only, because the days a
			// leg abstained on carry no information about its hit rate and
			// must not inflate the denominator of its standard error.
			dirDays[utcDay(ex.Ts)] = struct{}{}
			if (p > 0.5 && ex.Up == 1) || (p < 0.5 && ex.Up == 0) {
				hits++
			}
		}
		if n == 0 {
			continue
		}
		c.LegN[leg] = n
		c.LegDays[leg] = len(dirDays)
		if dirN > 0 {
			c.HitRates[leg] = float64(hits) / float64(dirN)
		}
		if ic, ok := pearson(ps, fwds); ok {
			c.IC[leg] = ic
		}
	}
	// Honesty gates: evidence is always shown, weights are withheld with a
	// stated reason. The ROW floor bounds raw evidence; the DAY floor is the
	// one that binds, because the predictor runs every 10 minutes against
	// daily labels and ~1,000 symbols share each day's market move.
	switch {
	case c.N < MinCellSamples:
		c.Gated = true
		c.Reason = fmt.Sprintf("cell holds %d labeled row(s), below the %d-row floor", c.N, MinCellSamples)
	case c.Days < MinCellDays:
		c.Gated = true
		c.Reason = fmt.Sprintf(
			"cell's %d row(s) span only %d distinct UTC day(s), below the %d-day floor — "+
				"same-day rows share one market move, so they are not independent evidence",
			c.N, c.Days, MinCellDays)
	}
	return c
}

// panelAlpha is the FAMILY-WISE one-sided error rate the whole leg x cell
// panel is held to. It is family-wise, not per-test, because the panel is a
// selection procedure: the leg that ends up with the largest weight is by
// construction the one that looked best across every audition, so its apparent
// edge must clear a bar that accounts for how many auditions there were.
const panelAlpha = 0.05

// candidate is one (cell, leg) hypothesis that cleared both floors.
type candidate struct {
	cell string
	leg  string
	edge float64 // measured hitRate - 0.5
	se   float64 // standard error of that edge under the no-skill null
}

// derivePanelWeights turns measured evidence into weights across the whole
// panel, and is the fix for H4 — the old estimator set weight ∝ max(0,
// hitRate-0.5) with no interval, no shrinkage and no multiplicity control, so
// the largest weight went to whichever leg got luckiest. Live proof at the time
// of the fix: the "range" cell gave 93.6% of its blend to gbm on a hit rate of
// 0.5216 over 232 rows, and the "all" cell gave 100% to sentiment.
//
// Three corrections, in the order they apply:
//
//  1. THE STANDARD ERROR IS COMPUTED OVER DAYS, NOT ROWS. A leg's hit rate is
//     measured on rows, but the rows repeat one call across a day's symbols, so
//     0.5/sqrt(rows) understates the noise by roughly sqrt(12) on this
//     platform's data. se = 0.5/sqrt(days) under the no-skill null p=0.5 —
//     null-centred deliberately, since the question is whether the leg beat a
//     coin flip, not how precisely its own rate is pinned down.
//
//  2. EMPIRICAL-BAYES SHRINKAGE TOWARD THE NO-SKILL PRIOR. The panel's spread
//     of edges has two parts: real between-leg differences (tau^2) and sampling
//     noise (mean se^2). Estimating tau^2 = max(0, mean(edge^2) - mean(se^2))
//     and shrinking each edge by tau^2/(tau^2+se^2) is the James-Stein/
//     Efron-Morris form with the prior centred at the no-skill point, which is
//     the correct centre here: absent evidence, a leg predicts nothing. When
//     the observed spread is fully explained by noise, tau^2 is 0 and every
//     edge shrinks to exactly nothing.
//
//  3. BONFERRONI-ADJUSTED LOWER BOUND. A leg earns weight only if
//     edge - z(alpha/tests)*se > 0 — it must beat the coin flip by more than
//     its own noise, allowing for every leg auditioned. Bonferroni is
//     conservative here in one direction and optimistic in another, and both
//     are worth stating: the cells overlap (every row is in AllCell and in its
//     regime cell) so the tests are not independent, while the per-symbol
//     learner runs this same panel once per symbol, so the true family is far
//     larger than any single call can see.
//
// Weight is then proportional to the SHRUNK edge among survivors, normalised
// within each cell. If nothing survives, the cell yields no learned weights and
// says why — the existing static-prior fallback, never a fabricated tilt.
func derivePanelWeights(cells map[string]Cell) Panel {
	var cands []candidate
	for _, name := range sortedCellNames(cells) {
		c := cells[name]
		if c.Gated {
			continue
		}
		for _, leg := range ensemble.LegNames {
			hr, ok := c.HitRates[leg]
			if !ok {
				continue
			}
			// EVERY leg must clear its OWN floors (H3, generalised to days by
			// H5): a leg present in a handful of the cell's rows, or in
			// thousands of rows spanning three days, can post a perfect hit
			// rate by pure luck. 3/3 alphax hits in a 40-row cell once earned
			// it a 0.77 weight.
			if c.LegN[leg] < MinCellSamples || c.LegDays[leg] < MinCellDays {
				continue
			}
			cands = append(cands, candidate{
				cell: name, leg: leg,
				edge: hr - 0.5,
				se:   0.5 / math.Sqrt(float64(c.LegDays[leg])),
			})
		}
	}

	p := Panel{Tests: len(cands), Alpha: panelAlpha}
	if len(cands) == 0 {
		annotateNoCandidates(cells)
		return p
	}

	var meanEdgeSq, meanVar float64
	for _, cd := range cands {
		meanEdgeSq += cd.edge * cd.edge
		meanVar += cd.se * cd.se
	}
	n := float64(len(cands))
	meanEdgeSq /= n
	meanVar /= n
	p.Tau2 = math.Max(0, meanEdgeSq-meanVar)
	p.Z = zOneSided(panelAlpha / n)

	byCell := map[string][]candidate{}
	for _, cd := range cands {
		byCell[cd.cell] = append(byCell[cd.cell], cd)
	}
	for name, cds := range byCell {
		c := cells[name]
		c.ShrunkEdge = map[string]float64{}
		c.EdgeLower = map[string]float64{}
		survivors := map[string]float64{}
		var sum float64
		for _, cd := range cds {
			var shrink float64
			if p.Tau2 > 0 {
				shrink = p.Tau2 / (p.Tau2 + cd.se*cd.se)
			}
			shrunk := shrink * cd.edge
			lower := cd.edge - p.Z*cd.se
			c.ShrunkEdge[cd.leg] = shrunk
			c.EdgeLower[cd.leg] = lower
			if shrunk > 0 && lower > 0 {
				survivors[cd.leg] = shrunk
				sum += shrunk
			}
		}
		if sum > 0 {
			c.Weights = map[string]float64{}
			for leg, e := range survivors {
				c.Weights[leg] = e / sum
			}
			c.Reason = ""
		} else {
			c.Reason = fmt.Sprintf(
				"none of the %d leg(s) with enough evidence beat a coin flip by more than their own noise "+
					"(family-wise alpha %.2f over %d panel test(s); a leg needs edge > %.3f x its standard error) "+
					"— the cell keeps the static equal prior",
				len(cds), panelAlpha, p.Tests, p.Z)
		}
		cells[name] = c
	}
	// Ungated cells whose legs were ALL floor-excluded never entered byCell.
	annotateNoCandidates(cells)
	return p
}

// annotateNoCandidates gives every ungated cell that produced no weights a
// stated reason, so no surface ever shows a withheld number without one.
func annotateNoCandidates(cells map[string]Cell) {
	for name, c := range cells {
		if c.Gated || len(c.Weights) > 0 || c.Reason != "" {
			continue
		}
		c.Reason = fmt.Sprintf(
			"no leg in this cell cleared its own floors (%d row(s) and %d distinct UTC day(s) each) "+
				"— the cell keeps the static equal prior",
			MinCellSamples, MinCellDays)
		cells[name] = c
	}
}

// zOneSided returns z such that P(Z > z) = p for a standard normal — the
// quantile the Bonferroni-adjusted lower bound is built from.
func zOneSided(p float64) float64 {
	if p <= 0 {
		return math.Inf(1)
	}
	if p >= 1 {
		return math.Inf(-1)
	}
	return math.Sqrt2 * math.Erfinv(1-2*p)
}

func sortedCellNames(cells map[string]Cell) []string {
	names := make([]string, 0, len(cells))
	for n := range cells {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Gate names Pick reports, so callers (API/UI) can say exactly which
// fallback tier produced the weights in use.
const (
	GateLearnedRegime = "learned:regime" // the symbol's own regime cell
	GateLearnedAll    = "learned:all"    // the pooled "all" cell
	GateStatic        = "static"         // equal prior (RawProbability)
)

// Pick selects the weights to use for a symbol in the given regime, applying
// the fallback chain: regime cell (if it yielded weights) -> AllCell (if IT
// yielded weights) -> static equal prior (nil weights; callers pass nil to
// ensemble.WeightedProbability, which then behaves as RawProbability).
// The returned gate string reports which tier applied.
func Pick(w Weights, regime string) (map[string]float64, string) {
	if regime != "" && regime != AllCell {
		if c, ok := w.Cells[regime]; ok && len(c.Weights) > 0 {
			return c.Weights, GateLearnedRegime
		}
	}
	if c, ok := w.Cells[AllCell]; ok && len(c.Weights) > 0 {
		return c.Weights, GateLearnedAll
	}
	return nil, GateStatic
}

// FromVector reconstructs the component legs + regime label from a persisted
// feature vector (the features table row the PredictionRunner wrote). It
// rebuilds ensemble.Components from the stored fields and derives the legs
// through the SAME code path the live blend uses, so attribution grades
// exactly what the ensemble did — no reimplementation drift.
func FromVector(vec map[string]float64) (legs map[string]float64, regime string) {
	c := ensemble.Components{PressureScore: vec["pressure_score"]}
	if v, ok := vec["expectancy_hit_rate"]; ok {
		c.ExpectancyHitRate = &v
	}
	if v, ok := vec["forecast_prob"]; ok {
		c.ForecastProb = &v
	}
	if v, ok := vec["forecast_lift"]; ok {
		c.ForecastLift = &v
	}
	if v, ok := vec["sentiment_score"]; ok {
		c.SentimentScore = &v
	}
	// STAGE 6 gated model legs. Their stored prob is present in the vector only
	// when the leg passed its OOS gate at prediction time (buildFeatureVector
	// writes them gated), so a positive lift sentinel here reconstructs exactly
	// the leg the live blend used — no reimplementation drift. We pass a
	// positive lift so LegProbabilities re-derives the same leg it did live.
	posLift := 1.0
	if v, ok := vec["gbm_prob"]; ok {
		c.GBMProb = &v
		c.GBMLift = &posLift
	}
	if v, ok := vec["meanrev_prob"]; ok {
		c.MeanRevProb = &v
		c.MeanRevLift = &posLift
	}
	if v, ok := vec["alphax_prob"]; ok {
		c.AlphaXProb = &v
		c.AlphaXLift = &posLift
	}
	for k, v := range vec {
		if v == 1 && len(k) > len("regime_") && k[:len("regime_")] == "regime_" {
			regime = k[len("regime_"):]
			break
		}
	}
	return ensemble.LegProbabilities(c), regime
}

// MaxWeightShift returns the largest absolute per-leg weight change between
// two weight sets, across all cells present in either (a missing cell/leg
// counts as weight 0). Drives the "weights materially changed" insight.
func MaxWeightShift(a, b Weights) float64 {
	var maxShift float64
	for _, name := range unionCellNames(a, b) {
		aw := a.Cells[name].Weights
		bw := b.Cells[name].Weights
		for _, leg := range ensemble.LegNames {
			if d := math.Abs(aw[leg] - bw[leg]); d > maxShift {
				maxShift = d
			}
		}
	}
	return maxShift
}

func unionCellNames(a, b Weights) []string {
	set := map[string]struct{}{}
	for n := range a.Cells {
		set[n] = struct{}{}
	}
	for n := range b.Cells {
		set[n] = struct{}{}
	}
	names := make([]string, 0, len(set))
	for n := range set {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// pearson computes the Pearson correlation of x and y (same shape as
// breakout's pearsonTail, over full slices). ok=false when n<3 or either
// side has zero variance — correlation is undefined, not zero.
func pearson(x, y []float64) (float64, bool) {
	n := len(x)
	if n < 3 || len(y) != n {
		return 0, false
	}
	var sx, sy float64
	for i := 0; i < n; i++ {
		sx += x[i]
		sy += y[i]
	}
	mx, my := sx/float64(n), sy/float64(n)
	var cov, vx, vy float64
	for i := 0; i < n; i++ {
		dx, dy := x[i]-mx, y[i]-my
		cov += dx * dy
		vx += dx * dx
		vy += dy * dy
	}
	if vx <= 0 || vy <= 0 {
		return 0, false
	}
	r := cov / math.Sqrt(vx*vy)
	if r > 1 {
		r = 1
	} else if r < -1 {
		r = -1
	}
	return r, true
}
