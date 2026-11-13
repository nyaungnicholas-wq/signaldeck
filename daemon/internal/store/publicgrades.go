package store

import "context"

// RegimeKindTally is one kind's frozen-call record: how many calls are open,
// how many are graded, over how many distinct days, and how often the call and
// the naive-persistence baseline were each right on the SAME graded rows.
type RegimeKindTally struct {
	Kind                                   string
	Open, Graded, Days, Correct, BaselineN int
	BaselineCorrect                        int
	// CorrectOnBaseline is Correct restricted to the BaselineN rows, so a
	// rate shown beside the baseline is measured on the same forecasts.
	CorrectOnBaseline int
}

// RegimeKindTallies counts live (not superseded, not ungradable) regime calls
// per kind. The baseline is tallied only on rows that carry one.
func (s *Store) RegimeKindTallies(ctx context.Context) ([]RegimeKindTally, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT kind,
		       COALESCE(SUM(resolved_at IS NULL), 0),
		       COALESCE(SUM(resolved_at IS NOT NULL), 0),
		       COUNT(DISTINCT CASE WHEN resolved_at IS NOT NULL THEN day END),
		       COALESCE(SUM(correct), 0),
		       COALESCE(SUM(resolved_at IS NOT NULL AND naive_label IS NOT NULL), 0),
		       COALESCE(SUM(resolved_at IS NOT NULL AND naive_label IS NOT NULL AND naive_label = actual), 0),
		       COALESCE(SUM(CASE WHEN resolved_at IS NOT NULL AND naive_label IS NOT NULL THEN correct END), 0)
		FROM regime_outcomes
		WHERE superseded_by IS NULL AND ungradable IS NULL
		GROUP BY kind ORDER BY kind`)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []RegimeKindTally
	for rows.Next() {
		var t RegimeKindTally
		if err := rows.Scan(&t.Kind, &t.Open, &t.Graded, &t.Days, &t.Correct, &t.BaselineN, &t.BaselineCorrect, &t.CorrectOnBaseline); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
