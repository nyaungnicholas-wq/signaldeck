// C4 (residual) — /api/attribution publishes an uncertainty band from
// internal/attribution.Wilson at a RAW count, with no day floor and no statement
// of what the count counts.
//
// The two evidence sources behave differently and the fix has to say so per
// source rather than applying one blanket correction:
//
//   - LIVE evidence comes from DirectionalAccuracyBySymbol, whose SQL is
//     ROW_NUMBER() OVER (PARTITION BY symbol_id, ts/86400) ... WHERE rn=1. For ONE
//     symbol that makes N distinct days == N observations, so the cross-sectional
//     day clustering that inflates every fleet-wide interval by 14.7x does not
//     apply here: the design effect is 1 BY CONSTRUCTION, not by assumption. What
//     does still apply is the day floor.
//   - The HISTORICAL PRIOR comes from the expectancy table, which stores only
//     (state_key, n, hit_rate). At 1d its samples are one non-overlapping daily
//     forward return per daily bar. At 1w internal/expectancy records
//     closes[i+5]/closes[i]-1 at EVERY i, so consecutive samples overlap by four
//     days and n counts roughly five windows per independent outcome — and no
//     timestamp survives into the table with which to correct it.
package api

import (
	"context"
	"encoding/json"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// attributionBandBody decodes the band and its provenance.
type attributionBandBody struct {
	Report struct {
		LiveN    int      `json:"liveResolvedN"`
		HistN    int      `json:"historicalPriorN"`
		Driver   string   `json:"driver"`
		BandLo   *float64 `json:"uncertaintyLo"`
		BandHi   *float64 `json:"uncertaintyHi"`
		Source   string   `json:"uncertaintyBandSource"`
		Reason   string   `json:"uncertaintyBandReason"`
		LiveDays int      `json:"liveDistinctDays"`
	} `json:"report"`
}

func getAttributionBand(t *testing.T, url string) attributionBandBody {
	t.Helper()
	res, err := newClient(t).Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close() //nolint:errcheck
	if res.StatusCode != 200 {
		t.Fatalf("status=%d want 200", res.StatusCode)
	}
	var body attributionBandBody
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return body
}

// seedAttributionLive writes days resolved 1d outcomes for one symbol, one per
// UTC day, correct on the first `correct` of them.
func seedAttributionLive(t *testing.T, st *store.Store, symbolID int64, days, correct int) {
	t.Helper()
	ctx := context.Background()
	for day := 0; day < days; day++ {
		ts := int64(20000+day)*86400 + 43200
		right := day < correct
		prob, fwd := 0.7, 0.01
		if !right {
			fwd = -0.01
		}
		if err := st.UpsertPrediction(ctx, store.Prediction{
			SymbolID: symbolID, Horizon: md.H1d, Ts: ts,
			RawProb: prob, CalProb: prob, NUsed: 3, Components: "{}",
		}); err != nil {
			t.Fatalf("upsert prediction: %v", err)
		}
		if err := st.ResolvePrediction(ctx, symbolID, md.H1d, ts, fwd); err != nil {
			t.Fatalf("resolve prediction: %v", err)
		}
	}
}

// TestAttribution_ThinPriorBandWithheld: with 4 live days and a 6-analog prior,
// neither source can carry an interval — 6 analogs is below the day floor every
// other surface enforces. The band must be null with a stated reason, not a
// number spanning most of the unit interval that a reader will quote.
func TestAttribution_ThinPriorBandWithheld(t *testing.T) {
	srv, st := newAttributionServer(t)
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "THIN", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.ReplaceExpectancy(ctx, sym.ID, md.H1d, []md.Expectancy{
		{Horizon: md.H1d, StateKey: "s", N: 6, HitRate: 0.83, MeanFwd: 0.004},
	}); err != nil {
		t.Fatal(err)
	}
	seedAttributionLive(t, st, sym.ID, 4, 3)

	body := getAttributionBand(t, srv.URL+"/api/attribution?symbol=THIN&market=stocks&horizon=1d")
	if body.Report.BandLo != nil || body.Report.BandHi != nil {
		t.Fatalf("band published off a 6-analog prior: [%v, %v]", body.Report.BandLo, body.Report.BandHi)
	}
	if body.Report.Reason == "" {
		t.Fatal("withheld band carries no reason")
	}
}

// TestAttribution_WeeklyPriorBandWithheld: the 1w expectancy prior samples a
// 5-day forward return at every daily bar, so its n counts ~5 overlapping
// windows per independent outcome and the table keeps no timestamp to correct
// with. A Wilson interval at that n asserts five times the evidence that exists,
// so it is withheld rather than shipped beside a caveat.
func TestAttribution_WeeklyPriorBandWithheld(t *testing.T) {
	srv, st := newAttributionServer(t)
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "WEEKLY", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.ReplaceExpectancy(ctx, sym.ID, md.H1w, []md.Expectancy{
		{Horizon: md.H1w, StateKey: "s", N: 400, HitRate: 0.62, MeanFwd: 0.011},
	}); err != nil {
		t.Fatal(err)
	}

	body := getAttributionBand(t, srv.URL+"/api/attribution?symbol=WEEKLY&market=stocks&horizon=1w")
	if body.Report.HistN != 400 {
		t.Fatalf("prior N = %d, want 400 (fixture did not load)", body.Report.HistN)
	}
	if body.Report.BandLo != nil || body.Report.BandHi != nil {
		t.Fatalf("band published off 400 OVERLAPPING weekly windows: [%v, %v]", body.Report.BandLo, body.Report.BandHi)
	}
	if body.Report.Source != "withheld" {
		t.Fatalf("band source = %q, want withheld", body.Report.Source)
	}
}

// TestAttribution_LiveBandReportsItsDayCount: once live evidence drives the
// band, the payload must say how many DISTINCT DAYS it rests on — which for one
// symbol is exactly its observation count, because the store admits at most one
// row per (symbol, UTC-day).
func TestAttribution_LiveBandReportsItsDayCount(t *testing.T) {
	srv, st := newAttributionServer(t)
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "LIVE", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}
	seedAttributionLive(t, st, sym.ID, 40, 26)

	body := getAttributionBand(t, srv.URL+"/api/attribution?symbol=LIVE&market=stocks&horizon=1d")
	if body.Report.LiveN != 40 {
		t.Fatalf("liveN = %d, want 40", body.Report.LiveN)
	}
	if body.Report.LiveDays != 40 {
		t.Fatalf("liveDistinctDays = %d, want 40 (one row per symbol-day by construction)", body.Report.LiveDays)
	}
	if body.Report.BandLo == nil || body.Report.BandHi == nil {
		t.Fatalf("band withheld on 40 independent days: %s", body.Report.Reason)
	}
	if body.Report.Source != "live" {
		t.Fatalf("band source = %q, want live", body.Report.Source)
	}
	if !(*body.Report.BandLo < 0.65 && *body.Report.BandHi > 0.65) {
		t.Fatalf("band [%.4f, %.4f] does not contain the measured 65%% accuracy", *body.Report.BandLo, *body.Report.BandHi)
	}
}
