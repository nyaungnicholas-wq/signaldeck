package store

import (
	"context"
	"strings"
	"testing"
)

// The outcome resolver reads its pending queue one keyset page at a time. On
// idx_outcomes_unresolved each page read every pending row of every horizon,
// sorted them in a temp b-tree and looked up each table row (0.59 s a page on
// a copy of the 2026-10-01 snapshot); on idx_outcomes_pending it is a covered
// seek from the cursor in index order (0.095 s).
func TestPendingOutcomePageIsACoveredSeek(t *testing.T) {
	st := openTemp(t)
	rows, err := st.w.QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+pendingOutcomesSQL, "1d", int64(1790800000), int64(1788800000), int64(0), 4000)
	if err != nil {
		t.Fatalf("failed to explain pending-outcome page: %v", err)
	}
	var details []string
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			_ = rows.Close()
			t.Fatalf("scan failed for pending-outcome page: %v", err)
		}
		details = append(details, detail)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		t.Fatalf("rows error for pending-outcome page: %v", err)
	}
	_ = rows.Close()
	plan := strings.Join(details, "; ")
	if !strings.Contains(plan, "USING COVERING INDEX idx_outcomes_pending") {
		t.Fatalf("pending-outcome page is not a covered seek on idx_outcomes_pending: %s", plan)
	}
	if strings.Contains(plan, "TEMP B-TREE") {
		t.Fatalf("pending-outcome page sorts in a temp b-tree instead of reading index order: %s", plan)
	}
	t.Logf("plan: %s", plan)
}
