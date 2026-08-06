package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// barBase is a Thursday-ish anchor; the fixture spaces bars exactly one
// calendar day apart so the resolver rule (target = base.Ts + 86400, then the
// first bar at/after it) lands cleanly on the next bar.
const barBase = int64(1780000000)

// seedExpectancyFixture writes 21 daily bars and 20 EVIDENCE rows (NUsed 0) for
// one symbol. The leg reads high exactly when the next close falls, except on
// the very last day — a PERFECT inversion grades AUC 0.0, which RankEdge and
// FleetLegAUC both refuse as a degenerate artifact, so the fixture has to be a
// shape that can actually occur.
func seedExpectancyFixture(t *testing.T, st *store.Store, ctx context.Context, name string) md.Symbol {
	t.Helper()
	sym, err := st.UpsertSymbol(ctx, name, md.Stocks, "")
	if err != nil {
		t.Fatalf("UpsertSymbol: %v", err)
	}
	closes := make([]float64, 21)
	closes[0] = 100
	for i := 0; i < 20; i++ {
		up := i < 10 || i == 19
		if up {
			closes[i+1] = closes[i] * 1.01
		} else {
			closes[i+1] = closes[i] * 0.99
		}
	}
	bars := make([]md.Bar, 0, 21)
	for i, c := range closes {
		ts := barBase + int64(i)*86400
		bars = append(bars, md.Bar{SymbolID: sym.ID, TF: md.TF1d, Ts: ts,
			Open: c, High: c, Low: c, Close: c, Volume: 1000})
	}
	if err := st.UpsertBars(ctx, bars); err != nil {
		t.Fatalf("UpsertBars: %v", err)
	}
	for i := 0; i < 20; i++ {
		hitRate := 0.30 + 0.02*float64(i) // rises as the forward move turns down
		comps, err := json.Marshal(map[string]float64{"ExpectancyHitRate": hitRate})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		// NUsed 0 — an EVIDENCE row. No prediction_outcomes row is created, so
		// this is only gradable because the trainer labels it from bars.
		if err := st.UpsertPrediction(ctx, store.Prediction{
			SymbolID: sym.ID, Horizon: md.H1d, Ts: barBase + int64(i)*86400 + 12*3600,
			RawProb: 0.5, CalProb: 0.5, NUsed: 0, Components: string(comps),
		}); err != nil {
			t.Fatalf("UpsertPrediction: %v", err)
		}
	}
	return sym
}

// The point of the whole change: a leg is graded from rows that carry NO
// outcome. If this ever regresses to joining prediction_outcomes, a benched leg
// stops being measured on the symbols it was benched for, and the gate seals
// itself shut.
func TestExpectancyTrainerGradesEvidenceRowsThatHaveNoOutcome(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	var syms []md.Symbol
	for i := 0; i < 3; i++ {
		syms = append(syms, seedExpectancyFixture(t, st, ctx, fmt.Sprintf("SYM%d", i)))
	}

	// Nothing seeded an outcome row; confirm that before grading, so a future
	// change to UpsertPrediction cannot quietly make this test pass for the
	// wrong reason.
	if _, ups, _, err := st.ResolvedRawPredictionPairs(ctx, md.H1d, 100); err != nil {
		t.Fatalf("ResolvedRawPredictionPairs: %v", err)
	} else if len(ups) != 0 {
		t.Fatalf("fixture created %d outcome rows; evidence rows must create none", len(ups))
	}

	if _, err := (&ExpectancyTrainer{St: st}).Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}

	aucMap, err := st.FleetLegAUC(ctx)
	if err != nil {
		t.Fatalf("FleetLegAUC: %v", err)
	}
	key := expectancyHorizonKey(md.H1d)
	auc, ok := aucMap[key]
	if !ok {
		t.Fatalf("FleetLegAUC missing %q — evidence rows were not graded: %v", key, aucMap)
	}
	if auc >= 0.5 {
		t.Fatalf("FleetLegAUC[%s] = %f; a leg that reads high before every DOWN move must grade below chance", key, auc)
	}

	rows, err := st.ModelForecasts(ctx, syms[0].ID)
	if err != nil {
		t.Fatalf("ModelForecasts: %v", err)
	}
	found := false
	for _, f := range rows {
		if f.Model == store.ModelExpectancy && f.Horizon == md.H1d {
			found = true
			if f.NEval != 20 {
				t.Errorf("NEval = %d, want 20 — the row stores this SYMBOL's contribution, not the fleet total", f.NEval)
			}
			if f.AUC != auc {
				t.Errorf("row AUC = %f, want the fleet estimate %f", f.AUC, auc)
			}
		}
	}
	if !found {
		t.Fatalf("no expectancy row written for %d: %+v", syms[0].ID, rows)
	}
}

