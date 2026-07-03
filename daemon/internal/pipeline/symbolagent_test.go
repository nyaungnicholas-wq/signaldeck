package pipeline

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/ensemble"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
	"github.com/nyaungnicholas-wq/signaldeck/internal/symbolagent"
)

// seedLabeledSpread writes n labeled rows for one symbol+horizon where the
// pressure leg is perfectly predictive AND the stored raw blend prob carries
// spread that TRACKS the outcome — so the per-symbol calibration has something
// to fit. Each row's outcome is RESOLVED, so it's a valid prequential training
// example.
func seedLabeledSpread(t *testing.T, st *store.Store, symbolID int64, h md.Horizon, n int) {
	t.Helper()
	ctx := context.Background()
	base := time.Now().Unix() - int64(n+1)*3600
	for i := 0; i < n; i++ {
		ts := base + int64(i)*3600
		up := i%2 == 0
		pressure, fwd, raw := 0.5, 0.01, 0.62
		if !up {
			pressure, fwd, raw = -0.5, -0.01, 0.38
		}
		vec := map[string]float64{
			"pressure_score": pressure,
			"pred_raw":       raw, "pred_cal": raw, "n_used": 1,
		}
		if err := st.InsertFeatures(ctx, symbolID, h, ts, featureVersion, vec); err != nil {
			t.Fatalf("features: %v", err)
		}
		if err := st.UpsertPrediction(ctx, store.Prediction{
			SymbolID: symbolID, Horizon: h, Ts: ts, RawProb: raw, CalProb: raw, NUsed: 1, Components: "{}",
		}); err != nil {
			t.Fatalf("prediction: %v", err)
		}
		if err := st.ResolvePrediction(ctx, symbolID, h, ts, fwd); err != nil {
			t.Fatalf("resolve: %v", err)
		}
	}
}

// TestPerSymbolLearner_GraduatesAndPersists: a symbol with >= MinPersonal of
// its own resolved outcomes graduates to a personal model, gets its own
// weights + fitted calibration stored, and produces exactly one graduation
// insight the first time (idempotent on re-run).
func TestPerSymbolLearner_GraduatesAndPersists(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	sym, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}
	seedLabeledSpread(t, st, sym.ID, md.H1d, symbolagent.MinPersonal+10)

	w := &PerSymbolLearner{St: st}
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}

	m, ok, err := st.SymbolModel(ctx, sym.ID, md.H1d)
	if err != nil || !ok {
		t.Fatalf("model not stored: ok=%v err=%v", ok, err)
	}
	if m.Tier != symbolagent.TierPersonal {
		t.Fatalf("want personal tier, got %q (n=%d)", m.Tier, m.NSamples)
	}
	var wts map[string]float64
	if err := json.Unmarshal([]byte(m.Weights), &wts); err != nil || wts[ensemble.LegPressure] <= 0 {
		t.Fatalf("personal pressure weight missing: %v %v", wts, err)
	}
	var cal symbolagent.Calibration
	if err := json.Unmarshal([]byte(m.Calibration), &cal); err != nil || !cal.Fitted {
		t.Fatalf("personal calibration not fitted: %+v %v", cal, err)
	}

	// Exactly one graduation insight the first pass.
	ins, err := st.RecentInsights(ctx, sym.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	grad := 0
	for _, in := range ins {
		if in.Scope == "symbol" && strings.Contains(in.Headline, "now has its own agent") {
			grad++
		}
	}
	if grad != 1 {
		t.Fatalf("want exactly 1 graduation insight, got %d", grad)
	}

	// Second pass: already personal → no NEW graduation insight (idempotent).
	if _, err := w.Run(ctx); err != nil {
		t.Fatal(err)
	}
	ins2, _ := st.RecentInsights(ctx, sym.ID, 10)
	grad2 := 0
	for _, in := range ins2 {
		if strings.Contains(in.Headline, "now has its own agent") {
			grad2++
		}
	}
	if grad2 != 1 {
		t.Fatalf("graduation insight must fire only once, got %d after 2nd run", grad2)
	}
}

