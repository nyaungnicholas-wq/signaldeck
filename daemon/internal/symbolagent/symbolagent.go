// Package symbolagent gives every symbol its OWN agent: weights, calibration,
// per-component skill, a plain-English personality, and an honest evidence
// tier — all learned ONLY from that symbol's own resolved outcomes.
//
// "Each stock has its own agent, since each behaves differently." This is the
// HONEST, efficient realization of that: ONE worker computes every symbol's
// model on a cheap upsert. There is no process or goroutine per stock — a
// symbol's specialization is a row, not a thread.
//
// # No leakage (the whole point)
//
// A symbol's model is fit from LabeledFeaturesBySymbol: feature vectors joined
// to their ALREADY-RESOLVED outcomes. A prediction becomes a training example
// only after its own outcome is realized, so:
//
//   - per-component hit-rate + IC + weights are measured on the symbol's own
//     graded history via the SAME adaptive attribution the global layer uses
//     (adaptive.Compute over one pooled cell — no reimplementation drift);
//   - per-symbol calibration is fit prequentially: the isotonic map is trained
//     on the symbol's resolved (raw-prob, outcome) pairs, then applied LIVE to
//     a fresh raw prob whose own outcome is still unresolved and therefore
//     absent from the training set. A point never trains on its own outcome.
//
// # Honesty gate (tier)
//
// A per-symbol model is only trusted when the symbol has earned it:
//
//	personal  n >= MinPersonal rows of the symbol's OWN resolved outcomes for
//	          this horizon, spanning >= MinPersonalDays DISTINCT UTC DAYS
//	          -> use its own weights + its own calibration;
//	regime    otherwise -> the caller falls back to the global per-regime
//	          learned weights (the symbol's current regime cell);
//	global    otherwise -> the pooled global weights;
//	static    otherwise -> equal-weight prior.
//
// Below either floor the model is still stored (so the UI can show "still
// learning n/30 days"), but its weights/calibration are NOT marked personal and
// the live predictor must not use them — it uses the global fallbacks instead.
//
// # Rows are not observations
//
// The predictor runs every ~10 minutes against daily labels, so a symbol
// accrues ~12 rows per symbol-day and a 40-ROW floor is roughly three market
// moves. Every floor here counts DISTINCT UTC DAYS; the row floors are kept
// only as a secondary bound on raw evidence.
package symbolagent

import (
	"fmt"
	"math"
	"sort"

	"github.com/nyaungnicholas-wq/signaldeck/internal/adaptive"
	"github.com/nyaungnicholas-wq/signaldeck/internal/ensemble"
)

// MinPersonal is the number of a symbol's OWN resolved ROWS (for one horizon)
// required before its personally-learned weights + calibration are trusted.
// Mirrors adaptive.MinCellSamples / ensemble.MinCalibrationPairs, and like
// them it bounds RAW evidence only — it is not a sample size. MinPersonalDays
// is the floor that binds.
const MinPersonal = 40

// MinPersonalDays is the number of DISTINCT UTC DAYS the symbol's own resolved
// outcomes must span before its personal weights + calibration are trusted.
//
// MinPersonal counts ROWS, and rows are not observations: the predictor runs
// every 10 minutes against daily labels, so a symbol accumulates ~12 rows per
// symbol-day. SUPERSEDED-SNAPSHOT, the dated measurement that sized this floor
// and not the current record: 158,204 resolved rows are 13,058 symbol-days.
// 1,045 of 1,050 symbols cleared the 40-ROW floor for the 1d horizon on a
// median of 12 distinct days — a personal model, the strongest per-symbol
// claim this platform makes, bought with about a fortnight of evidence.
//
// 30 days preserves the doctrine MinPersonal was chosen under — a personal
// model needs real margin over the shared floors (adaptive.MinCellDays and
// ensemble.MinCalibrationDays, both 20) before it displaces the far
// larger-sampled global model. 30 distinct days is roughly six trading weeks,
// and at n=30 the standard error of a hit rate under the no-skill null is
// 0.5/sqrt(30) = 0.091 — so a personal edge must exceed ~9 points before one
// standard error separates it from a coin flip.
const MinPersonalDays = 30