func TestExpectancyTrainerSkipsSymbolsBelowTheContributionFloor(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	sym, err := st.UpsertSymbol(ctx, "THIN", md.Stocks, "")
	if err != nil {
		t.Fatalf("UpsertSymbol: %v", err)
	}
	var bars []md.Bar
	for i := 0; i < 8; i++ {
		c := 100 + float64(i)
		bars = append(bars, md.Bar{SymbolID: sym.ID, TF: md.TF1d, Ts: barBase + int64(i)*86400,
			Open: c, High: c, Low: c, Close: c, Volume: 1000})
	}
	if err := st.UpsertBars(ctx, bars); err != nil {
		t.Fatalf("UpsertBars: %v", err)
	}
	for i := 0; i < 5; i++ { // fewer than expectancyMinPairs
		comps, _ := json.Marshal(map[string]float64{"ExpectancyHitRate": 0.5})
		if err := st.UpsertPrediction(ctx, store.Prediction{
			SymbolID: sym.ID, Horizon: md.H1d, Ts: barBase + int64(i)*86400 + 12*3600,
			RawProb: 0.5, CalProb: 0.5, NUsed: 0, Components: string(comps),
		}); err != nil {
			t.Fatalf("UpsertPrediction: %v", err)
		}
	}
	if _, err := (&ExpectancyTrainer{St: st}).Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	rows, err := st.ModelForecasts(ctx, sym.ID)
	if err != nil {
		t.Fatalf("ModelForecasts: %v", err)
	}
	for _, f := range rows {
		if f.Model == store.ModelExpectancy {
			t.Fatalf("a symbol too thin to estimate must not be written: %+v", f)
		}
	}
}

// labelFromBars must reproduce PredictionResolver.Run, including its refusal to
// label across a gap. A silently-different definition here would put two label
// vintages inside one AUC.
func TestLabelFromBarsMatchesTheResolverRule(t *testing.T) {
	mk := func(ts int64, c float64) md.Bar {
		return md.Bar{TF: md.TF1d, Ts: ts, Open: c, High: c, Low: c, Close: c}
	}
	bars := []md.Bar{
		mk(barBase, 100),
		mk(barBase+86400, 101),   // up
		mk(barBase+2*86400, 99),  // down
		mk(barBase+30*86400, 90), // a 28-day hole: beyond 3*horizon, unlabelable
	}
	tss := []int64{barBase + 3600, barBase + 86400 + 3600, barBase + 2*86400 + 3600}
	vals := []float64{0.1, 0.2, 0.3}
	gotV, gotU := labelFromBars(tss, vals, bars, 86400)
	if len(gotV) != 2 {
		t.Fatalf("labeled %d rows, want 2 (the third has no forward bar within 3 horizons): %v", len(gotV), gotV)
	}
	if gotU[0] != 1 {
		t.Errorf("100 -> 101 must label up, got %v", gotU[0])
	}
	if gotU[1] != 0 {
		t.Errorf("101 -> 99 must label down, got %v", gotU[1])
	}
	if gotV[0] != 0.1 || gotV[1] != 0.2 {
		t.Errorf("leg values must stay paired with their own row, got %v", gotV)
	}
}
