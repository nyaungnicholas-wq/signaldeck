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

func newHonestyGapServer(t *testing.T, mutate func(*config.Config)) (*httptest.Server, *store.Store) {
	t.Helper()
	srv, st, d := newTestServer(t, mutate)
	mux := http.NewServeMux()
	d.registerHonestyGaps(mux)
	srv.Config.Handler = d.secure(mux)
	return srv, st
}

// Every one of these surfaces must return 200 with an HONEST EMPTY payload
// before its worker has ever run. A 404 or a 500 on "nothing measured yet" is
// how an empty state gets mistaken for a broken one.
func TestHonestyGapEndpointsEmptyState(t *testing.T) {
	srv, _ := newHonestyGapServer(t, nil)

	rf := getJSON(t, srv, "/api/return-forecast")
	if rf["count"].(float64) != 0 || rf["howToRead"] == "" {
		t.Fatalf("return-forecast empty state = %+v", rf)
	}
	if rf["minSample"].(float64) <= 0 {
		t.Fatal("return-forecast did not report its sample floor")
	}

	fr := getJSON(t, srv, "/api/feature-redundancy")
	if fr["available"] != false || fr["note"] == "" {
		t.Fatalf("feature-redundancy empty state = %+v", fr)
	}

	cn := getJSON(t, srv, "/api/canary")
	if cn["count"].(float64) != 0 {
		t.Fatalf("canary empty state = %+v", cn)
	}
	gates := cn["gates"].(map[string]any)
	if gates["minObservations"].(float64) != 30 {
		t.Fatalf("canary gates not published: %+v", gates)
	}

	dv := getJSON(t, srv, "/api/dataset-versions")
	if dv["count"].(float64) != 0 || dv["revisedSymbols"].(float64) != 0 {
		t.Fatalf("dataset-versions empty state = %+v", dv)
	}

	pv := getJSON(t, srv, "/api/price-validation")
	if pv["enabled"] != false {
		t.Fatalf("price-validation must report disabled when unconfigured: %+v", pv)
	}
}

func TestReturnForecastEndpointServesStoredDistribution(t *testing.T) {
	srv, st := newHonestyGapServer(t, nil)
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	if err != nil {
		t.Fatal(err)
	}
	skill, cov := 0.08, 0.79
	if err := st.UpsertReturnForecast(ctx, store.ReturnForecast{
		SymbolID: sym.ID, Horizon: md.H1d, Ts: 1000, Regime: "calm", N: 300, Tau: 0.001,
		Mean: 0.0002, Sigma: 0.013, Q10: -0.016, Q50: 0.0001, Q90: 0.017,
		PUp: 0.43, PDown: 0.42, PInside: 0.15, Edge: 0.01, ExpectedValue: -0.0008,
		Skill: &skill, Coverage80: &cov, GradedN: 150,
	}); err != nil {
		t.Fatal(err)
	}
	// An ungraded second forecast, so the graded/positiveSkill split is exercised.
	if err := st.UpsertReturnForecast(ctx, store.ReturnForecast{
		SymbolID: sym.ID, Horizon: md.H1w, Ts: 1000, Regime: "calm", N: 200, Tau: 0.001,
		PInside: 0.4, PUp: 0.31, PDown: 0.29,
	}); err != nil {
		t.Fatal(err)
	}

	got := getJSON(t, srv, "/api/return-forecast")
	if got["count"].(float64) != 2 {
		t.Fatalf("count = %v, want 2", got["count"])
	}
	if got["graded"].(float64) != 1 || got["positiveSkill"].(float64) != 1 {
		t.Fatalf("graded/positiveSkill = %v/%v, want 1/1", got["graded"], got["positiveSkill"])
	}
	rows := got["forecasts"].([]any)
	first := rows[0].(map[string]any)
	// The three probabilities must reach the client — the no-trade zone is the
	// whole point and must not be dropped from the payload.
	for _, k := range []string{"pUp", "pDown", "pInside", "expectedValue", "q10", "q90", "regime"} {
		if _, ok := first[k]; !ok {
			t.Fatalf("payload missing %q: %+v", k, first)
		}
	}
	// An ungraded forecast must serialize skill as null, never as 0.
	for _, r := range rows {
		m := r.(map[string]any)
		if m["horizon"] == "1w" && m["skill"] != nil {
			t.Fatalf("ungraded forecast reported skill %v", m["skill"])
		}
	}

	// Filters must actually filter.
	if got := getJSON(t, srv, "/api/return-forecast?horizon=1d"); got["count"].(float64) != 1 {
		t.Fatalf("horizon filter returned %v rows", got["count"])
	}
	if got := getJSON(t, srv, "/api/return-forecast?symbol=msft"); got["count"].(float64) != 0 {
		t.Fatalf("symbol filter returned %v rows for an unknown symbol", got["count"])
	}
}

