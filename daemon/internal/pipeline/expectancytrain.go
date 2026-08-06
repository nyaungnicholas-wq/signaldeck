package pipeline

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/clusterstat"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// expectancyLegKey is the predictions.components key holding the expectancy
// leg's value at prediction time. It is a P(up) already, so it needs no
// transform before grading — unlike pressure, which is a [-1,+1] score.
const expectancyLegKey = "ExpectancyHitRate"

// expectancyMaxRows caps resolved pairs loaded per symbol+horizon.
const expectancyMaxRows = 5000

// expectancyMinPairs is the fewest independent symbol-days a SYMBOL must
// contribute before its own AUC is estimated at all. An AUC on a handful of
// points is mostly noise, and this estimator averages symbol AUCs — so a floor
// here keeps the fleet number from being dominated by symbols that carry no
// information either way. Deliberately far below clusterstat.RankEdgeMinEval:
// this is the bar to CONTRIBUTE an estimate, not the bar to be JUDGED by one.
const expectancyMinPairs = 10

// ExpectancyTrainer grades the ensemble's EXPECTANCY leg — the per-state
// historical hit rate — and persists the result so the ranking gate can judge
// it like every other leg.
//
// WHY THIS EXISTS. ExpectancyLift is assigned nowhere in production, so the leg
// has always been admitted on availability alone. That was survivable while the
// pressure and alphax legs dominated the blend; once those are benched on
// measured ranking, expectancy becomes the leg carrying most published
// probabilities, and it would be carrying them on no evidence at all.
//
// WHY THE GRADE IS A FLEET ESTIMATE. The other legs are graded per symbol from
// the feature store, which reaches back years. Expectancy cannot be: the
// ablation harness removed expectancy_hit_rate from the feature vector, so the
// only surface carrying the live leg is predictions.components, and that table
// spans about a month — 15 to 32 independent symbol-days per symbol, against
// the 60 a per-symbol walk-forward needs. So each symbol contributes an AUC
// (cheap: the pairs are already prequential, nothing is fit) and the fleet
// number is their n-weighted mean, which is well powered across ~650 symbols
// even though no single symbol is.
//
// WHY IT LABELS FROM BARS. It reads leg VALUES from predictions.components and
// derives the realized direction itself (labelFromBars), instead of joining
// prediction_outcomes. Two reasons, both load-bearing:
//
//   - An EVIDENCE row — no leg admitted, n_used=0 — has no outcome row by
//     design. Those are precisely the symbol-days a BENCHED leg produces, so
//     joining outcomes would grade a leg only on the symbols where it still
//     wins, and a leg benched fleet-wide would stop being measured at all. The
//     gate would seal itself shut.
//   - The frozen labels and a recomputation from final bars are different
//     vintages: over 3,000 resolved rows they agree on the sign 99.80% of the
//     time at 1w but only 94.17% at 1d, median drift 9bps — the shape of a
//     daily bar that was still forming when its label was frozen. One
//     definition applied to every row beats a mixture of two.
//
// Each row therefore stores the FLEET grade with NEval set to that symbol's own
// contribution. Both numbers then mean exactly what they say downstream:
// store.FleetLegAUC reads the fleet AUC and can veto the leg everywhere, while
// clusterstat.RankEdge sees a thin per-symbol count, returns ok=false, and
// leaves the leg on its historical gate rather than benching one symbol on
// evidence that was never symbol-specific.
//
// OPT-OUT like pressure: the row is written even when the grade is bad, because
// a measured anti-predictive grade is the whole point — it is what lets the gate
// bench a leg that is otherwise admitted by default.
type ExpectancyTrainer struct {
	St *store.Store
}

func (w *ExpectancyTrainer) Name() string            { return "expectancy-trainer" }
func (w *ExpectancyTrainer) Interval() time.Duration { return time.Hour }

// symbolPairs is one symbol's contribution to a horizon's fleet grade.
type symbolPairs struct {
	id     int64
	vals   []float64
	ups    []float64
	auc    float64
	graded bool
}

