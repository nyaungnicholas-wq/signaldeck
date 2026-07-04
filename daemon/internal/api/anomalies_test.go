// Signal8 wave Stage 3: /api/anomalies endpoint tests (separate harness file
// so parallel edits never collide).
package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/config"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

func newAnomalyServer(t *testing.T) (*httptest.Server, *store.Store) {
	t.Helper()
	srv, st, d := newTestServer(t, func(c *config.Config) { c.PublicReads = true })
	mux := http.NewServeMux()
	d.registerAnomalies(mux)
	srv.Config.Handler = d.secure(mux)
	return srv, st
}

type anomaliesResp struct {
	Anomalies []store.AnomalyRow `json:"anomalies"`
	Count     int                `json:"count"`
	Symbol    string             `json:"symbol"`
	Kind      string             `json:"kind"`
	Note      string             `json:"note"`
	ProxyNote string             `json:"proxyNote"`
}

func TestAnomaliesEndpoint(t *testing.T) {
	srv, st := newAnomalyServer(t)
	ctx := context.Background()
	btc, _ := st.UpsertSymbol(ctx, "BTC/USD", md.Crypto, "")
	nvda, _ := st.UpsertSymbol(ctx, "NVDA", md.Stocks, "")

	seed := []store.AnomalyRow{
		{SymbolID: btc.ID, Ts: 7200, Kind: "anomaly_imbalance", Z: 3.1, Detail: "unusual buy pressure … — descriptive statistic, not a prediction"},
		{SymbolID: nvda.ID, Ts: 10800, Kind: "anomaly_volume", Z: 2.7, Detail: "unusual volume …"},
		{SymbolID: nvda.ID, Ts: 14400, Kind: "anomaly_imbalance", Z: -2.9, Detail: "… volume-side proxy (no order-book on free stock data) …"},
	}
	for _, a := range seed {
		if fresh, err := st.InsertAnomaly(ctx, a); err != nil || !fresh {
			t.Fatalf("seed: fresh=%v err=%v", fresh, err)
		}
	}

	// Fleet-wide, newest first, honesty notes present.
	var body anomaliesResp
	if code := s8Get(t, srv.URL+"/api/anomalies", &body); code != 200 {
		t.Fatalf("status = %d", code)
	}
	if body.Count != 3 || body.Anomalies[0].Ts != 14400 {
		t.Fatalf("payload = %+v", body)
	}
	if !strings.Contains(body.Note, "not predictions") {
		t.Errorf("note = %q", body.Note)
	}
	if !strings.Contains(body.ProxyNote, "volume-side PROXY") {
		t.Errorf("proxyNote = %q", body.ProxyNote)
	}

	// Kind filter.
	if code := s8Get(t, srv.URL+"/api/anomalies?kind=anomaly_imbalance", &body); code != 200 {
		t.Fatalf("kind status = %d", code)
	}
	if body.Count != 2 {
		t.Fatalf("kind filter = %+v", body)
	}

	// Per-symbol (market inferred: stocks first, then crypto).
	if code := s8Get(t, srv.URL+"/api/anomalies?symbol=NVDA", &body); code != 200 {
		t.Fatalf("symbol status = %d", code)
	}
	if body.Count != 2 || body.Symbol != "NVDA" {
		t.Fatalf("symbol filter = %+v", body)
	}
	for _, a := range body.Anomalies {
		if a.Symbol != "NVDA" || a.Market != "stocks" {
			t.Errorf("row = %+v", a)
		}
	}

	// Crypto pair resolves without an explicit market.
	if code := s8Get(t, srv.URL+"/api/anomalies?symbol="+strings.ReplaceAll("BTC/USD", "/", "%2F"), &body); code != 200 {
		t.Fatalf("crypto status = %d", code)
	}
	if body.Count != 1 || body.Anomalies[0].Kind != "anomaly_imbalance" {
		t.Fatalf("crypto filter = %+v", body)
	}

	// Combined symbol+kind+market.
	if code := s8Get(t, srv.URL+"/api/anomalies?symbol=NVDA&market=stocks&kind=anomaly_volume", &body); code != 200 {
		t.Fatalf("combined status = %d", code)
	}
	if body.Count != 1 || body.Anomalies[0].Z != 2.7 {
		t.Fatalf("combined = %+v", body)
	}

	// Unknown symbol → 404 (never an empty 200 pretending coverage).
	if code := s8Get(t, srv.URL+"/api/anomalies?symbol=ZZZZ", &body); code != 404 {
		t.Fatalf("unknown symbol status = %d", code)
	}
	// Bad kind → 400.
	if code := s8Get(t, srv.URL+"/api/anomalies?kind=bogus", &body); code != 400 {
		t.Fatalf("bad kind status = %d", code)
	}
}
