// Visual-kit Stage 3: tests for the batched dashboard store reads. The
// contract under test is BATCHING — LastNDailyCloses must return every
// requested symbol's sparkline from ONE query (chronological, capped at n,
// only requested ids) — plus the gauge aggregates. t.TempDir store only.
package store

import (
	"context"
	"path/filepath"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

func openDashStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "dash.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestLastNDailyCloses(t *testing.T) {
	st := openDashStore(t)
	ctx := context.Background()

	a, _ := st.UpsertSymbol(ctx, "AAA", md.Stocks, "")
	b, _ := st.UpsertSymbol(ctx, "BBB", md.Stocks, "")
	c, _ := st.UpsertSymbol(ctx, "CCC", md.Stocks, "") // has bars but NOT requested
	empty, _ := st.UpsertSymbol(ctx, "DDD", md.Stocks, "")

	// AAA: 5 daily closes 101..105; BBB: 2 closes; CCC: decoy rows; and an
	// hourly bar on AAA that must be ignored (tf filter).
	var bars []md.Bar
	for i := 0; i < 5; i++ {
		bars = append(bars, md.Bar{SymbolID: a.ID, TF: md.TF1d, Ts: int64(86400 * (i + 1)), Close: float64(101 + i)})
	}
	bars = append(bars,
		md.Bar{SymbolID: b.ID, TF: md.TF1d, Ts: 86400, Close: 50},
		md.Bar{SymbolID: b.ID, TF: md.TF1d, Ts: 2 * 86400, Close: 55},
		md.Bar{SymbolID: c.ID, TF: md.TF1d, Ts: 86400, Close: 999},
		md.Bar{SymbolID: a.ID, TF: md.TF1h, Ts: 10 * 86400, Close: 12345},
	)
	if err := st.UpsertBars(ctx, bars); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// n=3 → AAA capped to its NEWEST 3 closes in chronological order.
	got, err := st.LastNDailyCloses(ctx, []int64{a.ID, b.ID, empty.ID}, 3)
	if err != nil {
		t.Fatalf("LastNDailyCloses: %v", err)
	}
	wantA := []float64{103, 104, 105}
	if len(got[a.ID]) != 3 {
		t.Fatalf("AAA closes = %v, want 3 newest", got[a.ID])
	}
	for i, v := range wantA {
		if got[a.ID][i] != v {
			t.Errorf("AAA[%d] = %v, want %v (chronological, newest-3)", i, got[a.ID][i], v)
		}
	}
	if len(got[b.ID]) != 2 || got[b.ID][0] != 50 || got[b.ID][1] != 55 {
		t.Errorf("BBB closes = %v, want [50 55]", got[b.ID])
	}
	// Bar-less symbol: honestly ABSENT, never a fabricated flat series.
	if _, has := got[empty.ID]; has {
		t.Errorf("DDD must be absent (no daily bars), got %v", got[empty.ID])
	}
	// Unrequested symbol must not leak in.
	if _, has := got[c.ID]; has {
		t.Errorf("CCC not requested but present")
	}
	// Empty id list → empty map, no error (and no SQL with an empty IN ()).
	if m, err := st.LastNDailyCloses(ctx, nil, 60); err != nil || len(m) != 0 {
		t.Errorf("empty ids: m=%v err=%v", m, err)
	}
}

