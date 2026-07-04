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

func newPaperServer(t *testing.T, mutate func(*config.Config)) (*httptest.Server, *store.Store) {
	t.Helper()
	srv, st, d := newTestServer(t, mutate)
	mux := http.NewServeMux()
	d.registerPaper(mux)
	srv.Config.Handler = d.secure(mux)
	return srv, st
}

// TestPaperEndpoint_ShapeAndHonesty: an initialized book with one open position
// + one buy returns the expected JSON, and ALWAYS carries live:false + a label
// so the UI cannot present the simulation as a live account.
func TestPaperEndpoint_ShapeAndHonesty(t *testing.T) {
	srv, st := newPaperServer(t, nil)
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "AAA", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.InitPaperBook(ctx, "flagship-1d", 100_000, 0); err != nil {
		t.Fatal(err)
	}
	// Apply one step: open a position + log a buy + mark equity.
	if _, err := st.ApplyPaperStep(ctx, store.PaperApply{
		Strategy: "flagship-1d", BarTs: 86400, NewCash: 0,
		Opens:    []store.PaperPosition{{Strategy: "flagship-1d", SymbolID: sym.ID, Qty: 500, AvgPx: 200, OpenedTs: 86400}},
		Trades:   []store.PaperTrade{{Strategy: "flagship-1d", SymbolID: sym.ID, Side: "buy", Qty: 500, Px: 200, Cost: 75, Ts: 86400, Reason: "cal_prob 0.900 >= long 0.60"}},
		EquityTs: 86400, EquityCash: 0, EquityPositionsValue: 100_000, EquityValue: 100_000,
	}); err != nil {
		t.Fatal(err)
	}

	res, err := newClient(t).Get(srv.URL + "/api/paper?strategy=flagship-1d")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close() //nolint:errcheck
	if res.StatusCode != 200 {
		t.Fatalf("status=%d want 200 (public read)", res.StatusCode)
	}
	var body struct {
		Strategy   string   `json:"strategy"`
		Strategies []string `json:"strategies"`
		Live       bool     `json:"live"`
		Label      string   `json:"label"`
		StartCash  float64  `json:"startCash"`
		Equity     []struct {
			Ts     int64   `json:"ts"`
			Equity float64 `json:"equity"`
		} `json:"equity"`
		Positions []struct {
			Symbol string  `json:"symbol"`
			Qty    float64 `json:"qty"`
		} `json:"positions"`
		Trades []struct {
			Side string  `json:"side"`
			Px   float64 `json:"px"`
		} `json:"trades"`
		Summary struct {
			NumFills int `json:"numFills"`
		} `json:"summary"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	// HONESTY: never presented as live.
	if body.Live {
		t.Fatal("paper payload must carry live:false")
	}
	if body.Label == "" {
		t.Fatal("paper payload must carry a simulation label")
	}
	if body.Strategy != "flagship-1d" || len(body.Strategies) < 2 {
		t.Fatalf("strategy metadata: %+v", body.Strategies)
	}
	if len(body.Equity) != 1 || body.Equity[0].Equity != 100_000 {
		t.Fatalf("equity curve: %+v", body.Equity)
	}
	if len(body.Positions) != 1 || body.Positions[0].Symbol != "AAA" || body.Positions[0].Qty != 500 {
		t.Fatalf("positions: %+v", body.Positions)
	}
	if len(body.Trades) != 1 || body.Trades[0].Side != "buy" || body.Trades[0].Px != 200 {
		t.Fatalf("trades: %+v", body.Trades)
	}
	if body.Summary.NumFills != 1 {
		t.Fatalf("summary numFills=%d want 1", body.Summary.NumFills)
	}
}

// TestReconstructRoundTrips pairs buys with their closing sells per symbol and
// computes win/loss net of both-side costs.
func TestReconstructRoundTrips(t *testing.T) {
	trades := []store.PaperTrade{
		// AAA: buy 10@100 (cost 5), sell 10@120 (cost 6) -> proceeds 1194 > outlay 1005 -> WON
		{SymbolID: 1, Side: "buy", Qty: 10, Px: 100, Cost: 5},
		{SymbolID: 1, Side: "sell", Qty: 10, Px: 120, Cost: 6},
		// BBB: buy 5@100 (cost 2), sell 5@90 (cost 2) -> proceeds 448 < outlay 502 -> LOST
		{SymbolID: 2, Side: "buy", Qty: 5, Px: 100, Cost: 2},
		{SymbolID: 2, Side: "sell", Qty: 5, Px: 90, Cost: 2},
		// CCC: still open (buy with no sell) -> not a round-trip
		{SymbolID: 3, Side: "buy", Qty: 1, Px: 50, Cost: 1},
	}
	closed, numFills, notional := reconstructRoundTrips(trades)
	if numFills != 5 {
		t.Fatalf("numFills=%d want 5", numFills)
	}
	if len(closed) != 2 {
		t.Fatalf("closed=%d want 2 (CCC still open)", len(closed))
	}
	if !closed[0].Won {
		t.Fatal("AAA round-trip should be a win")
	}
	if closed[1].Won {
		t.Fatal("BBB round-trip should be a loss")
	}
	// traded notional = 10*100 + 10*120 + 5*100 + 5*90 + 1*50 = 1000+1200+500+450+50 = 3200
	if notional != 3200 {
		t.Fatalf("tradedNotional=%v want 3200", notional)
	}
}
