package briefing

// STAGE 2 — weekly self-report tests: the pure NY-week gate (Sunday-hour +
// catch-up + week-key dedup) and the end-to-end composition from a seeded
// temp store (resolutions, adaptive-weights baseline → diff, paper book,
// agents nearest graduation, anomalies).

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/adaptive"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// 2026-07-05 is a Sunday; 2026-07-12 the next one.

func TestShouldRunWeekly(t *testing.T) {
	ny := NYLoc()
	at := func(day, h, m int) time.Time {
		return time.Date(2026, 7, day, h, m, 0, 0, ny)
	}
	cases := []struct {
		name    string
		now     time.Time
		lastKey string
		want    bool
		wantKey string
	}{
		{"sunday-before-hour", at(5, 16, 59), "", false, "2026-07-05"},
		{"sunday-at-hour", at(5, 17, 0), "", true, "2026-07-05"},
		{"sunday-evening", at(5, 21, 0), "", true, "2026-07-05"},
		{"same-week-dedup", at(5, 21, 0), "2026-07-05", false, "2026-07-05"},
		{"midweek-catchup", at(8, 10, 0), "", true, "2026-07-05"}, // daemon was down Sunday
		{"midweek-already-ran", at(8, 10, 0), "2026-07-05", false, "2026-07-05"},
		{"next-sunday-fires", at(12, 17, 1), "2026-07-05", true, "2026-07-12"},
	}
	for _, c := range cases {
		got, key := ShouldRunWeekly(c.now, c.lastKey, ny, weeklyRunHour)
		if got != c.want || key != c.wantKey {
			t.Errorf("%s: ShouldRunWeekly(%v, %q) = %v,%q want %v,%q",
				c.name, c.now, c.lastKey, got, key, c.want, c.wantKey)
		}
	}
}