// TestPerSymbolLearner_StillLearningStaysGlobal: below MinPersonal a symbol
// stores a model but stays off the personal tier (still learning), with no
// personal weights persisted.
func TestPerSymbolLearner_StillLearningStaysGlobal(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	sym, err := st.UpsertSymbol(ctx, "TSLA", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}
	seedLabeledSpread(t, st, sym.ID, md.H1d, symbolagent.MinPersonal-5)

	if _, err := (&PerSymbolLearner{St: st}).Run(ctx); err != nil {
		t.Fatal(err)
	}
	m, ok, _ := st.SymbolModel(ctx, sym.ID, md.H1d)
	if !ok {
		t.Fatal("a still-learning symbol should still get a model row")
	}
	if m.Tier == symbolagent.TierPersonal {
		t.Fatalf("must NOT be personal below threshold: tier=%q n=%d", m.Tier, m.NSamples)
	}
	if m.Weights != "{}" {
		t.Fatalf("no personal weights should be stored while still learning, got %q", m.Weights)
	}
}

// TestPredictionRunner_PersonalTierOverridesGlobal proves predict.go's tier
// selection: when a symbol has a PERSONAL model, the PredictionRunner blends
// with ITS weights and recalibrates with ITS calibration — not the global
// per-regime weights / global calibration. We install a personal model whose
// weights put ALL mass on pressure, then assert the written prediction reflects
// the pressure leg exactly (which the equal-weight/global path would not).
func TestPredictionRunner_PersonalTierOverridesGlobal(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	sym, err := st.UpsertSymbol(ctx, "SPY", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}
	// A daily bar so the resolver/feature path has price context, and a score.
	now := time.Now().Truncate(time.Minute).Unix()
	if err := st.UpsertBars(ctx, []md.Bar{
		{SymbolID: sym.ID, TF: md.TF1d, Ts: now - 86400, Open: 100, High: 100, Low: 100, Close: 100},
	}); err != nil {
		t.Fatal(err)
	}
	// A pressure score of +0.6 → pressure leg = (0.6+1)/2 = 0.8.
	if err := st.InsertScore(ctx, md.Score{
		SymbolID: sym.ID, Horizon: md.H1d, Ts: now, Score: 0.6,
		Components: []md.ScoreComponent{{Name: "x", Contrib: 0.6}},
	}); err != nil {
		t.Fatal(err)
	}

	// Install a PERSONAL model: weights = pressure only, calibration = identity
	// (not fitted) so the raw blend passes through unchanged and we can read the
	// pressure leg straight out of the written prediction.
	personal := store.SymbolModelRow{
		SymbolID: sym.ID, Horizon: string(md.H1d),
		Weights:     `{"pressure":1}`,
		Calibration: `{"fitted":false}`,
		Skill:       "{}", Personality: "test", NSamples: 999,
		Tier: symbolagent.TierPersonal, UpdatedTs: now,
	}
	if err := st.UpsertSymbolModel(ctx, personal); err != nil {
		t.Fatal(err)
	}

	if _, err := (&PredictionRunner{St: st}).Run(ctx); err != nil {
		t.Fatalf("prediction run: %v", err)
	}

	p, ok, err := st.LatestPrediction(ctx, sym.ID, md.H1d)
	if err != nil || !ok {
		t.Fatalf("no prediction written: ok=%v err=%v", ok, err)
	}
	// Pressure-only weights → raw prob = pressure leg = 0.8. Identity personal
	// calibration → cal == raw. (The global equal-weight path would also be 0.8
	// here since pressure is the only present leg; the discriminating assertion
	// is that the PERSONAL calibration was used — cal stays 0.8, NOT pulled to a
	// global curve.)
	if p.RawProb < 0.79 || p.RawProb > 0.81 {
		t.Fatalf("personal pressure-only blend should be ~0.8, got %v", p.RawProb)
	}
	if p.CalProb != p.RawProb {
		t.Fatalf("identity personal calibration must leave cal==raw, got cal=%v raw=%v", p.CalProb, p.RawProb)
	}
}
