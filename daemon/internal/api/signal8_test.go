package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/config"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// newSignal8Server wires only the Signal8 Stage-1 routes behind the real
// middleware (separate harness so parallel edits never collide).
func newSignal8Server(t *testing.T) (*httptest.Server, *store.Store) {
	t.Helper()
	srv, st, d := newTestServer(t, func(c *config.Config) { c.PublicReads = true })
	mux := http.NewServeMux()
	d.registerSignal8(mux)
	srv.Config.Handler = d.secure(mux)
	return srv, st
}

func s8Get(t *testing.T, url string, out any) int {
	t.Helper()
	res, err := newClient(t).Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer res.Body.Close() //nolint:errcheck
	if res.StatusCode == 200 {
		if err := json.NewDecoder(res.Body).Decode(out); err != nil {
			t.Fatalf("decode %s: %v", url, err)
		}
	}
	return res.StatusCode
}

func TestFilingsEndpoint(t *testing.T) {
	srv, st := newSignal8Server(t)
	ctx := context.Background()
	aapl, _ := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple Inc.")
	nvda, _ := st.UpsertSymbol(ctx, "NVDA", md.Stocks, "NVIDIA Corp")
	_, _ = st.InsertFiling(ctx, store.FilingRow{ID: "a1", SymbolID: aapl.ID, Form: "8-K", FiledTs: 200, Label: "8-K — earnings release (Item 2.02)"})
	_, _ = st.InsertFiling(ctx, store.FilingRow{ID: "a2", SymbolID: nvda.ID, Form: "S-3", FiledTs: 300, Label: "S-3 — shelf registration (dilution watch)"})

	// Fleet-wide.
	var body struct {
		Filings []store.FilingRow `json:"filings"`
		Count   int               `json:"count"`
		Note    string            `json:"note"`
	}
	if code := s8Get(t, srv.URL+"/api/filings", &body); code != 200 {
		t.Fatalf("status = %d", code)
	}
	if body.Count != 2 || body.Filings[0].ID != "a2" {
		t.Fatalf("payload = %+v", body)
	}
	if body.Note == "" {
		t.Error("missing honesty note")
	}

	// Per-symbol + form filter.
	if code := s8Get(t, srv.URL+"/api/filings?symbol=AAPL&form=8-K", &body); code != 200 {
		t.Fatalf("status = %d", code)
	}
	if body.Count != 1 || body.Filings[0].ID != "a1" {
		t.Fatalf("filtered = %+v", body)
	}

	// Unknown symbol is a 404 (not an empty 200 pretending coverage).
	if code := s8Get(t, srv.URL+"/api/filings?symbol=ZZZZ", &body); code != 404 {
		t.Fatalf("unknown symbol status = %d, want 404", code)
	}
}

func TestInsidersEndpoint(t *testing.T) {
	srv, st := newSignal8Server(t)
	ctx := context.Background()
	aapl, _ := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple Inc.")
	_ = st.InsertInsiderTrade(ctx, store.InsiderTradeRow{
		Accession: "t1", SymbolID: aapl.ID, Insider: "COOK TIMOTHY D", Title: "CEO",
		Code: "S", Shares: 75000, Price: 210.7, Value: 15802500, TxTs: 90, FiledTs: 100})
	_ = st.InsertInsiderTrade(ctx, store.InsiderTradeRow{
		Accession: "t2", SymbolID: aapl.ID, Insider: "DOE JANE", Title: "Director",
		Code: "A", Shares: 1200, Price: 0, Value: 0, TxTs: 190, FiledTs: 200})

	var body struct {
		Trades []map[string]any `json:"trades"`
		Note   string           `json:"note"`
	}
	if code := s8Get(t, srv.URL+"/api/insiders?symbol=AAPL", &body); code != 200 {
		t.Fatalf("status = %d", code)
	}
	if len(body.Trades) != 2 {
		t.Fatalf("trades = %+v", body.Trades)
	}
	// Newest first: the A-grant. Its label must SAY it isn't an open-market trade.
	if body.Trades[0]["code"] != "A" || body.Trades[0]["openMarket"] != false {
		t.Errorf("grant row = %+v", body.Trades[0])
	}
	if lbl, _ := body.Trades[0]["codeLabel"].(string); lbl == "" || lbl == "buy (open market)" {
		t.Errorf("grant codeLabel = %q", lbl)
	}
	if body.Trades[1]["code"] != "S" || body.Trades[1]["openMarket"] != true {
		t.Errorf("sale row = %+v", body.Trades[1])
	}
	if body.Note == "" {
		t.Error("missing honesty note")
	}

	// code filter.
	if code := s8Get(t, srv.URL+"/api/insiders?symbol=AAPL&code=S", &body); code != 200 || len(body.Trades) != 1 {
		t.Fatalf("code filter: status=%d trades=%+v", code, body.Trades)
	}
}

