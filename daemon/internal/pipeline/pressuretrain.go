package pipeline

import (
	"context"
	"fmt"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/pressure"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
	"github.com/nyaungnicholas-wq/signaldeck/internal/workers"
)

// pressureMaxRows caps labeled rows loaded per symbol+horizon for grading the
// pressure leg. Version-AGNOSTIC on purpose: pressure_score is written into the
// feature vector at every featureVersion and is schema-stable, so grading over
// ALL versions maximizes N — unlike the GBM, whose key-union flattening forces a
// version pin to avoid diluting absent-vs-zero.
const pressureMaxRows = 20000

// pressureFolds is the expanding-window fold count for the pressure OOS grade,
// matching the other legs (gbm/meanrev use 5).
const pressureFolds = 3

// PressureTrainer grades the ensemble's PRESSURE leg walk-forward, out-of-sample,
// for every active symbol+horizon from the feature store, and upserts its latest
// OOS grade into model_forecasts (model="pressure"). The PredictionRunner reads
// the stored lift and DROPS the pressure leg from the blend when that lift is
// <= 0 — the same honesty gate the model legs pass, applied at last to the
// platform's oldest, previously-unconditional base leg.
//
// Opt-OUT, not opt-in: unlike the model legs (whose absence simply means "off"),
// a MEASURED anti-predictive pressure grade must be PERSISTED so the gate can see
// it and bench the leg. So this trainer writes the row even when the graded lift
// is <= 0 — it does NOT delete on a gated regrade the way AlphaXTrainer does. A
// symbol with too little resolved history produces no row, and the ensemble keeps
// the pressure leg (fail-safe: we bench only on evidence).
//
// It never blocks predictions: grading errors surface as the worker's error (and
// its dq/worker_runs record); a thin symbol simply yields no row.
type PressureTrainer struct {
	St *store.Store
}

func (w *PressureTrainer) Name() string            { return "pressure-trainer" }
func (w *PressureTrainer) Interval() time.Duration { return time.Hour }

func (w *PressureTrainer) Run(ctx context.Context) (string, error) {
	syms, err := w.St.ListSymbols(ctx, true)
	if err != nil {
		return "", err
	}
	now := time.Now().Unix()
	graded, benched := 0, 0
	for _, s := range syms {
		for _, h := range predHorizons {
			rows, err := w.St.LabeledFeaturesBySymbol(ctx, s.ID, h, pressureMaxRows)
			if err != nil {
				return "", fmt.Errorf("labeled %s %s: %w", s.Symbol, h, err)
			}
			samples := pressureSamplesFromLabeled(rows)
			latest := 0.0
			if len(rows) > 0 {
				latest = rows[0].Vec["pressure_score"]
			}
			prob, g, ok := pressure.Run(samples, latest, pressureFolds)
			if !ok {
				continue // too little resolved history — leg stays live (fail-safe)
			}
			if err := w.St.UpsertModelForecast(ctx, store.ModelForecast{
				SymbolID: s.ID, Horizon: h, Model: store.ModelPressure, Ts: now,
				Prob: prob, Accuracy: g.Accuracy, Brier: g.BrierScore, AUC: g.AUC,
				BaseRate: g.BaseRate, Lift: g.Lift, NTrain: 0, NEval: g.N,
			}); err != nil {
				return "", fmt.Errorf("upsert pressure %s %s: %w", s.Symbol, h, err)
			}
			graded++
			if g.Lift <= 0 {
				benched++
			}
		}
	}
	msg := fmt.Sprintf("graded pressure leg over %d symbols: %d symbol-horizons graded, %d benched (OOS lift<=0)",
		len(syms), graded, benched)
	// Grading nothing, or grading only to bench everything, delivers no usable
	// leg — and reporting "ok" for it is how the fleet ran on one leg for days
	// with a green board. See workers.ErrDegraded.
	if graded == 0 {
		return msg, fmt.Errorf("no symbol-horizon could be graded: %w", workers.ErrDegraded)
	}
	if benched == graded {
		return msg, fmt.Errorf("every graded symbol-horizon was benched (OOS lift<=0): %w", workers.ErrDegraded)
	}
	return msg, nil
}

// pressureSamplesFromLabeled builds time-ASCENDING pressure.Samples from labeled
// rows (which arrive newest-first). Reads ONLY the stored pressure_score — never
// pred_raw / pred_cal, which CONTAIN the pressure leg (grading a leg off a blend
// that includes it would be circular). pressure_score is written on every row, so
// a missing key means an unexpectedly malformed vector and is skipped.
func pressureSamplesFromLabeled(rows []store.LabeledFeature) []pressure.Sample {
	out := make([]pressure.Sample, 0, len(rows))
	for i := len(rows) - 1; i >= 0; i-- {
		r := rows[i]
		ps, ok := r.Vec["pressure_score"]
		if !ok {
			continue
		}
		out = append(out, pressure.Sample{Ts: r.Ts, Pressure: ps, Up: r.Up})
	}
	return out
}
