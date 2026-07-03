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
//     MinCellSamples labeled examples; below that the caller falls back to the
//     "all" cell, and below that to the static equal prior (RawProbability's
//     existing behavior).
//   - Weight is proportional to max(0, hitRate-0.5) — edge OVER a coin flip.
//     A leg that cannot beat 50/50 gets exactly zero weight, and if NO leg
//     beats it the cell yields no learned weights at all (static prior again,
//     never a fabricated tilt).
//   - The sentiment leg additionally needs MinCellSamples of ITS OWN examples
//     in the cell before it can receive ANY weight: it is the newest, least
//     proven component and must earn its way in.
//
// Every cell carries its sample counts, per-leg hit-rates, and ICs, so the UI
// can show exactly WHY a weight is what it is (or why the gate refused).
package adaptive

import (
	"math"
	"sort"

	"github.com/nyaungnicholas-wq/signaldeck/internal/ensemble"
)

// MinCellSamples is the minimum number of labeled examples a regime cell (or
// a single leg, for the sentiment gate) needs before learned weights are
// trusted. Mirrors ensemble.MinCalibrationPairs: below this, apparent edge is
// indistinguishable from noise.
const MinCellSamples = 30

// AllCell is the key of the aggregate cell that pools every labeled example
// regardless of regime — the first fallback when a regime cell is too thin.
const AllCell = "all"

// MetaKey is the versioned meta key the computed weights are persisted under.
const MetaKey = "adaptive_weights:v1"

// Example is one labeled training row: the component legs' up-probabilities
// exactly as the ensemble saw them at prediction time, the regime label the
// symbol was in, and the realized outcome.
type Example struct {
	Legs      map[string]float64 // leg name -> P(up) leg, only legs present
	Regime    string             // regime label ("" = unknown)
	Up        int                // realized 1/0
	FwdReturn float64            // realized forward return
}

// Cell is the measured evidence + derived weights for one regime cell.
type Cell struct {
	// N is the number of labeled examples in the cell.
	N int `json:"n"`
	// Weights are the learned normalized blend weights per leg. nil when the
	// honesty gate refused (Gated) or when no leg beat the coin flip.
	Weights map[string]float64 `json:"weights,omitempty"`
	// HitRates is each leg's directional hit-rate (sign(p-0.5) vs realized
	// direction) over the examples where the leg was present and directional.
	HitRates map[string]float64 `json:"hitRates,omitempty"`
	// IC is each leg's Pearson correlation between leg probability and
	// realized forward return over the examples where the leg was present.
	IC map[string]float64 `json:"ic,omitempty"`
	// LegN is the per-leg sample count (a leg can be rarer than the cell,
	// e.g. sentiment only exists where fresh headlines existed).
	LegN map[string]int `json:"legN,omitempty"`
	// Gated is true when the cell has fewer than MinCellSamples examples, so
	// its learned weights are withheld (Weights==nil) by the honesty gate.
	Gated bool `json:"gated"`
}

// Weights is the versioned, persisted output of one attribution pass.
type Weights struct {
	ComputedTs int64           `json:"computedTs"`
	Cells      map[string]Cell `json:"cells"`
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
		out.Cells[name] = computeCell(exs)
	}
	return out
}

// computeCell measures one cell's per-leg evidence and derives its weights.
func computeCell(exs []Example) Cell {
	c := Cell{
		N:        len(exs),
		HitRates: map[string]float64{},
		IC:       map[string]float64{},
		LegN:     map[string]int{},
	}
	// Per-leg evidence.
	for _, leg := range ensemble.LegNames {
		var n, dirN, hits int
		var ps, fwds []float64
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
			if (p > 0.5 && ex.Up == 1) || (p < 0.5 && ex.Up == 0) {
				hits++
			}
		}
		if n == 0 {
			continue
		}
		c.LegN[leg] = n
		if dirN > 0 {
			c.HitRates[leg] = float64(hits) / float64(dirN)
		}
		if ic, ok := pearson(ps, fwds); ok {
			c.IC[leg] = ic
		}
	}
	// Honesty gate: too few examples in the cell -> evidence is shown but
	// weights are withheld.
	if c.N < MinCellSamples {
		c.Gated = true
		return c
	}
	// Weight ∝ max(0, hitRate-0.5); sentiment needs its own leg sample gate
	// before receiving ANY weight.
	raw := map[string]float64{}
	var sum float64
	for leg, hr := range c.HitRates {
		if leg == ensemble.LegSentiment && c.LegN[leg] < MinCellSamples {
			continue
		}
		if edge := hr - 0.5; edge > 0 {
			raw[leg] = edge
			sum += edge
		}
	}
	if sum <= 0 {
		// No leg beat the coin flip: an honest "nothing learned here yet".
		return c
	}
	c.Weights = map[string]float64{}
	for leg, edge := range raw {
		c.Weights[leg] = edge / sum
	}
	return c
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
