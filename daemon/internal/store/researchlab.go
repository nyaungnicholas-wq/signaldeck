package store

import (
	"context"
	"database/sql"
)

// HypothesisRow is one Research-Lab shadow/promoted/rejected hypothesis.
type HypothesisRow struct {
	ID           string  `json:"id"`
	Kind         string  `json:"kind"`
	Spec         string  `json:"spec"` // Hypothesis JSON
	Description  string  `json:"description"`
	Status       string  `json:"status"`
	DiscoveredAt int64   `json:"discoveredAt"`
	BaseLift     float64 `json:"baseLift"`
	DiscLift     float64 `json:"discLift"`
	LastLift     float64 `json:"lastLift"`
	LastWilson   float64 `json:"lastWilson"`
	LastN        int     `json:"lastN"`
	PassStreak   int     `json:"passStreak"`
	FailStreak   int     `json:"failStreak"`
	Evals        int     `json:"evals"`
	PromotedAt   int64   `json:"promotedAt"`
	UpdatedAt    int64   `json:"updatedAt"`
}

// InsertShadowHypothesis records a newly-discovered survivor as a shadow. It is
// idempotent: INSERT OR IGNORE on the spec-hash PK means re-discovering the same
// hypothesis on a later night does NOT reset its accrued streak/eval history.
func (s *Store) InsertShadowHypothesis(ctx context.Context, h HypothesisRow) error {
	_, err := s.w.ExecContext(ctx, `
		INSERT OR IGNORE INTO research_hypotheses
		  (id, kind, spec, description, status, discovered_at,
		   base_lift, disc_lift, last_lift, last_wilson, last_n,
		   pass_streak, fail_streak, evals, promoted_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		h.ID, h.Kind, h.Spec, h.Description, h.Status, h.DiscoveredAt,
		h.BaseLift, h.DiscLift, h.LastLift, h.LastWilson, h.LastN,
		h.PassStreak, h.FailStreak, h.Evals, h.PromotedAt, h.UpdatedAt)
	return err
}

// UpdateHypothesisEval writes the outcome of one re-evaluation: the fresh OOS
// numbers, the updated streaks/status, and promoted_at (0 unless promoting).
func (s *Store) UpdateHypothesisEval(ctx context.Context, h HypothesisRow) error {
	_, err := s.w.ExecContext(ctx, `
		UPDATE research_hypotheses SET
		  status=?, last_lift=?, last_wilson=?, last_n=?,
		  pass_streak=?, fail_streak=?, evals=?, promoted_at=?, updated_at=?
		WHERE id=?`,
		h.Status, h.LastLift, h.LastWilson, h.LastN,
		h.PassStreak, h.FailStreak, h.Evals, h.PromotedAt, h.UpdatedAt, h.ID)
	return err
}

// HypothesesByStatus returns hypotheses with the given status (empty ⇒ all),
// newest-discovered first, capped.
func (s *Store) HypothesesByStatus(ctx context.Context, status string, limit int) ([]HypothesisRow, error) {
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	var (
		rows *sql.Rows
		err  error
	)
	q := `SELECT id, kind, spec, description, status, discovered_at,
	             base_lift, disc_lift, last_lift, last_wilson, last_n,
	             pass_streak, fail_streak, evals, promoted_at, updated_at
	      FROM research_hypotheses `
	if status == "" {
		rows, err = s.db.QueryContext(ctx, q+`ORDER BY discovered_at DESC LIMIT ?`, limit)
	} else {
		rows, err = s.db.QueryContext(ctx, q+`WHERE status=? ORDER BY discovered_at DESC LIMIT ?`, status, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []HypothesisRow
	for rows.Next() {
		var h HypothesisRow
		if err := rows.Scan(&h.ID, &h.Kind, &h.Spec, &h.Description, &h.Status, &h.DiscoveredAt,
			&h.BaseLift, &h.DiscLift, &h.LastLift, &h.LastWilson, &h.LastN,
			&h.PassStreak, &h.FailStreak, &h.Evals, &h.PromotedAt, &h.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// HypothesisStatusCounts returns a status → count map for the lab summary.
func (s *Store) HypothesisStatusCounts(ctx context.Context) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT status, COUNT(*) FROM research_hypotheses GROUP BY status`)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	out := map[string]int{}
	for rows.Next() {
		var st string
		var n int
		if err := rows.Scan(&st, &n); err != nil {
			return nil, err
		}
		out[st] = n
	}
	return out, rows.Err()
}
