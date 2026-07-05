// Signal8 wave — Stage 5 store tests: companies directory upsert semantics
// (SIC preservation), SIC update by CIK, list filters, facets, and the two
// batched fleet-wide maps (LatestDailyAll, LatestPeriodicFilingAll).
// t.TempDir stores only.
package store

import (
	"context"
	"path/filepath"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

func openCompaniesStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "companies.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func seedDirectory(t *testing.T, st *Store) {
	t.Helper()
	err := st.UpsertCompanies(context.Background(), []CompanyRow{
		{CIK: 1045810, Ticker: "NVDA", Name: "NVIDIA CORP", Exchange: "Nasdaq", UpdatedTs: 100},
		{CIK: 320193, Ticker: "AAPL", Name: "Apple Inc.", Exchange: "Nasdaq", UpdatedTs: 100},
		{CIK: 1067983, Ticker: "BRK-B", Name: "BERKSHIRE HATHAWAY INC", Exchange: "NYSE", UpdatedTs: 100},
		{CIK: 803016, Ticker: "CFNB", Name: "CALIFORNIA FIRST LEASING CORP", Exchange: "", UpdatedTs: 100},
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
}

func TestUpsertCompanies_PreservesSICOnResync(t *testing.T) {
	st := openCompaniesStore(t)
	ctx := context.Background()
	seedDirectory(t, st)

	// Filings-poller enriches AAPL's SIC…
	n, err := st.UpdateCompanySICByCIK(ctx, 320193, "3571", "Electronic Computers", 200)
	if err != nil || n != 1 {
		t.Fatalf("update sic: n=%d err=%v", n, err)
	}
	// …then the daily sync re-upserts the whole map WITHOUT SIC (the exchange
	// file never carries it) — the enrichment must survive.
	if err := st.UpsertCompanies(ctx, []CompanyRow{
		{CIK: 320193, Ticker: "AAPL", Name: "Apple Inc. (renamed)", Exchange: "Nasdaq", UpdatedTs: 300},
	}); err != nil {
		t.Fatalf("resync: %v", err)
	}
	rows, err := st.ListCompanies(ctx, "AAPL", "", "")
	if err != nil || len(rows) != 1 {
		t.Fatalf("list: %v rows=%d", err, len(rows))
	}
	r := rows[0]
	if r.SIC != "3571" || r.SICDesc != "Electronic Computers" {
		t.Fatalf("SIC wiped by resync: %+v", r)
	}
	if r.Name != "Apple Inc. (renamed)" || r.UpdatedTs != 300 {
		t.Fatalf("resync did not refresh name/ts: %+v", r)
	}
}

func TestUpdateCompanySICByCIK_CoversShareClasses(t *testing.T) {
	st := openCompaniesStore(t)
	ctx := context.Background()
	if err := st.UpsertCompanies(ctx, []CompanyRow{
		{CIK: 1067983, Ticker: "BRK-A", Name: "BERKSHIRE", Exchange: "NYSE", UpdatedTs: 1},
		{CIK: 1067983, Ticker: "BRK-B", Name: "BERKSHIRE", Exchange: "NYSE", UpdatedTs: 1},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	n, err := st.UpdateCompanySICByCIK(ctx, 1067983, "6331", "Fire, Marine & Casualty Insurance", 2)
	if err != nil || n != 2 {
		t.Fatalf("share classes: n=%d err=%v (want 2)", n, err)
	}
	// Unknown CIK: zero rows affected (caller inserts a fallback).
	n, err = st.UpdateCompanySICByCIK(ctx, 999999, "1234", "Nope", 2)
	if err != nil || n != 0 {
		t.Fatalf("unknown cik: n=%d err=%v", n, err)
	}
}

func TestListCompanies_Filters(t *testing.T) {
	st := openCompaniesStore(t)
	ctx := context.Background()
	seedDirectory(t, st)
	_, _ = st.UpdateCompanySICByCIK(ctx, 320193, "3571", "Electronic Computers", 1)
	_, _ = st.UpdateCompanySICByCIK(ctx, 1045810, "3674", "Semiconductors & Related Devices", 1)

	// q: ticker prefix, case-insensitive.
	rows, err := st.ListCompanies(ctx, "nv", "", "")
	if err != nil || len(rows) != 1 || rows[0].Ticker != "NVDA" {
		t.Fatalf("q=nv: %+v err=%v", rows, err)
	}
	// q: name substring.
	rows, _ = st.ListCompanies(ctx, "berkshire", "", "")
	if len(rows) != 1 || rows[0].Ticker != "BRK-B" {
		t.Fatalf("q=berkshire: %+v", rows)
	}
	// sector: exact sic_desc.
	rows, _ = st.ListCompanies(ctx, "", "Electronic Computers", "")
	if len(rows) != 1 || rows[0].Ticker != "AAPL" {
		t.Fatalf("sector: %+v", rows)
	}
	// exchange: exact.
	rows, _ = st.ListCompanies(ctx, "", "", "Nasdaq")
	if len(rows) != 2 {
		t.Fatalf("exchange=Nasdaq: %+v", rows)
	}
	// combined.
	rows, _ = st.ListCompanies(ctx, "A", "Electronic Computers", "Nasdaq")
	if len(rows) != 1 || rows[0].Ticker != "AAPL" {
		t.Fatalf("combined: %+v", rows)
	}
	// no filters: everything, ticker order.
	rows, _ = st.ListCompanies(ctx, "", "", "")
	if len(rows) != 4 || rows[0].Ticker != "AAPL" {
		t.Fatalf("all: %+v", rows)
	}
	n, err := st.CompanyCount(ctx)
	if err != nil || n != 4 {
		t.Fatalf("count = %d err=%v", n, err)
	}
}

func TestCompanyFacets(t *testing.T) {
	st := openCompaniesStore(t)
	ctx := context.Background()
	seedDirectory(t, st)
	_, _ = st.UpdateCompanySICByCIK(ctx, 320193, "3571", "Electronic Computers", 1)

	sectors, err := st.CompanySectors(ctx, 10)
	if err != nil || len(sectors) != 1 || sectors[0].Value != "Electronic Computers" || sectors[0].N != 1 {
		t.Fatalf("sectors = %+v err=%v", sectors, err)
	}
	// '' exchange (SEC null) is not an option; counts are right.
	exch, err := st.CompanyExchanges(ctx)
	if err != nil || len(exch) != 2 {
		t.Fatalf("exchanges = %+v err=%v", exch, err)
	}
	if exch[0].Value != "Nasdaq" || exch[0].N != 2 || exch[1].Value != "NYSE" || exch[1].N != 1 {
		t.Fatalf("exchange order/counts = %+v", exch)
	}
}

func TestLatestDailyAll(t *testing.T) {
	st := openCompaniesStore(t)
	ctx := context.Background()
	a, _ := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "")
	b, _ := st.UpsertSymbol(ctx, "NVDA", md.Stocks, "")
	_, _ = st.UpsertSymbol(ctx, "MSFT", md.Stocks, "") // no bars — absent from map

	err := st.UpsertBars(ctx, []md.Bar{
		{SymbolID: a.ID, TF: md.TF1d, Ts: 1000, Open: 10, High: 10, Low: 10, Close: 10, Volume: 111},
		{SymbolID: a.ID, TF: md.TF1d, Ts: 2000, Open: 11, High: 11, Low: 11, Close: 11, Volume: 222},
		{SymbolID: b.ID, TF: md.TF1d, Ts: 2000, Open: 5, High: 5, Low: 5, Close: 5, Volume: 999},
		// 1m bars must NOT leak into the daily map.
		{SymbolID: a.ID, TF: md.TF1m, Ts: 3000, Open: 99, High: 99, Low: 99, Close: 99, Volume: 1},
	})
	if err != nil {
		t.Fatalf("bars: %v", err)
	}
	m, err := st.LatestDailyAll(ctx)
	if err != nil {
		t.Fatalf("latest daily: %v", err)
	}
	if len(m) != 2 {
		t.Fatalf("map size = %d, want 2 (honest absence for barless)", len(m))
	}
	da := m[a.ID]
	if da.Last != 11 || da.Prev != 10 || da.Volume != 222 || da.Ts != 2000 {
		t.Fatalf("AAPL = %+v", da)
	}
	db := m[b.ID]
	if db.Last != 5 || db.Prev != 0 || db.Volume != 999 {
		t.Fatalf("NVDA (single bar ⇒ prev 0) = %+v", db)
	}
}

func TestLatestPeriodicFilingAll(t *testing.T) {
	st := openCompaniesStore(t)
	ctx := context.Background()
	a, _ := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "")
	b, _ := st.UpsertSymbol(ctx, "NVDA", md.Stocks, "")

	ins := func(id, form string, symID, ts int64) {
		t.Helper()
		if _, err := st.InsertFiling(ctx, FilingRow{ID: id, SymbolID: symID, Form: form, FiledTs: ts}); err != nil {
			t.Fatalf("filing %s: %v", id, err)
		}
	}
	ins("acc-1", "10-Q", a.ID, 1000)
	ins("acc-2", "10-K", a.ID, 5000)   // newest periodic for AAPL
	ins("acc-3", "10-K/A", a.ID, 9000) // amendment — must NOT win
	ins("acc-4", "8-K", a.ID, 9500)    // not periodic
	ins("acc-5", "10-Q", b.ID, 7000)

	m, err := st.LatestPeriodicFilingAll(ctx)
	if err != nil {
		t.Fatalf("periodic: %v", err)
	}
	if len(m) != 2 {
		t.Fatalf("map = %+v", m)
	}
	if p := m[a.ID]; p.Form != "10-K" || p.FiledTs != 5000 {
		t.Fatalf("AAPL periodic = %+v (amendment/8-K leaked?)", p)
	}
	if p := m[b.ID]; p.Form != "10-Q" || p.FiledTs != 7000 {
		t.Fatalf("NVDA periodic = %+v", p)
	}
}
