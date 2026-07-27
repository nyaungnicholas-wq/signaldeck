// Round-trip tests for the cross-sectional alpha (alphax) store layer and the
// model-forecast legs it rides on: the per-horizon model+grade row, the pooled
// labeled training set, the live cross-section read, and the gated-regrade
// delete path that must never leave a stale score behind.
package store

import (
	"context"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

func TestAlphaXModel_UpsertAndHonestAbsence(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()

	if _, ok, err := st.AlphaXModel(ctx, md.H1d); err != nil || ok {
		t.Fatalf("absent model = ok=%v, %v; want false, nil", ok, err)
	}

	r := AlphaXModelRow{Horizon: md.H1d, Ts: 1000, OOSLift: 0.02, OOSAUC: 0.54,
		OOSAcc: 0.53, BaseRate: 0.51, NTrain: 400, NTest: 100, Gated: false,
		ModelJSON: `{"keys":["a"]}`}
	if err := st.UpsertAlphaXModel(ctx, r); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	// Regrade in place on the horizon key — now gated.
	r.Ts, r.OOSLift, r.Gated = 2000, -0.01, true
	if err := st.UpsertAlphaXModel(ctx, r); err != nil {
		t.Fatalf("regrade: %v", err)
	}
	got, ok, err := st.AlphaXModel(ctx, md.H1d)
	if err != nil || !ok {
		t.Fatalf("read model = ok=%v, %v", ok, err)
	}
	if got.Ts != 2000 || !got.Gated || got.OOSLift != -0.01 || got.ModelJSON != `{"keys":["a"]}` {
		t.Fatalf("regrade did not replace in place: %+v", got)
	}
}

func TestLabeledFeaturesAll_PoolsAcrossSymbolsAndVersions(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()

	a, _ := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	b, _ := st.UpsertSymbol(ctx, "MSFT", md.Stocks, "Microsoft")

	seed := func(symID, ts int64, version int, fwd float64) {
		t.Helper()
		if err := st.InsertFeatures(ctx, symID, md.H1d, ts, version,
			map[string]float64{"momo": 0.1}); err != nil {
			t.Fatalf("insert features: %v", err)
		}
		if err := st.UpsertPrediction(ctx, Prediction{
			SymbolID: symID, Horizon: md.H1d, Ts: ts, RawProb: 0.6, CalProb: 0.6,
			NUsed: 10, Components: "{}"}); err != nil {
			t.Fatalf("upsert prediction: %v", err)
		}
		if err := st.ResolvePrediction(ctx, symID, md.H1d, ts, fwd); err != nil {
			t.Fatalf("resolve: %v", err)
		}
	}
	seed(a.ID, 1000, 3, 0.02)  // v3 — excluded when minVersion=4
	seed(a.ID, 2000, 4, -0.01) // v4 down
	seed(b.ID, 3000, 4, 0.03)  // v4 up

	// Unresolved rows never enter the training set.
	if err := st.InsertFeatures(ctx, b.ID, md.H1d, 4000, 4,
		map[string]float64{"momo": 0.2}); err != nil {
		t.Fatalf("insert features: %v", err)
	}
	if err := st.UpsertPrediction(ctx, Prediction{SymbolID: b.ID, Horizon: md.H1d,
		Ts: 4000, RawProb: 0.5, CalProb: 0.5, NUsed: 10, Components: "{}"}); err != nil {
		t.Fatalf("upsert prediction: %v", err)
	}

	all, err := st.LabeledFeaturesAll(ctx, md.H1d, 3, 100)
	if err != nil || len(all) != 3 {
		t.Fatalf("pooled v3+ = %d rows, %v; want 3", len(all), err)
	}
	// Newest first, labels attached.
	if all[0].SymbolID != b.ID || all[0].Up != 1 || all[0].FwdReturn != 0.03 {
		t.Fatalf("pooled row 0 mangled: %+v", all[0])
	}
	if all[0].Vec["momo"] != 0.1 {
		t.Fatalf("vector did not round-trip: %+v", all[0].Vec)
	}
	v4, err := st.LabeledFeaturesAll(ctx, md.H1d, 4, 100)
	if err != nil || len(v4) != 2 {
		t.Fatalf("minVersion=4 = %d rows, %v; want 2", len(v4), err)
	}
}

func TestLatestFeaturesByHorizon_OneRowPerSymbolHighestVersion(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()

	a, _ := st.UpsertSymbol(ctx, "NVDA", md.Stocks, "Nvidia")
	b, _ := st.UpsertSymbol(ctx, "AMD", md.Stocks, "AMD")

	// Same symbol, same newest ts, two versions: the highest version wins.
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("insert features: %v", err)
		}
	}
	must(st.InsertFeatures(ctx, a.ID, md.H1d, 1000, 4, map[string]float64{"x": 1}))
	must(st.InsertFeatures(ctx, a.ID, md.H1d, 2000, 4, map[string]float64{"x": 2}))
	must(st.InsertFeatures(ctx, a.ID, md.H1d, 2000, 5, map[string]float64{"x": 3}))
	must(st.InsertFeatures(ctx, b.ID, md.H1d, 1500, 4, map[string]float64{"x": 9}))
	// Stale symbol outside the sinceTs window is excluded entirely.
	c, _ := st.UpsertSymbol(ctx, "INTC", md.Stocks, "Intel")
	must(st.InsertFeatures(ctx, c.ID, md.H1d, 100, 4, map[string]float64{"x": 7}))

	rows, err := st.LatestFeaturesByHorizon(ctx, md.H1d, 500)
	if err != nil || len(rows) != 2 {
		t.Fatalf("cross-section = %d rows, %v; want 2", len(rows), err)
	}
	byID := map[int64]FeatureRow{}
	for _, r := range rows {
		byID[r.SymbolID] = r
	}
	if r := byID[a.ID]; r.Ts != 2000 || r.Version != 5 || r.Vec["x"] != 3 {
		t.Fatalf("newest/highest-version row not kept: %+v", r)
	}
	if r := byID[b.ID]; r.Ts != 1500 || r.Vec["x"] != 9 {
		t.Fatalf("second symbol mangled: %+v", r)
	}
}