func TestAnomalyCountSince(t *testing.T) {
	st := openDashStore(t)
	ctx := context.Background()
	a, _ := st.UpsertSymbol(ctx, "AAA", md.Stocks, "")

	seed := []AnomalyRow{
		{SymbolID: a.ID, Ts: 1000, Kind: "anomaly_vol", Z: 3, Detail: "old"},
		{SymbolID: a.ID, Ts: 5000, Kind: "anomaly_vol", Z: 3, Detail: "in"},
		{SymbolID: a.ID, Ts: 6000, Kind: "anomaly_volume", Z: 4, Detail: "in"},
	}
	for _, r := range seed {
		if _, err := st.InsertAnomaly(ctx, r); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	n, err := st.AnomalyCountSince(ctx, 5000)
	if err != nil {
		t.Fatalf("AnomalyCountSince: %v", err)
	}
	if n != 2 {
		t.Errorf("count = %d, want 2 (ts>=5000 only)", n)
	}
}

func TestLatestPredictionStatsAndResolvedCount(t *testing.T) {
	st := openDashStore(t)
	ctx := context.Background()
	a, _ := st.UpsertSymbol(ctx, "AAA", md.Stocks, "")
	b, _ := st.UpsertSymbol(ctx, "BBB", md.Stocks, "")

	// AAA: stale 0.9 then latest 0.7 (|0.7-0.5|*2 = 0.4 must win);
	// BBB: single 0.4 (conf 0.2). A 1w row must not leak into the 1d stats.
	// NUsed>0 on every row: these are real forecasts. A row with NUsed==0 is an
	// evidence-only record of what the legs said and is excluded from every
	// published surface, this one included.
	seed := []Prediction{
		{SymbolID: a.ID, Horizon: md.H1d, Ts: 100, RawProb: 0.9, CalProb: 0.9, NUsed: 1},
		{SymbolID: a.ID, Horizon: md.H1d, Ts: 200, RawProb: 0.7, CalProb: 0.7, NUsed: 1},
		{SymbolID: b.ID, Horizon: md.H1d, Ts: 150, RawProb: 0.4, CalProb: 0.4, NUsed: 1},
		{SymbolID: b.ID, Horizon: md.H1w, Ts: 300, RawProb: 0.99, CalProb: 0.99, NUsed: 1},
	}
	for _, p := range seed {
		if err := st.UpsertPrediction(ctx, p); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	avg, n, err := st.LatestPredictionStats(ctx, md.H1d)
	if err != nil {
		t.Fatalf("LatestPredictionStats: %v", err)
	}
	if n != 2 {
		t.Fatalf("n = %d, want 2 (latest per symbol, 1d only)", n)
	}
	want := (0.4 + 0.2) / 2 // mean of |p-0.5|*2 over the LATEST rows
	if avg < want-1e-9 || avg > want+1e-9 {
		t.Errorf("avgConf = %v, want %v", avg, want)
	}

	// Resolve ONE 1d outcome → resolved count 1 for 1d, 0 for 1w untouched.
	if err := st.ResolvePrediction(ctx, a.ID, md.H1d, 200, 0.01); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	rn, err := st.ResolvedPredictionCount(ctx, md.H1d)
	if err != nil {
		t.Fatalf("ResolvedPredictionCount: %v", err)
	}
	if rn != 1 {
		t.Errorf("resolved 1d = %d, want 1", rn)
	}
	if rw, _ := st.ResolvedPredictionCount(ctx, md.H1w); rw != 0 {
		t.Errorf("resolved 1w = %d, want 0", rw)
	}
}

func TestUnseenAlertCount(t *testing.T) {
	st := openDashStore(t)
	ctx := context.Background()
	u1, _ := st.CreateUser(ctx, "u1", "hash", false)
	u2, _ := st.CreateUser(ctx, "u2", "hash", false)

	for i, a := range []Alert{
		{UserID: u1, Kind: "breakout", Detail: "a", Ts: 100},
		{UserID: u1, Kind: "regime_change", Detail: "b", Ts: 200},
		{UserID: u2, Kind: "breakout", Detail: "c", Ts: 300},
	} {
		if err := st.InsertAlert(ctx, a); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}
	if n, err := st.UnseenAlertCount(ctx, u1); err != nil || n != 2 {
		t.Fatalf("u1 unseen = %d (err %v), want 2", n, err)
	}
	if _, err := st.MarkAlertsSeen(ctx, u1); err != nil {
		t.Fatalf("mark seen: %v", err)
	}
	if n, _ := st.UnseenAlertCount(ctx, u1); n != 0 {
		t.Errorf("u1 unseen after mark = %d, want 0", n)
	}
	if n, _ := st.UnseenAlertCount(ctx, u2); n != 1 {
		t.Errorf("u2 unseen = %d, want 1 (isolation)", n)
	}
}