func TestCanaryAndDatasetEndpointsServeStoredRows(t *testing.T) {
	srv, st := newHonestyGapServer(t, nil)
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertCanaryTrial(ctx, store.CanaryTrial{
		Model: "directional-ensemble-1d", Incumbent: "v9", Challenger: "v10",
		Decision: "hold", Serving: "v9", Reason: "too few independent observations",
		IncN: 12000, IncAcc: 0.48, ChN: 14, ChAcc: 0.57, ChLower: 0.32, ChUpper: 0.78,
		Baseline: 0.544, DecidedAt: 1000,
	}); err != nil {
		t.Fatal(err)
	}
	got := getJSON(t, srv, "/api/canary")
	trials := got["trials"].([]any)
	if len(trials) != 1 {
		t.Fatalf("got %d trials", len(trials))
	}
	tr := trials[0].(map[string]any)
	if tr["decision"] != "hold" || tr["serving"] != "v9" || tr["reason"] == "" {
		t.Fatalf("trial payload = %+v", tr)
	}

	if err := st.UpsertDatasetVersion(ctx, store.DatasetVersion{
		SymbolID: sym.ID, Timeframe: "1d", FirstTs: 100, LastTs: 900, N: 500,
		Hash: "deadbeef", CheckedAt: 1000, Revisions: 2,
	}); err != nil {
		t.Fatal(err)
	}
	dv := getJSON(t, srv, "/api/dataset-versions")
	if dv["count"].(float64) != 1 || dv["revisedSymbols"].(float64) != 1 {
		t.Fatalf("dataset-versions = %+v", dv)
	}
	v := dv["versions"].([]any)[0].(map[string]any)
	if v["symbol"] != "AAPL" || v["revisions"].(float64) != 2 || v["hash"] != "deadbeef" {
		t.Fatalf("version payload = %+v", v)
	}
}

// The stored meta blobs must survive the round trip to the client, and a
// corrupt blob must degrade to "unavailable" rather than a 500.
func TestMetaBackedEndpoints(t *testing.T) {
	srv, st := newHonestyGapServer(t, nil)
	ctx := context.Background()

	if err := st.SetMeta(ctx, store.MetaFeatureRedundancy,
		`[{"horizon":"1d","report":{"fieldCount":40,"effectiveCount":12,"redundancyRatio":0.7}}]`); err != nil {
		t.Fatal(err)
	}
	fr := getJSON(t, srv, "/api/feature-redundancy")
	if fr["available"] != true {
		t.Fatalf("stored report not served: %+v", fr)
	}
	h := fr["horizons"].([]any)[0].(map[string]any)["report"].(map[string]any)
	if h["fieldCount"].(float64) != 40 || h["effectiveCount"].(float64) != 12 {
		t.Fatalf("report payload = %+v", h)
	}

	if err := st.SetMeta(ctx, store.MetaPriceValidation, `{"checked":3,"disagreeing":1}`); err != nil {
		t.Fatal(err)
	}
	pv := getJSON(t, srv, "/api/price-validation")
	if pv["enabled"] != true {
		t.Fatalf("stored validation not served: %+v", pv)
	}

	// Corrupt blob: honest unavailable, not a 500.
	if err := st.SetMeta(ctx, store.MetaFeatureRedundancy, `{not json`); err != nil {
		t.Fatal(err)
	}
	fr = getJSON(t, srv, "/api/feature-redundancy")
	if fr["available"] != false {
		t.Fatalf("corrupt blob served as available: %+v", fr)
	}
}
