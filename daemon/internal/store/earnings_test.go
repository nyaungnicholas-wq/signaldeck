package store

import (
	"context"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

func TestRecordEarningsEvents(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	sym, err := st.UpsertSymbol(ctx, "ZZZ", md.Stocks, "Zed")
	if err != nil {
		t.Fatal(err)
	}
	if n, err := st.RecordEarningsEvents(ctx, sym.ID, nil, 1); n != 0 || err != nil {
		t.Fatalf("empty: n=%d err=%v", n, err)
	}
	n, err := st.RecordEarningsEvents(ctx, sym.ID, []EarningsEvent{
		{"a1", "8-K", 1700000000, "2.02,9.01"},
		{"a2", "8-K/A", 1600000000, "2.02"},
	}, 1790000000)
	if err != nil || n != 2 {
		t.Fatalf("first insert: n=%d err=%v want 2", n, err)
	}
	// a1 seen again with a different time: the first value must be kept.
	n, err = st.RecordEarningsEvents(ctx, sym.ID, []EarningsEvent{
		{"a1", "8-K", 1700014400, "2.02"},
		{"a3", "8-K", 1500000000, "2.02"},
	}, 1790009999)
	if err != nil || n != 1 {
		t.Fatalf("second insert: n=%d err=%v want 1", n, err)
	}
	var filed, seen, sid int64
	var items string
	if err := st.db.QueryRowContext(ctx, `SELECT filed_ts, items, first_seen_ts, symbol_id FROM earnings_events WHERE accession='a1'`).Scan(&filed, &items, &seen, &sid); err != nil {
		t.Fatal(err)
	}
	if filed != 1700000000 || items != "2.02,9.01" || seen != 1790000000 || sid != sym.ID {
		t.Fatalf("a1 changed: filed=%d items=%q seen=%d sym=%d", filed, items, seen, sid)
	}
	var c int
	if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM earnings_events`).Scan(&c); err != nil || c != 3 {
		t.Fatalf("count=%d err=%v want 3", c, err)
	}
}