func TestModelForecasts_UpsertTopAndGatedDelete(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()

	a, _ := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	b, _ := st.UpsertSymbol(ctx, "MSFT", md.Stocks, "Microsoft")

	put := func(symID int64, model string, prob float64) {
		t.Helper()
		if err := st.UpsertModelForecast(ctx, ModelForecast{
			SymbolID: symID, Horizon: md.H1d, Model: model, Ts: 1000, Prob: prob,
			Accuracy: 0.55, Brier: 0.24, AUC: 0.53, BaseRate: 0.5, Lift: 0.05,
			NTrain: 200, NEval: 50}); err != nil {
			t.Fatalf("upsert forecast: %v", err)
		}
	}
	put(a.ID, ModelAlphaX, 0.61)
	put(b.ID, ModelAlphaX, 0.72)
	put(a.ID, ModelGBM, 0.58)

	// Upsert replaces in place on (symbol, horizon, model).
	put(a.ID, ModelAlphaX, 0.66)
	fs, err := st.ModelForecasts(ctx, a.ID)
	if err != nil || len(fs) != 2 {
		t.Fatalf("ModelForecasts = %d rows, %v; want 2", len(fs), err)
	}
	for _, f := range fs {
		if f.Model == ModelAlphaX && f.Prob != 0.66 {
			t.Fatalf("upsert did not replace prob: %+v", f)
		}
	}

	// Leaderboard read: prob-desc, symbol-resolved, limit<=0 normalized.
	top, err := st.TopModelForecastsByModel(ctx, ModelAlphaX, md.H1d, 0)
	if err != nil || len(top) != 2 {
		t.Fatalf("top = %d rows, %v; want 2", len(top), err)
	}
	if top[0].Symbol != "MSFT" || top[0].Prob != 0.72 || top[1].Symbol != "AAPL" {
		t.Fatalf("leaderboard order wrong: %+v", top)
	}

	// Gated regrade: every alphax score goes, the GBM leg stays.
	n, err := st.DeleteModelForecastsByModel(ctx, ModelAlphaX, md.H1d)
	if err != nil || n != 2 {
		t.Fatalf("delete = %d, %v; want 2", n, err)
	}
	if top, _ := st.TopModelForecastsByModel(ctx, ModelAlphaX, md.H1d, 5); len(top) != 0 {
		t.Fatal("stale alphax score lingered after gated delete")
	}
	if fs, _ := st.ModelForecasts(ctx, a.ID); len(fs) != 1 || fs[0].Model != ModelGBM {
		t.Fatalf("delete touched another model's rows: %+v", fs)
	}
}