func (w *ExpectancyTrainer) Run(ctx context.Context) (string, error) {
	syms, err := w.St.ListSymbols(ctx, true)
	if err != nil {
		return "", err
	}
	now := time.Now().Unix()
	var msg string
	for _, h := range predHorizons {
		// PASS 1 — collect every symbol's pairs and its own AUC estimate.
		var all []symbolPairs
		for _, s := range syms {
			tss, raw, err := w.St.LegValuesBySymbol(ctx, s.ID, h, expectancyLegKey, expectancyMaxRows)
			if err != nil {
				return "", fmt.Errorf("expectancy values %s %s: %w", s.Symbol, h, err)
			}
			if len(tss) < expectancyMinPairs {
				all = append(all, symbolPairs{id: s.ID})
				continue
			}
			bars, err := w.St.LastBars(ctx, s.ID, md.TF1d, expectancyBarLookback)
			if err != nil {
				return "", fmt.Errorf("expectancy bars %s: %w", s.Symbol, err)
			}
			vals, ups := labelFromBars(tss, raw, bars, horizonSecs(h))
			sp := symbolPairs{id: s.ID, vals: vals, ups: ups}
			if len(vals) >= expectancyMinPairs {
				sp.auc = clusterstat.RankAUC(vals, ups)
				sp.graded = true
			}
			all = append(all, sp)
		}
		// FLEET AUC — n-weighted mean of the per-symbol estimates. Weighting by
		// n rather than taking a plain mean stops a symbol with 10 points
		// counting as much as one with 32.
		var wsum, asum float64
		for _, sp := range all {
			if sp.graded {
				n := float64(len(sp.vals))
				asum += sp.auc * n
				wsum += n
			}
		}
		if wsum == 0 {
			continue // nothing resolved yet for this horizon
		}
		fleetAUC := asum / wsum

		// POOLED calibration-style metrics, for the stored row's other fields.
		// These describe the same population the AUC does.
		var n, hits, ups int
		var brier float64
		for _, sp := range all {
			if !sp.graded {
				continue
			}
			for i := range sp.vals {
				n++
				up := sp.ups[i] >= 0.5
				if up {
					ups++
				}
				if (sp.vals[i] > 0.5) == up {
					hits++
				}
				d := sp.vals[i] - sp.ups[i]
				brier += d * d
			}
		}
		acc := float64(hits) / float64(n)
		base := math.Max(float64(ups), float64(n-ups)) / float64(n)

		// PASS 2 — persist the fleet grade against every symbol that contributed.
		written := 0
		for _, sp := range all {
			if !sp.graded {
				continue
			}
			latest := sp.vals[0] // newest-first from the store
			if err := w.St.UpsertModelForecast(ctx, store.ModelForecast{
				SymbolID: sp.id, Horizon: h, Model: store.ModelExpectancy, Ts: now,
				Prob: latest, Accuracy: acc, Brier: brier / float64(n), AUC: fleetAUC,
				BaseRate: base, Lift: acc - base, NTrain: 0, NEval: len(sp.vals),
			}); err != nil {
				return "", fmt.Errorf("upsert expectancy %d %s: %w", sp.id, h, err)
			}
			written++
		}
		verdict := "passes the ranking gate"
		if fleetAUC <= 0.5 {
			verdict = "ANTI-PREDICTIVE — benched fleet-wide"
		}
		msg += fmt.Sprintf("%s: fleet AUC %.4f over %d symbols / %d symbol-days (%s); ",
			h, fleetAUC, written, n, verdict)
	}
	if msg == "" {
		return "expectancy leg: no resolved pairs yet — leg stays on its historical gate", nil
	}
	return "graded expectancy leg — " + msg, nil
}

// expectancyHorizonKey mirrors the key store.FleetLegAUC builds, so a test can
// assert the trainer and the gate agree on the spelling rather than trusting
// two string concatenations to stay in step.
func expectancyHorizonKey(h md.Horizon) string {
	return store.ModelExpectancy + "|" + string(h)
}

// expectancyBarLookback is how many daily bars one symbol's labeling needs. The
// leg values span at most expectancyMaxRows trading days; 400 covers well over a
// year of them with room for the forward window.
const expectancyBarLookback = 400

// labelFromBars attaches a realized direction to each leg value using EXACTLY
// the rule PredictionResolver.Run applies:
//
//	base   = last daily bar at or before the value's timestamp
//	target = base.Ts + horizonSecs        (calendar, so a Friday 1d target
//	                                       lands on the weekend)
//	fwd    = first daily bar at or after target
//	label  = fwd.Close > base.Close
//
// with the resolver's own staleness guard (a forward bar more than
// 3*horizonSecs past the target is a gap, not a horizon, and is dropped).
// Reimplementing the rule rather than reading prediction_outcomes is what lets
// EVIDENCE rows be graded at all — they have no outcome row by design — and it
// keeps one label definition across the whole estimate instead of two vintages.
//
// bars must be ascending by Ts (store.LastBars is). Rows that cannot be labeled
// are dropped, never defaulted: a missing forward bar is an unknown outcome,
// and inventing one would quietly improve the measured record.
func labelFromBars(tss []int64, vals []float64, bars []md.Bar, horizonSecs int64) ([]float64, []float64) {
	if len(bars) == 0 {
		return nil, nil
	}
	outV := make([]float64, 0, len(vals))
	outU := make([]float64, 0, len(vals))
	for i, ts := range tss {
		// base: last bar with Ts <= ts.
		lo, hi := 0, len(bars)-1
		base := -1
		for lo <= hi {
			mid := (lo + hi) / 2
			if bars[mid].Ts <= ts {
				base = mid
				lo = mid + 1
			} else {
				hi = mid - 1
			}
		}
		if base < 0 || bars[base].Close <= 0 {
			continue
		}
		target := bars[base].Ts + horizonSecs
		// fwd: first bar with Ts >= target.
		lo, hi = 0, len(bars)-1
		fwd := -1
		for lo <= hi {
			mid := (lo + hi) / 2
			if bars[mid].Ts >= target {
				fwd = mid
				hi = mid - 1
			} else {
				lo = mid + 1
			}
		}
		if fwd < 0 || bars[fwd].Ts-target > 3*horizonSecs {
			continue
		}
		up := 0.0
		if bars[fwd].Close > bars[base].Close {
			up = 1
		}
		outV = append(outV, vals[i])
		outU = append(outU, up)
	}
	return outV, outU
}
