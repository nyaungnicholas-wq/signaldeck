package pipeline

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// seedAlphaXPool seeds nSyms symbols × nDays UTC days of labeled feature rows
// (feature vector + resolved prediction outcome per (symbol, day)), newest day
// ending ~1 day ago so the latest vectors are inside the scorer's freshness
// window. fwd(symIdx, day) decides each row's forward return; x(symIdx) is the
// single informative feature. Rows alternate v3/v4 stamps to prove the pooled
// trainer accepts BOTH schema versions.
func seedAlphaXPool(t *testing.T, st *store.Store, nSyms, nDays int,
	fwd func(s, d int) float64, x func(s int) float64) []md.Symbol {
	t.Helper()
	ctx := context.Background()
	h := md.H1d
	base := time.Now().Add(-time.Duration(nDays) * 24 * time.Hour).Unix()
	syms := make([]md.Symbol, nSyms)
	for s := 0; s < nSyms; s++ {
		sym, err := st.UpsertSymbol(ctx, fmt.Sprintf("SYM%02d", s), md.Stocks, "")
		if err != nil {
			t.Fatal(err)
		}
		syms[s] = sym
	}
	for d := 0; d < nDays; d++ {
		for s := 0; s < nSyms; s++ {
			ts := base + int64(d)*86400 + int64(s)
			vec := map[string]float64{
				"pressure_score": 0.1,
				"x_alpha":        x(s),
				"pred_raw":       0.5, // must be excluded from training
				"pred_cal":       0.5,
			}
			version := 3 + (s % 2) // both v3 AND v4 rows enter the pool
			if err := st.InsertFeatures(ctx, syms[s].ID, h, ts, version, vec); err != nil {
				t.Fatal(err)
			}
			if err := st.UpsertPrediction(ctx, store.Prediction{
				SymbolID: syms[s].ID, Horizon: h, Ts: ts, RawProb: 0.5, CalProb: 0.5, NUsed: 1,
			}); err != nil {
				t.Fatal(err)
			}
			if err := st.ResolvePrediction(ctx, syms[s].ID, h, ts, fwd(s, d)); err != nil {
				t.Fatal(err)
			}
		}
	}
	return syms
}

// End-to-end LEARNABLE path: pooled v3+v4 rows across 30 symbols × 60 days
// where even symbols always beat the day median → the trainer must store an
// UNGATED grade with positive OOS lift AND per-symbol current scores under
// model="alphax", ranking even symbols above odd ones.
func TestAlphaXTrainer_LearnablePoolStoresGradeAndScores(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	syms := seedAlphaXPool(t, st, 30, 60,
		func(s, d int) float64 { // day-level market shift cancels in the label
			shift := 0.02 * float64(d%3)
			if s%2 == 0 {
				return shift + 0.01
			}
			return shift - 0.01
		},
		func(s int) float64 {
			if s%2 == 0 {
				return 1
			}
			return -1
		})

	detail, err := (&AlphaXTrainer{St: st}).Run(ctx)
	if err != nil {
		t.Fatalf("AlphaXTrainer.Run: %v", err)
	}
	if !strings.Contains(detail, "1d:") || !strings.Contains(detail, "scored") {
		t.Fatalf("detail should report the 1d grade + scored symbols, got %q", detail)
	}
	// 1w has no labeled rows — the detail must say so honestly.
	if !strings.Contains(detail, "1w: no labeled rows yet") {
		t.Fatalf("detail should state the 1w refusal, got %q", detail)
	}

	m, found, err := st.AlphaXModel(ctx, md.H1d)
	if err != nil || !found {
		t.Fatalf("alphax model row missing: found=%v err=%v", found, err)
	}
	if m.OOSLift <= 0 || m.Gated {
		t.Fatalf("learnable pool must grade ungated with positive lift, got lift=%.4f gated=%v", m.OOSLift, m.Gated)
	}
	if m.NTest < 200 || m.NTrain < 1200 {
		t.Fatalf("grade sample sizes implausible: nTrain=%d nTest=%d", m.NTrain, m.NTest)
	}
	if m.ModelJSON == "" || !strings.Contains(m.ModelJSON, "featureKeys") {
		t.Fatal("model_json must carry the serialized model + key order (provenance)")
	}
	if strings.Contains(m.ModelJSON, "pred_raw") {
		t.Fatal("model_json key order must not include pred_raw (self-reference exclusion)")
	}

	// Current scores stored for the fresh cross-section, relative ranking right.
	top, err := st.TopModelForecastsByModel(ctx, store.ModelAlphaX, md.H1d, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(top) == 0 {
		t.Fatal("ungated model must store per-symbol current scores")
	}
	evenBySym := map[string]bool{}
	for i, s := range syms {
		evenBySym[s.Symbol] = i%2 == 0
	}
	for _, r := range top[:5] {
		if !evenBySym[r.Symbol] {
			t.Fatalf("top scores should be the even (always-top-half) symbols, got %s in top-5", r.Symbol)
		}
	}
}

// GATED path: a pure-noise pool (constant feature) grades lift <= 0 → the
// model row is stored GATED, no scores are written, and any stale alphax
// scores from an earlier edged pass are DELETED.
func TestAlphaXTrainer_NoEdgeGatesAndClearsScores(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	syms := seedAlphaXPool(t, st, 25, 60,
		func(s, d int) float64 { // outcome depends only on parity…
			if s%2 == 0 {
				return 0.01
			}
			return -0.01
		},
		func(s int) float64 { return 1.0 }) // …but the feature is CONSTANT

	// A stale score from a hypothetical earlier edged pass.
	if err := st.UpsertModelForecast(ctx, store.ModelForecast{
		SymbolID: syms[0].ID, Horizon: md.H1d, Model: store.ModelAlphaX, Ts: 100,
		Prob: 0.9, Lift: 0.05,
	}); err != nil {
		t.Fatal(err)
	}

	detail, err := (&AlphaXTrainer{St: st}).Run(ctx)
	if err != nil {
		t.Fatalf("AlphaXTrainer.Run: %v", err)
	}
	if !strings.Contains(detail, "GATED") {
		t.Fatalf("detail should state the gate, got %q", detail)
	}

	m, found, err := st.AlphaXModel(ctx, md.H1d)
	if err != nil || !found {
		t.Fatalf("gated model row must still be stored (the record): found=%v err=%v", found, err)
	}
	if !m.Gated || m.OOSLift > 0 {
		t.Fatalf("constant-feature pool must gate: lift=%.4f gated=%v", m.OOSLift, m.Gated)
	}
	top, err := st.TopModelForecastsByModel(ctx, store.ModelAlphaX, md.H1d, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(top) != 0 {
		t.Fatalf("gated model must have NO stored scores (stale ones cleared), got %d", len(top))
	}
}

// Honest refusal: far too little pooled data → no alphax_models row at all,
// and the worker detail states the refusal reason.
func TestAlphaXTrainer_ThinPoolRefusesToGrade(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	seedAlphaXPool(t, st, 12, 8, // 96 rows — way below the 1000/200 floors
		func(s, d int) float64 { return 0.01 },
		func(s int) float64 { return 1 })

	detail, err := (&AlphaXTrainer{St: st}).Run(ctx)
	if err != nil {
		t.Fatalf("AlphaXTrainer.Run: %v", err)
	}
	if !strings.Contains(detail, "refused to grade") {
		t.Fatalf("detail should state the honest refusal, got %q", detail)
	}
	if _, found, _ := st.AlphaXModel(ctx, md.H1d); found {
		t.Fatal("a refused grade must not write an alphax_models row")
	}
}
