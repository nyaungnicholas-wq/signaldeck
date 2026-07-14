// ALPHAX-LEG wave tests: the pooled cross-sectional model joins the live
// ensemble as a GATED leg (separate file so parallel edits never collide).
//
// The leg's prob is RELATIVE (P(beat the same-day universe median)), blended
// into the directional ensemble as a tilt — these tests pin the gate
// semantics (lift<=0 dropped, >0 included, absent dropped), the gated
// feature-vector capture (alphax_prob), and the end-to-end PredictionRunner
// wiring from a stored model_forecasts row (model="alphax") into the blend.
package pipeline

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/ensemble"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// The ensemble must include the alphax leg ONLY with demonstrated OOS lift —
// the identical gate the forecast/gbm/meanrev legs pass through.
func TestEnsemble_AlphaXLegGate(t *testing.T) {
	base := ensemble.Components{PressureScore: 0.0} // pressure leg = 0.5
	pBase, nBase := ensemble.RawProbability(base)
	if nBase != 1 {
		t.Fatalf("base nUsed=%d, want 1", nBase)
	}

	// Bullish alphax leg WITH edge -> counted, blend moves up.
	ap, al := 0.9, 0.05
	with := base
	with.AlphaXProb, with.AlphaXLift = &ap, &al
	p, n := ensemble.RawProbability(with)
	if n != 2 {
		t.Fatalf("gated alphax leg should be counted, nUsed=%d", n)
	}
	if p <= pBase {
		t.Fatalf("bullish gated alphax leg should raise the blend: base=%.3f with=%.3f", pBase, p)
	}
	if legs := ensemble.LegProbabilities(with); legs[ensemble.LegAlphaX] != 0.9 {
		t.Fatalf("alphax leg prob = %v, want 0.9", legs[ensemble.LegAlphaX])
	}

	// Same prob but lift <= 0 -> dropped, blend unchanged.
	noLift := -0.01
	noEdge := base
	noEdge.AlphaXProb, noEdge.AlphaXLift = &ap, &noLift
	if p, n := ensemble.RawProbability(noEdge); n != 1 || p != pBase {
		t.Fatalf("edgeless alphax leg must be dropped: n=%d p=%.3f (base %.3f)", n, p, pBase)
	}

	// Prob without a lift (nil) -> dropped: no grade, no seat in the blend.
	nilLift := base
	nilLift.AlphaXProb = &ap
	if p, n := ensemble.RawProbability(nilLift); n != 1 || p != pBase {
		t.Fatalf("alphax leg with nil lift must be dropped: n=%d p=%.3f (base %.3f)", n, p, pBase)
	}
}

// buildFeatureVector must record alphax_prob ONLY when the leg cleared its
// gate — the same treatment gbm_prob/meanrev_prob get.
func TestBuildFeatureVector_AlphaXGated(t *testing.T) {
	ap, al := 0.67, 0.03
	c := ensemble.Components{PressureScore: 0.1, AlphaXProb: &ap, AlphaXLift: &al}
	vec := buildFeatureVector(md.Score{Score: 0.1}, c, 0.5, 0.5, 2, "", nil, 0)
	if vec["alphax_prob"] != 0.67 {
		t.Fatalf("alphax_prob = %v, want 0.67", vec["alphax_prob"])
	}

	// Edgeless -> ABSENT (an edgeless leg contributed nothing, so it isn't
	// recorded — absence is information).
	alNo := -0.02
	c.AlphaXLift = &alNo
	vec = buildFeatureVector(md.Score{Score: 0.1}, c, 0.5, 0.5, 1, "", nil, 0)
	if _, ok := vec["alphax_prob"]; ok {
		t.Fatal("an edgeless alphax leg must not appear in the feature vector")
	}
}

// End-to-end PredictionRunner wiring: a stored model_forecasts row
// (model="alphax", lift>0) must reach the blend — the persisted prediction's
// components carry AlphaXProb/AlphaXLift, nUsed counts the leg, and the
// persisted feature vector carries the gated alphax_prob.
func TestPredictionRunnerBlendsAlphaXLeg(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	sym, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	if err != nil {
		t.Fatal(err)
	}

	// Minimal prediction state (mirrors TestPredictionRunnerPersistsFeatures).
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
			Components: []md.ScoreComponent{{Name: "rsi", Contrib: 0.3}},
		}); err != nil {
			t.Fatal(err)
		}
	}
	// The alpha-trainer's live-shaped output: a per-symbol alphax score in
	// model_forecasts, present only because its OOS lift cleared the gate.
	for _, h := range predHorizons {
		if err := st.UpsertModelForecast(ctx, store.ModelForecast{
			SymbolID: sym.ID, Horizon: h, Model: store.ModelAlphaX, Ts: now,
			Prob: 0.9, Accuracy: 0.56, AUC: 0.58, BaseRate: 0.52, Lift: 0.04,
			NTrain: 2000, NEval: 400,
		}); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := (&PredictionRunner{St: st}).Run(ctx); err != nil {
		t.Fatal(err)
	}

	for _, h := range predHorizons {
		p, ok, err := st.LatestPrediction(ctx, sym.ID, h)
		if err != nil || !ok {
			t.Fatalf("%s: prediction missing: %v", h, err)
		}
		// The blend counted the leg (pressure + alphax = 2)…
		if p.NUsed != 2 {
			t.Fatalf("%s: nUsed = %d, want 2 (pressure + alphax)", h, p.NUsed)
		}
		// …and the persisted components carry the leg exactly as blended.
		var c ensemble.Components
		if err := json.Unmarshal([]byte(p.Components), &c); err != nil {
			t.Fatalf("%s: components: %v", h, err)
		}
		if c.AlphaXProb == nil || *c.AlphaXProb != 0.9 || c.AlphaXLift == nil || *c.AlphaXLift != 0.04 {
			t.Fatalf("%s: components missing the alphax leg: %s", h, p.Components)
		}
		// The bullish alphax leg must have pulled the blend above pressure alone.
		pressureOnly, _ := ensemble.RawProbability(ensemble.Components{PressureScore: 0.3})
		if p.RawProb <= pressureOnly {
			t.Fatalf("%s: raw %.3f should exceed pressure-only %.3f", h, p.RawProb, pressureOnly)
		}
		// The feature vector carries the gated alphax_prob at the new version.
		feats, err := st.FeaturesSince(ctx, sym.ID, h, 0, 0)
		if err != nil || len(feats) != 1 {
			t.Fatalf("%s: features: n=%d err=%v", h, len(feats), err)
		}
		if feats[0].Version != featureVersion {
			t.Fatalf("%s: feature version %d, want %d", h, feats[0].Version, featureVersion)
		}
		if got := feats[0].Vec["alphax_prob"]; got != 0.9 {
			t.Fatalf("%s: alphax_prob = %v, want 0.9 (vec %+v)", h, got, feats[0].Vec)
		}
	}
}