// Tier names the evidence tier the active model rests on (most→least specific).
const (
	TierPersonal = "personal"
	TierRegime   = "regime"
	TierGlobal   = "global"
	TierStatic   = "static"
)

// Skill is one component's measured predictive edge for this symbol.
type Skill struct {
	HitRate float64 `json:"hitRate"` // directional hit-rate (sign(p-0.5) vs realized), [0,1]
	IC      float64 `json:"ic"`      // Pearson corr(leg prob, realized fwd return), [-1,1]
	N       int     `json:"n"`       // examples where this component was present
	HasHR   bool    `json:"hasHR"`   // hit-rate is measured (component made directional calls)
	HasIC   bool    `json:"hasIC"`   // IC is measured (>=3 present examples, non-zero variance)
}

// Calibration is a persistable per-symbol isotonic recalibration map.
type Calibration struct {
	KX     []float64 `json:"kx"`     // strictly-increasing prediction knots
	KY     []float64 `json:"ky"`     // non-decreasing calibrated frequencies
	Fitted bool      `json:"fitted"` // false => identity (not enough evidence)
}

// Map rebuilds the live recalibration closure (identity when not fitted).
func (c Calibration) Map() func(float64) float64 {
	if !c.Fitted {
		return func(v float64) float64 { return clamp01(v) }
	}
	return ensemble.MapFromKnots(c.KX, c.KY)
}

// Model is one symbol+horizon agent: what it learned from its own history.
type Model struct {
	NSamples int `json:"nSamples"` // the symbol's own resolved ROWS, this horizon
	// NDays is how many DISTINCT UTC DAYS those rows span — the honest sample
	// size, and what the graduation gate is decided on. Not persisted as a
	// column; Personality carries it to the UI.
	NDays       int                `json:"nDays"`
	Tier        string             `json:"tier"`        // personal|regime|global|static
	Weights     map[string]float64 `json:"weights"`     // personal blend weights (nil unless personal)
	Calibration Calibration        `json:"calibration"` // personal calibration (identity unless personal)
	Skill       map[string]Skill   `json:"skill"`       // per-component measured edge
	Personality string             `json:"personality"` // deterministic plain-English read
	// TierBlocker names the FIRST unmet condition keeping this symbol off the
	// personal tier, empty when it reached it.
	//
	// It exists because "0 personal" was reported every hour for weeks with no
	// indication of WHY, and finding out took tracing four layers: the learner,
	// the tier gate, the adaptive panel, and finally the stored weights blob,
	// where every regime cell held EMPTY weights because it carried 14-16
	// distinct days against adaptive.MinCellDays of 20. Every symbol therefore
	// fell through to the fleet-wide calibration map, which is the mechanism
	// behind the 2026-07-27..08-04 cross-section collapse.
	//
	// A floor that blocks silently is indistinguishable from a broken feature.
	// This is not persisted as a column; the worker aggregates it into its run
	// detail so the binding constraint is legible from the Agents page.
	TierBlocker string `json:"-"`
}

