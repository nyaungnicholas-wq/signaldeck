package store

import (
	"context"
	"path/filepath"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

func openDataExpStore(t *testing.T) (*Store, md.Symbol, md.Symbol) {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "dataexp.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	nvda, err := st.UpsertSymbol(ctx, "NVDA", md.Stocks, "NVIDIA Corp")
	if err != nil {
		t.Fatalf("seed NVDA: %v", err)
	}
	btc, err := st.UpsertSymbol(ctx, "BTC/USD", md.Crypto, "Bitcoin")
	if err != nil {
		t.Fatalf("seed BTC: %v", err)
	}
	return st, nvda, btc
}

func TestShortInterestRoundTrip(t *testing.T) {
	st, nvda, _ := openDataExpStore(t)
	ctx := context.Background()
	rows := []ShortInterestRow{
		{SymbolID: nvda.ID, Settlement: "2026-05-31", ShortQty: 100, PrevQty: 90, ADV: 10, DaysToCover: 10, ChangePct: 11.1},
		{SymbolID: nvda.ID, Settlement: "2026-06-15", ShortQty: 120, PrevQty: 100, ADV: 12, DaysToCover: 10, ChangePct: 20},
	}
	if err := st.UpsertShortInterest(ctx, rows); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	// Re-upsert (revision) is idempotent, newest wins.
	rows[1].ShortQty = 125
	if err := st.UpsertShortInterest(ctx, rows[1:]); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	got, err := st.ShortInterestRecent(ctx, nvda.ID, 8)
	if err != nil {
		t.Fatalf("recent: %v", err)
	}
	if len(got) != 2 || got[0].Settlement != "2026-06-15" || got[0].ShortQty != 125 ||
		got[1].Settlement != "2026-05-31" {
		t.Errorf("recent wrong (want newest-first, revised qty): %+v", got)
	}
}

func TestCryptoPerpRoundTrip(t *testing.T) {
	st, _, btc := openDataExpStore(t)
	ctx := context.Background()
	for i, ts := range []int64{1000, 2000, 3000} {
		if err := st.InsertCryptoPerp(ctx, CryptoPerpRow{
			SymbolID: btc.ID, Ts: ts, Funding: float64(i) * 0.0001, OpenInterest: 100 + float64(i), MarkPx: 64000,
		}); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	got, err := st.CryptoPerpSeries(ctx, btc.ID, 2000)
	if err != nil {
		t.Fatalf("series: %v", err)
	}
	if len(got) != 2 || got[0].Ts != 2000 || got[1].Ts != 3000 {
		t.Errorf("series wrong (want ASC from since): %+v", got)
	}
}

func TestCOTRoundTrip(t *testing.T) {
	st, _, _ := openDataExpStore(t)
	ctx := context.Background()
	rows := []COTRow{
		{Contract: "E-MINI S&P 500", ReportDate: "2026-06-30", NoncommLong: 1, NoncommShort: 2, CommLong: 3, CommShort: 4, OpenInterest: 5},
		{Contract: "E-MINI S&P 500", ReportDate: "2026-07-07", NoncommLong: 6, NoncommShort: 7, CommLong: 8, CommShort: 9, OpenInterest: 10},
		{Contract: "BITCOIN", ReportDate: "2026-07-07", NoncommLong: 11, NoncommShort: 12, CommLong: 13, CommShort: 14, OpenInterest: 15},
	}
	if err := st.UpsertCOT(ctx, rows); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, err := st.COTSince(ctx, "2026-01-01")
	if err != nil {
		t.Fatalf("since: %v", err)
	}
	if len(got) != 3 || got[0].Contract != "BITCOIN" ||
		got[1].ReportDate != "2026-06-30" || got[2].ReportDate != "2026-07-07" {
		t.Errorf("ordering wrong (want contract ASC, date ASC): %+v", got)
	}
	latest, err := st.LatestCOTDate(ctx)
	if err != nil || latest != "2026-07-07" {
		t.Errorf("latest = %q err=%v, want 2026-07-07", latest, err)
	}
	// Windowing excludes older rows.
	if got, _ := st.COTSince(ctx, "2026-07-01"); len(got) != 2 {
		t.Errorf("windowed rows = %d, want 2", len(got))
	}
}

func TestStocktwitsRoundTrip(t *testing.T) {
	st, nvda, _ := openDataExpStore(t)
	ctx := context.Background()
	if err := st.InsertStocktwits(ctx, StocktwitsRow{SymbolID: nvda.ID, Ts: 5000, Bullish: 10, Bearish: 3, Untagged: 17, Total: 30}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	got, err := st.StocktwitsSeries(ctx, nvda.ID, 0)
	if err != nil || len(got) != 1 || got[0].Bullish != 10 || got[0].Total != 30 {
		t.Errorf("series wrong: %+v err=%v", got, err)
	}
}

func TestWikiArticleCacheAndViews(t *testing.T) {
	st, nvda, _ := openDataExpStore(t)
	ctx := context.Background()

	if _, found, err := st.GetWikiArticle(ctx, nvda.ID); err != nil || found {
		t.Fatalf("expected not-found before caching: found=%v err=%v", found, err)
	}
	// A FAILED resolution is cached too (so the worker never re-hammers).
	if err := st.SetWikiArticle(ctx, WikiArticle{SymbolID: nvda.ID, OK: false, ResolvedAt: 1}); err != nil {
		t.Fatalf("set failed resolution: %v", err)
	}
	wa, found, _ := st.GetWikiArticle(ctx, nvda.ID)
	if !found || wa.OK || wa.Article != "" {
		t.Errorf("cached failure wrong: %+v", wa)
	}
	// Overwrite with a success.
	if err := st.SetWikiArticle(ctx, WikiArticle{SymbolID: nvda.ID, Article: "Nvidia", OK: true, ResolvedAt: 2}); err != nil {
		t.Fatalf("set resolution: %v", err)
	}
	wa, _, _ = st.GetWikiArticle(ctx, nvda.ID)
	if !wa.OK || wa.Article != "Nvidia" {
		t.Errorf("resolution wrong: %+v", wa)
	}

	views := []WikiViewRow{
		{SymbolID: nvda.ID, Day: "2026-07-01", Views: 4826},
		{SymbolID: nvda.ID, Day: "2026-07-02", Views: 5120},
	}
	if err := st.UpsertWikiViews(ctx, views); err != nil {
		t.Fatalf("upsert views: %v", err)
	}
	got, err := st.WikiViewsSeries(ctx, nvda.ID, 90)
	if err != nil || len(got) != 2 || got[0].Day != "2026-07-01" || got[1].Views != 5120 {
		t.Errorf("views series wrong (want ASC): %+v err=%v", got, err)
	}
}

func TestCompanyNamesByTickers(t *testing.T) {
	st, _, _ := openDataExpStore(t)
	ctx := context.Background()
	if err := st.UpsertCompanies(ctx, []CompanyRow{
		{CIK: 1045810, Ticker: "NVDA", Name: "NVIDIA CORP", Exchange: "Nasdaq", UpdatedTs: 1},
		{CIK: 320193, Ticker: "AAPL", Name: "Apple Inc.", Exchange: "Nasdaq", UpdatedTs: 1},
	}); err != nil {
		t.Fatalf("seed companies: %v", err)
	}
	got, err := st.CompanyNamesByTickers(ctx, []string{"NVDA", "ZZZZ"})
	if err != nil {
		t.Fatalf("names: %v", err)
	}
	if got["NVDA"] != "NVIDIA CORP" {
		t.Errorf("NVDA name = %q", got["NVDA"])
	}
	if _, ok := got["ZZZZ"]; ok {
		t.Error("unknown ticker must be absent (honest absence)")
	}
	if empty, err := st.CompanyNamesByTickers(ctx, nil); err != nil || len(empty) != 0 {
		t.Errorf("empty input must be a cheap no-op: %v %v", empty, err)
	}
}

func TestCboePCRoundTrip(t *testing.T) {
	st, _, _ := openDataExpStore(t)
	ctx := context.Background()
	for d, pc := range map[string]float64{"2026-07-08": 0.80, "2026-07-09": 0.86} {
		if err := st.UpsertCboePC(ctx, CboePCRow{
			Day: d, TotalPC: pc, IndexPC: 1.01, EquityPC: 0.57,
			VIXPC: 0.51, CallVol: 100, PutVol: 90, TotalVol: 190,
		}); err != nil {
			t.Fatalf("upsert: %v", err)
		}
	}
	got, err := st.CboePCSeries(ctx, 90)
	if err != nil || len(got) != 2 || got[0].Day != "2026-07-08" || got[1].TotalPC != 0.86 {
		t.Errorf("series wrong (want ASC): %+v err=%v", got, err)
	}
	has, err := st.HasCboePCDay(ctx, "2026-07-09")
	if err != nil || !has {
		t.Errorf("HasCboePCDay = %v err=%v", has, err)
	}
	if has, _ := st.HasCboePCDay(ctx, "2026-07-10"); has {
		t.Error("absent day must report false")
	}
}
