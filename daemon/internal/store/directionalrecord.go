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
	"database/sql"

	"github.com/nyaungnicholas-wq/signaldeck/internal/clusterstat"
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

	// Days is the per-UTC-day tally behind N — the clusters a consumer needs
	// to measure the design effect (clusterstat.DesignEffect) and evaluate any
	// interval at an EFFECTIVE sample size instead of treating N's symbol-days
	// as independent trials. Not serialized: surfaces publish the derived
	// effective N, not the raw tallies.
	Days []clusterstat.Day `json:"-"`
}

// FirstResolutionAt returns the earliest resolved_at among a horizon's graded
// outcome rows (ok=false when none have resolved yet). Model-health uses it to
// align the ensemble-vs-benchmark head-to-head on the benchmark's own live
// window: the prequential-majority rows only exist since the tracked-benchmark
// wave, and grading the ensemble's whole record against them would compare
// different stretches of market.
func (s *Store) FirstResolutionAt(ctx context.Context, h md.Horizon) (int64, bool, error) {
	var ts sql.NullInt64
	err := s.db.QueryRowContext(ctx, `
		SELECT MIN(resolved_at) FROM prediction_outcomes
		WHERE horizon=? AND resolved_at IS NOT NULL AND up IS NOT NULL`,
		string(h)).Scan(&ts)
	if err != nil {
		return 0, false, err
	}
	return ts.Int64, ts.Valid, nil
}

// DirectionalRecord grades resolved predictions for one horizon. `since`
// filters by resolution time (0 = the whole record).
func (s *Store) DirectionalRecord(ctx context.Context, h md.Horizon, since int64) (DirectionalRecordRow, error) {
	q := `
	WITH dedup AS (
	  SELECT symbol_id, prob, up,
	         ROW_NUMBER() OVER (PARTITION BY symbol_id, settle_day(settle_ts, ts) ORDER BY ts DESC) rn
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

	// The same dedup, folded per day, so the caller can measure how much of N
	// is one market move counted many times.
	qDays := `
	WITH dedup AS (
	  SELECT settle_day(settle_ts, ts) AS day, prob, up,
	         ROW_NUMBER() OVER (PARTITION BY symbol_id, settle_day(settle_ts, ts) ORDER BY ts DESC) rn
	  FROM prediction_outcomes
	  WHERE horizon = ? AND resolved_at IS NOT NULL AND up IS NOT NULL
	    AND prob IS NOT NULL AND resolved_at >= ?
	)
	SELECT day, COUNT(*),
	       SUM(CASE WHEN (prob >= 0.5) = (up = 1) THEN 1 ELSE 0 END)
	FROM dedup WHERE rn = 1 GROUP BY day ORDER BY day`
	rows, err := s.db.QueryContext(ctx, qDays, string(h), since)
	if err != nil {
		return r, err
	}
	defer rows.Close() //nolint:errcheck
	for rows.Next() {
		var d clusterstat.Day
		if err := rows.Scan(&d.Day, &d.N, &d.Hits); err != nil {
			return r, err
		}
		r.Days = append(r.Days, d)
	}
	return r, rows.Err()
}