// Learn computes a symbol's model from its OWN labeled examples plus the tier
// of global fallback available to it. It never fabricates: below MinPersonal
// rows OR MinPersonalDays distinct days the returned model carries the measured
// skill (so the UI shows what it has) but Tier is the best AVAILABLE fallback
// and Weights is nil / Calibration is identity, so the predictor uses the
// global model.
//
//   - examples:      the symbol's own resolved LabeledFeatures → adaptive.Example
//     (legs + regime + up + fwdReturn), built by the caller via
//     adaptive.FromVector. rawPairs pairs each example's stored
//     raw prob with its realized outcome for calibration.
//   - rawPairs:      (Pred=stored raw blend prob, Actual=realized up) for THIS
//     symbol+horizon, one per example — prequential by
//     construction (each Actual was realized after its Pred).
//   - regimeLearned: whether the symbol's current regime cell yielded global
//     weights (adaptive.GateLearnedRegime).
//   - globalLearned: whether the pooled global ("all") cell yielded weights.
//
// nowTs stamps nothing here (kept deterministic); the worker owns updated_ts.
func Learn(examples []adaptive.Example, rawPairs []ensemble.Pair, regimeLearned, globalLearned bool) Model {
	m := Model{
		NSamples: len(examples),
		NDays:    adaptive.DistinctDays(examples),
		Skill:    map[string]Skill{},
	}

	// Measure per-component skill + candidate weights via the SAME attribution
	// the global layer uses, over ONE pooled cell (this symbol's history). The
	// AllCell aggregates every example regardless of regime — exactly the
	// symbol-level view we want.
	attr := adaptive.Compute(examples, 0)
	cell := attr.Cells[adaptive.AllCell]
	for _, leg := range ensemble.LegNames {
		n := cell.LegN[leg]
		if n == 0 {
			continue
		}
		sk := Skill{N: n}
		if hr, ok := cell.HitRates[leg]; ok {
			sk.HitRate, sk.HasHR = hr, true
		}
		if ic, ok := cell.IC[leg]; ok {
			sk.IC, sk.HasIC = ic, true
		}
		m.Skill[leg] = sk
	}

	// Tier gate. A symbol EARNS a personal model at MinPersonal of its own
	// resolved outcomes spanning MinPersonalDays DISTINCT UTC DAYS, AND only if
	// that history actually yielded weights that survived the adaptive panel's
	// shrinkage and multiplicity correction (cell.Weights non-empty).
	// Otherwise fall back down the same chain the global picker uses.
	//
	// The day floor is the one that binds, and it is not redundant with the row
	// floor: the predictor writes ~12 rows per symbol-day, so 40 rows is about
	// three market moves. It is also not redundant with adaptive.MinCellDays —
	// that floor (20) qualifies the WEIGHTS; this one (30) qualifies the claim
	// that this symbol needs its own model at all.
	// Record the FIRST unmet condition, in the same order the gate tests them,
	// so the count of blockers a run reports sums to the symbols it refused.
	switch {
	case len(examples) < MinPersonal:
		m.TierBlocker = fmt.Sprintf("rows %d/%d", len(examples), MinPersonal)
	case m.NDays < MinPersonalDays:
		m.TierBlocker = fmt.Sprintf("distinct days %d/%d", m.NDays, MinPersonalDays)
	case len(cell.Weights) == 0:
		// The adaptive panel produced no weights that survived its shrinkage and
		// multiplicity correction. Measured 2026-08-08 this was the binding
		// constraint for EVERY symbol: cells carried 14-16 days against
		// adaptive.MinCellDays of 20.
		m.TierBlocker = fmt.Sprintf("adaptive weights empty (cell days %d/%d)",
			cell.Days, adaptive.MinCellDays)
	}

	switch {
	case len(examples) >= MinPersonal && m.NDays >= MinPersonalDays && len(cell.Weights) > 0:
		m.Tier = TierPersonal
		m.Weights = cell.Weights
		if kx, ky, ok := ensemble.CalibrateKnots(rawPairs); ok {
			m.Calibration = Calibration{KX: kx, KY: ky, Fitted: true}
		}
	case regimeLearned:
		m.Tier = TierRegime
	case globalLearned:
		m.Tier = TierGlobal
	default:
		m.Tier = TierStatic
	}

	m.Personality = personality(m)
	return m
}

