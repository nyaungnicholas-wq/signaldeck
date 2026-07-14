// CROSS-SECTIONAL ALPHA wave — the alpha-trainer worker (6h).
//
// Pools labeled feature rows across ALL symbols at once (LabeledFeaturesAll)
// and trains ONE cross-sectional model per horizon via internal/alphax: the
// label is "did this row's forward return beat the same-UTC-day universe
// median" — relative alpha, the approach the serious competitors (Danelfin,
// Zacks, quant funds) actually use, and one that consumes the whole ~35k-row
// labeled store instead of starving per-symbol at min-60-rows each.
//
// VERSION POOLING (deliberate, documented choice): unlike the per-symbol GBM
// trainer (which pins ONE featureVersion so absent-vs-zero stays clean), this
// worker pools v3 AND v4 AND later rows together (version >= 3). The
// cross-sectional pool needs volume above schema purity; the union-of-keys
// flatten handles fields a given version lacks; and any absent-vs-zero
// dilution biases toward "no edge" — it can only DAMPEN a grade, never
// flatter it (fails safe).
//
// GRADING + GATE: purged walk-forward splits BY DAY with a HORIZON-AWARE
// embargo (label span + 1 day: 2 for 1d, 8 for 1w — de Prado), refusing to
// grade below 1000 pooled train rows / 200 OOS test rows.
// The model + grade are stored in alphax_models every pass; per-symbol
// CURRENT scores go to model_forecasts (model="alphax") ONLY when the
// measured OOS lift > 0, and any stored scores are DELETED when a regrade
// lands gated.
//
// IN THE BLEND (alphax-leg wave): the PredictionRunner now consumes these
// rows as a GATED ensemble leg (ensemble.LegAlphaX) — a row exists only while
// the measured OOS lift > 0 (deleted on a gated regrade), and the ensemble
// re-applies the identical lift>0 gate. The prob is RELATIVE (beat the
// same-day universe median), blended as a directional tilt; the adaptive
// attribution grades per regime whether it helps (core doctrine: a leg enters
// the live blend ONLY with measured OOS lift > 0, and stays on the referee's
// scorecard).
package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/alphax"
	"github.com/nyaungnicholas-wq/signaldeck/internal/gbm"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

const (
	// alphaXMaxRows caps the pooled labeled set one pass consumes per horizon
	// (newest-first from the store; the dataset builder re-orders by day).
	alphaXMaxRows = 50000
	// alphaXMinFeatureVersion admits v3+ rows into the pool (see the file
	// comment for why versions are pooled here, unlike the per-symbol GBM).
	alphaXMinFeatureVersion = 3
	// alphaXFolds mirrors gbm/forecast (5 walk-forward blocks, here by day).
	alphaXFolds = 5
	// alphaXScoreFreshSecs bounds how old a symbol's newest feature vector
	// may be and still receive a current score (a week-old vector is not the
	// live cross-section).
	//
	// KNOWN LIMITATION (M4): a 2-day window mixes vintages — a symbol scored
	// off a vector captured 47 hours ago sits in the "live" cross-section next
	// to one captured 5 minutes ago, and the score row's ts=now is the SCORE
	// time, not the feature time. The staleness bound caps the skew but does
	// not remove it; consumers comparing scored symbols should treat the
	// cross-section as "within 2 days", not "simultaneous".
	alphaXScoreFreshSecs = 2 * 86400
)

// alphaXEmbargoDays is the purge gap Evaluate needs for one horizon: the
// label span in whole days plus one (H1 — horizon-aware embargo). A 1d label
// overlaps 1 later day → 2; a 1w label's close lies up to 7 days ahead → 8.
// The +1 covers rows time-stamped late in their UTC day whose span straddles
// one more calendar day than the integer division suggests.
func alphaXEmbargoDays(h md.Horizon) int {
	return int((horizonSecs(h)+86399)/86400) + 1
}

// alphaXModelBlob is the serialized model stored in alphax_models.model_json:
// the canonical key order PLUS the fitted gbm — both are needed to reproduce
// any score the model ever produced (provenance).
type alphaXModelBlob struct {
	FeatureKeys []string   `json:"featureKeys"`
	Model       *gbm.Model `json:"model"`
}

// AlphaXTrainer is the alpha-trainer worker.
type AlphaXTrainer struct {
	St *store.Store
}

func (w *AlphaXTrainer) Name() string            { return "alpha-trainer" }
func (w *AlphaXTrainer) Interval() time.Duration { return 6 * time.Hour }