func TestWeeklyReportEndToEnd(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	ny := NYLoc()
	now := time.Date(2026, 7, 5, 17, 30, 0, 0, ny) // Sunday 5:30pm ET

	nvda, err := st.UpsertSymbol(ctx, "NVDA", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}

	// One resolved 1d outcome (ResolvePrediction stamps resolved_at with the
	// real clock — inside the report's 7-day window relative to the fake now).
	if err := st.UpsertPrediction(ctx, store.Prediction{
		SymbolID: nvda.ID, Horizon: md.H1d, Ts: now.Add(-72 * time.Hour).Unix(),
		RawProb: 0.7, CalProb: 0.7, NUsed: 3, Components: "{}",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.ResolvePrediction(ctx, nvda.ID, md.H1d, now.Add(-72*time.Hour).Unix(), 0.01); err != nil {
		t.Fatal(err)
	}

	// Current adaptive weights but NO previous snapshot → first run records
	// the baseline.
	if err := st.SetJSON(ctx, adaptive.MetaKey, adaptive.Weights{
		ComputedTs: now.Unix(),
		Cells: map[string]adaptive.Cell{
			"all": {N: 40, Weights: map[string]float64{"pressure": 1}},
		},
	}); err != nil {
		t.Fatal(err)
	}

	// A per-symbol agent at 32/40 — nearest graduation.
	if err := st.UpsertSymbolModel(ctx, store.SymbolModelRow{
		SymbolID: nvda.ID, Horizon: "1d", Weights: "{}", Calibration: "{}",
		Skill: "{}", Personality: "steady", NSamples: 32, Tier: "global",
		UpdatedTs: now.Unix(),
	}); err != nil {
		t.Fatal(err)
	}

	// A paper book with one trade + one equity mark this week.
	if _, err := st.InitPaperBook(ctx, "flagship-1d", 10000, now.Add(-6*24*time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	barTs := now.Add(-2 * 24 * time.Hour).Unix()
	if _, err := st.ApplyPaperStep(ctx, store.PaperApply{
		Strategy: "flagship-1d", BarTs: barTs, NewCash: 9900,
		Opens:    []store.PaperPosition{{Strategy: "flagship-1d", SymbolID: nvda.ID, Qty: 1, AvgPx: 100, OpenedTs: barTs}},
		Trades:   []store.PaperTrade{{Strategy: "flagship-1d", SymbolID: nvda.ID, Side: "buy", Qty: 1, Px: 100, Ts: barTs}},
		EquityTs: barTs, EquityCash: 9900, EquityPositionsValue: 100, EquityValue: 10000,
	}); err != nil {
		t.Fatal(err)
	}

	// One anomaly this week.
	if _, err := st.InsertAnomaly(ctx, store.AnomalyRow{
		SymbolID: nvda.ID, Ts: now.Add(-24 * time.Hour).Unix(),
		Kind: "anomaly_vol", Z: 3.1, Detail: "test",
	}); err != nil {
		t.Fatal(err)
	}

	w := &WeeklyWorker{St: st, Loc: ny, Now: func() time.Time { return now }}
	detail, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(detail, "2026-07-05") {
		t.Errorf("detail = %q", detail)
	}

	ins, err := st.InsightsByKind(ctx, WeeklyKind, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(ins) != 1 {
		t.Fatalf("weekly reports stored = %d want 1", len(ins))
	}
	body := ins[0].Body
	for _, want := range []string{
		"1d +1 raw (+1 independent", // measured resolution accrual
		"baseline recorded",         // first run has nothing to diff
		"NVDA 1d 32/40",             // agent nearest graduation
		"1 trade(s) filled",         // paper book activity
		"Anomalies detected in the last 7 days: 1",
		"not a forecast", // the honesty line survives
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q:\n%s", want, body)
		}
	}

	// The current weights were snapshotted for next week's diff.
	if prev, _ := st.GetMeta(ctx, MetaAdaptivePrev); prev == "" {
		t.Fatal("adaptive_weights_prev not snapshotted after the report")
	}

	// Same-week rerun: meta week-key dedup → still exactly one insight.
	if _, err := w.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if ins, _ = st.InsightsByKind(ctx, WeeklyKind, 5); len(ins) != 1 {
		t.Fatalf("same-week rerun duplicated the report: %d", len(ins))
	}

	// Next Sunday: fires again, and now the weights DIFF against the snapshot
	// (no "baseline recorded" any more).
	w.Now = func() time.Time { return time.Date(2026, 7, 12, 17, 5, 0, 0, ny) }
	if _, err := w.Run(ctx); err != nil {
		t.Fatal(err)
	}
	ins, _ = st.InsightsByKind(ctx, WeeklyKind, 5)
	if len(ins) != 2 {
		t.Fatalf("next-week report missing: %d", len(ins))
	}
	second := ins[0].Body // newest first
	if strings.Contains(second, "baseline recorded") {
		t.Errorf("second report must diff, not re-record the baseline:\n%s", second)
	}
	if !strings.Contains(second, "max per-leg shift") {
		t.Errorf("second report missing the weights diff:\n%s", second)
	}

	// The evidence blob carries the kind + week for downstream consumers.
	var data struct {
		Kind    string `json:"kind"`
		WeekKey string `json:"weekKey"`
	}
	if err := json.Unmarshal([]byte(ins[0].Data), &data); err != nil {
		t.Fatal(err)
	}
	if data.Kind != WeeklyKind || data.WeekKey != "2026-07-12" {
		t.Errorf("data = %+v", data)
	}
}

// TestWeeklyReportHoldsBeforeSundayHour: Sunday afternoon before the gate hour
// writes nothing.
func TestWeeklyReportHoldsBeforeSundayHour(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	ny := NYLoc()
	w := &WeeklyWorker{St: st, Loc: ny, Now: func() time.Time {
		return time.Date(2026, 7, 5, 14, 0, 0, 0, ny) // Sunday 2pm ET
	}}
	detail, err := w.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(detail, "waiting") {
		t.Errorf("detail = %q want waiting", detail)
	}
	if ins, _ := st.InsightsByKind(ctx, WeeklyKind, 5); len(ins) != 0 {
		t.Fatalf("report written before the Sunday gate hour: %d", len(ins))
	}
}