// personality derives a deterministic plain-English read of the symbol's
// measured skill. Deterministic == same skill always yields the same string
// (stable, testable, no LLM). It never overclaims: when the symbol is still
// learning it says so and names the fallback in use.
func personality(m Model) string {
	// Not personal yet → honest "still learning" line naming the fallback.
	if m.Tier != TierPersonal {
		fallback := map[string]string{
			TierRegime: "global per-regime model",
			TierGlobal: "global model",
			TierStatic: "equal-weight prior",
		}[m.Tier]
		// Progress is stated in DAYS because days are what the gate counts:
		// quoting "114/40 outcomes" while withholding the model reads as a
		// contradiction, and it was the row count that was misleading in the
		// first place.
		return fmt.Sprintf("Still learning (%d/%d own-outcome days, %d graded rows) — using the %s until this symbol has enough of its own resolved calls.",
			m.NDays, MinPersonalDays, m.NSamples, fallback)
	}

	// Personal: lead with the strongest measured component, then flag the ones
	// that add nothing, then a directional note from the leading IC.
	type ranked struct {
		leg string
		sk  Skill
	}
	var rs []ranked
	for leg, sk := range m.Skill {
		rs = append(rs, ranked{leg, sk})
	}
	// Rank by directional edge over a coin flip (hit-rate - 0.5), then |IC|,
	// then leg name for a fully deterministic order.
	sort.Slice(rs, func(i, j int) bool {
		ei, ej := edge(rs[i].sk), edge(rs[j].sk)
		if ei != ej {
			return ei > ej
		}
		if math.Abs(rs[i].sk.IC) != math.Abs(rs[j].sk.IC) {
			return math.Abs(rs[i].sk.IC) > math.Abs(rs[j].sk.IC)
		}
		return rs[i].leg < rs[j].leg
	})

	if len(rs) == 0 {
		return fmt.Sprintf("Personal model (%d own-outcome days, %d graded rows), but no component shows a measurable edge yet.", m.NDays, m.NSamples)
	}

	lead := rs[0]
	var b string
	if edge(lead.sk) > 0 {
		b = fmt.Sprintf("%s-led: %s hits %.0f%% of its directional calls (%d)",
			legLabel(lead.leg), legLabel(lead.leg), lead.sk.HitRate*100, lead.sk.N)
		if lead.sk.HasIC && math.Abs(lead.sk.IC) >= 0.05 {
			b += fmt.Sprintf(", IC %.2f", lead.sk.IC)
		}
		b += "."
	} else {
		b = fmt.Sprintf("Personal model (%d own-outcome days, %d graded rows); no component beats a coin flip yet — leaning on the blend.", m.NDays, m.NSamples)
	}

	// Name components that add nothing (present, but no edge over 50/50).
	var dead []string
	for _, r := range rs[1:] {
		if edge(r.sk) <= 0 && r.sk.HasHR {
			dead = append(dead, legLabel(r.leg))
		}
	}
	if len(dead) > 0 {
		b += " " + joinDead(dead) + " " + addsVerb(dead) + " nothing."
	}

	// Directional colour from the leading IC.
	if lead.sk.HasIC {
		switch {
		case lead.sk.IC >= 0.10:
			b += fmt.Sprintf(" Best signal when %s pushes up.", legLabel(lead.leg))
		case lead.sk.IC <= -0.10:
			b += fmt.Sprintf(" %s reads inverted here — high readings precede pullbacks.", legLabel(lead.leg))
		}
	}
	return b
}

// edge is a component's directional edge over a coin flip (only when the
// component actually made directional calls).
func edge(sk Skill) float64 {
	if !sk.HasHR {
		return 0
	}
	return sk.HitRate - 0.5
}

// legLabel gives each canonical leg a readable, capitalized name.
func legLabel(leg string) string {
	switch leg {
	case ensemble.LegPressure:
		return "Pressure"
	case ensemble.LegExpectancy:
		return "Expectancy"
	case ensemble.LegForecast:
		return "Forecast"
	case ensemble.LegSentiment:
		return "Sentiment"
	default:
		return leg
	}
}

func joinDead(dead []string) string {
	switch len(dead) {
	case 1:
		return dead[0]
	case 2:
		return dead[0] + " and " + dead[1]
	default:
		return joinList(dead)
	}
}

func joinList(xs []string) string {
	out := ""
	for i, x := range xs {
		switch {
		case i == 0:
			out = x
		case i == len(xs)-1:
			out += ", and " + x
		default:
			out += ", " + x
		}
	}
	return out
}

func addsVerb(dead []string) string {
	if len(dead) == 1 {
		return "adds"
	}
	return "add"
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
