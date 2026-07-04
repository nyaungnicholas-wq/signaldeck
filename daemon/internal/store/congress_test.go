package store

import (
	"context"
	"path/filepath"
	"testing"
)

func openCongressStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "congress.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestCongressInsertDedupAndQuery(t *testing.T) {
	st := openCongressStore(t)
	ctx := context.Background()

	sid := int64(7)
	rows := []CongressTradeRow{
		{ID: "h1", Chamber: "senate", Member: "Jane Q Senator", Symbol: "AAPL", SymbolID: &sid,
			TxType: "purchase", AmountRange: "$15,001 - $50,000", TxTs: 3000, DisclosedTs: 6000},
		{ID: "h2", Chamber: "house", Member: "Hon. Alexis Example", Symbol: "NVDA",
			TxType: "sale_full", AmountRange: "$1,001 - $15,000", TxTs: 2000, DisclosedTs: 5000},
		{ID: "h3", Chamber: "senate", Member: "John P Senator", Symbol: "ZZTOP",
			TxType: "sale_partial", AmountRange: "$1,001 - $15,000", TxTs: 1000, DisclosedTs: 4000},
	}
	for _, r := range rows {
		isNew, err := st.InsertCongressTrade(ctx, r)
		if err != nil || !isNew {
			t.Fatalf("insert %s: new=%v err=%v", r.ID, isNew, err)
		}
	}
	// Re-insert = ignored (hash-PK idempotency).
	if isNew, err := st.InsertCongressTrade(ctx, rows[0]); err != nil || isNew {
		t.Fatalf("re-insert: new=%v err=%v (want false, nil)", isNew, err)
	}

	// Unfiltered, newest tx first.
	got, err := st.CongressTrades(ctx, "", "", "", 0)
	if err != nil {
		t.Fatalf("CongressTrades: %v", err)
	}
	if len(got) != 3 || got[0].ID != "h1" || got[2].ID != "h3" {
		t.Fatalf("order = %+v", got)
	}
	// Matched vs unmatched symbol_id round-trips.
	if got[0].SymbolID == nil || *got[0].SymbolID != 7 {
		t.Errorf("h1 symbolId = %v, want 7", got[0].SymbolID)
	}
	if got[1].SymbolID != nil {
		t.Errorf("h2 symbolId = %v, want nil (untracked ticker stays honest NULL)", got[1].SymbolID)
	}

	// Symbol filter matches the disclosed ticker text (tracked or not).
	if got, _ = st.CongressTrades(ctx, "ZZTOP", "", "", 0); len(got) != 1 || got[0].ID != "h3" {
		t.Fatalf("symbol filter = %+v", got)
	}
	// Member substring, case-insensitive.
	if got, _ = st.CongressTrades(ctx, "", "alexis", "", 0); len(got) != 1 || got[0].ID != "h2" {
		t.Fatalf("member filter = %+v", got)
	}
	// Chamber filter.
	if got, _ = st.CongressTrades(ctx, "", "", "senate", 0); len(got) != 2 {
		t.Fatalf("chamber filter = %+v", got)
	}
	// Limit.
	if got, _ = st.CongressTrades(ctx, "", "", "", 1); len(got) != 1 || got[0].ID != "h1" {
		t.Fatalf("limit = %+v", got)
	}
}

func TestCongressActivitySince(t *testing.T) {
	st := openCongressStore(t)
	ctx := context.Background()
	for _, r := range []CongressTradeRow{
		{ID: "a", Chamber: "senate", Member: "M", Symbol: "AAPL", TxType: "purchase", TxTs: 100, DisclosedTs: 150},
		{ID: "b", Chamber: "house", Member: "N", Symbol: "AAPL", TxType: "sale_full", TxTs: 900, DisclosedTs: 950},
		{ID: "c", Chamber: "house", Member: "N", Symbol: "TSLA", TxType: "purchase", TxTs: 900, DisclosedTs: 950},
	} {
		if _, err := st.InsertCongressTrade(ctx, r); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	n, last, err := st.CongressActivitySince(ctx, "AAPL", 500)
	if err != nil || n != 1 || last != 900 {
		t.Fatalf("since 500: n=%d last=%d err=%v", n, last, err)
	}
	n, last, err = st.CongressActivitySince(ctx, "AAPL", 0)
	if err != nil || n != 2 || last != 900 {
		t.Fatalf("since 0: n=%d last=%d err=%v", n, last, err)
	}
	// No activity → zeros, no error (honest empty).
	n, last, err = st.CongressActivitySince(ctx, "MSFT", 0)
	if err != nil || n != 0 || last != 0 {
		t.Fatalf("no activity: n=%d last=%d err=%v", n, last, err)
	}
}
