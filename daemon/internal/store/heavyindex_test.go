package store

import (
	"context"
	"strings"
	"testing"
)

// These are the exact reads of ScoresHeavyBelow and CompositeHeavyBelow (scoresretention.go).
// On the plain ts index they walked every already-stripped row below the cutoff (3.21M of 3.30M on 2026-10-01),
// keeping one read snapshot open up to the scores-compactor's 3h deadline, which pinned the WAL (14 GB).
// The partial indexes hold only un-stripped rows.
func TestCompactorHeavyReadsUseThePartialIndexes(t *testing.T) {
	st := openTemp(t)
	cases := []struct {
		query string
		index string
	}{
		{
			query: `SELECT symbol_id, horizon, ts, score, components FROM scores WHERE ts < 1 AND components != '[]' AND EXISTS (SELECT 1 FROM scores s3 WHERE s3.symbol_id=scores.symbol_id AND s3.horizon=scores.horizon AND s3.ts>scores.ts) ORDER BY ts ASC LIMIT 50000`,
			index: `idx_scores_heavy`,
		},
		{
			query: `SELECT symbol_id, ts, horizon, score, curve_pct, edge, payload FROM composite_scores WHERE ts < 1 AND payload != '{}' AND EXISTS (SELECT 1 FROM composite_scores c3 WHERE c3.symbol_id=composite_scores.symbol_id AND c3.horizon=composite_scores.horizon AND c3.ts>composite_scores.ts) ORDER BY ts ASC LIMIT 50000`,
			index: `idx_composite_heavy`,
		},
	}
	for _, c := range cases {
		rows, err := st.w.QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+c.query)
		if err != nil {
			t.Fatalf("failed to explain %s: %v", c.index, err)
		}
		var details []string
		for rows.Next() {
			var id, parent, notused int
			var detail string
			if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
				_ = rows.Close()
				t.Fatalf("scan failed for %s: %v", c.index, err)
			}
			details = append(details, detail)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			t.Fatalf("rows error for %s: %v", c.index, err)
		}
		_ = rows.Close()
		plan := strings.Join(details, "; ")
		if !strings.Contains(plan, "USING INDEX "+c.index) {
			t.Fatalf("compactor read does not seek %s: %s\nquery: %s", c.index, plan, c.query)
		}
		t.Logf("%s: %s", c.index, plan)
	}
}
