package pipeline

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/gbm"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/meanrev"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// gbmMaxRows caps how many of a symbol's own labeled examples one training pass
// consumes per horizon (newest-first from the store, then re-ordered ascending).
const gbmMaxRows = 5000

// meanRevCost is the round-trip cost (fraction) the mean-reversion leg is graded
// NET OF — a contrarian trade must clear it to count as a win. 0.001 = 10 bps
// round-trip, a deliberately conservative all-in estimate for a liquid name; the
// leg only enters the blend if its edge survives this bar.
const meanRevCost = 0.001

// gbmFolds / meanRevFolds are the walk-forward fold counts (mirrors
// forecast.defaultFolds = 5).
const (
	gbmFolds     = 5
	meanRevFolds = 5
)

// canonicalFeatureKeys returns a STABLE, sorted union of feature-vector keys
// across the given labeled rows, EXCLUDING the model's own outputs (pred_raw /
// pred_cal) so the GBM cannot trivially copy the blend it is trying to
// out-predict — training on pred_raw would be a leakage shortcut, not learning.
// A deterministic ordering is essential: the GBM is index-based, so every
// sample (and the live latest vector) must be flattened with the identical key
// order.
func canonicalFeatureKeys(rows []store.LabeledFeature) []string {
	set := map[string]struct{}{}
	for _, r := range rows {
		for k := range r.Vec {
			if excludedGBMKey(k) {
				continue
			}
			set[k] = struct{}{}
		}
	}
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// excludedGBMKey reports feature keys the GBM must NOT train on:
//   - the blend's own probabilities (pred_raw/pred_cal) — training on them would
//     let the tree learn "copy the ensemble" instead of finding independent
//     structure, and would couple the GBM leg to the very blend it's meant to
//     diversify;
//   - the model legs' OWN prior outputs (gbm_prob/meanrev_prob) — a leg must not
//     be fed its own past prediction, both to avoid a self-referential shortcut
//     and to keep the GBM leg independent of the mean-reversion leg (and itself).
func excludedGBMKey(k string) bool {
	switch k {
	case "pred_raw", "pred_cal", "gbm_prob", "meanrev_prob":
		return true
	}
	return false
}

// flatten turns a feature map into a fixed-dimension vector in the given key
// order. Missing keys become 0 (a feature absent from a row is treated as its
// neutral value — the GBM splits handle it, and the key set is the union so
// dimensions always match).
func flatten(vec map[string]float64, keys []string) []float64 {
	out := make([]float64, len(keys))
	for i, k := range keys {
		out[i] = vec[k] // zero if absent
	}
	return out
}

// gbmSamplesFromLabeled builds time-ASCENDING gbm.Samples from labeled feature
// rows (which arrive newest-first). The caller passes the canonical key order so
// every sample — and the latest live vector — share one feature layout.
func gbmSamplesFromLabeled(rows []store.LabeledFeature, keys []string) []gbm.Sample {
	out := make([]gbm.Sample, 0, len(rows))
	// rows are newest-first; iterate in reverse for ascending ts.
	for i := len(rows) - 1; i >= 0; i-- {
		r := rows[i]
		out = append(out, gbm.Sample{
			Ts:   r.Ts,
			Feat: flatten(r.Vec, keys),
			Y:    float64(r.Up),
		})
	}
	return out
}

// meanRevSamplesFromLabeled builds time-ASCENDING meanrev.Samples from labeled
// rows. Each carries the stored momentum raw prob (pred_raw) the blend produced
// plus the realized outcome + forward return for cost-net grading. Rows lacking
// a stored pred_raw are skipped (no momentum lean to invert).
func meanRevSamplesFromLabeled(rows []store.LabeledFeature) []meanrev.Sample {
	out := make([]meanrev.Sample, 0, len(rows))
	for i := len(rows) - 1; i >= 0; i-- {
		r := rows[i]
		raw, ok := r.Vec["pred_raw"]
		if !ok {
			continue
		}
		out = append(out, meanrev.Sample{
			Ts:        r.Ts,
			RawProb:   raw,
			Up:        r.Up,
			FwdReturn: r.FwdReturn,
		})
	}
	return out
}

// GBMTrainer trains the STAGE 6 non-linear (GBM) and mean-reversion model legs
// for every active symbol+horizon from the feature store, walk-forward and
// out-of-sample, and upserts each leg's latest probability + OOS grade into
// model_forecasts. The PredictionRunner then includes a leg in the blend ONLY
// when its stored lift > 0 (the same honesty gate the linear forecast passes).
//
// It never blocks predictions: a symbol with too little resolved history simply
// produces no GBM/mean-rev row (Run returns ok=false), and the ensemble falls
// back to the legs that do have measured edge.
type GBMTrainer struct {
	St *store.Store
}

func (w *GBMTrainer) Name() string            { return "gbm-trainer" }
func (w *GBMTrainer) Interval() time.Duration { return time.Hour }

func (w *GBMTrainer) Run(ctx context.Context) (string, error) {
	syms, err := w.St.ListSymbols(ctx, true)
	if err != nil {
		return "", err
	}
	now := time.Now().Unix()
	gbmEdged, mrEdged, trained := 0, 0, 0
	for _, s := range syms {
		for _, h := range predHorizons {
			// Version-pinned: the GBM flattens the key union across rows, so
			// mixing feature-schema versions would dilute absent-vs-zero
			// (review finding). Train on the current layout only.
			rows, err := w.St.LabeledFeaturesBySymbolVersion(ctx, s.ID, h, featureVersion, gbmMaxRows)
			if err != nil {
				return "", fmt.Errorf("labeled %s %s: %w", s.Symbol, h, err)
			}
			if len(rows) == 0 {
				continue
			}

			// ── GBM leg ──────────────────────────────────────────────────
			keys := canonicalFeatureKeys(rows)
			if len(keys) > 0 {
				samples := gbmSamplesFromLabeled(rows, keys)
				// Latest live vector = newest row (rows[0]) flattened with the
				// same key order. It IS in the training set (its outcome already
				// resolved, so it's a legitimate labeled example); Run grades
				// out-of-sample via walk-forward before predicting it.
				latest := flatten(rows[0].Vec, keys)
				if prob, g, ok := gbm.Run(samples, latest, gbmFolds, gbm.Defaults()); ok {
					if err := w.St.UpsertModelForecast(ctx, store.ModelForecast{
						SymbolID: s.ID, Horizon: h, Model: store.ModelGBM, Ts: now,
						Prob: prob, Accuracy: g.Accuracy, Brier: g.BrierScore, AUC: g.AUC,
						BaseRate: g.BaseRate, Lift: g.Lift, NTrain: len(samples), NEval: g.N,
					}); err != nil {
						return "", fmt.Errorf("upsert gbm %s %s: %w", s.Symbol, h, err)
					}
					trained++
					if g.Lift > 0 {
						gbmEdged++
					}
				}
			}

			// ── mean-reversion leg ───────────────────────────────────────
			mrSamples := meanRevSamplesFromLabeled(rows)
			latestRaw, hasRaw := rows[0].Vec["pred_raw"]
			if hasRaw {
				if prob, g, ok := meanrev.Run(mrSamples, latestRaw, meanRevFolds, meanrev.DefaultStrength, meanRevCost); ok {
					if err := w.St.UpsertModelForecast(ctx, store.ModelForecast{
						SymbolID: s.ID, Horizon: h, Model: store.ModelMeanRev, Ts: now,
						Prob: prob, Accuracy: g.Accuracy, Brier: g.BrierScore, AUC: g.AUC,
						BaseRate: g.BaseRate, Lift: g.Lift, NTrain: len(mrSamples), NEval: g.N,
					}); err != nil {
						return "", fmt.Errorf("upsert meanrev %s %s: %w", s.Symbol, h, err)
					}
					trained++
					if g.Lift > 0 {
						mrEdged++
					}
				}
			}
		}
	}
	return fmt.Sprintf("trained %d model legs over %d symbols (%d GBM + %d mean-rev with OOS edge)",
		trained, len(syms), gbmEdged, mrEdged), nil
}

// modelLegProbLift pulls one model's stored prob+lift for a symbol+horizon from
// a preloaded slice, returning ok=false when the leg isn't present. Used by the
// PredictionRunner to feed the gated legs into the ensemble.
func modelLegProbLift(models []store.ModelForecast, h md.Horizon, name string) (prob, lift float64, ok bool) {
	for _, m := range models {
		if m.Horizon == h && m.Model == name {
			return m.Prob, m.Lift, true
		}
	}
	return 0, 0, false
}
