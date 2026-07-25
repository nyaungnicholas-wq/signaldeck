package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/config"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/sentcorr"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

func newSentCorrServer(t *testing.T) (string, *store.Store) {
	t.Helper()
	srv, st, d := newTestServer(t, func(c *config.Config) {})
	mux := http.NewServeMux()
	d.registerSentCorr(mux)
	srv.Config.Handler = d.secure(mux)
	return srv.URL, st
}

func getSentCorr(t *testing.T, url, query string) map[string]any {
	t.Helper()
	res, err := newClient(t).Get(url + "/api/sentiment-correlation" + query)
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

// With no study stored yet the endpoint must still answer, with an empty studies
// list and every caveat present. An experimental surface that 500s before its
// worker first runs reads as broken.
func TestSentCorrEndpoint_EmptyIsHonestNotBroken(t *testing.T) {
	url, _ := newSentCorrServer(t)
	out := getSentCorr(t, url, "")

	studies, ok := out["studies"].([]any)
	if !ok {
		t.Fatalf("studies must serialise as an array, got %T", out["studies"])
	}
	if len(studies) != 0 {
		t.Errorf("studies = %d, want 0 before the first run", len(studies))
	}
	// [] not null: the page maps over this.
	if _, ok := out["symbolFeatures"].([]any); !ok {
		t.Errorf("symbolFeatures must be an array, got %T", out["symbolFeatures"])
	}
	for _, k := range []string{"methodology", "whyPartialNotRaw", "whyClusteredCI",
		"scoring", "gates", "caveat", "expectation"} {
		if s, _ := out[k].(string); s == "" {
			t.Errorf("%s must ship in every payload", k)
		}
	}
	if hm, _ := out["headlineMetric"].(string); hm != "partialIC" {
		t.Errorf("headlineMetric = %q, want partialIC — the raw IC must never be the headline", hm)
	}
	caveat, _ := out["caveat"].(string)
	if !strings.Contains(caveat, "not a signal") {
		t.Errorf("caveat must say this is wired into nothing, got %q", caveat)
	}
}

// A stored GATED result must reach the client with its metrics still null. If the
// JSON round-trip turned a withheld metric into 0 the page would render "0.0000"
// and read as "no effect measured" instead of "not measurable yet".
func TestSentCorrEndpoint_GatedMetricsStayNull(t *testing.T) {
	url, st := newSentCorrServer(t)
	ctx := context.Background()

	gated := sentcorr.Study(nil, sentcorr.DefaultConfig(5))
	if !gated.Gated || gated.PartialIC != nil {
		t.Fatalf("fixture should be gated with nil metrics, got %+v", gated)
	}
	payload, err := json.Marshal(gated)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertSentCorrResult(ctx, "mean_score", 5, 1_700_000_000, gated.Obs, true, string(payload)); err != nil {
		t.Fatal(err)
	}

	out := getSentCorr(t, url, "")
	studies, _ := out["studies"].([]any)
	if len(studies) != 1 {
		t.Fatalf("studies = %d, want 1", len(studies))
	}
	first, _ := studies[0].(map[string]any)
	result, _ := first["result"].(map[string]any)
	if result == nil {
		t.Fatal("result missing")
	}
	if g, _ := result["gated"].(bool); !g {
		t.Error("gated flag lost in transit")
	}
	for _, k := range []string{"partialIC", "rawIC", "hitRate", "baseRate", "partialICLo"} {
		if v, present := result[k]; !present {
			t.Errorf("%s key missing entirely; the page needs it to render 'withheld'", k)
		} else if v != nil {
			t.Errorf("%s = %v, want null — a withheld metric must not arrive as a number", k, v)
		}
	}
	if reason, _ := result["gateReason"].(string); reason == "" {
		t.Error("gateReason must state why no verdict was claimed")
	}
}

// An ungated result must arrive with its numbers, its family-corrected interval,
// and the verdict text the engine generated.
func TestSentCorrEndpoint_UngatedCarriesVerdictAndInterval(t *testing.T) {
	url, st := newSentCorrServer(t)
	ctx := context.Background()

	res := sentcorr.Result{
		Horizon: 5, Obs: 2589, Symbols: 650, Months: 14,
		PartialIC: fptr(-0.078), PartialLo: fptr(-0.134), PartialHi: fptr(-0.018),
		PartialLo95: fptr(-0.122), PartialHi95: fptr(-0.033),
		CILevel: "98.33% (Bonferroni over 3 horizons)", FamilySize: 3,
		RawIC: fptr(-0.064), Quintiles: []sentcorr.QuintileRow{},
		SpreadAligned: fptr(0.011), AlignedSide: "CONTRARIAN: long the most-NEGATIVE-sentiment quintile, short the most-positive",
		Verdict: "measured verdict text",
	}
	payload, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertSentCorrResult(ctx, "mean_score", 5, 1_700_000_000, res.Obs, false, string(payload)); err != nil {
		t.Fatal(err)
	}

	out := getSentCorr(t, url, "")
	studies, _ := out["studies"].([]any)
	if len(studies) != 1 {
		t.Fatalf("studies = %d, want 1", len(studies))
	}
	result, _ := studies[0].(map[string]any)["result"].(map[string]any)
	if got, _ := result["verdict"].(string); got != "measured verdict text" {
		t.Errorf("verdict = %q, want the engine's own text", got)
	}
	if got, _ := result["ciLevel"].(string); !strings.Contains(got, "Bonferroni") {
		t.Errorf("ciLevel = %q; the multiple-testing correction must be named", got)
	}
	// The aligned side must survive: without it a negative spread number can be
	// read as the wrong portfolio.
	if got, _ := result["alignedSide"].(string); !strings.Contains(got, "CONTRARIAN") {
		t.Errorf("alignedSide = %q, want the contrarian book named", got)
	}
	if got, _ := result["partialIC"].(float64); got >= 0 {
		t.Errorf("partialIC = %v, want the stored negative value", got)
	}
}

// The optional per-symbol view returns that symbol's aligned rows.
func TestSentCorrEndpoint_PerSymbolView(t *testing.T) {
	url, st := newSentCorrServer(t)
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "AAA", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertSentimentFeatures(ctx, []store.SentimentFeature{
		{SymbolID: sym.ID, Day: "2026-03-02", NPolar: 2, NAll: 3, MeanScore: 0.25, Pos: 2, Ver: 1},
	}); err != nil {
		t.Fatal(err)
	}

	out := getSentCorr(t, url, "?symbol=AAA&market=stocks")
	if got, _ := out["symbol"].(string); got != "AAA" {
		t.Errorf("symbol = %q, want AAA", got)
	}
	rows, _ := out["symbolFeatures"].([]any)
	if len(rows) != 1 {
		t.Fatalf("symbolFeatures = %d rows, want 1", len(rows))
	}
	row, _ := rows[0].(map[string]any)
	if day, _ := row["day"].(string); day != "2026-03-02" {
		t.Errorf("day = %q, want 2026-03-02", day)
	}

	// An unknown symbol must not fail the whole payload — the fleet-wide study is
	// still the point of the page.
	out = getSentCorr(t, url, "?symbol=NOPE&market=stocks")
	if got, _ := out["symbol"].(string); got != "" {
		t.Errorf("symbol = %q, want empty for an unknown ticker", got)
	}
	if _, ok := out["symbolFeatures"].([]any); !ok {
		t.Error("symbolFeatures must stay an array for an unknown ticker")
	}
}

func fptr(v float64) *float64 { return &v }