func TestInstitutionsEndpoint(t *testing.T) {
	srv, st := newSignal8Server(t)
	ctx := context.Background()
	aapl, _ := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple Inc.")
	sid := aapl.ID
	_ = st.UpsertInstHolding(ctx, store.InstHoldingRow{
		CIK: "1067983", Manager: "Berkshire Hathaway", Period: "2026-03-31",
		SymbolID: &sid, CUSIP: "037833100", Name: "APPLE INC", Value: 65e9, Shares: 300e6})
	_ = st.UpsertInstHolding(ctx, store.InstHoldingRow{
		CIK: "1067983", Manager: "Berkshire Hathaway", Period: "2026-03-31",
		CUSIP: "674599105", Name: "OBSCURE WIDGETS LTD", Value: 12e6, Shares: 450e3})

	// Overview: stored managers + curated list.
	var overview struct {
		Managers []map[string]any `json:"managers"`
		Curated  []map[string]any `json:"curated"`
		Note     string           `json:"note"`
	}
	if code := s8Get(t, srv.URL+"/api/institutions", &overview); code != 200 {
		t.Fatalf("status = %d", code)
	}
	if len(overview.Managers) != 1 || len(overview.Curated) == 0 || overview.Note == "" {
		t.Fatalf("overview = %+v", overview)
	}

	// By symbol.
	var bySym struct {
		Holdings []store.InstHoldingRow `json:"holdings"`
	}
	if code := s8Get(t, srv.URL+"/api/institutions?symbol=AAPL", &bySym); code != 200 {
		t.Fatalf("status = %d", code)
	}
	if len(bySym.Holdings) != 1 || bySym.Holdings[0].Manager != "Berkshire Hathaway" {
		t.Fatalf("bySym = %+v", bySym.Holdings)
	}

	// By manager (name substring), symbol string resolved on matched rows.
	var byMgr struct {
		Holdings []store.InstHoldingRow `json:"holdings"`
		CIK      string                 `json:"cik"`
	}
	if code := s8Get(t, srv.URL+"/api/institutions?manager=berkshire", &byMgr); code != 200 {
		t.Fatalf("status = %d", code)
	}
	if byMgr.CIK != "1067983" || len(byMgr.Holdings) != 2 {
		t.Fatalf("byMgr = %+v", byMgr)
	}
	if byMgr.Holdings[0].Symbol != "AAPL" {
		t.Errorf("matched holding missing symbol string: %+v", byMgr.Holdings[0])
	}
	if byMgr.Holdings[1].SymbolID != nil {
		t.Errorf("unmatched holding must keep null symbolId: %+v", byMgr.Holdings[1])
	}

	// Unknown manager → 404.
	var junk map[string]any
	if code := s8Get(t, srv.URL+"/api/institutions?manager=nosuchfund", &junk); code != 404 {
		t.Fatalf("unknown manager status = %d", code)
	}
}

func TestDilutionEndpoint(t *testing.T) {
	srv, st := newSignal8Server(t)
	ctx := context.Background()
	aapl, _ := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple Inc.")
	nvda, _ := st.UpsertSymbol(ctx, "NVDA", md.Stocks, "NVIDIA Corp")
	_ = st.UpsertDilutionFlag(ctx, store.DilutionFlagRow{
		SymbolID: nvda.ID, Level: "high",
		Reasons:   `["1 dilution-shaped filing(s) in last 180d: 424B5","shares outstanding +4.0% over the trailing window (15B → 15.6B)"]`,
		UpdatedTs: 42})

	// Derived flag.
	var one struct {
		Symbol  string   `json:"symbol"`
		Level   string   `json:"level"`
		Derived bool     `json:"derived"`
		Reasons []string `json:"reasons"`
		Note    string   `json:"note"`
	}
	if code := s8Get(t, srv.URL+"/api/dilution?symbol=NVDA", &one); code != 200 {
		t.Fatalf("status = %d", code)
	}
	if !one.Derived || one.Level != "high" || len(one.Reasons) != 2 || one.Note == "" {
		t.Fatalf("payload = %+v", one)
	}

	// Never-derived symbol: honest "unknown", not a fabricated "low".
	if code := s8Get(t, srv.URL+"/api/dilution?symbol=AAPL", &one); code != 200 {
		t.Fatalf("status = %d", code)
	}
	if one.Derived || one.Level != "unknown" {
		t.Fatalf("underived payload = %+v", one)
	}
	_ = aapl

	// Fleet-wide flagged list.
	var flagged struct {
		Flagged []map[string]any `json:"flagged"`
		Count   int              `json:"count"`
	}
	if code := s8Get(t, srv.URL+"/api/dilution", &flagged); code != 200 {
		t.Fatalf("status = %d", code)
	}
	if flagged.Count != 1 || flagged.Flagged[0]["symbol"] != "NVDA" {
		t.Fatalf("flagged = %+v", flagged)
	}
}
