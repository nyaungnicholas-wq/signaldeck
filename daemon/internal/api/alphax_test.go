package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/config"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// newAlphaXServer wires just the alphax route against a temp store.
func newAlphaXServer(t *testing.T) (string, *store.Store) {
	t.Helper()
	srv, st, d := newTestServer(t, func(c *config.Config) {})
	mux := http.NewServeMux()
	d.registerAlphaX(mux)
	srv.Config.Handler = d.secure(mux)
	return srv.URL, st
}

func getAlphaX(t *testing.T, url string) map[string]any {
	t.Helper()
	res, err := newClient(t).Get(url + "/api/alphax")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close() //nolint:errcheck
	if res.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	var out map[string]any
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

// No trained model at all → both horizons unavailable, gated, with the
// stated "no graded model yet" reason — and the verbatim caveat regardless.
func TestAlphaXEndpoint_NoModelYet(t *testing.T) {
	url, _ := newAlphaXServer(t)
	out := getAlphaX(t, url)

	note, _ := out["note"].(string)
	if note != alphaxAPINote {
		t.Fatalf("caveat must ship verbatim, got %q", note)
	}
	horizons := out["horizons"].(map[string]any)
	for _, h := range []string{"1d", "1w"} {
		hOut, ok := horizons[h].(map[string]any)
		if !ok {
			t.Fatalf("horizon %s missing: %v", h, horizons)
		}
		if hOut["available"] != false || hOut["gated"] != true {
			t.Fatalf("%s: want unavailable+gated, got %v", h, hOut)
		}
		if reason, _ := hOut["gateReason"].(string); !strings.Contains(reason, "no graded model yet") {
			t.Fatalf("%s: gate reason must be stated, got %v", h, hOut["gateReason"])
		}
		if _, ok := hOut["topScores"]; ok {
			t.Fatalf("%s: no scores may ever appear without an ungated grade", h)
		}
	}
}

// A GATED grade (OOS lift <= 0) shows its honest numbers but NEVER scores —
// even if stray score rows exist in model_forecasts.
func TestAlphaXEndpoint_GatedShowsGradeNeverScores(t *testing.T) {
	ctx := context.Background()
	url, st := newAlphaXServer(t)
	if err := st.UpsertAlphaXModel(ctx, store.AlphaXModelRow{
		Horizon: md.H1d, Ts: 1000, OOSLift: -0.012, OOSAUC: 0.49, OOSAcc: 0.51,
		BaseRate: 0.522, NTrain: 4000, NTest: 800, Gated: true, ModelJSON: "{}",
	}); err != nil {
		t.Fatal(err)
	}
	// A stray stored score must not surface while gated.
	sym, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertModelForecast(ctx, store.ModelForecast{
		SymbolID: sym.ID, Horizon: md.H1d, Model: store.ModelAlphaX, Ts: 1000, Prob: 0.9,
	}); err != nil {
		t.Fatal(err)
	}

	out := getAlphaX(t, url)
	h := out["horizons"].(map[string]any)["1d"].(map[string]any)
	if h["available"] != true || h["gated"] != true {
		t.Fatalf("want available+gated, got %v", h)
	}
	grade := h["grade"].(map[string]any)
	if grade["lift"].(float64) >= 0 || grade["nTest"].(float64) != 800 {
		t.Fatalf("gated grade must carry its honest negative lift, got %v", grade)
	}
	if reason, _ := h["gateReason"].(string); !strings.Contains(reason, "never blended") {
		t.Fatalf("gate reason must state the consequence, got %v", h["gateReason"])
	}
	if _, ok := h["topScores"]; ok {
		t.Fatal("a gated model must NEVER display scores")
	}
}

// An UNGATED grade (OOS lift > 0) surfaces the top current scores with the
// relative-to-universe framing — and only alphax rows, never other legs.
func TestAlphaXEndpoint_UngatedServesTopScores(t *testing.T) {
	ctx := context.Background()
	url, st := newAlphaXServer(t)
	if err := st.UpsertAlphaXModel(ctx, store.AlphaXModelRow{
		Horizon: md.H1d, Ts: 2000, OOSLift: 0.034, OOSAUC: 0.57, OOSAcc: 0.556,
		BaseRate: 0.522, NTrain: 5000, NTest: 900, Gated: false, ModelJSON: "{}",
	}); err != nil {
		t.Fatal(err)
	}
	a, _ := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	b, _ := st.UpsertSymbol(ctx, "MSFT", md.Stocks, "Microsoft")
	for _, row := range []store.ModelForecast{
		{SymbolID: a.ID, Horizon: md.H1d, Model: store.ModelAlphaX, Ts: 2000, Prob: 0.61},
		{SymbolID: b.ID, Horizon: md.H1d, Model: store.ModelAlphaX, Ts: 2000, Prob: 0.72},
		// A GBM leg on the same horizon must NOT leak into the alphax list.
		{SymbolID: a.ID, Horizon: md.H1d, Model: store.ModelGBM, Ts: 2000, Prob: 0.99},
	} {
		if err := st.UpsertModelForecast(ctx, row); err != nil {
			t.Fatal(err)
		}
	}

	out := getAlphaX(t, url)
	h := out["horizons"].(map[string]any)["1d"].(map[string]any)
	if h["gated"] != false {
		t.Fatalf("want ungated, got %v", h)
	}
	scores := h["topScores"].([]any)
	if len(scores) != 2 {
		t.Fatalf("want exactly the 2 alphax scores, got %d (%v)", len(scores), scores)
	}
	first := scores[0].(map[string]any)
	if first["symbol"] != "MSFT" || first["prob"].(float64) != 0.72 {
		t.Fatalf("scores must rank by prob desc, got %v", scores)
	}
	if sn, _ := h["scoresNote"].(string); !strings.Contains(sn, "relative to the universe, not absolute direction") {
		t.Fatalf("scores must carry the relative framing, got %q", sn)
	}
	// 1w still honestly absent.
	hw := out["horizons"].(map[string]any)["1w"].(map[string]any)
	if hw["available"] != false || hw["gated"] != true {
		t.Fatalf("1w should stay unavailable+gated, got %v", hw)
	}
}
