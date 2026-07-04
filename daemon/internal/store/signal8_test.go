package store

import (
	"context"
	"path/filepath"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

func openSignal8Store(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "s8.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestFilings_InsertDedupAndQuery(t *testing.T) {
	st := openSignal8Store(t)
	ctx := context.Background()
	aapl, _ := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple Inc.")
	nvda, _ := st.UpsertSymbol(ctx, "NVDA", md.Stocks, "NVIDIA Corp")

	rows := []FilingRow{
		{ID: "acc-1", SymbolID: aapl.ID, Form: "4", FiledTs: 100, Title: "FORM 4", URL: "u1", Label: "Form 4 — insider transaction"},
		{ID: "acc-2", SymbolID: aapl.ID, Form: "8-K", FiledTs: 200, Title: "8-K", URL: "u2", Label: "8-K — earnings release (Item 2.02)"},
		{ID: "acc-3", SymbolID: nvda.ID, Form: "424B5", FiledTs: 300, Title: "424B5", URL: "u3", Label: "424B5 — offering prospectus (dilution)"},
	}
	for _, r := range rows {
		isNew, err := st.InsertFiling(ctx, r)
		if err != nil || !isNew {
			t.Fatalf("insert %s: new=%v err=%v", r.ID, isNew, err)
		}
	}
	// Idempotent re-insert reports NOT new.
	if isNew, err := st.InsertFiling(ctx, rows[0]); err != nil || isNew {
		t.Fatalf("re-insert: new=%v err=%v", isNew, err)
	}

	// Fleet-wide, newest first.
	all, err := st.Filings(ctx, 0, "", 10)
	if err != nil || len(all) != 3 {
		t.Fatalf("all = %d err=%v", len(all), err)
	}
	if all[0].ID != "acc-3" || all[0].Symbol != "NVDA" {
		t.Errorf("newest first wrong: %+v", all[0])
	}

	// Per-symbol + form-prefix filter (424B catches 424B5).
	nv, err := st.Filings(ctx, nvda.ID, "424B", 10)
	if err != nil || len(nv) != 1 || nv[0].ID != "acc-3" {
		t.Fatalf("form filter = %+v err=%v", nv, err)
	}

	// FilingsSince for dilution derivation.
	since, err := st.FilingsSince(ctx, nvda.ID, []string{"S-1", "S-3", "424B"}, 250)
	if err != nil || len(since) != 1 {
		t.Fatalf("since = %+v err=%v", since, err)
	}
	if got, _ := st.FilingsSince(ctx, nvda.ID, []string{"S-1", "S-3", "424B"}, 301); len(got) != 0 {
		t.Errorf("since-cutoff leak: %+v", got)
	}
}

func TestUnparsedForm4s(t *testing.T) {
	st := openSignal8Store(t)
	ctx := context.Background()
	aapl, _ := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple Inc.")

	_, _ = st.InsertFiling(ctx, FilingRow{ID: "f4-a", SymbolID: aapl.ID, Form: "4", FiledTs: 100})
	_, _ = st.InsertFiling(ctx, FilingRow{ID: "f4-b", SymbolID: aapl.ID, Form: "4", FiledTs: 200})
	_, _ = st.InsertFiling(ctx, FilingRow{ID: "k8", SymbolID: aapl.ID, Form: "8-K", FiledTs: 300})

	// One parsed already.
	_ = st.InsertInsiderTrade(ctx, InsiderTradeRow{Accession: "f4-a", SymbolID: aapl.ID, Code: "S"})

	got, err := st.UnparsedForm4s(ctx, 10)
	if err != nil || len(got) != 1 || got[0].ID != "f4-b" {
		t.Fatalf("unparsed = %+v err=%v", got, err)
	}
}

func TestInsiderTrades_QueryAndFilter(t *testing.T) {
	st := openSignal8Store(t)
	ctx := context.Background()
	aapl, _ := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple Inc.")
	nvda, _ := st.UpsertSymbol(ctx, "NVDA", md.Stocks, "NVIDIA Corp")

	trades := []InsiderTradeRow{
		{Accession: "t1", SymbolID: aapl.ID, Insider: "COOK TIMOTHY D", Title: "CEO", Code: "S", Shares: 75000, Price: 210.7, Value: 15802500, TxTs: 90, FiledTs: 100},
		{Accession: "t2", SymbolID: nvda.ID, Insider: "DOE JANE", Title: "Director", Code: "P", Shares: 500, Price: 120, Value: 60000, TxTs: 190, FiledTs: 200},
		{Accession: "t3", SymbolID: nvda.ID, Insider: "DOE JANE", Title: "Director", Code: "A", Shares: 1200, Price: 0, Value: 0, TxTs: 290, FiledTs: 300},
	}
	for _, tr := range trades {
		if err := st.InsertInsiderTrade(ctx, tr); err != nil {
			t.Fatalf("insert %s: %v", tr.Accession, err)
		}
	}
	// Idempotent.
	if err := st.InsertInsiderTrade(ctx, trades[0]); err != nil {
		t.Fatalf("re-insert: %v", err)
	}

	all, err := st.InsiderTrades(ctx, 0, "", 10)
	if err != nil || len(all) != 3 {
		t.Fatalf("all = %d err=%v", len(all), err)
	}
	if all[0].Accession != "t3" || all[0].Symbol != "NVDA" {
		t.Errorf("newest first wrong: %+v", all[0])
	}

	buys, err := st.InsiderTrades(ctx, nvda.ID, "P", 10)
	if err != nil || len(buys) != 1 || buys[0].Accession != "t2" {
		t.Fatalf("code filter = %+v err=%v", buys, err)
	}
}

func TestInstHoldings_UpsertPeriodsAndViews(t *testing.T) {
	st := openSignal8Store(t)
	ctx := context.Background()
	aapl, _ := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple Inc.")
	sid := aapl.ID

	old := InstHoldingRow{CIK: "1067983", Manager: "Berkshire Hathaway", Period: "2025-12-31",
		SymbolID: &sid, CUSIP: "037833100", Name: "APPLE INC", Value: 60e9, Shares: 280e6}
	cur := InstHoldingRow{CIK: "1067983", Manager: "Berkshire Hathaway", Period: "2026-03-31",
		SymbolID: &sid, CUSIP: "037833100", Name: "APPLE INC", Value: 65e9, Shares: 300e6}
	unmatched := InstHoldingRow{CIK: "1067983", Manager: "Berkshire Hathaway", Period: "2026-03-31",
		CUSIP: "674599105", Name: "OBSCURE WIDGETS LTD", Value: 12e6, Shares: 450e3}
	for _, h := range []InstHoldingRow{old, cur, unmatched} {
		if err := st.UpsertInstHolding(ctx, h); err != nil {
			t.Fatalf("upsert: %v", err)
		}
	}

	if has, _ := st.HasInstPeriod(ctx, "1067983", "2026-03-31"); !has {
		t.Error("HasInstPeriod current = false, want true")
	}
	if has, _ := st.HasInstPeriod(ctx, "1067983", "2026-06-30"); has {
		t.Error("HasInstPeriod future = true, want false")
	}

	// By symbol: only the LATEST period row shows.
	bySym, err := st.InstHoldingsBySymbol(ctx, aapl.ID, 10)
	if err != nil || len(bySym) != 1 {
		t.Fatalf("bySym = %+v err=%v", bySym, err)
	}
	if bySym[0].Period != "2026-03-31" || bySym[0].Value != 65e9 {
		t.Errorf("latest-period wrong: %+v", bySym[0])
	}

	// By manager: latest period only, unmatched row kept with nil symbol.
	byMgr, err := st.InstHoldingsByManager(ctx, "1067983", 10)
	if err != nil || len(byMgr) != 2 {
		t.Fatalf("byMgr = %+v err=%v", byMgr, err)
	}
	if byMgr[0].Value != 65e9 { // largest first
		t.Errorf("order wrong: %+v", byMgr[0])
	}
	if byMgr[1].SymbolID != nil {
		t.Errorf("unmatched row must keep symbol_id NULL: %+v", byMgr[1])
	}

	// Managers overview.
	mgrs, err := st.InstManagers(ctx)
	if err != nil || len(mgrs) != 1 {
		t.Fatalf("managers = %+v err=%v", mgrs, err)
	}
	if mgrs[0]["period"] != "2026-03-31" || mgrs[0]["positions"] != int64(2) {
		t.Errorf("manager row = %+v", mgrs[0])
	}
}

func TestDilutionFlags_UpsertReadFlagged(t *testing.T) {
	st := openSignal8Store(t)
	ctx := context.Background()
	aapl, _ := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple Inc.")
	nvda, _ := st.UpsertSymbol(ctx, "NVDA", md.Stocks, "NVIDIA Corp")

	if _, ok, err := st.DilutionFlag(ctx, aapl.ID); err != nil || ok {
		t.Fatalf("empty read: ok=%v err=%v", ok, err)
	}

	_ = st.UpsertDilutionFlag(ctx, DilutionFlagRow{SymbolID: aapl.ID, Level: "low", Reasons: "[]", UpdatedTs: 10})
	_ = st.UpsertDilutionFlag(ctx, DilutionFlagRow{SymbolID: nvda.ID, Level: "high",
		Reasons: `["2 dilution-shaped filing(s) in last 180d: S-3","shares outstanding +4.0%"]`, UpdatedTs: 20})

	f, ok, err := st.DilutionFlag(ctx, nvda.ID)
	if err != nil || !ok || f.Level != "high" {
		t.Fatalf("read = %+v ok=%v err=%v", f, ok, err)
	}

	// Upsert replaces in place.
	_ = st.UpsertDilutionFlag(ctx, DilutionFlagRow{SymbolID: nvda.ID, Level: "elevated", Reasons: "[]", UpdatedTs: 30})
	f, _, _ = st.DilutionFlag(ctx, nvda.ID)
	if f.Level != "elevated" || f.UpdatedTs != 30 {
		t.Errorf("upsert-in-place failed: %+v", f)
	}

	// Flagged list excludes low.
	flagged, err := st.DilutionFlagged(ctx, 10)
	if err != nil || len(flagged) != 1 || flagged[0].Symbol != "NVDA" {
		t.Fatalf("flagged = %+v err=%v", flagged, err)
	}
}
