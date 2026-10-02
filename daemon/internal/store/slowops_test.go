package store

import (
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// SLOW-OPS (2026-10-02): /api/postmortems answered in 28-150 s on a loaded
// daemon because both of its reads scanned the whole 80 MB table. Each read
// must stay on its index, and the indexes must not change any result.
func TestPostmortemReadsUseTheirIndexes(t *testing.T) {
	st := openTestStore(t)
	ctx := t.Context()

	// Create 5 stock symbols
	var symbols [5]md.Symbol
	for i := 0; i < 5; i++ {
		sym, err := st.UpsertSymbol(ctx, fmt.Sprintf("PM%d", i), md.Stocks, fmt.Sprintf("PM%d", i))
		if err != nil {
			t.Fatalf("UpsertSymbol: %v", err)
		}
		symbols[i] = sym
	}

	// Insert 400 rows in one transaction
	tx, err := st.w.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	for i := 0; i < 400; i++ {
		symbolID := symbols[i%5].ID
		horizon := "1d"
		ts := int64(1700000000 + int64(i)*3600)
		prob := 0.6
		up := 0
		fwdReturn := -0.001 * float64(i%17+1)
		conviction := 0.1 + 0.001*float64(i%13)
		magnitude := 0.001 * float64(i%17+1)
		primaryReason := []string{"calibration_error", "regime_shift", "data_quality", "news_shock"}[i%4]
		if i%7 == 0 {
			primaryReason = "model_disagreement"
		}
		secondaryReason := ""
		reasons := fmt.Sprintf(`[{"code":"r%d"}]`, i)
		createdAt := int64(1700000000 + int64(i)*1800)

		_, err = tx.ExecContext(ctx, `
			INSERT OR IGNORE INTO prediction_postmortems
			  (symbol_id, horizon, ts, prob, up, fwd_return, conviction, magnitude,
			   primary_reason, secondary_reason, reasons, created_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
			symbolID, horizon, ts, prob, up, fwdReturn, conviction, magnitude,
			primaryReason, secondaryReason, reasons, createdAt)
		if err != nil {
			tx.Rollback() //nolint:errcheck
			t.Fatalf("Insert row %d: %v", i, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	// Helper to get EXPLAIN QUERY PLAN as a string
	plan := func(q string, args ...any) string {
		// The write connection: it ran the DROPs below, so its schema is current.
		rows, err := st.w.QueryContext(ctx, "EXPLAIN QUERY PLAN "+q, args...)
		if err != nil {
			t.Fatalf("EXPLAIN: %v", err)
		}
		defer rows.Close() //nolint:errcheck
		var details []string
		for rows.Next() {
			var id, parent, notused int
			var detail string
			if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
				t.Fatalf("Scan EXPLAIN row: %v", err)
			}
			details = append(details, detail)
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("Rows error: %v", err)
		}
		return strings.Join(details, "\n")
	}

	// Check index usage before dropping
	since := int64(1700000000 + 200*1800)
	if !strings.Contains(plan(pmClustersSQL, since), "COVERING INDEX idx_postmortem_reason_cover") {
		t.Fatalf("Clusters plan does not use index idx_postmortem_reason_cover:\n%s", plan(pmClustersSQL, since))
	}
	if !strings.Contains(plan(pmRecentSQL, 100), "idx_postmortem_ts") {
		t.Fatalf("Recent plan does not use index idx_postmortem_ts:\n%s", plan(pmRecentSQL, 100))
	}
	if strings.Contains(plan(pmRecentSQL, 100), "TEMP B-TREE FOR ORDER BY") {
		t.Fatalf("Recent plan uses temp b-tree for order by:\n%s", plan(pmRecentSQL, 100))
	}

	// Read with indexes
	c1, n1, err := st.PostmortemClusters(ctx, since)
	if err != nil {
		t.Fatalf("PostmortemClusters: %v", err)
	}
	r1, err := st.RecentPostmortems(ctx, 100)
	if err != nil {
		t.Fatalf("RecentPostmortems: %v", err)
	}
	c1all, n1all, err := st.PostmortemClusters(ctx, 0)
	if err != nil {
		t.Fatalf("PostmortemClusters all: %v", err)
	}
	if n1 != 200 {
		t.Fatalf("Expected n1=200, got %d", n1)
	}
	if n1all != 400 {
		t.Fatalf("Expected n1all=400, got %d", n1all)
	}
	if len(r1) != 100 {
		t.Fatalf("Expected 100 recent rows, got %d", len(r1))
	}
	expectedTs := int64(1700000000 + 399*3600)
	if r1[0].Ts != expectedTs {
		t.Fatalf("Expected most recent ts %d, got %d", expectedTs, r1[0].Ts)
	}

	// Drop indexes
	if _, err := st.w.ExecContext(ctx, "DROP INDEX idx_postmortem_ts"); err != nil {
		t.Fatalf("Drop ts index: %v", err)
	}
	if _, err := st.w.ExecContext(ctx, "DROP INDEX idx_postmortem_reason_cover"); err != nil {
		t.Fatalf("Drop cover index: %v", err)
	}
	if strings.Contains(plan(pmClustersSQL, since), "idx_postmortem_reason_cover") {
		t.Fatalf("Clusters plan still mentions index idx_postmortem_reason_cover after drop:\n%s", plan(pmClustersSQL, since))
	}

	// Read again after dropping indexes
	c2, n2, err := st.PostmortemClusters(ctx, since)
	if err != nil {
		t.Fatalf("PostmortemClusters after drop: %v", err)
	}
	r2, err := st.RecentPostmortems(ctx, 100)
	if err != nil {
		t.Fatalf("RecentPostmortems after drop: %v", err)
	}
	c2all, n2all, err := st.PostmortemClusters(ctx, 0)
	if err != nil {
		t.Fatalf("PostmortemClusters all after drop: %v", err)
	}
	if n2 != n1 {
		t.Fatalf("Expected n2=%d, got %d", n1, n2)
	}
	if n2all != n1all {
		t.Fatalf("Expected n2all=%d, got %d", n1all, n2all)
	}
	if !reflect.DeepEqual(r1, r2) {
		t.Fatalf("Recent rows changed after dropping indexes")
	}
	if len(c1) != len(c2) {
		t.Fatalf("Cluster length changed: %d vs %d", len(c1), len(c2))
	}
	if len(c1all) != len(c2all) {
		t.Fatalf("Cluster all length changed: %d vs %d", len(c1all), len(c2all))
	}
	sameClusters := func(t *testing.T, name string, a, b []PostmortemCluster) {
		if len(a) != len(b) {
			t.Fatalf("%s length: %d vs %d", name, len(a), len(b))
			return
		}
		for i := range a {
			if a[i].Code != b[i].Code {
				t.Fatalf("%s[%d].Code: %s vs %s", name, i, a[i].Code, b[i].Code)
			}
			if a[i].Count != b[i].Count {
				t.Fatalf("%s[%d].Count: %d vs %d", name, i, a[i].Count, b[i].Count)
			}
			if math.Abs(a[i].Share-b[i].Share) > 1e-12 {
				t.Fatalf("%s[%d].Share: %g vs %g", name, i, a[i].Share, b[i].Share)
			}
			if math.Abs(a[i].MeanMag-b[i].MeanMag) > 1e-12 {
				t.Fatalf("%s[%d].MeanMag: %g vs %g", name, i, a[i].MeanMag, b[i].MeanMag)
			}
			if math.Abs(a[i].MeanConv-b[i].MeanConv) > 1e-12 {
				t.Fatalf("%s[%d].MeanConv: %g vs %g", name, i, a[i].MeanConv, b[i].MeanConv)
			}
		}
	}
	sameClusters(t, "clusters since", c1, c2)
	sameClusters(t, "clusters all", c1all, c2all)
}

// SLOW-OPS (2026-10-02): LoopEngineHealth needs only the corpus row count. It
// used to compute ResearchWeeksStats, whose coverage pass scans every daily bar
// in the corpus span (2.4 s alone on the 2026-10-01 snapshot; /api/research-loop
// 86-150 s under load). The count must be unchanged and must not touch bars.
func TestLoopEngineHealthCountsWithoutReadingBars(t *testing.T) {
	st := openTestStore(t)
	ctx := t.Context()

	// Upsert symbol RW0
	sym, err := st.UpsertSymbol(ctx, "RW0", md.Stocks, "RW0")
	if err != nil {
		t.Fatalf("UpsertSymbol: %v", err)
	}

	// Build 30 ResearchWeek rows
	var rows []ResearchWeek
	for i := 0; i < 30; i++ {
		week := int64(2800 + i)
		ts := (2800+int64(i))*604800 + 86400
		vec := map[string]float64{"x": float64(i)}
		rows = append(rows, ResearchWeek{
			SymbolID:  sym.ID,
			Week:      week,
			Ts:        ts,
			Vec:       vec,
			FwdReturn: 0.01,
			Up:        true,
			Era:       "y2026",
			HighVol:   false,
		})
	}
	if err := st.UpsertResearchWeeks(ctx, rows, 0); err != nil {
		t.Fatalf("UpsertResearchWeeks: %v", err)
	}

	// Get ResearchWeeksStats
	stats, err := st.ResearchWeeksStats(ctx)
	if err != nil {
		t.Fatalf("ResearchWeeksStats: %v", err)
	}
	if stats.Rows != 30 {
		t.Fatalf("Expected stats.Rows=30, got %d", stats.Rows)
	}

	// Get LoopEngineHealth
	h, err := st.LoopEngineHealth(ctx, 1, time.Now())
	if err != nil {
		t.Fatalf("LoopEngineHealth: %v", err)
	}
	if h.CorpusRows != stats.Rows {
		t.Fatalf("Expected health.CorpusRows=%d, got %d", stats.Rows, h.CorpusRows)
	}

	// Rename bars table
	if _, err := st.w.ExecContext(ctx, "ALTER TABLE bars RENAME TO bars_moved_for_test"); err != nil {
		t.Fatalf("Rename bars: %v", err)
	}

	// Premise: ResearchWeeksStats should now fail because it reads bars for coverage
	if _, err := st.ResearchWeeksStats(ctx); err == nil {
		t.Fatal("ResearchWeeksStats no longer reads bars; this test's premise is gone")
	}

	// LoopEngineHealth should still work
	h2, err := st.LoopEngineHealth(ctx, 1, time.Now())
	if err != nil {
		t.Fatalf("LoopEngineHealth read bars: %v", err)
	}
	if h2.CorpusRows != 30 {
		t.Fatalf("Expected health2.CorpusRows=30, got %d", h2.CorpusRows)
	}
}
