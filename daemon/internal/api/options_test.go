// OPTIONS wave API tests: the pure pricing route (value, Greeks, implied-vol
// inversion and its honest refusal) and the vol-edge route (forecast -> vol
// level -> verdict), including the gates that must withhold a verdict rather
// than manufacture one. Fixture data only.
package api

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/options"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

func newOptionsServer(t *testing.T) (*httptest.Server, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "options_api.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	d := Deps{St: st, Cfg: baseCfg(), Version: "test", Started: time.Now()}
	srv := httptest.NewUnstartedServer(nil)
	t.Cleanup(srv.Close)
	d.Cfg.AllowedHosts = []string{srv.Listener.Addr().String()}
	mux := http.NewServeMux()
	d.registerOptions(mux)
	srv.Config.Handler = d.secure(mux)
	srv.Start()
	return srv, st
}

// seedVolRegimeBars writes daily bars whose volatility alternates between calm
// and violent blocks — the regime structure the predictor exists to read, so
// the walk produces both outcome groups.
func seedVolRegimeBars(t *testing.T, st *store.Store, id int64, blocks, blockLen int) {
	t.Helper()
	var bars []md.Bar
	px := 100.0
	ts := int64(1)
	for b := 0; b < blocks; b++ {
		sigma := 0.005
		if b%2 == 1 {
			sigma = 0.03
		}
		for i := 0; i < blockLen; i++ {
			r := sigma
			if i%2 == 0 {
				r = -sigma
			}
			prev := px
			px *= 1 + r
			bars = append(bars, md.Bar{SymbolID: id, TF: md.TF1d, Ts: ts * 86400,
				Open: prev, High: math.Max(prev, px), Low: math.Min(prev, px),
				Close: px, Volume: 1_000_000})
			ts++
		}
	}
	if err := st.UpsertBars(context.Background(), bars); err != nil {
		t.Fatalf("seed bars: %v", err)
	}
}

func TestOptionsPriceRoute(t *testing.T) {
	srv, _ := newOptionsServer(t)

	if code, _ := apiGET(t, srv, "/api/options/price?spot=0&strike=100"); code != 400 {
		t.Fatalf("missing spot should 400, got %d", code)
	}

	// The textbook contract, priced through the HTTP layer.
	code, out := apiGET(t, srv, "/api/options/price?spot=100&strike=100&days=365&rate=0.05&vol=0.2&type=call")
	if code != 200 {
		t.Fatalf("code %d: %v", code, out)
	}
	priced, _ := out["priced"].(map[string]any)
	if priced == nil {
		t.Fatalf("no priced block: %v", out)
	}
	if got := priced["price"].(float64); math.Abs(got-10.4506) > 0.01 {
		t.Fatalf("call price %v, want ~10.4506", got)
	}
	if out["assumptions"] == nil || out["probITMNote"] == nil {
		t.Fatal("model assumptions and the risk-neutral-probability caveat must ship in every payload")
	}

	// Supplying the model's own price must invert back to the model's own vol.
	_, out = apiGET(t, srv, "/api/options/price?spot=100&strike=100&days=365&rate=0.05&vol=0.2&type=call&price=10.450584")
	iv, ok := out["impliedVol"].(float64)
	if !ok || math.Abs(iv-0.2) > 1e-3 {
		t.Fatalf("implied vol %v (ok=%v), want ~0.20", out["impliedVol"], ok)
	}

	// An unpriceable quote gets the refusal note, never a number.
	_, out = apiGET(t, srv, "/api/options/price?spot=100&strike=80&days=365&rate=0.05&vol=0.2&type=call&price=1")
	if out["impliedVol"] != nil || out["impliedVolNote"] != ivRefusalNote {
		t.Fatalf("below-intrinsic quote must be refused verbatim, got %v / %v", out["impliedVol"], out["impliedVolNote"])
	}

	// Straddle mode prices both legs and reports the breakeven move.
	_, out = apiGET(t, srv, "/api/options/price?spot=100&strike=100&days=90&vol=0.4&type=straddle")
	sd, _ := out["straddle"].(map[string]any)
	if sd == nil || sd["price"].(float64) <= 0 || sd["breakevenMovePct"].(float64) <= 0 {
		t.Fatalf("straddle payload incomplete: %v", out)
	}
}

