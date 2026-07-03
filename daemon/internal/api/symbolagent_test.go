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
	"github.com/nyaungnicholas-wq/signaldeck/internal/symbolagent"
)

// newSymbolAgentServer wires only /api/symbol-agent behind the real middleware.
func newSymbolAgentServer(t *testing.T, mutate func(*config.Config)) (*httptest.Server, *store.Store) {
	t.Helper()
	srv, st, d := newTestServer(t, mutate)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/symbol-agent", d.symbolAgent)
	srv.Config.Handler = d.secure(mux)
	return srv, st
}

func TestSymbolAgentEndpoint_Shape(t *testing.T) {
	srv, st := newSymbolAgentServer(t, nil)
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	if err != nil {
		t.Fatal(err)
	}

	// No model row yet → honest still-learning static default (200, not 404).
	res, err := newClient(t).Get(srv.URL + "/api/symbol-agent?symbol=AAPL&market=stocks&horizon=1d")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close() //nolint:errcheck
	if res.StatusCode != 200 {
		t.Fatalf("status = %d, want 200 (public read default)", res.StatusCode)
	}
	var body symbolAgentResp
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Available {
		t.Error("no model stored yet: available must be false")
	}
	if body.Tier != symbolagent.TierStatic {
		t.Errorf("tier = %q, want static default", body.Tier)
	}
	if body.Threshold != symbolagent.MinPersonal {
		t.Errorf("threshold = %d, want MinPersonal %d", body.Threshold, symbolagent.MinPersonal)
	}
	if body.ActiveWeights == nil {
		t.Error("activeWeights must be a (possibly empty) object, not null")
	}

	// Store a PERSONAL model → the payload must reflect its own weights + skill.
	if err := st.UpsertSymbolModel(ctx, store.SymbolModelRow{
		SymbolID: sym.ID, Horizon: string(md.H1d),
		Weights:     `{"pressure":0.7,"forecast":0.3}`,
		Calibration: `{"fitted":true,"kx":[0.2,0.8],"ky":[0.3,0.7]}`,
		Skill:       `{"pressure":{"hitRate":0.62,"ic":0.11,"n":80,"hasHR":true,"hasIC":true}}`,
		Personality: "Pressure-led: Pressure hits 62% of its directional calls (80).",
		NSamples:    80, Tier: symbolagent.TierPersonal, UpdatedTs: 123,
	}); err != nil {
		t.Fatal(err)
	}

	res2, err := newClient(t).Get(srv.URL + "/api/symbol-agent?symbol=AAPL&market=stocks&horizon=1d")
	if err != nil {
		t.Fatal(err)
	}
	defer res2.Body.Close() //nolint:errcheck
	var body2 symbolAgentResp
	if err := json.NewDecoder(res2.Body).Decode(&body2); err != nil {
		t.Fatal(err)
	}
	if !body2.Available || !body2.Personal || body2.Tier != symbolagent.TierPersonal {
		t.Fatalf("personal model not surfaced: %+v", body2)
	}
	if body2.NSamples != 80 {
		t.Errorf("nSamples = %d, want 80", body2.NSamples)
	}
	if len(body2.Skill) != 1 || body2.Skill[0].Component != "pressure" || body2.Skill[0].HitRate != 0.62 {
		t.Fatalf("skill rows wrong: %+v", body2.Skill)
	}
	if body2.ActiveWeights["pressure"] != 0.7 {
		t.Fatalf("active weights (personal) not surfaced: %+v", body2.ActiveWeights)
	}
	if body2.Personality == "" {
		t.Error("personality must be present for a personal model")
	}
}

// A still-learning (non-personal) model must NOT expose personal weights: the
// panel should show the global model is driving the blend.
func TestSymbolAgentEndpoint_StillLearningHidesWeights(t *testing.T) {
	srv, st := newSymbolAgentServer(t, nil)
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "TSLA", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertSymbolModel(ctx, store.SymbolModelRow{
		SymbolID: sym.ID, Horizon: string(md.H1d),
		Weights: "{}", Calibration: `{"fitted":false}`,
		Skill:       `{"pressure":{"hitRate":0.55,"ic":0.0,"n":12,"hasHR":true,"hasIC":false}}`,
		Personality: "Still learning (12/40 own outcomes) — using the global per-regime model.",
		NSamples:    12, Tier: symbolagent.TierRegime, UpdatedTs: 1,
	}); err != nil {
		t.Fatal(err)
	}

	res, err := newClient(t).Get(srv.URL + "/api/symbol-agent?symbol=TSLA&market=stocks&horizon=1d")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close() //nolint:errcheck
	var body symbolAgentResp
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Personal {
		t.Fatal("regime-tier model must not report personal=true")
	}
	if len(body.ActiveWeights) != 0 {
		t.Fatalf("still-learning model must NOT expose personal weights, got %+v", body.ActiveWeights)
	}
	// Skill is still shown (measured evidence), and the honest progress is there.
	if body.NSamples != 12 || body.Threshold != symbolagent.MinPersonal {
		t.Errorf("progress fields wrong: n=%d threshold=%d", body.NSamples, body.Threshold)
	}
}

// Auth: with PublicReads=false, anonymous /api/symbol-agent is 401.
func TestSymbolAgentEndpoint_AuthGated(t *testing.T) {
	srv, st := newSymbolAgentServer(t, func(c *config.Config) { c.PublicReads = false })
	ctx := context.Background()
	if _, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, ""); err != nil {
		t.Fatal(err)
	}
	res, err := newClient(t).Get(srv.URL + "/api/symbol-agent?symbol=AAPL&market=stocks&horizon=1d")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close() //nolint:errcheck
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anon with PublicReads=false: status %d, want 401", res.StatusCode)
	}
}

// A bad symbol → 404 (matches symbolFromQuery semantics of other reads).
func TestSymbolAgentEndpoint_UnknownSymbol(t *testing.T) {
	srv, _ := newSymbolAgentServer(t, nil)
	res, err := newClient(t).Get(srv.URL + "/api/symbol-agent?symbol=NOPE&market=stocks&horizon=1d")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close() //nolint:errcheck
	if res.StatusCode != 404 {
		t.Fatalf("unknown symbol: status %d, want 404", res.StatusCode)
	}
}