func (w *AlphaXTrainer) Run(ctx context.Context) (string, error) {
	now := time.Now().Unix()
	var parts []string
	for _, h := range predHorizons {
		rows, err := w.St.LabeledFeaturesAll(ctx, h, alphaXMinFeatureVersion, alphaXMaxRows)
		if err != nil {
			return "", fmt.Errorf("labeled pool %s: %w", h, err)
		}
		if len(rows) == 0 {
			parts = append(parts, fmt.Sprintf("%s: no labeled rows yet", h))
			continue
		}
		lrows := make([]alphax.LabeledRow, len(rows))
		for i, r := range rows {
			lrows[i] = alphax.LabeledRow{
				SymbolID: r.SymbolID, Ts: r.Ts, Features: r.Vec, FwdReturn: r.FwdReturn,
			}
		}
		ds := alphax.BuildDataset(lrows)
		grade, ok, reason := alphax.Evaluate(ds, alphaXFolds, alphaXEmbargoDays(h), gbm.Defaults())
		if !ok {
			// Honest refusal — no grade is stored (a prior grade, if any,
			// stands with its own timestamp), and the detail says exactly why.
			parts = append(parts, fmt.Sprintf(
				"%s: refused to grade (%s) — %d pooled rows over %d usable days (%d thin day(s)/%d row(s) dropped, %d intraday duplicate(s) collapsed)",
				h, reason, len(ds.Samples), len(ds.Days), ds.ThinDaysDropped, ds.RowsDropped, ds.DupRowsDropped))
			continue
		}
		model, err := alphax.TrainFull(ds, gbm.Defaults())
		if err != nil {
			parts = append(parts, fmt.Sprintf("%s: graded but full train refused (%v)", h, err))
			continue
		}
		blob, err := json.Marshal(alphaXModelBlob{FeatureKeys: ds.Keys, Model: model})
		if err != nil {
			return "", fmt.Errorf("serialize alphax model %s: %w", h, err)
		}
		gated := grade.Lift <= 0
		if err := w.St.UpsertAlphaXModel(ctx, store.AlphaXModelRow{
			Horizon: h, Ts: now,
			OOSLift: grade.Lift, OOSAUC: grade.AUC, OOSAcc: grade.Accuracy,
			BaseRate: grade.BaseRate, NTrain: len(ds.Samples), NTest: grade.N,
			Gated: gated, ModelJSON: string(blob),
		}); err != nil {
			return "", fmt.Errorf("upsert alphax model %s: %w", h, err)
		}

		if gated {
			// No measured edge: store nothing that could be read as a signal,
			// and remove any scores an earlier (edged) pass left behind.
			removed, err := w.St.DeleteModelForecastsByModel(ctx, store.ModelAlphaX, h)
			if err != nil {
				return "", fmt.Errorf("clear gated alphax scores %s: %w", h, err)
			}
			part := fmt.Sprintf("%s: graded lift=%+.4f (auc=%.3f, n_test=%d) → GATED, no scores stored",
				h, grade.Lift, grade.AUC, grade.N)
			if removed > 0 {
				part += fmt.Sprintf(" (%d stale score(s) cleared)", removed)
			}
			parts = append(parts, part)
			continue
		}

		// Edge measured: score the LIVE cross-section (each symbol's newest
		// fresh feature vector, flattened with the model's own key order).
		feats, err := w.St.LatestFeaturesByHorizon(ctx, h, now-alphaXScoreFreshSecs)
		if err != nil {
			return "", fmt.Errorf("latest features %s: %w", h, err)
		}
		scored := 0
		for _, f := range feats {
			prob := model.Predict(alphax.Flatten(f.Vec, ds.Keys))
			if err := w.St.UpsertModelForecast(ctx, store.ModelForecast{
				SymbolID: f.SymbolID, Horizon: h, Model: store.ModelAlphaX, Ts: now,
				Prob: prob, Accuracy: grade.Accuracy, Brier: grade.BrierScore,
				AUC: grade.AUC, BaseRate: grade.BaseRate, Lift: grade.Lift,
				NTrain: len(ds.Samples), NEval: grade.N,
			}); err != nil {
				return "", fmt.Errorf("upsert alphax score %s: %w", h, err)
			}
			scored++
		}
		parts = append(parts, fmt.Sprintf(
			"%s: lift=%+.4f (auc=%.3f, acc=%.3f vs base %.3f, n_test=%d) over %d rows/%d days (%d intraday duplicate(s) collapsed) → scored %d symbol(s)",
			h, grade.Lift, grade.AUC, grade.Accuracy, grade.BaseRate, grade.N,
			len(ds.Samples), len(ds.Days), ds.DupRowsDropped, scored))
	}
	if len(parts) == 0 {
		return "no horizons processed", nil
	}
	return strings.Join(parts, "; "), nil
}
