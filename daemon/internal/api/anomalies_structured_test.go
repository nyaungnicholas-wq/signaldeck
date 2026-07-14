// SIGNALS-hub overhaul: structured anomaly payload fields (measure/value/
// proxy) — the machine-readable replacement for the web's detail string-
// sniffing. Separate harness file so parallel edits never collide.
package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/config"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

func newAnomalyStructuredServer(t *testing.T) (*httptest.Server, *store.Store) {
	t.Helper()
	srv, st, d := newTestServer(t, func(c *config.Config) { c.PublicReads = true })
	mux := http.NewServeMux()
	d.registerAnomalies(mux)
	srv.Config.Handler = d.secure(mux)
	return srv, st
}

func TestAnomaliesStructuredFields(t *testing.T) {
	srv, st := newAnomalyStructuredServer(t)
	ctx := context.Background()
	btc, _ := st.UpsertSymbol(ctx, "BTC/USD", md.Crypto, "")
	nvda, _ := st.UpsertSymbol(ctx, "NVDA", md.Stocks, "")

	// Details verbatim from internal/anomaly's formats — the derivation must
	// match what the detectors actually write (and what the web sniffs).
	seed := []store.AnomalyRow{
		{SymbolID: btc.ID, Ts: 7200, Kind: "anomaly_imbalance", Z: 3.1,
			Detail: "unusual buy pressure: mean order-book imbalance +0.45 over last 5m (z=+3.1 vs the means of 12 trailing 5m windows across a 60m baseline) — descriptive statistic, not a prediction"},
		{SymbolID: nvda.ID, Ts: 10800, Kind: "anomaly_imbalance", Z: -2.9,
			Detail: "unusual sell-side volume: up/down-volume share -0.62 over last 5×1m bars (z=-2.9 vs trailing baseline of 11 windows) — volume-side proxy (no order-book on free stock data); descriptive, not a prediction"},
		{SymbolID: nvda.ID, Ts: 14400, Kind: "anomaly_vol", Z: 3.4,
			Detail: "true-range spike: last 1d bar range 3.4× the trailing ATR(14) (value here is the TR/ATR ratio, not a z-score) — descriptive, not a prediction"},
	}
	for _, a := range seed {
		if fresh, err := st.InsertAnomaly(ctx, a); err != nil || !fresh {
			t.Fatalf("seed: fresh=%v err=%v", fresh, err)
		}
	}

	var body struct {
		Anomalies []struct {
			store.AnomalyRow
			Measure string  `json:"measure"`
			Value   float64 `json:"value"`
			Proxy   bool    `json:"proxy"`
		} `json:"anomalies"`
		Count     int    `json:"count"`
		Note      string `json:"note"`
		ProxyNote string `json:"proxyNote"`
	}
	if code := s8Get(t, srv.URL+"/api/anomalies", &body); code != 200 {
		t.Fatalf("status = %d", code)
	}
	if body.Count != 3 || len(body.Anomalies) != 3 {
		t.Fatalf("payload = %+v", body)
	}

	// Newest first: TR/ATR spike, stock proxy imbalance, crypto imbalance.
	tr, px, im := body.Anomalies[0], body.Anomalies[1], body.Anomalies[2]

	// TR/ATR rows: measure "ratio", never a z; not a proxy.
	if tr.Measure != "ratio" || tr.Value != 3.4 || tr.Proxy {
		t.Fatalf("TR/ATR row = measure %q value %v proxy %v", tr.Measure, tr.Value, tr.Proxy)
	}
	// Stock imbalance: a z AND the proxy flag.
	if px.Measure != "z" || px.Value != -2.9 || !px.Proxy {
		t.Fatalf("stock proxy row = measure %q value %v proxy %v", px.Measure, px.Value, px.Proxy)
	}
	// Crypto imbalance: real book data — z, no proxy.
	if im.Measure != "z" || im.Value != 3.1 || im.Proxy {
		t.Fatalf("crypto row = measure %q value %v proxy %v", im.Measure, im.Value, im.Proxy)
	}

	// ADDITIVE contract: every pre-existing field still present + the notes.
	if tr.Kind != "anomaly_vol" || tr.Ts != 14400 || tr.Z != 3.4 || tr.Symbol != "NVDA" || tr.Detail == "" {
		t.Fatalf("existing fields broken: %+v", tr.AnomalyRow)
	}
	if body.Note == "" || body.ProxyNote == "" {
		t.Fatalf("honesty notes dropped: note=%q proxyNote=%q", body.Note, body.ProxyNote)
	}
}
