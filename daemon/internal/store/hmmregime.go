package store

import "context"

// UpsertHMMRegime records the HMM's current volatility label for one symbol.
// One row per symbol, overwritten each pass — this is CURRENT state, not a
// timeline. The graded timeline lives in the labeled feature vectors (the
// hmm_<label> one-hot written at prediction time), which is what any later
// comparison against regime_state has to be run on.
func (s *Store) UpsertHMMRegime(ctx context.Context, symbolID, ts int64,
	label string, prob float64, nStates int, sdLow, sdHigh float64) error {
	_, err := s.w.ExecContext(ctx, `
		INSERT INTO hmm_regime_state (symbol_id, ts, label, prob, n_states, sd_low, sd_high)
		VALUES (?,?,?,?,?,?,?)
		ON CONFLICT(symbol_id) DO UPDATE SET
		  ts=excluded.ts, label=excluded.label, prob=excluded.prob,
		  n_states=excluded.n_states, sd_low=excluded.sd_low, sd_high=excluded.sd_high`,
		symbolID, ts, label, prob, nStates, sdLow, sdHigh)
	return err
}

// HMMRegimeLabels returns the latest HMM volatility label per symbol id.
// Symbols the runner has not reached yet are simply absent, and every caller
// treats a missing label the same way it treats a missing regime_state label:
// as unknown, never as a default state.
func (s *Store) HMMRegimeLabels(ctx context.Context) (map[int64]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT symbol_id, label FROM hmm_regime_state`)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	out := map[int64]string{}
	for rows.Next() {
		var id int64
		var lbl string
		if err := rows.Scan(&id, &lbl); err != nil {
			return nil, err
		}
		out[id] = lbl
	}
	return out, rows.Err()
}
