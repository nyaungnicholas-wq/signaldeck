// Tests for the intraday score-compactor persistence: the heavy-row feeds
// (which must carve out each symbol's newest row), the key-set strips (strip
// set == archive set by construction), the components/payload fail-safe on the
// daily-last prunes, and the research_weeks retention pair.
package store

import (
	"context"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

func heavyScore(symID, ts int64) md.Score {
	return md.Score{SymbolID: symID, Horizon: md.H1d, Ts: ts, Score: 0.2,
		Components: []md.ScoreComponent{{Name: "rsi", Value: 61, Norm: 0.2,
			Weight: 0.5, Contrib: 0.1, Note: "n"}}}
}

func TestScoresCompactor_StripAndDailyLastPrune(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()

	sym, _ := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	day := int64(86400)
	// Three intraday rows on day 1, one newer row on day 2 (the carve-out).
	for _, ts := range []int64{day + 600, day + 1200, day + 1800, 2*day + 600} {
		if err := st.InsertScore(ctx, heavyScore(sym.ID, ts)); err != nil {
			t.Fatalf("insert score: %v", err)
		}
	}

	heavy, err := st.ScoresHeavyBelow(ctx, 3*day, 100)
	if err != nil {
		t.Fatalf("ScoresHeavyBelow: %v", err)
	}
	// The newest (symbol, horizon) row is carved out of the strip feed.
	if len(heavy) != 3 {
		t.Fatalf("heavy feed = %d rows; want 3 (newest carved out)", len(heavy))
	}
	for _, r := range heavy {
		if r.Ts == 2*day+600 {
			t.Fatal("carve-out row leaked into the strip feed")
		}
	}

	// Un-stripped rows must survive the prune (archive-before-destroy in SQL).
	if n, err := st.PruneScoresKeepDailyLast(ctx, 3*day); err != nil || n != 0 {
		t.Fatalf("prune before strip = %d, %v; want 0 (fail-safe)", n, err)
	}

	n, err := st.StripScoreComponents(ctx, heavy)
	if err != nil || n != 3 {
		t.Fatalf("StripScoreComponents = %d, %v; want 3", n, err)
	}
	// Stripped rows leave the heavy feed.
	if h, _ := st.ScoresHeavyBelow(ctx, 3*day, 100); len(h) != 0 {
		t.Fatalf("stripped rows still in heavy feed: %d", len(h))
	}

	// Now the prune downsamples day 1 to its LAST row.
	pruned, err := st.PruneScoresKeepDailyLast(ctx, 3*day)
	if err != nil || pruned != 2 {
		t.Fatalf("PruneScoresKeepDailyLast = %d, %v; want 2", pruned, err)
	}
	left, err := st.ScoresBefore(ctx, 1<<40, 0)
	if err != nil || len(left) != 2 {
		t.Fatalf("rows left = %d, %v; want 2 (day1 last + day2)", len(left), err)
	}
	if left[0].Ts != day+1800 || left[1].Ts != 2*day+600 {
		t.Fatalf("kept the wrong rows: %+v", left)
	}
}

func TestCompositeCompactor_StripAndDailyLastPrune(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()

	sym, _ := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	day := int64(86400)
	put := func(ts int64) {
		t.Helper()
		if err := st.UpsertCompositeScore(ctx, CompositeScore{SymbolID: sym.ID,
			Horizon: "1d", Ts: ts, Score: 72, CurvePct: 0.8, Edge: 0.01,
			Payload: `{"parts":[1]}`}); err != nil {
			t.Fatalf("upsert composite: %v", err)
		}
	}
	put(day + 600)
	put(day + 1200)
	put(2*day + 600) // newest — carved out

	heavy, err := st.CompositeHeavyBelow(ctx, 3*day, 100)
	if err != nil || len(heavy) != 2 {
		t.Fatalf("composite heavy feed = %d rows, %v; want 2", len(heavy), err)
	}
	if heavy[0].Payload != `{"parts":[1]}` || heavy[0].Score != 72 {
		t.Fatalf("archive row mangled: %+v", heavy[0])
	}

	if n, err := st.PruneCompositeKeepDailyLast(ctx, 3*day); err != nil || n != 0 {
		t.Fatalf("prune before strip = %d, %v; want 0 (fail-safe)", n, err)
	}
	n, err := st.StripCompositePayload(ctx, heavy)
	if err != nil || n != 2 {
		t.Fatalf("StripCompositePayload = %d, %v; want 2", n, err)
	}
	pruned, err := st.PruneCompositeKeepDailyLast(ctx, 3*day)
	if err != nil || pruned != 1 {
		t.Fatalf("PruneCompositeKeepDailyLast = %d, %v; want 1", pruned, err)
	}
	// Day 1's last row survived with its payload stripped.
	c, ok, err := st.LatestCompositeScore(ctx, sym.ID, "1d")
	if err != nil || !ok || c.Ts != 2*day+600 {
		t.Fatalf("latest composite = %+v, %v, %v", c, ok, err)
	}
}

func TestResearchWeeksRetentionPair(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()

	sym, _ := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	week := int64(604800)
	rows := []ResearchWeek{
		{SymbolID: sym.ID, Week: 1, Ts: week + 100, Vec: map[string]float64{"m": 1},
			FwdReturn: 0.02, Up: true, Era: "qe", HighVol: false},
		{SymbolID: sym.ID, Week: 2, Ts: 2*week + 100, Vec: map[string]float64{"m": 2},
			FwdReturn: -0.01, Up: false, Era: "qe", HighVol: true},
	}
	if err := st.UpsertResearchWeeks(ctx, rows, 999); err != nil {
		t.Fatalf("upsert research weeks: %v", err)
	}

	old, err := st.ResearchWeeksBefore(ctx, 2*week, 0)
	if err != nil || len(old) != 1 || old[0].Week != 1 {
		t.Fatalf("ResearchWeeksBefore = %+v, %v; want week 1 only", old, err)
	}
	// The vec JSON and labels ride the archive row verbatim.
	if old[0].Vec == "" || old[0].Up != 1 || old[0].Era != "qe" || old[0].CreatedAt != 999 {
		t.Fatalf("archive row incomplete: %+v", old[0])
	}
	if capped, _ := st.ResearchWeeksBefore(ctx, 1<<40, 1); len(capped) != 1 {
		t.Fatal("limit not applied")
	}
	if n, err := st.DeleteResearchWeeksBefore(ctx, 2*week); err != nil || n != 1 {
		t.Fatalf("DeleteResearchWeeksBefore = %d, %v; want 1", n, err)
	}
	if left, _ := st.ResearchWeeksBefore(ctx, 1<<40, 0); len(left) != 1 || left[0].Week != 2 {
		t.Fatalf("prune removed the wrong week: %+v", left)
	}
}

func TestStorePathIsExposed(t *testing.T) {
	st := openTemp(t)
	if st.Path() == "" {
		t.Fatal("Path() returned empty — headroom checks would guess")
	}
}
