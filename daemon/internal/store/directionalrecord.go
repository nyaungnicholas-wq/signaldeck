// Directional-record accessor for model-health grading (2026-07-24).
//
// One query, one honest shape: accuracy, the naive baseline it must beat,
// Brier skill, and calibration error — computed over INDEPENDENT observations
// only (one per symbol per UTC day, keeping that day's latest prediction).
//
// The independence collapse is not an optimisation. The prediction pipeline
// writes many rows per symbol per forward period that all resolve against the
// SAME move; pooling them inflates n by ~60x and turns noise into a confident
// verdict. A health gate computed on pseudo-replicated counts would retire and
// revive models on nothing at all.
package store

import (
	"context"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// DirectionalRecordRow is one model's live scoreboard.
type DirectionalRecordRow struct {
	N              int     `json:"n"`
	Accuracy       float64 `json:"accuracy"`
	BaselineAcc    float64 `json:"baselineAcc"` // best naive constant predictor
	UpRate         float64 `json:"upRate"`
	BrierSkill     float64 `json:"brierSkill"`
	CalibrationErr float64 `json:"calibrationErr"`
}

// DirectionalRecord grades resolved predictions for one horizon. `since`
// filters by resolution time (0 = the whole record).
func (s *Store) DirectionalRecord(ctx context.Context, h md.Horizon, since int64) (DirectionalRecordRow, error) {
	q := `
	WITH dedup AS (
	  SELECT symbol_id, prob, up,
	         ROW_NUMBER() OVER (PARTITION BY symbol_id, ts/86400 ORDER BY ts DESC) rn
	  FROM prediction_outcomes
	  WHERE horizon = ? AND resolved_at IS NOT NULL AND up IS NOT NULL
	    AND prob IS NOT NULL AND resolved_at >= ?
	)
	SELECT COUNT(*),
	       AVG(CASE WHEN (prob >= 0.5) = (up = 1) THEN 1.0 ELSE 0.0 END),
	       AVG(CASE WHEN up = 1 THEN 1.0 ELSE 0.0 END),
	       AVG((prob - up) * (prob - up)),
	       AVG(ABS(prob - up))
	FROM dedup WHERE rn = 1`

	var r DirectionalRecordRow
	var acc, upRate, brier, calErr *float64
	if err := s.db.QueryRowContext(ctx, q, string(h), since).
		Scan(&r.N, &acc, &upRate, &brier, &calErr); err != nil {
		return r, err
	}
	if r.N == 0 || acc == nil || upRate == nil {
		return r, nil
	}
	r.Accuracy = *acc
	r.UpRate = *upRate

	// The honest benchmark is the best CONSTANT predictor — always guess the
	// majority class. Grading against 50% manufactures skill whenever the
	// up-rate is imbalanced, which for equities it always is.
	r.BaselineAcc = r.UpRate
	if 1-r.UpRate > r.BaselineAcc {
		r.BaselineAcc = 1 - r.UpRate
	}

	if brier != nil {
		// Brier skill vs the base-rate constant forecast: >0 beats it.
		ref := r.UpRate * (1 - r.UpRate)
		if ref > 0 {
			r.BrierSkill = 1 - *brier/ref
		}
	}
	if calErr != nil {
		r.CalibrationErr = *calErr
	}
	return r, nil
}
