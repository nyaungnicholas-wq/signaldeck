package store

import (
	"context"
	"path/filepath"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// The scoring pipeline emits many rows per symbol per UTC day, and every one of
// them resolves against the SAME daily forward move. Any store method that
// feeds a grade, a weight or a reported statistic must therefore hand back the
// INDEPENDENT set — one row per (symbol, UTC-day) — not the raw rows.
//
// These tests pin that contract at the SQL boundary. The bar is the one the
// task set: reported N must equal the number of unique symbol-days.

const testDay = int64(86400)

// mkSyms registers n real symbols and returns their ids (prediction_outcomes
// has a FK to symbols, so ids cannot be invented).
func mkSyms(t *testing.T, st *Store, n int) []int64 {
	t.Helper()
	ctx := context.Background()
	out := make([]int64, 0, n)
	for i := 0; i < n; i++ {
		s, err := st.UpsertSymbol(ctx, "SYM"+string(rune('A'+i)), md.Stocks, "test symbol")
		if err != nil {
			t.Fatalf("upsert symbol: %v", err)
		}
		out = append(out, s.ID)
	}
	return out
}

// seedReplicated writes `perDay` rows for each of `syms` symbols on each of
// `days` UTC days — i.e. a pseudo-replicated set whose TRUE independent size is
// len(syms)*days regardless of how large perDay is.
func seedReplicated(t *testing.T, st *Store, h md.Horizon, syms []int64, days, perDay int) (base int64) {
	t.Helper()
	ctx := context.Background()
	base = int64(1_700_000_000)
	base -= base % testDay // align to a UTC-day boundary
	for _, sym := range syms {
		for d := 0; d < days; d++ {
			for k := 0; k < perDay; k++ {
				// Spread rows within the day; the LAST one is the keeper.
				ts := base + int64(d)*testDay + int64(k)*60
				vec := map[string]float64{"pred_raw": 0.6, "pressure_score": 0.5, "seq": float64(k)}
				if err := st.InsertFeatures(ctx, sym, h, ts, 1, vec); err != nil {
					t.Fatalf("insert features: %v", err)
				}
				if err := st.UpsertPrediction(ctx, Prediction{
					SymbolID: sym, Horizon: h, Ts: ts,
					RawProb: 0.6, CalProb: 0.6, NUsed: 2, Components: "{}",
				}); err != nil {
					t.Fatalf("upsert prediction: %v", err)
				}
				if err := st.ResolvePrediction(ctx, sym, h, ts, 0.01); err != nil {
					t.Fatalf("resolve: %v", err)
				}
			}
		}
	}
	return base
}

// TestLabeledFeaturesIndependent_NEqualsUniqueSymbolDays is the headline
// assertion: 3 symbols x 5 days x 50 rows/day = 750 raw rows but only 15
// independent observations.
func TestLabeledFeaturesIndependent_NEqualsUniqueSymbolDays(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "indep.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	syms := mkSyms(t, st, 3)
	const days, perDay = 5, 50
	seedReplicated(t, st, md.H1d, syms, days, perDay)

	raw, err := st.LabeledFeatures(ctx, md.H1d, 100000)
	if err != nil {
		t.Fatalf("raw: %v", err)
	}
	if len(raw) != len(syms)*days*perDay {
		t.Fatalf("precondition: raw rows = %d, want %d", len(raw), len(syms)*days*perDay)
	}

	indep, err := st.LabeledFeaturesIndependent(ctx, md.H1d, 100000)
	if err != nil {
		t.Fatalf("independent: %v", err)
	}
	want := len(syms) * days
	if len(indep) != want {
		t.Fatalf("independent N = %d, want %d (unique symbol-days); raw was %d", len(indep), want, len(raw))
	}

	// Exactly one row per (symbol, UTC-day), and it is the LATEST that day —
	// the symbol's last word, matching alphax.BuildDataset and dedupeIndependent.
	seen := map[[2]int64]bool{}
	for _, r := range indep {
		k := [2]int64{r.SymbolID, r.Ts / testDay}
		if seen[k] {
			t.Fatalf("duplicate (symbol %d, day %d) in the independent set", k[0], k[1])
		}
		seen[k] = true
		if got := r.Vec["seq"]; got != float64(perDay-1) {
			t.Fatalf("sym %d day %d: kept seq=%v, want the LATEST row (seq=%d)", k[0], k[1], got, perDay-1)
		}
	}
}

// TestLabeledFeaturesIndependent_LimitBuysDaysNotReplicas is the reason the
// dedupe lives in SQL rather than in the caller. A caller-side dedupe runs
// AFTER the LIMIT, so the budget is spent on replicas and the window collapses
// to a day or two. In SQL the same budget buys distinct days.
func TestLabeledFeaturesIndependent_LimitBuysDaysNotReplicas(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "window.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	const days, perDay = 10, 40
	seedReplicated(t, st, md.H1d, mkSyms(t, st, 1), days, perDay)

	const budget = 10

	// Raw + dedupe-in-caller: the newest 10 ROWS all sit on the newest day.
	raw, err := st.LabeledFeatures(ctx, md.H1d, budget)
	if err != nil {
		t.Fatalf("raw: %v", err)
	}
	rawDays := map[int64]bool{}
	for _, r := range raw {
		rawDays[r.Ts/testDay] = true
	}
	if len(rawDays) != 1 {
		t.Fatalf("precondition: %d raw rows should collapse to 1 day, got %d", budget, len(rawDays))
	}

	// Dedupe-in-SQL: the same budget reaches 10 distinct days.
	indep, err := st.LabeledFeaturesIndependent(ctx, md.H1d, budget)
	if err != nil {
		t.Fatalf("independent: %v", err)
	}
	indepDays := map[int64]bool{}
	for _, r := range indep {
		indepDays[r.Ts/testDay] = true
	}
	if len(indepDays) != days {
		t.Fatalf("a %d-row budget must buy %d distinct days, got %d", budget, days, len(indepDays))
	}
}

// TestResolvedPredictionPairsIndependent_NAndDays pins both returns: the pair
// count is unique symbol-days, and distinctDays is unique days.
func TestResolvedPredictionPairsIndependent_NAndDays(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "pairs.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	syms := mkSyms(t, st, 4)
	const days, perDay = 3, 25
	seedReplicated(t, st, md.H1d, syms, days, perDay)

	rawProbs, _, err := st.ResolvedPredictionPairs(ctx, md.H1d, 100000)
	if err != nil {
		t.Fatalf("raw pairs: %v", err)
	}
	if len(rawProbs) != len(syms)*days*perDay {
		t.Fatalf("precondition: raw pairs = %d, want %d", len(rawProbs), len(syms)*days*perDay)
	}

	probs, ups, distinctDays, err := st.ResolvedPredictionPairsIndependent(ctx, md.H1d, 100000)
	if err != nil {
		t.Fatalf("independent pairs: %v", err)
	}
	if want := len(syms) * days; len(probs) != want {
		t.Fatalf("independent pairs = %d, want %d (unique symbol-days)", len(probs), want)
	}
	if len(ups) != len(probs) {
		t.Fatalf("probs/ups misaligned: %d vs %d", len(probs), len(ups))
	}
	if distinctDays != days {
		t.Fatalf("distinctDays = %d, want %d", distinctDays, days)
	}
}

// TestResolvedPredictionIndependentCount_CountsBetsNotRows pins the gate unit:
// minIndependentN is documented as a floor of distinct symbol-days, so the
// count feeding it must be symbol-days.
func TestResolvedPredictionIndependentCount_CountsBetsNotRows(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "count.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	syms := mkSyms(t, st, 2)
	const days, perDay = 4, 30
	seedReplicated(t, st, md.H1d, syms, days, perDay)

	rawN, err := st.ResolvedPredictionCount(ctx, md.H1d)
	if err != nil {
		t.Fatalf("raw count: %v", err)
	}
	indepN, distinctDays, err := st.ResolvedPredictionIndependentCount(ctx, md.H1d)
	if err != nil {
		t.Fatalf("independent count: %v", err)
	}
	if want := len(syms) * days * perDay; rawN != want {
		t.Fatalf("precondition: raw count = %d, want %d", rawN, want)
	}
	if want := len(syms) * days; indepN != want {
		t.Fatalf("independent count = %d, want %d (unique symbol-days)", indepN, want)
	}
	if distinctDays != days {
		t.Fatalf("distinctDays = %d, want %d", distinctDays, days)
	}
	// The whole point: the raw count overstates the evidence, and only ever in
	// the permissive direction.
	if rawN <= indepN {
		t.Fatalf("precondition: raw (%d) must overstate independent (%d)", rawN, indepN)
	}
}

// TestLabeledFeaturesBySymbolIndependent_OnePerDay pins the per-symbol learner's
// unit. MinPersonal counts a symbol's own resolved OUTCOMES; on raw rows a hot
// name's same-day feature rows each read as an outcome, so it could graduate to
// its own calibration map on a single day of data.
func TestLabeledFeaturesBySymbolIndependent_OnePerDay(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "bysym.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	sym := mkSyms(t, st, 1)[0]
	const days, perDay = 2, 99
	seedReplicated(t, st, md.H1d, []int64{sym}, days, perDay)

	rawRows, err := st.LabeledFeaturesBySymbol(ctx, sym, md.H1d, 100000)
	if err != nil {
		t.Fatalf("raw by symbol: %v", err)
	}
	if len(rawRows) != days*perDay {
		t.Fatalf("precondition: raw = %d, want %d", len(rawRows), days*perDay)
	}

	indep, err := st.LabeledFeaturesBySymbolIndependent(ctx, sym, md.H1d, 100000)
	if err != nil {
		t.Fatalf("independent by symbol: %v", err)
	}
	if len(indep) != days {
		t.Fatalf("independent = %d, want %d (one per UTC day); raw claimed %d 'outcomes'",
			len(indep), days, len(rawRows))
	}
	for _, r := range indep {
		if r.SymbolID != sym {
			t.Fatalf("SymbolID = %d, want %d — the caller needs it to key the dedupe", r.SymbolID, sym)
		}
		if got := r.Vec["seq"]; got != float64(perDay-1) {
			t.Fatalf("kept seq=%v, want the LATEST row of the day (seq=%d)", got, perDay-1)
		}
	}
}
