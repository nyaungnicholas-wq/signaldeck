package store

import (
	"context"
	"math"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/structregime"
)

// The property these guard: a structural predictor's accuracy is meaningless
// without the persistence base rate beside it. "83% correct" is impressive only
// if regimes persist less than 83% of the time on their own — and for trend
// forecasts they nearly always persist more than people expect.

func newStructStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(t.TempDir() + "/s.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// seed writes one resolved structural outcome.
func seed(t *testing.T, st *Store, symID int64, kind string, ts int64,
	regime, actual string, conviction, claimed float64) {
	t.Helper()
	ctx := context.Background()
	correct := 0
	if regime == actual {
		correct = 1
	}
	_, err := st.w.ExecContext(ctx, `
		INSERT INTO regime_outcomes
		  (symbol_id, kind, ts, day, horizon_days, regime, conviction,
		   historical_accuracy, rank, resolved_at, actual, correct)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		symID, kind, ts, ts/86400, 21, regime, conviction, claimed, conviction,
		ts+21*86400, actual, correct)
	if err != nil {
		t.Fatal(err)
	}
}

func TestStructuralRecordMeasuresAccuracyAndClaim(t *testing.T) {
	ctx := context.Background()
	st := newStructStore(t)
	sym, _ := st.UpsertSymbol(ctx, "AAA", md.Stocks, "")

	// 8 of 10 correct, each shipping a 90% claim — so the live record UNDERSHOOTS
	// what was advertised, which is exactly the drift this is built to surface.
	for i := 0; i < 10; i++ {
		actual := "uptrend"
		if i >= 8 {
			actual = "downtrend"
		}
		seed(t, st, sym.ID, "trend21", int64(i+1)*86400, "uptrend", actual, 0.95, 0.90)
	}

	recs, err := st.StructuralRecords(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 {
		t.Fatalf("want 1 kind, got %d", len(recs))
	}
	r := recs[0]
	if r.N != 10 || r.Correct != 8 {
		t.Fatalf("n/correct = %d/%d, want 10/8", r.N, r.Correct)
	}
	if math.Abs(r.Accuracy-0.8) > 1e-9 {
		t.Fatalf("accuracy %.3f, want 0.800", r.Accuracy)
	}
	if math.Abs(r.ClaimedAccuracy-0.90) > 1e-9 {
		t.Fatalf("claimed %.3f, want 0.900 — the shipped number must be carried "+
			"alongside the realized one", r.ClaimedAccuracy)
	}
}

// THE test. A predictor that always says "it persists" scores the persistence
// base rate and has no edge, however high its raw accuracy looks.
func TestPersistenceBaseRateExposesAZeroEdgePredictor(t *testing.T) {
	ctx := context.Background()
	st := newStructStore(t)
	sym, _ := st.UpsertSymbol(ctx, "AAA", md.Stocks, "")

	// 9 of 10 regimes persist. The predictor calls "uptrend" every time, so it
	// scores 90% — and contributes nothing, because the base rate is also 90%.
	for i := 0; i < 10; i++ {
		actual := "uptrend"
		if i == 9 {
			actual = "downtrend"
		}
		seed(t, st, sym.ID, "trend21", int64(i+1)*86400, "uptrend", actual, 0.8, 0.85)
	}
	r, err := st.StructuralRecords(ctx, 0)
	if err != nil || len(r) != 1 {
		t.Fatalf("records: %v %v", r, err)
	}
	if math.Abs(r[0].Accuracy-0.9) > 1e-9 {
		t.Fatalf("accuracy %.3f, want 0.900", r[0].Accuracy)
	}
	if math.Abs(r[0].PersistenceBase-0.9) > 1e-9 {
		t.Fatalf("persistence base %.3f, want 0.900", r[0].PersistenceBase)
	}
	if edge := r[0].Accuracy - r[0].PersistenceBase; math.Abs(edge) > 1e-9 {
		t.Fatalf("a predictor that only echoes persistence must show ZERO edge, "+
			"got %+.3f", edge)
	}
}

func TestIndependenceIsEnforcedBySchemaNotJustQuery(t *testing.T) {
	ctx := context.Background()
	st := newStructStore(t)
	sym, _ := st.UpsertSymbol(ctx, "AAA", md.Stocks, "")
	day := int64(100) * 86400
	seed(t, st, sym.ID, "trend21", day+100, "uptrend", "uptrend", 0.8, 0.9)

	// Pseudo-replication is impossible at WRITE time: a UNIQUE index on
	// (symbol_id, kind, day) rejects a second forecast for the same symbol and
	// day, so many intraday rows can never resolve against one forward move and
	// be counted as independent evidence. That is a stronger guarantee than
	// deduping at read time, and this asserts the constraint is really there.
	_, err := st.w.ExecContext(ctx, `
		INSERT INTO regime_outcomes
		  (symbol_id, kind, ts, day, horizon_days, regime, conviction,
		   historical_accuracy, rank, resolved_at, actual, correct)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		sym.ID, "trend21", day+200, day/86400, 21, "uptrend", 0.8, 0.9, 0.8,
		day+21*86400, "uptrend", 1)
	if err == nil {
		t.Fatal("a second same-day forecast must be rejected by the unique index — " +
			"without it, intraday rows could inflate the evidence count")
	}

	r, _ := st.StructuralRecords(ctx, 0)
	if len(r) != 1 || r[0].N != 1 {
		t.Fatalf("want exactly 1 independent observation, got %+v", r)
	}
}

func TestConvictionFilterSelectsTheActionableTier(t *testing.T) {
	ctx := context.Background()
	st := newStructStore(t)
	sym, _ := st.UpsertSymbol(ctx, "AAA", md.Stocks, "")
	// Low-conviction calls are wrong, high-conviction ones right — the spread
	// that IS the product.
	for i := 0; i < 5; i++ {
		seed(t, st, sym.ID, "trend21", int64(i+1)*86400, "uptrend", "downtrend", 0.2, 0.73)
	}
	for i := 5; i < 10; i++ {
		seed(t, st, sym.ID, "trend21", int64(i+1)*86400, "uptrend", "uptrend", 0.95, 0.97)
	}
	all, _ := st.StructuralRecords(ctx, 0)
	hi, _ := st.StructuralRecords(ctx, 0.9)
	if math.Abs(all[0].Accuracy-0.5) > 1e-9 {
		t.Fatalf("all-decisions accuracy %.3f, want 0.500", all[0].Accuracy)
	}
	if len(hi) != 1 || math.Abs(hi[0].Accuracy-1.0) > 1e-9 {
		t.Fatalf("high-conviction band should isolate the accurate calls: %+v", hi)
	}
}

func TestKindsAreGradedSeparately(t *testing.T) {
	ctx := context.Background()
	st := newStructStore(t)
	sym, _ := st.UpsertSymbol(ctx, "AAA", md.Stocks, "")
	seed(t, st, sym.ID, "trend21", 86400, "uptrend", "uptrend", 0.9, 0.97)
	seed(t, st, sym.ID, "vol21", 86400, "calm", "elevated", 0.9, 0.72)
	r, _ := st.StructuralRecords(ctx, 0)
	if len(r) != 2 {
		t.Fatalf("kinds must not be pooled, got %d", len(r))
	}
	byKind := map[string]StructuralRecordRow{}
	for _, x := range r {
		byKind[x.Kind] = x
	}
	if byKind["trend21"].Accuracy != 1.0 || byKind["vol21"].Accuracy != 0.0 {
		t.Fatalf("per-kind accuracy wrong: %+v", byKind)
	}
}

func TestUnresolvedOutcomesAreExcluded(t *testing.T) {
	ctx := context.Background()
	st := newStructStore(t)
	sym, _ := st.UpsertSymbol(ctx, "AAA", md.Stocks, "")
	seed(t, st, sym.ID, "trend21", 86400, "uptrend", "uptrend", 0.9, 0.97)
	// An unresolved row: horizon has not elapsed, so it is not evidence.
	_, err := st.w.ExecContext(ctx, `
		INSERT INTO regime_outcomes
		  (symbol_id, kind, ts, day, horizon_days, regime, conviction,
		   historical_accuracy, rank, resolved_at, actual, correct)
		VALUES (?,?,?,?,?,?,?,?,?,NULL,'',-1)`,
		sym.ID, "trend21", 2*86400, 2, 21, "uptrend", 0.9, 0.97, 0.9)
	if err != nil {
		t.Fatal(err)
	}
	r, _ := st.StructuralRecords(ctx, 0)
	if r[0].N != 1 {
		t.Fatalf("unresolved forecasts must not count as evidence, got n=%d", r[0].N)
	}
}

func TestEmptyRecordIsSafe(t *testing.T) {
	r, err := newStructStore(t).StructuralRecords(context.Background(), 0)
	if err != nil || len(r) != 0 {
		t.Fatalf("empty store should yield no rows, got %v %v", r, err)
	}
}

func TestStructuralKindsCoversTheValidatedThree(t *testing.T) {
	got := map[string]bool{}
	for _, k := range StructuralKinds() {
		got[k] = true
	}
	for _, want := range []structregime.Kind{
		structregime.KindTrend21, structregime.KindVol21, structregime.KindLiquidity21} {
		if !got[string(want)] {
			t.Fatalf("%s must be tracked as a first-class model", want)
		}
	}
}
