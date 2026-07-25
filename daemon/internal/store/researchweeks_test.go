package store

import (
	"context"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	rl "github.com/nyaungnicholas-wq/signaldeck/internal/researchledger"
)

func TestResearchWeeks_RoundTripAndProjection(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()

	week := int64(2600)
	ts := week*604800 + 4*86400
	rows := []ResearchWeek{
		{SymbolID: 2, Week: week, Ts: ts,
			Vec:       map[string]float64{"pressure_score": -0.4, "rsi_pct": 0.9, "ext_score": 0.7},
			FwdReturn: 0.031, Up: true, Era: "ai_rally_2023_25", HighVol: false},
		{SymbolID: 1, Week: week, Ts: ts,
			Vec:       map[string]float64{"pressure_score": 0.6, "rsi_pct": 0.2},
			FwdReturn: -0.012, Up: false, Era: "ai_rally_2023_25", HighVol: true},
		{SymbolID: 1, Week: week + 1, Ts: ts + 604800,
			Vec:       map[string]float64{"pressure_score": 0.1},
			FwdReturn: 0.002, Up: true, Era: "ai_rally_2023_25", HighVol: false},
	}
	if err := st.UpsertResearchWeeks(ctx, rows, 1000); err != nil {
		t.Fatal(err)
	}

	// Full range, nil keys = full vectors, ordered (week, symbol_id).
	got, err := st.ResearchWeeks(ctx, week, week+1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("rows = %d, want 3", len(got))
	}
	if got[0].SymbolID != 1 || got[1].SymbolID != 2 || got[2].Week != week+1 {
		t.Fatalf("order wrong: %+v", got)
	}
	if !got[0].HighVol || got[0].Up || got[0].FwdReturn != -0.012 {
		t.Fatalf("row fields wrong: %+v", got[0])
	}
	if got[1].Vec["ext_score"] != 0.7 || len(got[1].Vec) != 3 {
		t.Fatalf("vec wrong: %+v", got[1].Vec)
	}

	// Inclusive range bounds: [week+1, week+1] returns only the later row.
	one, err := st.ResearchWeeks(ctx, week+1, week+1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(one) != 1 || one[0].Week != week+1 {
		t.Fatalf("inclusive range wrong: %+v", one)
	}

	// Key projection drops everything outside keys.
	proj, err := st.ResearchWeeks(ctx, week, week, []string{"rsi_pct"})
	if err != nil {
		t.Fatal(err)
	}
	if len(proj) != 2 {
		t.Fatalf("projected rows = %d, want 2", len(proj))
	}
	for _, r := range proj {
		if len(r.Vec) != 1 || r.Vec["rsi_pct"] == 0 {
			t.Fatalf("projection wrong for symbol %d: %+v", r.SymbolID, r.Vec)
		}
	}

	// Re-upsert of the same (symbol, week) replaces, never duplicates.
	rep := rows[0]
	rep.FwdReturn = 0.05
	if err := st.UpsertResearchWeeks(ctx, []ResearchWeek{rep}, 2000); err != nil {
		t.Fatal(err)
	}
	again, err := st.ResearchWeeks(ctx, week, week, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 2 || again[1].FwdReturn != 0.05 {
		t.Fatalf("replace failed: %+v", again)
	}
}

func TestResearchWeeksStats(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()

	// Honest empty state before any backfill.
	empty, err := st.ResearchWeeksStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if empty.Rows != 0 || empty.MinTs != 0 || empty.MaxTs != 0 || len(empty.ByEra) != 0 {
		t.Fatalf("empty stats wrong: %+v", empty)
	}

	rows := []ResearchWeek{
		{SymbolID: 1, Week: 2620, Ts: 2620 * 604800, Vec: map[string]float64{"x": 1}, Era: "bear_2022"},
		{SymbolID: 2, Week: 2620, Ts: 2620*604800 + 86400, Vec: map[string]float64{"x": 1}, Era: "bear_2022"},
		{SymbolID: 1, Week: 2700, Ts: 2700 * 604800, Vec: map[string]float64{"x": 1}, Era: "ai_rally_2023_25"},
	}
	if err := st.UpsertResearchWeeks(ctx, rows, 1); err != nil {
		t.Fatal(err)
	}
	stats, err := st.ResearchWeeksStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Rows != 3 || stats.Symbols != 2 || stats.Weeks != 2 {
		t.Fatalf("counts wrong: %+v", stats)
	}
	if stats.MinTs != 2620*604800 || stats.MaxTs != 2700*604800 {
		t.Fatalf("span wrong: %+v", stats)
	}
	if stats.ByEra["bear_2022"] != 2 || stats.ByEra["ai_rally_2023_25"] != 1 {
		t.Fatalf("byEra wrong: %+v", stats.ByEra)
	}
}

func TestResearchWeeks_EarliestBarTs(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()

	if ts, err := st.EarliestBarTs(ctx, 7, md.TF1d); err != nil || ts != 0 {
		t.Fatalf("no bars: ts=%d err=%v, want 0", ts, err)
	}
	bars := []md.Bar{
		{SymbolID: 7, TF: md.TF1d, Ts: 5000, Open: 1, High: 1, Low: 1, Close: 1},
		{SymbolID: 7, TF: md.TF1d, Ts: 3000, Open: 1, High: 1, Low: 1, Close: 1},
		{SymbolID: 7, TF: md.TF1h, Ts: 100, Open: 1, High: 1, Low: 1, Close: 1},
	}
	if err := st.UpsertBars(ctx, bars); err != nil {
		t.Fatal(err)
	}
	if ts, err := st.EarliestBarTs(ctx, 7, md.TF1d); err != nil || ts != 3000 {
		t.Fatalf("1d earliest = %d err=%v, want 3000", ts, err)
	}
	// Timeframe-scoped: 1h bars must not leak into the 1d probe.
	if ts, err := st.EarliestBarTs(ctx, 7, md.TF1h); err != nil || ts != 100 {
		t.Fatalf("1h earliest = %d err=%v, want 100", ts, err)
	}
}

func TestLedgerHypothesis_SpecAndDecayFields(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()

	h := rl.Hypothesis{
		ID: "H002-R1", Family: "meanrev", Statement: "pressure x extension",
		Horizon: "1w", Prior: 0.20, MaxEdge: 0.20, Posterior: 0.20,
		Status: rl.StatusDoubtful, Spec: `{"conds":[{"key":"ext_score","op":">=","val":0.7}]}`,
	}
	if err := st.UpsertLedgerHypothesis(ctx, h, 100); err != nil {
		t.Fatal(err)
	}
	got := ledgerHypByID(ctx, t, st, "H002-R1")
	if got.Spec != h.Spec {
		t.Fatalf("spec round-trip: %q", got.Spec)
	}
	if got.PeakPosterior != 0 || got.PeakTs != 0 || got.LastGradeTs != 0 {
		t.Fatalf("fresh decay fields not zero: %+v", got)
	}

	// Conflict path: spec updates alongside statement; prior stays immutable.
	h.Spec = `{"conds":[],"call":"inverse_pressure"}`
	h.Statement = "revised"
	h.Prior = 0.90 // must NOT stick
	if err := st.UpsertLedgerHypothesis(ctx, h, 200); err != nil {
		t.Fatal(err)
	}
	got = ledgerHypByID(ctx, t, st, "H002-R1")
	if got.Spec != h.Spec || got.Statement != "revised" {
		t.Fatalf("conflict update wrong: %+v", got)
	}
	if got.Prior != 0.20 {
		t.Fatalf("prior mutated to %v — must be immutable", got.Prior)
	}

	// Decay updater writes only the three decay fields.
	if err := st.UpdateLedgerDecay(ctx, "H002-R1", 0.81, 5000, 6000); err != nil {
		t.Fatal(err)
	}
	got = ledgerHypByID(ctx, t, st, "H002-R1")
	if got.PeakPosterior != 0.81 || got.PeakTs != 5000 || got.LastGradeTs != 6000 {
		t.Fatalf("decay fields wrong: %+v", got)
	}
	if got.Posterior != 0.20 || got.Spec == "" {
		t.Fatalf("decay update touched other fields: %+v", got)
	}
}

func TestLedgerEvidenceWindowCursors_KindScoped(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()

	h := rl.Hypothesis{ID: "H002", Family: "meanrev", Statement: "s", Prior: 0.2,
		MaxEdge: 0.2, Posterior: 0.2, Status: rl.StatusDoubtful}
	if err := st.UpsertLedgerHypothesis(ctx, h, 1); err != nil {
		t.Fatal(err)
	}
	ev := []rl.Evidence{
		{HypID: "H002", Ts: 10, Kind: rl.KindExperiment, BF: 2, WindowFrom: 1000, WindowTo: 2000},
		{HypID: "H002", Ts: 20, Kind: rl.KindReplication, BF: 2, WindowFrom: 2000, WindowTo: 3000},
		{HypID: "H002", Ts: 30, Kind: rl.KindBacktest, BF: 2, WindowFrom: 100, WindowTo: 5000},
		{HypID: "H002", Ts: 40, Kind: rl.KindAttack, BF: 0.5, WindowFrom: 0, WindowTo: 9000},
	}
	for _, e := range ev {
		if err := st.InsertLedgerEvidence(ctx, e); err != nil {
			t.Fatal(err)
		}
	}

	// The live cursor must include backtest windows: a live grade may never
	// re-measure data an era grade already consumed.
	if maxTo, err := st.LedgerEvidenceMaxWindow(ctx, "H002"); err != nil || maxTo != 5000 {
		t.Fatalf("live cursor = %d err=%v, want 5000 (backtest window counted)", maxTo, err)
	}

	// Kind-scoped cursors; attack windows never count.
	if v, err := st.LedgerEvidenceMaxWindowKinds(ctx, "H002", []string{rl.KindExperiment, rl.KindReplication}); err != nil || v != 3000 {
		t.Fatalf("live-only max = %d err=%v, want 3000", v, err)
	}
	if v, err := st.LedgerEvidenceMaxWindowKinds(ctx, "H002", []string{rl.KindBacktest}); err != nil || v != 5000 {
		t.Fatalf("backtest max = %d err=%v, want 5000", v, err)
	}
	if v, err := st.LedgerEvidenceMinWindowKinds(ctx, "H002", []string{rl.KindExperiment, rl.KindReplication}); err != nil || v != 1000 {
		t.Fatalf("live min = %d err=%v, want 1000", v, err)
	}
	if v, err := st.LedgerEvidenceMinWindowKinds(ctx, "H002", []string{rl.KindBacktest}); err != nil || v != 100 {
		t.Fatalf("backtest min = %d err=%v, want 100", v, err)
	}

	// Empty inputs are honest zeros.
	if v, err := st.LedgerEvidenceMaxWindowKinds(ctx, "H002", nil); err != nil || v != 0 {
		t.Fatalf("empty kinds max = %d err=%v, want 0", v, err)
	}
	if v, err := st.LedgerEvidenceMinWindowKinds(ctx, "NOPE", []string{rl.KindBacktest}); err != nil || v != 0 {
		t.Fatalf("unknown hyp min = %d err=%v, want 0", v, err)
	}
}

func ledgerHypByID(ctx context.Context, t *testing.T, st *Store, id string) rl.Hypothesis {
	t.Helper()
	hyps, err := st.LedgerHypotheses(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hyps {
		if h.ID == id {
			return h
		}
	}
	t.Fatalf("hypothesis %s not found", id)
	return rl.Hypothesis{}
}
