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

func newSignalBTServer(t *testing.T, mutate func(*config.Config)) (*httptest.Server, *store.Store) {
	t.Helper()
	srv, st, d := newTestServer(t, mutate)
	mux := http.NewServeMux()
	d.registerSignalBT(mux)
	srv.Config.Handler = d.secure(mux)
	return srv, st
}

// signalBTBody is the decoded /api/signal-backtest response.
type signalBTBody struct {
	Result struct {
		Horizon         string  `json:"horizon"`
		RawN            int     `json:"rawN"`
		IndependentN    int     `json:"independentN"`
		MinIndependentN int     `json:"minIndependentN"`
		Gated           bool    `json:"gated"`
		IC              float64 `json:"ic"`
		ICDecay         []struct {
			LagDays int     `json:"lagDays"`
			IC      float64 `json:"ic"`
			N       int     `json:"n"`
		} `json:"icDecay"`
		Quintiles []struct {
			Quintile int     `json:"quintile"`
			N        int     `json:"n"`
			MeanFwd  float64 `json:"meanFwd"`
		} `json:"quintiles"`
		QuintileSpread  float64 `json:"quintileSpread"`
		HitRate         float64 `json:"hitRate"`
		Turnover        float64 `json:"turnover"`
		CostBps         float64 `json:"costBps"`
		Equity          []struct {
			Ts        int64   `json:"ts"`
			Strategy  float64 `json:"strategy"`
			Benchmark float64 `json:"benchmark"`
		} `json:"equity"`
		StrategyReturn  float64 `json:"strategyReturn"`
		BenchmarkReturn float64 `json:"benchmarkReturn"`
		ExcessReturn    float64 `json:"excessReturn"`
		Live            bool    `json:"live"`
		TrackLabel      string  `json:"trackLabel"`
		Note            string  `json:"note"`
	} `json:"result"`
	Horizons        []string `json:"horizons"`
	BenchmarkSymbol string   `json:"benchmarkSymbol"`
	HasBenchmark    bool     `json:"hasBenchmark"`
}

func getSignalBT(t *testing.T, url string) signalBTBody {
	t.Helper()
	res, err := newClient(t).Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close() //nolint:errcheck
	if res.StatusCode != 200 {
		t.Fatalf("status=%d want 200 (public read)", res.StatusCode)
	}
	var body signalBTBody
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return body
}

// TestSignalBacktest_InsufficientDataIsGatedAndHonest: with no resolved
// predictions (the state of the live system today) the endpoint returns a GATED
// result with 0 independent obs, live:false, and an honest note — never a
// fabricated skill number.
func TestSignalBacktest_InsufficientDataIsGatedAndHonest(t *testing.T) {
	srv, _ := newSignalBTServer(t, nil)
	body := getSignalBT(t, srv.URL+"/api/signal-backtest?horizon=1d")
	if body.Result.IndependentN != 0 {
		t.Fatalf("independentN=%d want 0 (no resolved predictions)", body.Result.IndependentN)
	}
	if !body.Result.Gated {
		t.Fatal("empty result must be gated")
	}
	if body.Result.Live {
		t.Fatal("result must carry live:false")
	}
	if body.Result.TrackLabel == "" || body.Result.Note == "" {
		t.Fatal("gated result must carry a track label + honest note")
	}
	if body.Result.MinIndependentN <= 0 {
		t.Fatal("minIndependentN must be surfaced")
	}
	if len(body.Horizons) != 2 {
		t.Fatalf("want the two supported horizons advertised, got %v", body.Horizons)
	}
}

