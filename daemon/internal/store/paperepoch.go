// Paper-book EPOCHS: the strategy-change boundaries a track record must be
// split at.
//
// See the schema.sql banner for the doctrine. The short version: the book is
// continuous across a boundary (same cash, same positions) but the MEASUREMENT
// is not, because the rules that produced the numbers changed. Anything
// published — return, Sharpe, drawdown, win rate, the Kelly edge the next trade
// is sized from — must be computed inside one epoch.
package store

import (
	"context"
	"database/sql"
)

// PaperEpoch is one segment of a simulated book's life under a fixed rule set.
type PaperEpoch struct {
	Strategy string `json:"-"`
	Epoch    int    `json:"epoch"`
	FromTs   int64  `json:"fromTs"` // inclusive; 0 on epoch 1 means "since inception"
	Label    string `json:"label"`
	Reason   string `json:"reason"`
}

// UpsertPaperEpoch records or refreshes one boundary.
//
// Idempotent by (strategy, epoch) so the schedule declared in code can be
// re-applied on every pass and a rebuilt database reconstructs the same
// boundaries.
func (s *Store) UpsertPaperEpoch(ctx context.Context, e PaperEpoch) error {
	_, err := s.w.ExecContext(ctx, `
		INSERT INTO paper_epochs (strategy, epoch, from_ts, label, reason)
		VALUES (?,?,?,?,?)
		ON CONFLICT(strategy, epoch) DO UPDATE SET
		  from_ts=excluded.from_ts, label=excluded.label, reason=excluded.reason`,
		e.Strategy, e.Epoch, e.FromTs, e.Label, e.Reason)
	return err
}

// PaperEpochs returns a strategy's epochs, oldest first.
func (s *Store) PaperEpochs(ctx context.Context, strategy string) ([]PaperEpoch, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT epoch, from_ts, label, reason FROM paper_epochs
		WHERE strategy=? ORDER BY epoch`, strategy)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []PaperEpoch
	for rows.Next() {
		e := PaperEpoch{Strategy: strategy}
		if err := rows.Scan(&e.Epoch, &e.FromTs, &e.Label, &e.Reason); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// CurrentPaperEpoch returns the epoch in force at `at` — the newest boundary
// at or before it.
//
// ok=false means no epoch is declared for this strategy. Callers must treat
// that as "do not scope", never as "epoch starting at 0": silently inventing a
// boundary at the epoch is how a filter that was supposed to protect a
// measurement quietly stops filtering anything.
func (s *Store) CurrentPaperEpoch(ctx context.Context, strategy string, at int64) (PaperEpoch, bool, error) {
	e := PaperEpoch{Strategy: strategy}
	err := s.db.QueryRowContext(ctx, `
		SELECT epoch, from_ts, label, reason FROM paper_epochs
		WHERE strategy=? AND from_ts<=? ORDER BY from_ts DESC, epoch DESC LIMIT 1`,
		strategy, at).Scan(&e.Epoch, &e.FromTs, &e.Label, &e.Reason)
	if err != nil {
		if err == sql.ErrNoRows {
			return PaperEpoch{}, false, nil
		}
		return PaperEpoch{}, false, err
	}
	return e, true, nil
}

// EpochBounds returns the half-open [from, to) window of the i-th epoch in a
// list ordered oldest-first. `to` is 0 for the newest epoch, meaning "open".
//
// Half-open on purpose: a mark exactly ON a boundary belongs to the NEW epoch,
// so no row is counted twice and none falls between the two.
func EpochBounds(epochs []PaperEpoch, i int) (from, to int64) {
	if i < 0 || i >= len(epochs) {
		return 0, 0
	}
	from = epochs[i].FromTs
	if i+1 < len(epochs) {
		to = epochs[i+1].FromTs
	}
	return from, to
}

// InEpoch reports whether ts falls inside the half-open window [from, to).
func InEpoch(ts, from, to int64) bool {
	if ts < from {
		return false
	}
	return to == 0 || ts < to
}
