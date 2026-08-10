package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/adaptive"
	"github.com/nyaungnicholas-wq/signaldeck/internal/ensemble"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// seedLabeled writes n labeled training rows for one horizon: a persisted
// feature vector (pressure leg perfectly predictive, regime one-hot) plus a
// RESOLVED prediction outcome at the same (symbol, horizon, ts) key.
//
// ONE ROW PER UTC DAY. The attribution's floors count distinct days, so an
// hourly stride would put 40 rows on two days and the cell would be gated —
// for the right reason, but it would stop this test exercising anything else.
func seedLabeled(t *testing.T, st *store.Store, symbolID int64, h md.Horizon, n int, regime string) {
	t.Helper()
	ctx := context.Background()
	base := time.Now().Unix() - int64(n+1)*86400
	for i := 0; i < n; i++ {
		ts := base + int64(i)*86400
		up := i%2 == 0
		pressure, fwd := 0.5, 0.01 // pressure_score 0.5 -> leg 0.75 (up call)
		if !up {
			pressure, fwd = -0.5, -0.01
		}
		vec := map[string]float64{
			"pressure_score": pressure,
			"pred_raw":       0.5, "pred_cal": 0.5, "n_used": 1,
		}
		if regime != "" {
			vec["regime_"+regime] = 1
		}
		if err := st.InsertFeatures(ctx, symbolID, h, ts, featureVersion, vec); err != nil {
			t.Fatalf("features: %v", err)
		}
		if err := st.UpsertPrediction(ctx, store.Prediction{
			SymbolID: symbolID, Horizon: h, Ts: ts, RawProb: 0.5, CalProb: 0.5, NUsed: 1, Components: "{}",
		}); err != nil {
			t.Fatalf("prediction: %v", err)
		}
		if err := st.ResolvePrediction(ctx, symbolID, h, ts, fwd); err != nil {
			t.Fatalf("resolve: %v", err)
		}
	}
}

func TestAdaptiveWeightsWorker_LearnsPersistsAndRoundtrips(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	sym, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}

	w := &AdaptiveWeightsWorker{St: st}

	// No labeled outcomes: an honest no-op, nothing persisted.
	detail, err := w.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if raw, _ := st.GetMeta(ctx, adaptive.MetaKey); raw != "" {
		t.Fatalf("no data must persist nothing, got %q (detail %q)", raw, detail)
	}

	// 40 labeled examples (perfect pressure leg, regime "uptrend").
	seedLabeled(t, st, sym.ID, md.H1d, 40, "uptrend")
	if _, err := w.Run(ctx); err != nil {
		t.Fatal(err)
	}

	// Roundtrip: stored JSON parses back into the exact structure the
	// PredictionRunner loads.
	raw, err := st.GetMeta(ctx, adaptive.MetaKey)
	if err != nil || raw == "" {
		t.Fatalf("weights not persisted: %q %v", raw, err)
	}
	var got adaptive.Weights
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("roundtrip parse: %v", err)
	}
	if got.ComputedTs == 0 {
		t.Fatal("computedTs missing after roundtrip")
	}
	for _, cell := range []string{"uptrend", adaptive.AllCell} {
		c, ok := got.Cells[cell]
		if !ok || c.N != 40 || c.Gated {
			t.Fatalf("cell %q wrong after roundtrip: %+v", cell, got.Cells)
		}
		if math.Abs(c.Weights[ensemble.LegPressure]-1.0) > 1e-9 {
			t.Fatalf("cell %q weights: %+v", cell, c.Weights)
		}
		if c.HitRates[ensemble.LegPressure] != 1.0 {
			t.Fatalf("cell %q hitRates lost in roundtrip: %+v", cell, c)
		}
	}
	// And Pick over the roundtripped value selects the learned regime cell.
	if ws, gate := adaptive.Pick(got, "uptrend"); gate != adaptive.GateLearnedRegime || ws[ensemble.LegPressure] != 1 {
		t.Fatalf("pick after roundtrip: %v %q", ws, gate)
	}
}