func TestOptionsVolEdgeRoute(t *testing.T) {
	srv, st := newOptionsServer(t)
	ctx := context.Background()
	sym, _ := st.UpsertSymbol(ctx, "NVDA", md.Stocks, "NVIDIA")
	seedVolRegimeBars(t, st, sym.ID, 12, 160) // ~1900 bars, alternating vol blocks

	if code, _ := apiGET(t, srv, "/api/options/vol-edge?symbol=ZZZ&market=stocks"); code != 404 {
		t.Fatal("unknown symbol must 404")
	}

	// No iv supplied: the forecast stands alone, with no verdict invented.
	code, out := apiGET(t, srv, "/api/options/vol-edge?symbol=NVDA&market=stocks")
	if code != 200 {
		t.Fatalf("code %d: %v", code, out)
	}
	if out["whyNoChain"] == nil {
		t.Fatal("the no-options-feed disclosure must ship in every payload")
	}
	if out["forecast"] == nil {
		t.Fatalf("expected a regime forecast on a full history: %v", out)
	}
	if out["edge"] != nil {
		t.Fatal("a verdict was issued without a market implied vol")
	}
	if out["next"] == nil {
		t.Fatal("payload must say what input is missing")
	}
	vs, _ := out["volStats"].(map[string]any)
	if vs == nil || vs["elevatedVol"].(float64) <= vs["calmVol"].(float64) {
		t.Fatalf("conditional vol levels missing or inverted: %v", out["volStats"])
	}
	exp, _ := out["expectation"].(map[string]any)
	if exp == nil {
		t.Fatalf("no expectation: %v", out)
	}
	expected := exp["expected"].(float64)
	ifRight, ifWrong := exp["ifRight"].(float64), exp["ifWrong"].(float64)
	lo, hi := math.Min(ifRight, ifWrong), math.Max(ifRight, ifWrong)
	if expected < lo || expected > hi {
		t.Fatalf("expected vol %v must lie between the two branch levels %v/%v", expected, lo, hi)
	}

	// A market IV far above anything this symbol has realized must read rich —
	// and, being clear of BOTH branches, must read robust.
	_, out = apiGET(t, srv, "/api/options/vol-edge?symbol=NVDA&market=stocks&iv=2.5")
	edge, _ := out["edge"].(map[string]any)
	if edge == nil || edge["verdict"] != "iv-rich" {
		t.Fatalf("verdict %v, want iv-rich: %v", edge, out)
	}
	if edge["robust"] != true {
		t.Fatal("a verdict clear of both branches must be flagged robust")
	}
	if edge["caveat"] == nil || out["vrpNote"] == nil {
		t.Fatal("the hit-rate-is-not-profit caveat and the VRP assumption must both ship")
	}
	if bv := edge["breakevenVRP"].(float64); math.Abs(bv-(2.5-expected)) > 1e-9 {
		t.Fatalf("breakevenVRP %v, want %v", bv, 2.5-expected)
	}
	trade, _ := out["trade"].(map[string]any)
	if trade == nil || trade["edgePerContract"].(float64) <= 0 {
		t.Fatalf("straddle gap should favour the seller at a rich IV: %v", trade)
	}

	// A nonsense implied vol is rejected outright.
	if code, _ := apiGET(t, srv, "/api/options/vol-edge?symbol=NVDA&market=stocks&iv=9"); code != 400 {
		t.Fatalf("out-of-range iv must 400, got %d", code)
	}

	// An expiry far from the forecast's own horizon is labeled, not silently
	// compared.
	_, out = apiGET(t, srv, "/api/options/vol-edge?symbol=NVDA&market=stocks&iv=0.5&days=7")
	if out["horizonMismatch"] == nil {
		t.Fatal("a 7-day expiry vs a 63-trading-day forecast must be flagged")
	}
}

// Thin history must produce an honest gate, never a forecast.
func TestOptionsVolEdgeGatesThinHistory(t *testing.T) {
	srv, st := newOptionsServer(t)
	ctx := context.Background()
	sym, _ := st.UpsertSymbol(ctx, "TINY", md.Stocks, "Thin Co")
	seedVolRegimeBars(t, st, sym.ID, 2, 40) // 80 bars — far below the 230 needed
	code, out := apiGET(t, srv, "/api/options/vol-edge?symbol=TINY&market=stocks")
	if code != 200 {
		t.Fatalf("code %d", code)
	}
	if out["forecast"] != nil || out["edge"] != nil {
		t.Fatalf("thin history must yield no forecast: %v", out)
	}
	if out["gate"] == nil {
		t.Fatal("a withheld forecast must say why")
	}
}

// The route's default variance-risk-premium must be the package constant — a
// silently different default would move every verdict.
func TestOptionsVRPDefaultIsThePackageConstant(t *testing.T) {
	if options.DefaultVRP != 0.02 {
		t.Fatalf("DefaultVRP = %v; verdicts and the UI's stated assumption must agree", options.DefaultVRP)
	}
}
