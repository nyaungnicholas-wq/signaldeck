// Per-symbol accessors for the signal detail report (GET /api/signal-report).
package store

import "context"

// SymbolPredictionOutcome is one resolved live prediction on a symbol — the
// per-symbol slice of the platform's forward record.
type SymbolPredictionOutcome struct {
	Ts        int64   `json:"ts"`
	Horizon   string  `json:"horizon"`
	Prob      float64 `json:"prob"`
	Up        bool    `json:"up"`
	FwdReturn float64 `json:"fwdReturn"`
	Correct   bool    `json:"correct"`
}

// PredictionOutcomesForSymbol returns a symbol's resolved prediction outcomes,
// newest first, plus the symbol-level hit count — its personal live record.
func (s *Store) PredictionOutcomesForSymbol(ctx context.Context, symbolID int64, horizon string, limit int) (rows []SymbolPredictionOutcome, correct, total int, err error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	res, err := s.db.QueryContext(ctx, `
		SELECT ts, horizon, prob, up, COALESCE(fwd_return,0)
		FROM prediction_outcomes
		WHERE symbol_id=? AND horizon=? AND resolved_at IS NOT NULL AND up IS NOT NULL
		ORDER BY ts DESC LIMIT ?`, symbolID, horizon, limit)
	if err != nil {
		return nil, 0, 0, err
	}
	defer res.Close() //nolint:errcheck
	for res.Next() {
		var o SymbolPredictionOutcome
		var up int
		if err := res.Scan(&o.Ts, &o.Horizon, &o.Prob, &up, &o.FwdReturn); err != nil {
			return nil, 0, 0, err
		}
		o.Up = up == 1
		o.Correct = (o.Prob >= 0.5) == o.Up
		rows = append(rows, o)
	}
	if err := res.Err(); err != nil {
		return nil, 0, 0, err
	}
	err = s.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(CASE WHEN (prob>=0.5)=(up=1) THEN 1 ELSE 0 END),0), COUNT(*)
		FROM prediction_outcomes
		WHERE symbol_id=? AND horizon=? AND resolved_at IS NOT NULL AND up IS NOT NULL`,
		symbolID, horizon).Scan(&correct, &total)
	return rows, correct, total, err
}
