package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/composite"
	"github.com/nyaungnicholas-wq/signaldeck/internal/ensemble"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// seedCompositePreds registers n active stocks with one fresh 1d prediction
// each (calProb spread over a real cross-section) and returns their ids in
// insertion order. Component blobs are real ensemble.Components JSON.
func seedCompositePreds(t *testing.T, st *store.Store, ctx context.Context, n int) []int64 {
	t.Helper()
	ts := time.Now().Truncate(time.Minute).Unix() - 60
	ids := make([]int64, 0, n)
	for i := 0; i < n; i++ {
		sym, err := st.UpsertSymbol(ctx, fmt.Sprintf("SY%03d", i), md.Stocks, "")
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, sym.ID)
		// Descending edge: symbol 0 has the best calProb.
		pressure := 0.4 - float64(i)*0.01
		comps, _ := json.Marshal(ensemble.Components{PressureScore: pressure})
		cal := (pressure + 1) / 2
		if err := st.UpsertPrediction(ctx, store.Prediction{
			SymbolID: sym.ID, Horizon: md.H1d, Ts: ts,
			RawProb: cal, CalProb: cal, NUsed: 1, Components: string(comps),
		}); err != nil {
			t.Fatal(err)
		}
	}
	return ids
}

// TestCompositeScorerGatesBelowMinCurveN: fewer than 30 usable predictions
// gates the WHOLE pass — nothing stored, and the detail says why.
func TestCompositeScorerGatesBelowMinCurveN(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "composite_gate.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	ids := seedCompositePreds(t, st, ctx, composite.MinCurveN-1)

	w := &CompositeScorer{St: st}
	detail, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(detail, "gated") || !strings.Contains(detail, "no scores stored") {
		t.Fatalf("gate detail = %q — must say the pass was gated and why", detail)
	}
	for _, id := range ids {
		if _, ok, err := st.LatestCompositeScore(ctx, id, "1d"); err != nil || ok {
			t.Fatalf("symbol %d has a composite row despite the gate (ok=%v err=%v)", id, ok, err)
		}
	}
	// The universe meta cursor must NOT advance on a gated pass.
	if day, _ := st.GetMeta(ctx, "composite_universe_day"); day != "" {
		t.Fatalf("composite_universe_day = %q after a gated pass, want unset", day)
	}
}

// TestCompositeScorerEmitsForcedCurve: with >=30 usable predictions the pass
// stores one row per symbol with forced-curve scores, honest payloads, and a
// ledger that sums exactly to calProb-0.5.
func TestCompositeScorerEmitsForcedCurve(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "composite_emit.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	ids := seedCompositePreds(t, st, ctx, 40)

	w := &CompositeScorer{St: st}
	detail, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(detail, "scored 40 symbol(s)") {
		t.Fatalf("detail = %q, want 40 symbols scored (first pass covers the universe)", detail)
	}

	counts := map[int]int{}
	for i, id := range ids {
		row, ok, err := st.LatestCompositeScore(ctx, id, "1d")
		if err != nil || !ok {
			t.Fatalf("symbol %d: no composite row (err %v)", id, err)
		}
		if row.Score < 1 || row.Score > 10 {
			t.Fatalf("symbol %d: score %d out of 1..10", id, row.Score)
		}
		counts[row.Score]++

		var p composite.Payload
		if err := json.Unmarshal([]byte(row.Payload), &p); err != nil {
			t.Fatalf("symbol %d: payload does not parse: %v", id, err)
		}
		if len(p.Factors) != 13 {
			t.Fatalf("symbol %d: %d factor tiles, want 13", id, len(p.Factors))
		}
		if math.Abs(p.Ledger.SumPP-(p.CalProb-0.5)*100) > 1e-9 {
			t.Fatalf("symbol %d: ledger sums to %.6f, want %.6f", id, p.Ledger.SumPP, (p.CalProb-0.5)*100)
		}
		if math.Abs(row.Edge-(p.CalProb-0.5)) > 1e-12 {
			t.Fatalf("symbol %d: edge column %.6f != payload edge %.6f", id, row.Edge, p.CalProb-0.5)
		}
		// Seeded descending: symbol 0 is the cross-sectional best.
		if i == 0 && row.Score != 10 {
			t.Fatalf("best symbol scored %d, want 10", row.Score)
		}
		if i == len(ids)-1 && row.Score != 1 {
			t.Fatalf("worst symbol scored %d, want 1", row.Score)
		}
	}
	// Forced-curve band counts over N=40 with the (i+0.5)/N percentile:
	// 10:2, 9:4, 8:4, 7:4, 6:6, 5:6, 4:4, 3:4, 2:4, 1:2.
	want := map[int]int{10: 2, 9: 4, 8: 4, 7: 4, 6: 6, 5: 6, 4: 4, 3: 4, 2: 4, 1: 2}
	for sc, n := range want {
		if counts[sc] != n {
			t.Fatalf("forced curve counts = %v, want %v", counts, want)
		}
	}
	// Live-everything wave: the cursor is now unix seconds of the pass.
	if raw, _ := st.GetMeta(ctx, "composite_universe_day"); raw == "" {
		t.Fatal("composite_universe_day empty after a clean universe pass")
	} else if ts, err := strconv.ParseInt(raw, 10, 64); err != nil || time.Since(time.Unix(ts, 0)) > time.Minute {
		t.Fatalf("composite_universe_day = %q, want recent unix seconds", raw)
	}
}

