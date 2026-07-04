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

// newFreeDataServer wires only the Stage-2 free-data routes behind the real
// middleware (kept separate from the shared harness so parallel edits never
// collide with this wave).
func newFreeDataServer(t *testing.T, mutate func(*config.Config)) (*httptest.Server, *store.Store) {
	t.Helper()
	srv, st, d := newTestServer(t, mutate)
	mux := http.NewServeMux()
	d.registerFreeData(mux)
	srv.Config.Handler = d.secure(mux)
	return srv, st
}

func TestMacroSeriesEndpoint(t *testing.T) {
	srv, st := newFreeDataServer(t, nil)
	ctx := context.Background()
	day := int64(86400)
	_ = st.InsertMacro(ctx, "VIXCLS", day, 12.0)
	_ = st.InsertMacro(ctx, "VIXCLS", 2*day, 13.5)
	_ = st.InsertMacro(ctx, "DGS10", day, 4.28)

	// overview (no ?series=): latest of each series.
	res, err := newClient(t).Get(srv.URL + "/api/macro-series")
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != 200 {
		t.Fatalf("overview status = %d", res.StatusCode)
	}
	var overview struct {
		Latest map[string]store.MacroPoint `json:"latest"`
	}
	_ = json.NewDecoder(res.Body).Decode(&overview)
	res.Body.Close() //nolint:errcheck
	if overview.Latest["VIXCLS"].Value != 13.5 {
		t.Fatalf("overview latest VIXCLS = %+v, want 13.5", overview.Latest["VIXCLS"])
	}

	// series time series (oldest-first).
	res2, err := newClient(t).Get(srv.URL + "/api/macro-series?series=VIXCLS")
	if err != nil {
		t.Fatal(err)
	}
	defer res2.Body.Close() //nolint:errcheck
	var body struct {
		Series string             `json:"series"`
		Points []store.MacroPoint `json:"points"`
		Latest *store.MacroPoint  `json:"latest"`
	}
	if err := json.NewDecoder(res2.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Series != "VIXCLS" || len(body.Points) != 2 {
		t.Fatalf("series payload wrong: %+v", body)
	}
	if body.Points[0].Value != 12.0 || body.Points[1].Value != 13.5 {
		t.Fatalf("points not oldest-first: %+v", body.Points)
	}
	if body.Latest == nil || body.Latest.Value != 13.5 {
		t.Fatalf("latest wrong: %+v", body.Latest)
	}
}

func TestFundamentalsEndpoint(t *testing.T) {
	srv, st := newFreeDataServer(t, nil)
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	if err != nil {
		t.Fatal(err)
	}
	_ = st.UpsertFundamental(ctx, store.FundamentalRow{
		SymbolID: sym.ID, Metric: "Revenues", Value: 383e9, AsOf: 100, FetchedAt: 1,
	})
	_ = st.UpsertFundamental(ctx, store.FundamentalRow{
		SymbolID: sym.ID, Metric: "Revenues", Value: 400e9, AsOf: 200, FetchedAt: 1,
	})
	_ = st.UpsertFundamental(ctx, store.FundamentalRow{
		SymbolID: sym.ID, Metric: "EPS", Value: 6.13, AsOf: 200, FetchedAt: 1,
	})

	res, err := newClient(t).Get(srv.URL + "/api/fundamentals?symbol=AAPL&history=Revenues")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close() //nolint:errcheck
	if res.StatusCode != 200 {
		t.Fatalf("status = %d", res.StatusCode)
	}
	var body struct {
		Symbol        string                 `json:"symbol"`
		Metrics       []store.FundamentalRow `json:"metrics"`
		History       []store.FundamentalRow `json:"history"`
		HistoryMetric string                 `json:"historyMetric"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Symbol != "AAPL" {
		t.Fatalf("symbol = %q", body.Symbol)
	}
	// latest metrics: newest revenue (400e9) + EPS
	metrics := map[string]float64{}
	for _, m := range body.Metrics {
		metrics[m.Metric] = m.Value
		if m.Symbol != "AAPL" {
			t.Fatalf("metric row missing symbol: %+v", m)
		}
	}
	if metrics["Revenues"] != 400e9 || metrics["EPS"] != 6.13 {
		t.Fatalf("latest metrics wrong: %+v", metrics)
	}
	// history: both revenue periods oldest-first
	if body.HistoryMetric != "Revenues" || len(body.History) != 2 {
		t.Fatalf("history wrong: %+v", body)
	}
	if body.History[0].Value != 383e9 || body.History[1].Value != 400e9 {
		t.Fatalf("history not oldest-first: %+v", body.History)
	}
}

func TestFundamentals_UnknownSymbol404(t *testing.T) {
	srv, _ := newFreeDataServer(t, nil)
	res, err := newClient(t).Get(srv.URL + "/api/fundamentals?symbol=NOPEXYZ")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close() //nolint:errcheck
	if res.StatusCode != 404 {
		t.Fatalf("status = %d, want 404", res.StatusCode)
	}
}
