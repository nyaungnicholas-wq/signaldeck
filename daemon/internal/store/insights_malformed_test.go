// Stage 6 verify regression: one insights row whose `data` is not valid JSON
// ('' — the Go zero value, written by pre-fix InsertInsight for writers like
// the Risk watcher that persist no evidence blob) made InsightsByKind fail
// outright with "SQL logic error: malformed JSON (1)", which 500'd
// GET /api/dashboard. Guards both halves of the fix:
//   - InsertInsight normalizes "" → '{}' (schema default, still valid JSON);
//   - InsightsByKind tolerates legacy malformed rows already on disk.
package store

import (
	"context"
	"testing"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

func TestInsertInsight_EmptyDataStoredAsEmptyJSONObject(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()

	if err := st.InsertInsight(ctx, md.Insight{
		Scope: "market", Ts: time.Now().Unix(),
		Headline: "Risk watcher", Body: "flags…", // Data left unset
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}

	var data string
	if err := st.db.QueryRowContext(ctx,
		`SELECT data FROM insights ORDER BY id DESC LIMIT 1`).Scan(&data); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if data != "{}" {
		t.Fatalf("empty Data stored as %q, want %q", data, "{}")
	}
}

func TestInsightsByKind_ToleratesLegacyMalformedDataRows(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()
	now := time.Now().Unix()

	// Legacy row exactly as pre-fix InsertInsight wrote it: data = ''.
	if _, err := st.w.ExecContext(ctx, `
		INSERT INTO insights (scope, symbol_id, ts, headline, body, data)
		VALUES ('market', NULL, ?, 'Risk watcher', 'legacy', '')`, now); err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}
	// A real briefing row that must still be found.
	if err := st.InsertInsight(ctx, md.Insight{
		Scope: "market", Ts: now + 1,
		Headline: "Daily briefing", Body: "text",
		Data: `{"kind":"daily_briefing"}`,
	}); err != nil {
		t.Fatalf("insert briefing: %v", err)
	}

	got, err := st.InsightsByKind(ctx, "daily_briefing", 10)
	if err != nil {
		t.Fatalf("InsightsByKind must tolerate malformed legacy rows, got: %v", err)
	}
	if len(got) != 1 || got[0].Headline != "Daily briefing" {
		t.Fatalf("want exactly the briefing row, got %+v", got)
	}
}