// TestSignalBacktest_PopulatedWithBenchmark: seed >= MinIndependentN independent
// resolved predictions where the signal genuinely leads the forward return, plus
// a rising SPY, and assert the endpoint ungates and reports positive IC, a
// quintile spread, the SPY benchmark, and honest labeling. Everything runs on a
// TEMP db — the live DB is never touched.
func TestSignalBacktest_PopulatedWithBenchmark(t *testing.T) {
	srv, st := newSignalBTServer(t, nil)
	ctx := context.Background()
	day := int64(86400)

	// A tracked SPY with a rising daily series for the benchmark.
	spy, err := st.UpsertSymbol(ctx, "SPY", md.Stocks, "S&P 500 ETF")
	if err != nil {
		t.Fatal(err)
	}
	spyBars := make([]md.Bar, 0, 40)
	px := 400.0
	for i := 0; i < 40; i++ {
		spyBars = append(spyBars, md.Bar{SymbolID: spy.ID, TF: md.TF1d, Ts: int64(i) * day, Close: px})
		px *= 1.005
	}
	if err := st.UpsertBars(ctx, spyBars); err != nil {
		t.Fatal(err)
	}

	// 40 independent (symbol,day) resolved predictions: a leading signal (higher
	// cal_prob → higher realized fwd_return). Distinct symbols keep them
	// independent; one obs per symbol per day.
	for i := 0; i < 40; i++ {
		sym, err := st.UpsertSymbol(ctx, symName(i), md.Stocks, "")
		if err != nil {
			t.Fatal(err)
		}
		sig := 0.30 + 0.40*float64(i)/39.0 // 0.30 .. 0.70
		fwd := (sig - 0.5) * 0.10          // leads the return
		ts := int64(i) * day
		if err := st.UpsertPrediction(ctx, store.Prediction{
			SymbolID: sym.ID, Horizon: md.H1d, Ts: ts, RawProb: sig, CalProb: sig, NUsed: 2, Components: "{}",
		}); err != nil {
			t.Fatal(err)
		}
		if err := st.ResolvePrediction(ctx, sym.ID, md.H1d, ts, fwd); err != nil {
			t.Fatal(err)
		}
	}

	body := getSignalBT(t, srv.URL+"/api/signal-backtest?horizon=1d")
	if body.Result.IndependentN != 40 {
		t.Fatalf("independentN=%d want 40", body.Result.IndependentN)
	}
	if body.Result.Gated {
		t.Fatal("40 independent obs must clear the gate")
	}
	if body.Result.IC <= 0.5 {
		t.Fatalf("leading-signal IC=%v want > 0.5", body.Result.IC)
	}
	if body.Result.QuintileSpread <= 0 {
		t.Fatalf("quintile spread=%v want > 0", body.Result.QuintileSpread)
	}
	if len(body.Result.Quintiles) != 5 {
		t.Fatalf("want 5 quintiles, got %d", len(body.Result.Quintiles))
	}
	if body.Result.CostBps <= 0 {
		t.Fatal("a per-side cost must be applied (net-of-cost equity)")
	}
	// IC-decay curve present with the primary lag first.
	if len(body.Result.ICDecay) == 0 || body.Result.ICDecay[0].LagDays != 1 {
		t.Fatalf("IC-decay must lead with the primary lag: %+v", body.Result.ICDecay)
	}
	// SPY benchmark aligned + reported.
	if !body.HasBenchmark || body.BenchmarkSymbol != "SPY" {
		t.Fatalf("benchmark not wired: hasBenchmark=%v sym=%q", body.HasBenchmark, body.BenchmarkSymbol)
	}
	if body.Result.BenchmarkReturn <= 0 {
		t.Fatalf("rising-SPY benchmark return=%v want > 0", body.Result.BenchmarkReturn)
	}
	if len(body.Result.Equity) == 0 {
		t.Fatal("costed equity curve must be present")
	}
	// Honest labeling always.
	if body.Result.Live {
		t.Fatal("own-signal backtest is a replay — live must be false")
	}
	if body.Result.TrackLabel == "" {
		t.Fatal("result must carry a track label")
	}
}

// symName gives a deterministic distinct ticker for the i-th synthetic symbol.
func symName(i int) string {
	const alpha = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	return "T" + string(alpha[i/26%26]) + string(alpha[i%26])
}
