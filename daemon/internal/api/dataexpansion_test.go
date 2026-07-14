package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// newDataExpServer stands up the data-expansion routes behind the real
// middleware against a temp store (PublicReads on, like /api/shorts).
func newDataExpServer(t *testing.T) (*httptest.Server, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "dataexp_api.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	d := Deps{St: st, Cfg: baseCfg(), Version: "test", Started: time.Now()}
	srv := httptest.NewUnstartedServer(nil)
	t.Cleanup(srv.Close)
	d.Cfg.AllowedHosts = []string{srv.Listener.Addr().String()}

	mux := http.NewServeMux()
	d.registerDataExpansion(mux)
	srv.Config.Handler = d.secure(mux)
	srv.Start()
	return srv, st
}

func getDataExpJSON(t *testing.T, srv *httptest.Server, path string) map[string]any {
	t.Helper()
	resp, err := srv.Client().Get(srv.URL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close() //nolint:errcheck
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return out
}

func TestShortInterestAPI(t *testing.T) {
	srv, st := newDataExpServer(t)
	ctx := context.Background()
	nvda, _ := st.UpsertSymbol(ctx, "NVDA", md.Stocks, "NVIDIA")

	// Empty: honest absence, caveat still shipped verbatim.
	out := getDataExpJSON(t, srv, "/api/short-interest?symbol=NVDA&market=stocks")
	if out["latest"] != nil || out["emptyNote"] == nil {
		t.Errorf("empty payload wrong: %v", out)
	}
	if !strings.Contains(out["note"].(string), "published ~2wks lagged") ||
		!strings.Contains(out["note"].(string), "not advice") {
		t.Errorf("caveat must ship verbatim: %v", out["note"])
	}

	_ = st.UpsertShortInterest(ctx, []store.ShortInterestRow{
		{SymbolID: nvda.ID, Settlement: "2026-05-31", ShortQty: 90},
		{SymbolID: nvda.ID, Settlement: "2026-06-15", ShortQty: 100, DaysToCover: 2.5},
	})
	out = getDataExpJSON(t, srv, "/api/short-interest?symbol=NVDA&market=stocks")
	latest := out["latest"].(map[string]any)
	prev := out["previous"].(map[string]any)
	if latest["settlementDate"] != "2026-06-15" || latest["shortQty"] != 100.0 ||
		prev["settlementDate"] != "2026-05-31" {
		t.Errorf("latest/previous wrong: %v / %v", latest, prev)
	}
}

func TestCryptoPerpAPI(t *testing.T) {
	srv, st := newDataExpServer(t)
	ctx := context.Background()
	btc, _ := st.UpsertSymbol(ctx, "BTC/USD", md.Crypto, "Bitcoin")
	now := time.Now().Unix()
	_ = st.InsertCryptoPerp(ctx, store.CryptoPerpRow{SymbolID: btc.ID, Ts: now - 60, Funding: 0.0000125, OpenInterest: 35315.99, MarkPx: 64135})

	out := getDataExpJSON(t, srv, "/api/crypto-perp?symbol=BTC/USD&market=crypto")
	if !strings.Contains(out["note"].(string), "Hyperliquid (a DEX)") {
		t.Errorf("caveat must ship verbatim: %v", out["note"])
	}
	latest := out["latest"].(map[string]any)
	if latest["funding"] != 0.0000125 || len(out["series"].([]any)) != 1 {
		t.Errorf("payload wrong: %v", out)
	}
}

func TestCOTAPI(t *testing.T) {
	srv, st := newDataExpServer(t)
	ctx := context.Background()

	out := getDataExpJSON(t, srv, "/api/cot")
	if out["emptyNote"] == nil || !strings.Contains(out["note"].(string), "positioning, not prediction") {
		t.Errorf("empty payload wrong: %v", out)
	}

	recent := time.Now().AddDate(0, -1, 0).Format("2006-01-02")
	older := time.Now().AddDate(0, -2, 0).Format("2006-01-02")
	_ = st.UpsertCOT(ctx, []store.COTRow{
		{Contract: "E-MINI S&P 500", ReportDate: older, NoncommLong: 1, OpenInterest: 5},
		{Contract: "E-MINI S&P 500", ReportDate: recent, NoncommLong: 2, OpenInterest: 6},
		{Contract: "BITCOIN", ReportDate: recent, NoncommLong: 3, OpenInterest: 7},
	})
	out = getDataExpJSON(t, srv, "/api/cot")
	contracts := out["contracts"].([]any)
	if len(contracts) != 2 {
		t.Fatalf("contracts = %d, want 2", len(contracts))
	}
	es := contracts[1].(map[string]any) // sorted: BITCOIN, E-MINI
	if es["contract"] != "E-MINI S&P 500" ||
		es["latest"].(map[string]any)["reportDate"] != recent ||
		len(es["series"].([]any)) != 2 {
		t.Errorf("E-mini group wrong: %v", es)
	}
}

func TestStocktwitsAPI(t *testing.T) {
	srv, st := newDataExpServer(t)
	ctx := context.Background()
	nvda, _ := st.UpsertSymbol(ctx, "NVDA", md.Stocks, "")
	_ = st.InsertStocktwits(ctx, store.StocktwitsRow{SymbolID: nvda.ID, Ts: time.Now().Unix(), Bullish: 10, Bearish: 3, Untagged: 17, Total: 30})

	out := getDataExpJSON(t, srv, "/api/stocktwits?symbol=NVDA&market=stocks")
	if !strings.Contains(out["note"].(string), "self-selected crowd") ||
		!strings.Contains(out["snapshotNote"].(string), "PAGE SNAPSHOT") {
		t.Errorf("caveats must ship verbatim: %v", out)
	}
	if out["latest"].(map[string]any)["bullish"] != 10.0 {
		t.Errorf("latest wrong: %v", out["latest"])
	}
}

func TestWikiAttentionAPI(t *testing.T) {
	srv, st := newDataExpServer(t)
	ctx := context.Background()
	nvda, _ := st.UpsertSymbol(ctx, "NVDA", md.Stocks, "")
	_ = st.SetWikiArticle(ctx, store.WikiArticle{SymbolID: nvda.ID, Article: "Nvidia", OK: true, ResolvedAt: 1})
	rows := make([]store.WikiViewRow, 0, 12)
	base := time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 12; i++ {
		rows = append(rows, store.WikiViewRow{SymbolID: nvda.ID, Day: base.AddDate(0, 0, i).Format("2006-01-02"), Views: int64(1000 + i)})
	}
	_ = st.UpsertWikiViews(ctx, rows)

	out := getDataExpJSON(t, srv, "/api/wiki-attention?symbol=NVDA&market=stocks")
	if out["note"] != "public attention proxy — not a trading signal" {
		t.Errorf("caveat must ship verbatim: %v", out["note"])
	}
	if out["article"] != "Nvidia" || out["resolved"] != true {
		t.Errorf("resolution echo wrong: %v", out)
	}
	if len(out["series"].([]any)) != 12 {
		t.Errorf("series = %d, want 12", len(out["series"].([]any)))
	}
	if out["latestZ"] == nil {
		t.Error("12 days (11 prior) should clear the 10-prior z gate")
	}

	// Unresolved symbol: honest resolution note.
	junk, _ := st.UpsertSymbol(ctx, "JUNK", md.Stocks, "")
	_ = st.SetWikiArticle(ctx, store.WikiArticle{SymbolID: junk.ID, OK: false, ResolvedAt: 1})
	out = getDataExpJSON(t, srv, "/api/wiki-attention?symbol=JUNK&market=stocks")
	if out["resolved"] != false || out["resolutionNote"] == nil {
		t.Errorf("unresolved payload wrong: %v", out)
	}
}

func TestCboePCAPI(t *testing.T) {
	srv, st := newDataExpServer(t)
	ctx := context.Background()

	out := getDataExpJSON(t, srv, "/api/cboe-pc")
	if out["latest"] != nil || out["emptyNote"] == nil {
		t.Errorf("empty payload wrong: %v", out)
	}
	if !strings.Contains(out["note"].(string), "NOT directly bearish") {
		t.Errorf("caveat must ship verbatim: %v", out["note"])
	}

	_ = st.UpsertCboePC(ctx, store.CboePCRow{Day: "2026-07-08", TotalPC: 0.80})
	_ = st.UpsertCboePC(ctx, store.CboePCRow{Day: "2026-07-09", TotalPC: 0.86, EquityPC: 0.57})
	out = getDataExpJSON(t, srv, "/api/cboe-pc?days=30")
	latest := out["latest"].(map[string]any)
	if latest["day"] != "2026-07-09" || latest["totalPC"] != 0.86 || len(out["series"].([]any)) != 2 {
		t.Errorf("payload wrong: %v", out)
	}
}

func TestTVQuoteAPI(t *testing.T) {
	srv, st := newDataExpServer(t)
	ctx := context.Background()
	nvda, _ := st.UpsertSymbol(ctx, "NVDA", md.Stocks, "")

	out := getDataExpJSON(t, srv, "/api/tv-quote?symbol=NVDA&market=stocks")
	if out["available"] != false || out["emptyNote"] == nil {
		t.Errorf("empty payload wrong: %v", out)
	}
	if !strings.Contains(out["note"].(string), "not a replacement") {
		t.Errorf("caveat must ship verbatim: %v", out["note"])
	}

	_ = st.InsertTVQuote(ctx, store.TVQuoteRow{
		SymbolID: nvda.ID, Ts: time.Now().Unix(), Price: 314.96, DelayedClose: 315.32,
		ChangePct: -1.4, DayVolume: 34132321, Realtime: true,
	})
	out = getDataExpJSON(t, srv, "/api/tv-quote?symbol=NVDA&market=stocks")
	q := out["quote"].(map[string]any)
	if out["available"] != true || q["price"] != 314.96 || q["realtime"] != true || q["delayedClose"] != 315.32 {
		t.Errorf("quote payload wrong: %v", out)
	}
}
