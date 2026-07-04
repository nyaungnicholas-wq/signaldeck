package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/config"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// newModelForecastsServer wires just the quant routes (which include the Stage-6
// /api/model-forecasts endpoint) against a temp store.
func newModelForecastsServer(t *testing.T) (string, *store.Store) {
	t.Helper()
	srv, st, d := newTestServer(t, func(c *config.Config) {})
	mux := http.NewServeMux()
	d.registerQuant(mux)
	srv.Config.Handler = d.secure(mux)
	return srv.URL, st
}

func TestModelForecastsEndpoint(t *testing.T) {
	ctx := context.Background()
	url, st := newModelForecastsServer(t)
	sym, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	if err != nil {
		t.Fatal(err)
	}

	// Store one GBM leg (with edge) and one mean-rev leg (no edge) for 1d.
	if err := st.UpsertModelForecast(ctx, store.ModelForecast{
		SymbolID: sym.ID, Horizon: md.H1d, Model: store.ModelGBM, Ts: 100,
		Prob: 0.61, Accuracy: 0.58, Brier: 0.24, AUC: 0.62, BaseRate: 0.52, Lift: 0.06,
		NTrain: 200, NEval: 80,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertModelForecast(ctx, store.ModelForecast{
		SymbolID: sym.ID, Horizon: md.H1d, Model: store.ModelMeanRev, Ts: 100,
		Prob: 0.47, Accuracy: 0.49, Brier: 0.26, AUC: 0.48, BaseRate: 0.51, Lift: -0.02,
		NTrain: 200, NEval: 80,
	}); err != nil {
		t.Fatal(err)
	}

	res, err := newClient(t).Get(url + "/api/model-forecasts?symbol=AAPL&market=stocks")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close() //nolint:errcheck
	if res.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	var got []store.ModelForecast
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 model legs, got %d", len(got))
	}
	byModel := map[string]store.ModelForecast{}
	for _, m := range got {
		byModel[m.Model] = m
	}
	if g := byModel[store.ModelGBM]; g.Lift <= 0 || g.Prob != 0.61 {
		t.Fatalf("gbm leg wrong: %+v", g)
	}
	if m := byModel[store.ModelMeanRev]; m.Lift > 0 {
		t.Fatalf("mean-rev leg should carry its honest negative lift, got %+v", m)
	}
}

// An unknown symbol yields 404 (the honest "no such symbol"), not a fabricated
// empty 200.
func TestModelForecastsUnknownSymbol(t *testing.T) {
	url, _ := newModelForecastsServer(t)
	res, err := newClient(t).Get(url + "/api/model-forecasts?symbol=ZZZZ&market=stocks")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close() //nolint:errcheck
	if res.StatusCode != 404 {
		t.Fatalf("status = %d, want 404", res.StatusCode)
	}
}