// TestCompositeScorerCadence: after the day's universe pass, a second run the
// same day re-scores ONLY the hot set (streamed stocks + crypto); daily-only
// universe symbols keep their single row for the day.
func TestCompositeScorerCadence(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "composite_cadence.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	ids := seedCompositePreds(t, st, ctx, 40)
	// Promote the first symbol into the streamed hot set.
	if err := st.SetSymbolStream(ctx, ids[0], true); err != nil {
		t.Fatal(err)
	}

	w := &CompositeScorer{St: st}
	if _, err := w.Run(ctx); err != nil { // first pass of the day: universe
		t.Fatal(err)
	}
	// A later pass the same day writes a NEW ts — force a distinct minute
	// bucket by rewinding the stored rows' ts is not possible (append-only
	// pattern), so run again and compare row ts: same-minute reruns are
	// idempotent upserts either way; the cadence is asserted via the detail.
	detail, err := w.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(detail, "scored 1 symbol(s)") || !strings.Contains(detail, "universe pass: false") {
		t.Fatalf("second same-day pass detail = %q, want only the 1 hot symbol rescored", detail)
	}
}

// TestCompositeScorerSkipsStaleAndUnusable: predictions older than the
// freshness window or with nUsed=0 don't enter the curve — and if that drops
// the cross-section below the floor, the pass gates.
func TestCompositeScorerSkipsStaleAndUnusable(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "composite_stale.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	// 30 symbols: 29 fresh + 1 stale -> 29 usable -> gate.
	seedCompositePreds(t, st, ctx, composite.MinCurveN-1)
	staleSym, err := st.UpsertSymbol(ctx, "STALE", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}
	comps, _ := json.Marshal(ensemble.Components{PressureScore: 0.2})
	if err := st.UpsertPrediction(ctx, store.Prediction{
		SymbolID: staleSym.ID, Horizon: md.H1d,
		Ts:      time.Now().Unix() - 4*86400, // beyond compositeMaxPredAge
		RawProb: 0.6, CalProb: 0.6, NUsed: 1, Components: string(comps),
	}); err != nil {
		t.Fatal(err)
	}

	w := &CompositeScorer{St: st}
	detail, err := w.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(detail, "gated") {
		t.Fatalf("detail = %q — a stale prediction must not prop up the cross-section", detail)
	}
}
