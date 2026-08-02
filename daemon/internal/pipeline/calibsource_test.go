// Regression for the 2026-07-26 hostile review, finding C3 (second and third
// defects): the GLOBAL fallback calibration was FIT ON cal_prob and APPLIED TO
// raw, and each day's calibrated output trained the next day's map.
//
// The mechanism, verified in the live schema: UpsertPrediction seeds
// prediction_outcomes.prob with p.CalProb (store/predict.go), and
// ResolvedPredictionPairs selects that column — so the pairs handed to
// ensemble.Calibrate carried Pred=cal_prob while the fitted map was then
// evaluated at raw. A recalibration map is only meaningful on the variable it
// was fit against; fitting on one and applying to another is a coordinate
// error, and because cal_prob is itself the map's own output from the previous
// pass, the map trained on its own history — recursive, not out-of-sample.
//
// (The PER-SYMBOL path was already correct: pipeline/symbolagent.go builds its
// pairs from the stored pred_raw feature. Only the global fallback was wrong.)
package pipeline

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/ensemble"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// seedCalibrationHistory writes resolved predictions whose raw_prob and
// cal_prob are DELIBERATELY different, so a map fit on one is distinguishable
// from a map fit on the other.
func seedCalibrationHistory(t *testing.T, st *store.Store, h md.Horizon) {
	t.Helper()
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "CALTEST", md.Stocks, "Calibration Fixture")
	if err != nil {
		t.Fatal(err)
	}
	// raw sweeps 0.20..0.80; cal_prob is the MIRROR (1-raw), the sharpest
	// possible way to tell which variable a map was fit against: a map fit on
	// raw leans up with raw, a map fit on cal_prob leans down with raw.
	for i := 0; i < 200; i++ {
		raw := 0.20 + 0.60*float64(i)/199
		cal := 1 - raw
		// Outcome tracks RAW: high raw resolves up. A correctly-sourced map
		// therefore has to come out increasing in raw.
		fwd := -1.0
		if raw > 0.5 {
			fwd = 1.0
		}
		ts := int64(1_700_000_000 + i*86400)
		if err := st.UpsertPrediction(ctx, store.Prediction{
			SymbolID: sym.ID, Horizon: h, Ts: ts,
			RawProb: raw, CalProb: cal, NUsed: 4, Components: "{}",
		}); err != nil {
			t.Fatal(err)
		}
		if err := st.ResolvePrediction(ctx, sym.ID, h, ts, fwd); err != nil {
			t.Fatal(err)
		}
	}
}

// The live map must be fit on the SAME variable it is applied to. It is
// applied to raw, so it must be fit on raw_prob — never on cal_prob (which is
// its own previous output, making the fit recursive rather than out-of-sample).
func TestGlobalCalibrationIsFitOnRawNotCalProb(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "calibsource.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close() //nolint:errcheck
	seedCalibrationHistory(t, st, md.H1d)

	fn, ok, err := globalCalibration(ctx, st, md.H1d)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected the global calibration to fit on 200 resolved pairs")
	}
	// Outcomes track raw, so a map fit on raw is INCREASING in raw. A map fit
	// on cal_prob (= 1-raw) is decreasing in raw — the shipped behaviour.
	lo, hi := fn(0.25), fn(0.75)
	if lo >= hi {
		t.Fatalf("calibration map is inverted in raw (fn(0.25)=%.4f >= fn(0.75)=%.4f) — "+
			"it was fit on cal_prob and applied to raw", lo, hi)
	}

	// And it must be the fit over the RAW pairs — the property this test exists
	// to protect. It used to assert exact equality with ensemble.Calibrate,
	// which pinned one implementation rather than the property: globalCalibration
	// now uses CalibrateRanking (isotonic is only weakly monotone and collapsed
	// distinct per-symbol probabilities to one value), so equality with the
	// isotonic map is no longer the contract. Reproduce the same construction
	// the caller uses and require the same map.
	raws, ups, err := st.ResolvedRawPredictionPairs(ctx, md.H1d, calibrationPairLimit)
	if err != nil {
		t.Fatal(err)
	}
	pairs := make([]ensemble.Pair, len(raws))
	for i := range raws {
		src := len(raws) - 1 - i // chronological, matching globalCalibration
		pairs[i] = ensemble.Pair{Pred: raws[src], Actual: ups[src], Ts: int64(i)}
	}
	want, wok, _ := ensemble.CalibrateRanking(pairs)
	if !wok {
		t.Fatal("reference fit on raw pairs failed")
	}
	for _, x := range []float64{0.1, 0.3, 0.5, 0.7, 0.9} {
		if got, exp := fn(x), want(x); got != exp {
			t.Errorf("fn(%.2f)=%v, want %v (map must be the raw-pair fit)", x, got, exp)
		}
	}

	// Whichever map won, it must never REORDER its inputs — a more bullish raw
	// score publishing a lower probability breaks every ranking keyed on the
	// result. Ties are permitted here (isotonic may legitimately win and tie);
	// inversions never are.
	prev := -1.0
	for i := 0; i <= 100; i++ {
		v := fn(float64(i) / 100)
		if v < prev {
			t.Fatalf("calibration inverted at raw=%.2f: %.6f < %.6f", float64(i)/100, v, prev)
		}
		prev = v
	}
}

// ResolvedRawPredictionPairs must return raw_prob, not the calibrated column.
// This is the query-level guard: prediction_outcomes.prob holds cal_prob, so a
// join that reads the wrong column silently reintroduces the recursion.
func TestResolvedRawPredictionPairsReturnsRawProb(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "rawpairs.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close() //nolint:errcheck
	seedCalibrationHistory(t, st, md.H1d)

	raws, ups, err := st.ResolvedRawPredictionPairs(ctx, md.H1d, 500)
	if err != nil {
		t.Fatal(err)
	}
	if len(raws) != 200 || len(ups) != 200 {
		t.Fatalf("got %d raws / %d ups, want 200 each", len(raws), len(ups))
	}
	// Every seeded row has cal_prob = 1-raw_prob and outcome up iff raw>0.5.
	for i := range raws {
		wantUp := 0.0
		if raws[i] > 0.5 {
			wantUp = 1
		}
		if ups[i] != wantUp {
			t.Fatalf("row %d: raw=%.4f up=%v want %v — the join paired the wrong outcome",
				i, raws[i], ups[i], wantUp)
		}
	}
	// Unresolved rows must never appear (training strictly on resolved
	// outcomes is what keeps the fit out-of-sample).
	sym, err := st.UpsertSymbol(ctx, "CALTEST", md.Stocks, "Calibration Fixture")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertPrediction(ctx, store.Prediction{
		SymbolID: sym.ID, Horizon: md.H1d, Ts: 1_900_000_000,
		RawProb: 0.99, CalProb: 0.01, NUsed: 4, Components: "{}",
	}); err != nil {
		t.Fatal(err)
	}
	raws2, _, err := st.ResolvedRawPredictionPairs(ctx, md.H1d, 500)
	if err != nil {
		t.Fatal(err)
	}
	if len(raws2) != 200 {
		t.Fatalf("unresolved prediction leaked into the training set: %d pairs", len(raws2))
	}
}