func TestAdaptiveWeightsWorker_InsightOnMaterialShift(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	sym, err := st.UpsertSymbol(ctx, "NVDA", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}
	seedLabeled(t, st, sym.ID, md.H1d, 40, "uptrend")
	w := &AdaptiveWeightsWorker{St: st}

	// Previous weights say EXPECTANCY carries everything; the new pass will
	// learn pressure=1.0 — a >0.15 move that must produce one insight.
	prev := adaptive.Weights{ComputedTs: 1, Cells: map[string]adaptive.Cell{
		adaptive.AllCell: {N: 40, Weights: map[string]float64{ensemble.LegExpectancy: 1}},
	}}
	if err := st.SetJSON(ctx, adaptive.MetaKey, prev); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Run(ctx); err != nil {
		t.Fatal(err)
	}
	ins, err := st.InsightsByKind(ctx, "adaptive_weights_shift", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(ins) != 1 {
		t.Fatalf("want exactly 1 shift insight, got %d", len(ins))
	}

	// Re-running on identical data: weights are stable, NO new insight.
	if _, err := w.Run(ctx); err != nil {
		t.Fatal(err)
	}
	ins, err = st.InsightsByKind(ctx, "adaptive_weights_shift", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(ins) != 1 {
		t.Fatalf("stable weights must not re-alert, got %d insights", len(ins))
	}
}

func TestPredictionRunner_UsesLearnedWeightsForRegimeCell(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	sym, err := st.UpsertSymbol(ctx, "TSLA", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}
	// Streamed hot-set symbol: predicted every run (the cadence split scores
	// daily-only universe symbols just once per UTC day, which this two-run
	// test would otherwise trip on).
	if err := st.SetSymbolStream(ctx, sym.ID, true); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	var bars []md.Bar
	for i := int64(0); i < 30; i++ {
		bars = append(bars, md.Bar{
			SymbolID: sym.ID, TF: md.TF1d, Ts: now - (30-i)*86400,
			Open: 100, High: 101, Low: 99, Close: 100, Volume: 1,
		})
	}
	if err := st.UpsertBars(ctx, bars); err != nil {
		t.Fatal(err)
	}
	for _, h := range predHorizons {
		if err := st.InsertScore(ctx, md.Score{SymbolID: sym.ID, Horizon: h, Ts: now - 60, Score: 0.3}); err != nil {
			t.Fatal(err)
		}
		// A forecast WITH edge: without learned weights it would join the
		// blend at equal weight.
		if err := st.UpsertForecast(ctx, store.Forecast{
			SymbolID: sym.ID, Horizon: h, Ts: now, Prob: 0.9, Lift: 0.1, NTrain: 100, NEval: 50,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.UpsertRegime(ctx, sym.ID, now, "uptrend", 0.8, ""); err != nil {
		t.Fatal(err)
	}
	// Learned weights for the symbol's regime: pressure carries EVERYTHING.
	learned := adaptive.Weights{ComputedTs: now, Cells: map[string]adaptive.Cell{
		"uptrend": {N: 40, Weights: map[string]float64{ensemble.LegPressure: 1}},
	}}
	if err := st.SetJSON(ctx, adaptive.MetaKey, learned); err != nil {
		t.Fatal(err)
	}

	seedGradedPressureLeg(t, st, sym.ID, now)
	if _, err := (&PredictionRunner{St: st}).Run(ctx); err != nil {
		t.Fatal(err)
	}
	p, ok, err := st.LatestPrediction(ctx, sym.ID, md.H1d)
	if err != nil || !ok {
		t.Fatalf("prediction missing: %v", err)
	}
	// pressure leg = (0.3+1)/2 = 0.65. Equal-weight would blend in the 0.9
	// forecast ((0.65+0.9)/2 = 0.775); the learned weights must not.
	if math.Abs(p.RawProb-0.65) > 1e-9 {
		t.Fatalf("rawProb = %v, want 0.65 (pressure-only learned weights)", p.RawProb)
	}
	if p.NUsed != 1 {
		t.Fatalf("nUsed = %d, want 1 (only the weighted leg counts)", p.NUsed)
	}

	// Static-prior control: drop the learned weights and re-run — the same
	// inputs blend equal-weight again (the fallback IS the old behavior).
	if err := st.SetMeta(ctx, adaptive.MetaKey, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := (&PredictionRunner{St: st}).Run(ctx); err != nil {
		t.Fatal(err)
	}
	p2, ok, err := st.LatestPrediction(ctx, sym.ID, md.H1d)
	if err != nil || !ok {
		t.Fatal("second prediction missing")
	}
	if math.Abs(p2.RawProb-(0.65+0.9)/2) > 1e-9 || p2.NUsed != 2 {
		t.Fatalf("static fallback: raw=%v nUsed=%d, want %v / 2", p2.RawProb, p2.NUsed, (0.65+0.9)/2)
	}
}

func TestSentimentAggregatorWorker_FeedsThePredictionFeature(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	sym, err := st.UpsertSymbol(ctx, "SPY", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()

	// Three rated headlines TODAY (meets the n>=3 depth gate) …
	for i := 0; i < 3; i++ {
		id := fmt.Sprintf("h%d", i)
		if err := st.InsertNews(ctx, store.NewsItem{ID: id, SymbolID: sym.ID, Ts: now, Headline: id}); err != nil {
			t.Fatal(err)
		}
		if err := st.RateNews(ctx, id, "bullish", 0.6, ""); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := (&SentimentAggregator{St: st}).Run(ctx); err != nil {
		t.Fatal(err)
	}
	mean, n, ok, err := st.LatestSentiment(ctx, sym.ID, 3)
	if err != nil || !ok || n != 3 || math.Abs(mean-0.6) > 1e-9 {
		t.Fatalf("aggregate: mean=%v n=%d ok=%v err=%v", mean, n, ok, err)
	}

	// …so the PredictionRunner captures the sentiment feature into both the
	// blend components and the persisted vector.
	var bars []md.Bar
	for i := int64(0); i < 30; i++ {
		bars = append(bars, md.Bar{
			SymbolID: sym.ID, TF: md.TF1d, Ts: now - (30-i)*86400,
			Open: 100, High: 101, Low: 99, Close: 100, Volume: 1,
		})
	}
	if err := st.UpsertBars(ctx, bars); err != nil {
		t.Fatal(err)
	}
	for _, h := range predHorizons {
		if err := st.InsertScore(ctx, md.Score{SymbolID: sym.ID, Horizon: h, Ts: now - 60, Score: 0}); err != nil {
			t.Fatal(err)
		}
	}
	seedGradedPressureLeg(t, st, sym.ID, now)
	if _, err := (&PredictionRunner{St: st}).Run(ctx); err != nil {
		t.Fatal(err)
	}
	feats, err := st.FeaturesSince(ctx, sym.ID, md.H1d, 0, 0)
	if err != nil || len(feats) != 1 {
		t.Fatalf("features: %v (%d rows)", err, len(feats))
	}
	vec := feats[0].Vec
	if math.Abs(vec["sentiment_score"]-0.6) > 1e-9 || vec["sentiment_n"] != 3 {
		t.Fatalf("sentiment feature not captured: %+v", vec)
	}
	// The FEATURE is captured but the LEG is not blended, and the split is the
	// point of this test now that the runner defaults to production mode.
	//
	// Nothing assigns SentimentLift and the leg is graded on too few symbols to
	// clear the fleet evidence floor, so it carries no measured edge — and an
	// unmeasured leg is benched rather than blended in at a conservative weight.
	// Its measured within-day AUC on the live record is 0.3805, i.e. it ranks
	// backwards, so blending it in weakly was not the harmless default it looked
	// like. Capturing the feature regardless is what lets a trainer eventually
	// grade it and earn the leg back; that is the whole re-admission path, which
	// is why the vector assertion above still stands.
	p, ok, err := st.LatestPrediction(ctx, sym.ID, md.H1d)
	if err != nil || !ok {
		t.Fatal("prediction missing")
	}
	if p.NUsed != 1 || math.Abs(p.RawProb-0.5) > 1e-9 {
		t.Fatalf("raw=%v nUsed=%d, want 0.5 / 1 (pressure only; ungraded sentiment benched)", p.RawProb, p.NUsed)
	}
}

func TestPredictionRunner_ThinSentimentIsAbsent(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	sym, err := st.UpsertSymbol(ctx, "QQQ", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	// Only TWO rated headlines: below the n>=3 depth gate.
	for i := 0; i < 2; i++ {
		id := fmt.Sprintf("q%d", i)
		if err := st.InsertNews(ctx, store.NewsItem{ID: id, SymbolID: sym.ID, Ts: now, Headline: id}); err != nil {
			t.Fatal(err)
		}
		if err := st.RateNews(ctx, id, "bullish", 0.9, ""); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := (&SentimentAggregator{St: st}).Run(ctx); err != nil {
		t.Fatal(err)
	}
	var bars []md.Bar
	for i := int64(0); i < 30; i++ {
		bars = append(bars, md.Bar{
			SymbolID: sym.ID, TF: md.TF1d, Ts: now - (30-i)*86400,
			Open: 100, High: 101, Low: 99, Close: 100, Volume: 1,
		})
	}
	if err := st.UpsertBars(ctx, bars); err != nil {
		t.Fatal(err)
	}
	for _, h := range predHorizons {
		if err := st.InsertScore(ctx, md.Score{SymbolID: sym.ID, Horizon: h, Ts: now - 60, Score: 0}); err != nil {
			t.Fatal(err)
		}
	}
	seedGradedPressureLeg(t, st, sym.ID, now)
	if _, err := (&PredictionRunner{St: st}).Run(ctx); err != nil {
		t.Fatal(err)
	}
	feats, err := st.FeaturesSince(ctx, sym.ID, md.H1d, 0, 0)
	if err != nil || len(feats) != 1 {
		t.Fatalf("features: %v (%d rows)", err, len(feats))
	}
	if _, has := feats[0].Vec["sentiment_score"]; has {
		t.Fatalf("thin sentiment (n=2) must be ABSENT from the vector: %+v", feats[0].Vec)
	}
	p, _, err := st.LatestPrediction(ctx, sym.ID, md.H1d)
	if err != nil {
		t.Fatal(err)
	}
	if p.NUsed != 1 || p.RawProb != 0.5 {
		t.Fatalf("thin sentiment must not join the blend: raw=%v nUsed=%d", p.RawProb, p.NUsed)
	}
}
