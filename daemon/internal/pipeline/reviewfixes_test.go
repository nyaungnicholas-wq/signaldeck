// Regression tests for the adversarial-review fixes that live at the
// pipeline layer: the horizon-aware embargo mapping (H1) and the freshness
// cap on consumed model_forecasts rows (M3). The alphax-internal geometry
// proofs live in internal/alphax; these pin the values the trainer/runner
// actually pass.
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

// H1 — the trainer must pass a label-span-aware embargo to alphax.Evaluate:
// 2 for 1d (1-day span + 1) and 8 for 1w (7-day span + 1). A fixed 2 let
// 1-week train labels be computed from closes inside the test block.
func TestAlphaXEmbargoDays_HorizonAware(t *testing.T) {
	if got := alphaXEmbargoDays(md.H1d); got != 2 {
		t.Fatalf("1d embargo = %d, want 2", got)
	}
	if got := alphaXEmbargoDays(md.H1w); got != 8 {
		t.Fatalf("1w embargo = %d, want 8 (7-day label span + 1)", got)
	}
}

// M3 — a stored model_forecasts row older than maxModelForecastAgeSecs must
// stop feeding the blend: before the cap, a prob computed from weeks-old bars
// re-entered every 10-minute prediction pass indefinitely (the v6
// featureVersion bump means universe symbols won't retrain for weeks).
func TestModelLegProbLift_FreshnessCap(t *testing.T) {
	now := int64(10_000_000)
	rows := []store.ModelForecast{
		{Horizon: md.H1d, Model: store.ModelGBM, Ts: now - maxModelForecastAgeSecs, Prob: 0.61, Lift: 0.04},
		{Horizon: md.H1d, Model: store.ModelAlphaX, Ts: now - maxModelForecastAgeSecs - 1, Prob: 0.7, Lift: 0.09},
		{Horizon: md.H1w, Model: store.ModelMeanRev, Ts: now - 30*86400, Prob: 0.55, Lift: 0.02},
	}

	// Exactly at the cap is still fresh (the trainer that wrote it may lag
	// the runner by the full window without being "stopped").
	if p, l, ok := modelLegProbLift(rows, md.H1d, store.ModelGBM, now); !ok || p != 0.61 || l != 0.04 {
		t.Fatalf("row aged exactly the cap must still feed the blend: p=%v l=%v ok=%v", p, l, ok)
	}
	// One second past the cap: excluded, even though its stored lift > 0.
	if _, _, ok := modelLegProbLift(rows, md.H1d, store.ModelAlphaX, now); ok {
		t.Fatal("row older than the freshness cap must NOT feed the blend")
	}
	// A weeks-old row (the v6-bump scenario) is likewise excluded.
	if _, _, ok := modelLegProbLift(rows, md.H1w, store.ModelMeanRev, now); ok {
		t.Fatal("a 30-day-old row must NOT feed the blend")
	}
	// Absent legs stay absent (unchanged semantics).
	if _, _, ok := modelLegProbLift(rows, md.H1w, store.ModelGBM, now); ok {
		t.Fatal("missing leg must report ok=false")
	}
}

// H2 (end-to-end) — the trainer's stored grade must count each resolved
// outcome ONCE. Hot symbols write ~144 intraday feature rows/day that all
// share one resolved fwd_return; pre-fix the pooled dataset (and so NTrain,
// and grade.N) counted every repeat (proven live: N=480 where 60 unique
// outcomes existed). Here 5 "hot" symbols write 3 rows/day: NTrain must equal
// the unique (symbol, day) count, and the detail must state the collapse.
func TestAlphaXTrainer_IntradayDuplicatesCollapse(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	const nSyms, nDays, nHot, repeats = 30, 60, 5, 3
	h := md.H1d
	base := time.Now().Add(-nDays * 24 * time.Hour).Unix()
	for s := 0; s < nSyms; s++ {
		sym, err := st.UpsertSymbol(ctx, fmt.Sprintf("DUP%02d", s), md.Stocks, "")
		if err != nil {
			t.Fatal(err)
		}
		x, fwdSign := -1.0, -1.0
		if s%2 == 0 {
			x, fwdSign = 1.0, 1.0
		}
		for d := 0; d < nDays; d++ {
			n := 1
			if s < nHot {
				n = repeats
			}
			fwd := fwdSign*0.01 + 0.02*float64(d%3) // one outcome per (sym, day)
			for r := 0; r < n; r++ {
				ts := base + int64(d)*86400 + int64(600*r+s)
				vec := map[string]float64{"pressure_score": 0.1, "x_alpha": x}
				if err := st.InsertFeatures(ctx, sym.ID, h, ts, 3, vec); err != nil {
					t.Fatal(err)
				}
				if err := st.UpsertPrediction(ctx, store.Prediction{
					SymbolID: sym.ID, Horizon: h, Ts: ts, RawProb: 0.5, CalProb: 0.5, NUsed: 1,
				}); err != nil {
					t.Fatal(err)
				}
				if err := st.ResolvePrediction(ctx, sym.ID, h, ts, fwd); err != nil {
					t.Fatal(err)
				}
			}
		}
	}

	detail, err := (&AlphaXTrainer{St: st}).Run(ctx)
	if err != nil {
		t.Fatalf("AlphaXTrainer.Run: %v", err)
	}
	m, found, err := st.AlphaXModel(ctx, h)
	if err != nil || !found {
		t.Fatalf("alphax model row missing: found=%v err=%v", found, err)
	}
	if m.NTrain != nSyms*nDays {
		t.Fatalf("NTrain = %d, want %d unique (symbol,day) outcomes — duplicates inflated the grade",
			m.NTrain, nSyms*nDays)
	}
	wantDups := fmt.Sprintf("%d intraday duplicate(s) collapsed", nHot*nDays*(repeats-1))
	if !strings.Contains(detail, wantDups) {
		t.Fatalf("detail must account for the collapse (%s), got %q", wantDups, detail)
	}
}
