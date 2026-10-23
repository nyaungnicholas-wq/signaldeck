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
		if len(p.Factors) != 14 {
			t.Fatalf("symbol %d: %d factor tiles, want 14", id, len(p.Factors))
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

// The Reg SHO short-volume tile (FINRA, retired 22 Oct 2026) and the
// TradingView tile (retired 21 Oct 2026) are no longer fed. Seeded short_volume
// rows must not reach the tile, and both tiles must say "retired", not "no rows".
func TestCompositeScorerRetiredTilesStayUnfed(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "composite_retired.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	ids := seedCompositePreds(t, st, ctx, 30)
	// A prediction frozen before 2026-10-23 still carries an LLM sentiment
	// score in its components; the tile must not be fed from it.
	sent := 0.8
	comps, _ := json.Marshal(ensemble.Components{PressureScore: 0.4, SentimentScore: &sent})
	if err := st.UpsertPrediction(ctx, store.Prediction{
		SymbolID: ids[0], Horizon: md.H1d, Ts: time.Now().Truncate(time.Minute).Unix() - 60,
		RawProb: 0.7, CalProb: 0.7, NUsed: 1, Components: string(comps),
	}); err != nil {
		t.Fatal(err)
	}

	rows := make([]store.ShortVolumeRow, 30)
	now := time.Now().UTC()
	for d := 1; d <= 30; d++ {
		day := now.AddDate(0, 0, -d).Format("2006-01-02")
		sv := float64(30 + d%7)
		rows[d-1] = store.ShortVolumeRow{
			SymbolID: ids[0], Day: day, ShortVol: sv, TotalVol: 100, ShortPct: sv / 100,
		}
	}
	if err := st.UpsertShortVolume(ctx, rows); err != nil {
		t.Fatal(err)
	}

	if _, err := (&CompositeScorer{St: st}).Run(ctx); err != nil {
		t.Fatal(err)
	}

	row, ok, err := st.LatestCompositeScore(ctx, ids[0], "1d")
	if err != nil || !ok {
		t.Fatalf("no composite row (err %v)", err)
	}

	var p composite.Payload
	if err := json.Unmarshal([]byte(row.Payload), &p); err != nil {
		t.Fatal(err)
	}

	want := map[string]string{
		composite.FactorShortVol:  composite.ShortVolRetiredReason,
		composite.FactorTVRating:  composite.TVRatingRetiredReason,
		composite.FactorSentiment: composite.SentimentRetiredReason,
	}
	seen := 0
	for _, f := range p.Factors {
		if r, ok := want[f.Key]; ok {
			seen++
			if !f.Gated || f.GateReason != r {
				t.Errorf("%s tile = %+v, want gated with %q", f.Key, f, r)
			}
		}
	}
	if seen < 3 {
		t.Fatalf("expected all three retired factors, saw %d", seen)
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

// The pooled TradingView skill rescans the whole tv_ratings history, so the
// scorer memoizes it for tvSkillTTL: within the TTL new evidence is NOT
// re-read; past it, it is.
func TestCompositeScorerMemoizesTVSkill(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "composite_tvskill.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	clock := time.Unix(1_700_000_000, 0)
	w := &CompositeScorer{St: st, Now: func() time.Time { return clock }}
	if sk, err := w.tvRatingSkill(ctx); err != nil || sk.N != 0 {
		t.Fatalf("empty skill = %+v err=%v", sk, err)
	}

	// Three resolvable (symbol, day) observations.
	sym, _ := st.UpsertSymbol(ctx, "SPY", md.Stocks, "S&P 500 ETF")
	const day = 86400
	base := int64(1_600_000_000)/day*day + 5*3600
	var bars []md.Bar
	for i := 0; i < 4; i++ {
		c := 100 + float64(i)
		bars = append(bars, md.Bar{SymbolID: sym.ID, TF: md.TF1d, Ts: base + int64(i)*day, Open: c, High: c, Low: c, Close: c})
	}
	if err := st.UpsertBars(ctx, bars); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := st.UpsertTVRating(ctx, store.TVRatingRow{SymbolID: sym.ID, Ts: base + int64(i)*day + 100, RecoAll: 0.5, Label: "x"}); err != nil {
			t.Fatal(err)
		}
	}

	clock = clock.Add(tvSkillTTL - time.Minute)
	if sk, _ := w.tvRatingSkill(ctx); sk.N != 0 {
		t.Fatalf("inside the TTL the skill was recomputed: N=%d", sk.N)
	}
	clock = clock.Add(2 * time.Minute)
	if sk, _ := w.tvRatingSkill(ctx); sk.N != 3 {
		t.Fatalf("past the TTL N=%d, want 3", sk.N)
	}
}
