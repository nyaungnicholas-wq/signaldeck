package store

import (
	"context"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// CommittedSignal is one calibrated P(up) the pipeline actually committed at
// Ts for a symbol — read back for the stress replay, which follows signalbt's
// discipline of never recomputing a signal, only replaying the committed one.
// Resolution status is irrelevant here: the replay grades system BEHAVIOR on
// a counterfactual path, not the signal's realized outcome.
type CommittedSignal struct {
	Ts   int64
	Prob float64
}

// CommittedSignals returns the committed signal series for one symbol/horizon
// over [from, to], ascending, capped at limit.
func (s *Store) CommittedSignals(ctx context.Context, symbolID int64, h md.Horizon, from, to int64, limit int) ([]CommittedSignal, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT ts, prob FROM prediction_outcomes
		WHERE symbol_id=? AND horizon=? AND ts BETWEEN ? AND ?
		ORDER BY ts ASC LIMIT ?`,
		symbolID, string(h), from, to, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []CommittedSignal
	for rows.Next() {
		var c CommittedSignal
		if err := rows.Scan(&c.Ts, &c.Prob); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
