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
		QuintileSpread float64 `json:"quintileSpread"`
		HitRate        float64 `json:"hitRate"`
		Turnover       float64 `json:"turnover"`
		CostBps        float64 `json:"costBps"`
		Equity         []struct {
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

// TestSignalBacktest_CompoundsPerDayNotPerSymbolDayRow pins the C1 regression.
//
// The equity curve used to compound once per (symbol, UTC-day) ROW while holding
// a single scalar position shared across every symbol, so a universe of S
// symbols observed over D days compounded S*D times — against a benchmark built
// over D distinct days, making the two series incomparable. Live that printed
// strategyReturn=-0.9995 at turnover=0.00032: an unlevered book cannot lose
// 99.95% of capital while trading 0.03% of itself, so the number was an
// accounting artifact, not a result.
//
// The fixture is the exact shape that broke it and that the per-day test in the
// engine cannot see end to end: 40 symbols observed on the SAME 3 days, each
// losing 1% over its forward window. One book, long the whole universe, down 1%
// a day for three days, is down ~3% — not ~70%.
func TestSignalBacktest_CompoundsPerDayNotPerSymbolDayRow(t *testing.T) {
	srv, st := newSignalBTServer(t, nil)
	ctx := context.Background()
	day := int64(86400)

	const days, symbols = 3, 40
	const dailyFwd = -0.01

	// SPY over the same calendar so the benchmark shares the strategy's day index.
	spy, err := st.UpsertSymbol(ctx, "SPY", md.Stocks, "S&P 500 ETF")
	if err != nil {
		t.Fatal(err)
	}
	spyBars := make([]md.Bar, 0, days+2)
	px := 400.0
	for i := 0; i < days+2; i++ {
		spyBars = append(spyBars, md.Bar{SymbolID: spy.ID, TF: md.TF1d, Ts: int64(i) * day, Close: px})
		px *= 1.001
	}
	if err := st.UpsertBars(ctx, spyBars); err != nil {
		t.Fatal(err)
	}

	for s := 0; s < symbols; s++ {
		sym, err := st.UpsertSymbol(ctx, symName(s), md.Stocks, "")
		if err != nil {
			t.Fatal(err)
		}
		for d := 0; d < days; d++ {
			ts := int64(d) * day
			if err := st.UpsertPrediction(ctx, store.Prediction{
				SymbolID: sym.ID, Horizon: md.H1d, Ts: ts,
				RawProb: 0.9, CalProb: 0.9, NUsed: 2, Components: "{}",
			}); err != nil {
				t.Fatal(err)
			}
			if err := st.ResolvePrediction(ctx, sym.ID, md.H1d, ts, dailyFwd); err != nil {
				t.Fatal(err)
			}
		}
	}

	body := getSignalBT(t, srv.URL+"/api/signal-backtest?horizon=1d")
	if body.Result.IndependentN != days*symbols {
		t.Fatalf("independentN=%d want %d (one obs per symbol-day)", body.Result.IndependentN, days*symbols)
	}
	if body.Result.Gated {
		t.Fatalf("120 independent obs must clear the gate: note=%q", body.Result.Note)
	}

	// One equity mark per DISTINCT DAY, on the day index the benchmark uses.
	if len(body.Result.Equity) != days {
		t.Fatalf("equity has %d marks, want %d — the curve is compounding per (symbol,day) row, not per day", len(body.Result.Equity), days)
	}
	for i, pt := range body.Result.Equity {
		if want := int64(i) * day; pt.Ts != want {
			t.Fatalf("equity[%d].ts=%d want day-start %d (strategy and benchmark must share the day index)", i, pt.Ts, want)
		}
	}

	// Three consecutive -1% days on a fully invested book, minus one entry cost.
	if body.Result.StrategyReturn < -0.10 || body.Result.StrategyReturn > -0.01 {
		t.Fatalf("strategyReturn=%v want about -0.030 (three -1%% days on one book); a value near -0.70 is per-row compounding", body.Result.StrategyReturn)
	}

	// The book is bought once and held: mean per-day turnover = 1 buy / 3 days.
	if body.Result.Turnover < 0.30 || body.Result.Turnover > 0.34 {
		t.Fatalf("turnover=%v want about 0.333 (whole book bought on day 1, held after)", body.Result.Turnover)
	}
}

// symName gives a deterministic distinct ticker for the i-th synthetic symbol.
func symName(i int) string {
	const alpha = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	return "T" + string(alpha[i/26%26]) + string(alpha[i%26])
}
