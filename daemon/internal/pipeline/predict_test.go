package pipeline

import (
	"context"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/ensemble"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// ── storage-permanence wave: feature capture at prediction time ─────────

func TestBuildFeatureVector(t *testing.T) {
	hr, fp, fl, pct := 0.62, 0.71, 0.05, 83.0
	sc := md.Score{
		Score: 0.4,
		Components: []md.ScoreComponent{
			// Weights are stated because production always sets them
			// (assemble renormalizes to sum 1); only the informational
			// vol_regime component carries Weight 0. Verified on the live
			// scores table: every other component's weight is > 0.
			{Name: "rsi", Norm: 0.3, Weight: 1.0 / 3, Contrib: 0.1},
			{Name: "trend_sma", Norm: 0.45, Weight: 2.0 / 3, Contrib: 0.3},
		},
	}
	sent := 0.3
	c := ensemble.Components{
		PressureScore:     0.4,
		ExpectancyHitRate: &hr,
		ForecastProb:      &fp,
		ForecastLift:      &fl,
		SentimentScore:    &sent,
	}
	vec := buildFeatureVector(sc, c, 0.66, 0.61, 3, "uptrend", &pct, 5)

	want := map[string]float64{
		"pressure_score":      0.4,
		"comp_rsi":            0.1,
		"comp_trend_sma":      0.3,
		"expectancy_hit_rate": 0.62,
		"forecast_prob":       0.71,
		"forecast_lift":       0.05,
		"sentiment_score":     0.3,
		"sentiment_n":         5,
		"regime_uptrend":      1,
		"rank_pct":            83,
		"pred_raw":            0.66,
		"pred_cal":            0.61,
		"n_used":              3,
	}
	if len(vec) != len(want) {
		t.Fatalf("vec has %d keys, want %d: %+v", len(vec), len(want), vec)
	}
	for k, v := range want {
		if vec[k] != v {
			t.Fatalf("vec[%q] = %v, want %v", k, vec[k], v)
		}
	}

	// Optional signals absent → keys absent (absence is information).
	vec = buildFeatureVector(md.Score{Score: -0.2}, ensemble.Components{PressureScore: -0.2}, 0.4, 0.4, 1, "", nil, 0)
	for _, k := range []string{"expectancy_hit_rate", "forecast_prob", "forecast_lift", "rank_pct", "sentiment_score", "sentiment_n"} {
		if _, ok := vec[k]; ok {
			t.Fatalf("absent signal %q must not appear in the vector", k)
		}
	}
	for k := range vec {
		if len(k) > 7 && k[:7] == "regime_" {
			t.Fatalf("no regime known, yet %q is set", k)
		}
	}
}

// The PredictionRunner must persist the exact input vector alongside every
// prediction it writes — the feature store is fed at prediction time, never
// reconstructed later.
func TestPredictionRunnerPersistsFeatures(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	sym, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	if err != nil {
		t.Fatal(err)
	}

	// Minimal state: daily bars + a current score per predicted horizon +
	// regime + ranking so those features have values to capture.
	now := time.Now().Unix()
	var bars []md.Bar
	for i := int64(0); i < 30; i++ {
		bars = append(bars, md.Bar{
			SymbolID: sym.ID, TF: md.TF1d, Ts: now - (30-i)*86400,
			Open: 100, High: 101, Low: 99, Close: 100 + float64(i), Volume: 1,
		})
	}
	if err := st.UpsertBars(ctx, bars); err != nil {
		t.Fatal(err)
	}
	for _, h := range predHorizons {
		if err := st.InsertScore(ctx, md.Score{
			SymbolID: sym.ID, Horizon: h, Ts: now - 60, Score: 0.3,
			Components: []md.ScoreComponent{{Name: "rsi", Norm: 0.3, Weight: 1, Contrib: 0.3}},
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.UpsertRegime(ctx, sym.ID, now, "uptrend", 0.8, ""); err != nil {
		t.Fatal(err)
	}
	if err := st.ReplaceRanking(ctx, now, []struct {
		SymbolID     int64
		Score        float64
		Rank         int
		Ret1M, Ret3M float64
	}{{SymbolID: sym.ID, Score: 91, Rank: 1}}); err != nil {
		t.Fatal(err)
	}

	if _, err := (&PredictionRunner{St: st}).Run(ctx); err != nil {
		t.Fatal(err)
	}

	for _, h := range predHorizons {
		// The prediction itself was written…
		p, ok, err := st.LatestPrediction(ctx, sym.ID, h)
		if err != nil || !ok {
			t.Fatalf("%s: prediction missing: %v", h, err)
		}
		// …and so was its feature vector, at the same ts and version.
		feats, err := st.FeaturesSince(ctx, sym.ID, h, 0, 0)
		if err != nil {
			t.Fatalf("%s: features: %v", h, err)
		}
		if len(feats) != 1 {
			t.Fatalf("%s: want 1 feature row, got %d", h, len(feats))
		}
		f := feats[0]
		if f.Ts != p.Ts || f.Version != featureVersion {
			t.Fatalf("%s: feature row keyed (%d,v%d), prediction at %d", h, f.Ts, f.Version, p.Ts)
		}
		for _, k := range []string{"pressure_score", "comp_rsi", "regime_uptrend", "rank_pct", "pred_raw", "pred_cal"} {
			if _, okK := f.Vec[k]; !okK {
				t.Fatalf("%s: vector missing %q: %+v", h, k, f.Vec)
			}
		}
		if f.Vec["pred_raw"] != p.RawProb || f.Vec["pred_cal"] != p.CalProb {
			t.Fatalf("%s: vector probs (%v,%v) != prediction (%v,%v)",
				h, f.Vec["pred_raw"], f.Vec["pred_cal"], p.RawProb, p.CalProb)
		}
		if f.Vec["rank_pct"] != 91 {
			t.Fatalf("%s: rank_pct = %v, want 91", h, f.Vec["rank_pct"])
		}
	}
}

// STAGE 3: the PredictionRunner must also commit each prediction to the
// append-only hash-chained ledger — one entry per (symbol,horizon) prediction,
// whose feature_hash reproduces the sha256 of the SAME persisted feature
// vector, and the whole chain must verify intact.
func TestPredictionRunnerAppendsLedger(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	sym, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	if err != nil {
		t.Fatal(err)
	}
	// Promote to the STREAMED hot set so the predictor runs it EVERY pass (the
	// broad daily-only universe is predicted once per UTC day, which would make
	// the "grows on re-run" assertion below flaky).
	if err := st.SetSymbolStream(ctx, sym.ID, true); err != nil {
		t.Fatal(err)
	}

	now := time.Now().Unix()
	var bars []md.Bar
	for i := int64(0); i < 30; i++ {
		bars = append(bars, md.Bar{
			SymbolID: sym.ID, TF: md.TF1d, Ts: now - (30-i)*86400,
			Open: 100, High: 101, Low: 99, Close: 100 + float64(i), Volume: 1,
		})
	}
	if err := st.UpsertBars(ctx, bars); err != nil {
		t.Fatal(err)
	}
	for _, h := range predHorizons {
		if err := st.InsertScore(ctx, md.Score{
			SymbolID: sym.ID, Horizon: h, Ts: now - 60, Score: 0.3,
			Components: []md.ScoreComponent{{Name: "rsi", Norm: 0.3, Weight: 1, Contrib: 0.3}},
		}); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := (&PredictionRunner{St: st}).Run(ctx); err != nil {
		t.Fatal(err)
	}

	// Ledger has exactly one entry per predicted horizon for this symbol, each
	// linked to the persisted feature vector by hash.
	for _, h := range predHorizons {
		entries, err := st.LedgerFor(ctx, sym.ID, h, 100)
		if err != nil {
			t.Fatalf("%s: ledger: %v", h, err)
		}
		if len(entries) != 1 {
			t.Fatalf("%s: want 1 ledger entry, got %d", h, len(entries))
		}
		e := entries[0]
		if e.ModelVersion != ledgerModelVersion {
			t.Errorf("%s: model_version = %d, want %d", h, e.ModelVersion, ledgerModelVersion)
		}
		// The committed feature_hash must equal the hash of the persisted vector.
		feats, err := st.FeaturesSince(ctx, sym.ID, h, 0, 0)
		if err != nil || len(feats) != 1 {
			t.Fatalf("%s: features: n=%d err=%v", h, len(feats), err)
		}
		if want := store.HashFeatureVector(feats[0].Vec); e.FeatureHash != want {
			t.Errorf("%s: ledger feature_hash %q != hash of persisted vector %q", h, e.FeatureHash, want)
		}
		// The committed probabilities match the prediction.
		p, ok, err := st.LatestPrediction(ctx, sym.ID, h)
		if err != nil || !ok {
			t.Fatalf("%s: prediction missing", h)
		}
		if e.RawProb != p.RawProb || e.CalProb != p.CalProb || e.BarTs != p.Ts {
			t.Errorf("%s: ledger (%v,%v,bar %d) != prediction (%v,%v,ts %d)",
				h, e.RawProb, e.CalProb, e.BarTs, p.RawProb, p.CalProb, p.Ts)
		}
	}

	// The full chain (both horizons) verifies intact.
	v, err := st.VerifyLedger(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !v.Intact || v.Count != int64(len(predHorizons)) {
		t.Errorf("verify: intact=%v count=%d, want true/%d", v.Intact, v.Count, len(predHorizons))
	}

	// Running the predictor AGAIN appends new entries (append-only) without
	// breaking the chain — the ledger grows, never rewrites.
	if _, err := (&PredictionRunner{St: st}).Run(ctx); err != nil {
		t.Fatal(err)
	}
	v2, err := st.VerifyLedger(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !v2.Intact {
		t.Error("chain broke after a second predictor pass")
	}
	if v2.Count <= v.Count {
		t.Errorf("ledger did not grow on re-run: %d then %d", v.Count, v2.Count)
	}
}

// A8 — a weight-0 component's Contrib is ALGEBRAICALLY zero (Contrib = Norm ×
// Weight), so storing it makes the column a constant across every row ever
// written. The derived "__has" bit is then the only varying signal from that
// component, and it encodes "enough history exists to compute it" — a
// data-completeness proxy a tree can split on to learn recency, not a market
// state. Informational components must therefore contribute their READING
// (comp_<name>_value), never their contribution.
func TestInformationalComponentStoresItsReadingNotAConstantContrib(t *testing.T) {
	sc := md.Score{
		Score: 0.3,
		Components: []md.ScoreComponent{
			{Name: "rsi", Norm: 0.5, Weight: 0.6, Contrib: 0.3},
			// vol_regime as signals.volRegimeComponent actually emits it.
			{Name: "vol_regime", Value: 83, Norm: 0, Weight: 0, Contrib: 0},
		},
	}
	vec := buildFeatureVector(sc, ensemble.Components{PressureScore: 0.3}, 0.5, 0.5, 1, "", nil, 0)

	if _, ok := vec["comp_vol_regime"]; ok {
		t.Fatalf("comp_vol_regime stored the always-zero Contrib: %v", vec["comp_vol_regime"])
	}
	if got, ok := vec["comp_vol_regime_value"]; !ok || got != 83 {
		t.Fatalf("comp_vol_regime_value = %v (present=%v), want 83", got, ok)
	}
	if got := vec["comp_rsi"]; got != 0.3 {
		t.Fatalf("weighted component must keep its Contrib: comp_rsi = %v, want 0.3", got)
	}
}
