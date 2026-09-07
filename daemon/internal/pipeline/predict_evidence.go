package pipeline

import (
	"context"
	"encoding/json"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// writeEvidenceRow writes a single evidence row for a symbol and horizon.
// NUsed=0 marks the row as evidence-only (never graded, never served),
// one per symbol per trading day, written by BOTH the no-admitted-leg path
// and the cross-section-gate path so the coverage monitor's denominator
// is the whole universe.
func (w *PredictionRunner) writeEvidenceRow(ctx context.Context, evidenceDay map[md.Horizon]map[int64]int64, h md.Horizon, symbolID, ts int64, raw float64, comps any, basis string) error {
	day := md.TradingDay(ts)
	if evidenceDay[h] == nil {
		evidenceDay[h] = make(map[int64]int64)
	}
	if prev, seen := evidenceDay[h][symbolID]; seen && prev >= day {
		return nil
	}
	b, err := json.Marshal(comps)
	if err != nil {
		b = []byte("{}")
	}
	components := string(b)
	pred := store.Prediction{
		SymbolID:   symbolID,
		Horizon:    h,
		Ts:         ts,
		RawProb:    raw,
		CalProb:    raw,
		NUsed:      0,
		Components: components,
		Weights:    "{}",
		Basis:      basis,
	}
	if err := w.St.UpsertPrediction(ctx, pred); err != nil {
		return err
	}
	evidenceDay[h][symbolID] = day
	return nil
}
