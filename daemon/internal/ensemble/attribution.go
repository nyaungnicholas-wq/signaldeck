// Prediction attribution (Layer 6): decompose the blended probability that
// actually trades into named parts, so "why 0.62?" has a checkable answer.
//
// DECOMPOSITION CONTRACT (the one this file promises and the tests assert):
// parts are PROBABILITY deltas from the neutral 0.5 prior, and they SUM to
// (blended raw probability − 0.5) EXACTLY — the same raw probability
// WeightedProbability returns for the same Components + weights. Probability
// space (not logit space) was chosen because the blend itself is a weighted
// MEAN of leg probabilities, so the decomposition is exact by algebra, not by
// approximation:
//
//	P − 0.5 = Σ_legs wn_leg · (p_leg − 0.5),  wn = weight / Σ weights
//
// Calibration is applied AFTER the blend by a monotone map and is NOT part of
// this decomposition — the attributed quantity is the RAW blend, and callers
// surfacing calibrated probabilities must say so.
//
// Granularity:
//   - the pressure leg splits into its comp_* score components (each
//     ScoreComponent.Contrib sums to the pressure score, so the split is
//     exact);
//   - the GBM leg may split into per-feature parts derived from gbm.Attribute
//     (Saabas path attribution). Those are LOGIT-space contributions from a
//     different model run, so they are projected PROPORTIONALLY onto the GBM
//     leg's probability contribution — shares are Saabas-faithful, the total
//     is exact, the per-feature split inherits Saabas's disclosed biases and
//     is NOT exact SHAP;
//   - every other leg is one part.
package ensemble

import (
	"math"
	"sort"
)

// AttributionPart kinds.
const (
	KindComponent  = "component"   // one comp_* pressure-score component
	KindGBMFeature = "gbm_feature" // one GBM input feature (Saabas share)
	KindLeg        = "leg"         // one whole ensemble leg
)

// AttributionPart is one named slice of the blended probability's delta from
// the neutral 0.5 prior. Contribution is in probability units (+0.12 = this
// part pushed the blend up 12 points).
type AttributionPart struct {
	Name         string  `json:"name"`
	Contribution float64 `json:"contribution"`
	Kind         string  `json:"kind"` // component | gbm_feature | leg
}

// AttributeProbability decomposes the raw blended probability for (c, weights)
// into AttributionParts per the contract in the file header. It mirrors
// WeightedProbability's leg selection and weighting EXACTLY (same
// LegProbabilities gates, same equal-weight fallback), so the parts always sum
// to WeightedProbability(c, weights) − 0.5 within float tolerance.
//
// pressureComps optionally splits the pressure leg: pass the score's
// components as parts whose Contributions sum to the pressure score (each
// md.ScoreComponent.Contrib, named "comp_<name>"). nil, or a pressure score
// outside [-1,1] (where the leg's clamp breaks the linear map), keeps the
// pressure leg whole.
//
// gbmLogitContribs optionally splits the GBM leg: per-feature LOGIT
// contributions from gbm.Attribute (Saabas), which are projected
// proportionally onto the GBM leg's probability contribution. nil, or a
// near-zero logit total (no shares to project), keeps the GBM leg whole.
func AttributeProbability(c Components, weights map[string]float64, pressureComps []AttributionPart, gbmLogitContribs []AttributionPart) []AttributionPart {
	legs := LegProbabilities(c)
	if len(legs) == 0 {
		return nil
	}
	// Normalized weights, mirroring WeightedProbability: use the supplied
	// weights over available legs when they carry positive mass, else equal.
	wn := map[string]float64{}
	var wsum float64
	if len(weights) > 0 {
		for name := range legs {
			if w := weights[name]; w > 0 {
				wn[name] = w
				wsum += w
			}
		}
	}
	if wsum <= 0 {
		wn = map[string]float64{}
		for name := range legs {
			wn[name] = 1
		}
		wsum = float64(len(legs))
	}

	var parts []AttributionPart
	// Deterministic order: canonical leg order, then any stragglers.
	for _, name := range legOrder(legs) {
		w, ok := wn[name]
		if !ok {
			continue
		}
		contrib := (w / wsum) * (legs[name] - 0.5)
		switch {
		case name == LegPressure && len(pressureComps) > 0 && math.Abs(c.PressureScore) <= 1:
			// Exact split: p_pressure − 0.5 = score/2 and the comps sum to the
			// score, so scaling each comp by (w/wsum)/2 sums to contrib exactly.
			scale := (w / wsum) / 2
			for _, cp := range pressureComps {
				parts = append(parts, AttributionPart{
					Name: cp.Name, Contribution: cp.Contribution * scale, Kind: KindComponent,
				})
			}
		case name == LegGBM && len(gbmLogitContribs) > 0:
			var total float64
			for _, g := range gbmLogitContribs {
				total += g.Contribution
			}
			if math.Abs(total) < 1e-12 {
				parts = append(parts, AttributionPart{Name: name, Contribution: contrib, Kind: KindLeg})
				break
			}
			// Proportional projection of Saabas logit shares onto the leg's
			// probability contribution — exact in total, Saabas in shares.
			for _, g := range gbmLogitContribs {
				parts = append(parts, AttributionPart{
					Name: "gbm_" + g.Name, Contribution: contrib * g.Contribution / total, Kind: KindGBMFeature,
				})
			}
		default:
			parts = append(parts, AttributionPart{Name: name, Contribution: contrib, Kind: KindLeg})
		}
	}
	return parts
}

// legOrder returns the available leg names in canonical LegNames order (any
// unknown names appended sorted, defensively).
func legOrder(legs map[string]float64) []string {
	out := make([]string, 0, len(legs))
	seen := map[string]bool{}
	for _, n := range LegNames {
		if _, ok := legs[n]; ok {
			out = append(out, n)
			seen[n] = true
		}
	}
	var extra []string
	for n := range legs {
		if !seen[n] {
			extra = append(extra, n)
		}
	}
	sort.Strings(extra)
	return append(out, extra...)
}

// TopParts returns the n parts with the largest absolute contribution, in
// descending |contribution| order (ties broken by name for determinism). The
// input is not mutated.
func TopParts(parts []AttributionPart, n int) []AttributionPart {
	cp := append([]AttributionPart(nil), parts...)
	sort.SliceStable(cp, func(i, j int) bool {
		ai, aj := math.Abs(cp[i].Contribution), math.Abs(cp[j].Contribution)
		if ai != aj {
			return ai > aj
		}
		return cp[i].Name < cp[j].Name
	})
	if n > 0 && len(cp) > n {
		cp = cp[:n]
	}
	return cp
}
