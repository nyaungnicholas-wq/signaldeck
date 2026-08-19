package store

import (
	"context"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

func TestLabeledFeaturesRecentDays_ReturnsNewestWholeDays(t *testing.T) {
	ctx := context.Background()
	st := openTemp(t)
	sym, err := st.UpsertSymbol(ctx, "SPY", md.Stocks, "")
	if err != nil {
		t.Fatalf("UpsertSymbol: %v", err)
	}

	// Seed 5 distinct days with different row counts: day0=2, day1=3, day2=1, day3=4, day4=2 (newest)
	// We'll insert in chronological order so day4 is newest
	dayCounts := []int{2, 3, 1, 4, 2}
	for dayIdx, count := range dayCounts {
		baseTS := int64(dayIdx) * 86400
		for i := 0; i < count; i++ {
			ts := baseTS + int64(i)*3600 // intra-day offsets
			mustPredict(t, st, sym.ID, md.H1d, ts, 0.5+float64(i)*0.1, map[string]float64{"pressure_score": 0.4, "pred_raw": 0.7})
			if err := st.ResolvePrediction(ctx, sym.ID, md.H1d, ts, 0.02); err != nil {
				t.Fatalf("ResolvePrediction day %d row %d: %v", dayIdx, i, err)
			}
		}
	}

	// Ask for newest 3 days (days 2,3,4) = 1+4+2 = 7 rows
	rows, ceilingBound, err := st.LabeledFeaturesRecentDays(ctx, md.H1d, 3, 100)
	if err != nil {
		t.Fatalf("LabeledFeaturesRecentDays: %v", err)
	}
	if ceilingBound {
		t.Fatalf("ceilingBound=true, expected false (history shorter than ceiling)")
	}
	if len(rows) != 7 {
		t.Fatalf("expected 7 rows (sum of newest 3 days), got %d", len(rows))
	}

	// Verify only days 2,3,4 present (day = ts/86400)
	seenDays := make(map[int64]int)
	for _, r := range rows {
		day := r.Ts / 86400
		seenDays[day]++
	}
	expectedDays := map[int64]int{2: 1, 3: 4, 4: 2}
	for day, expCount := range expectedDays {
		if seenDays[day] != expCount {
			t.Fatalf("day %d: expected %d rows, got %d", day, expCount, seenDays[day])
		}
	}
	for day := range seenDays {
		if _, ok := expectedDays[day]; !ok {
			t.Fatalf("unexpected day %d present in results", day)
		}
	}
}

func TestLabeledFeaturesRecentDays_CeilingDropsWholeDaysNotPartialOnes(t *testing.T) {
	ctx := context.Background()
	st := openTemp(t)
	sym, err := st.UpsertSymbol(ctx, "SPY", md.Stocks, "")
	if err != nil {
		t.Fatalf("UpsertSymbol: %v", err)
	}

	// Seed 4 days with 3 rows each = 12 rows total
	for dayIdx := 0; dayIdx < 4; dayIdx++ {
		baseTS := int64(dayIdx) * 86400
		for i := 0; i < 3; i++ {
			ts := baseTS + int64(i)*3600
			mustPredict(t, st, sym.ID, md.H1d, ts, 0.5+float64(i)*0.1, map[string]float64{"pressure_score": 0.4, "pred_raw": 0.7})
			if err := st.ResolvePrediction(ctx, sym.ID, md.H1d, ts, 0.02); err != nil {
				t.Fatalf("ResolvePrediction day %d row %d: %v", dayIdx, i, err)
			}
		}
	}

	// Ask for 4 days, maxRows=7. Only 2 whole days (6 rows) fit; 3rd day would make 9 > 7
	rows, ceilingBound, err := st.LabeledFeaturesRecentDays(ctx, md.H1d, 4, 7)
	if err != nil {
		t.Fatalf("LabeledFeaturesRecentDays: %v", err)
	}
	if !ceilingBound {
		t.Fatalf("ceilingBound=false, expected true (ceiling forced drop of whole day)")
	}
	if len(rows) != 6 {
		t.Fatalf("expected 6 rows (2 whole days), got %d", len(rows))
	}

	// Verify exactly newest 2 days (day 2 and 3), each complete with 3 rows
	seenDays := make(map[int64]int)
	for _, r := range rows {
		day := r.Ts / 86400
		seenDays[day]++
	}
	expectedDays := map[int64]int{2: 3, 3: 3}
	for day, expCount := range expectedDays {
		if seenDays[day] != expCount {
			t.Fatalf("day %d: expected %d rows (complete day), got %d", day, expCount, seenDays[day])
		}
	}
	for day := range seenDays {
		if _, ok := expectedDays[day]; !ok {
			t.Fatalf("unexpected day %d present in results", day)
		}
	}
}

func TestLabeledFeaturesRecentDays_ShortHistoryIsNotCeilingBound(t *testing.T) {
	ctx := context.Background()
	st := openTemp(t)
	sym, err := st.UpsertSymbol(ctx, "SPY", md.Stocks, "")
	if err != nil {
		t.Fatalf("UpsertSymbol: %v", err)
	}

	// Seed only 2 days
	for dayIdx := 0; dayIdx < 2; dayIdx++ {
		baseTS := int64(dayIdx) * 86400
		for i := 0; i < 3; i++ {
			ts := baseTS + int64(i)*3600
			mustPredict(t, st, sym.ID, md.H1d, ts, 0.5+float64(i)*0.1, map[string]float64{"pressure_score": 0.4, "pred_raw": 0.7})
			if err := st.ResolvePrediction(ctx, sym.ID, md.H1d, ts, 0.02); err != nil {
				t.Fatalf("ResolvePrediction day %d row %d: %v", dayIdx, i, err)
			}
		}
	}

	// Ask for 20 days, huge maxRows — history is short, not ceiling-limited
	rows, ceilingBound, err := st.LabeledFeaturesRecentDays(ctx, md.H1d, 20, 100000)
	if err != nil {
		t.Fatalf("LabeledFeaturesRecentDays: %v", err)
	}
	if ceilingBound {
		t.Fatalf("ceilingBound=true, expected false (short history, not ceiling truncation)")
	}
	if len(rows) != 6 {
		t.Fatalf("expected 6 rows (2 days * 3), got %d", len(rows))
	}

	seenDays := make(map[int64]int)
	for _, r := range rows {
		day := r.Ts / 86400
		seenDays[day]++
	}
	if len(seenDays) != 2 {
		t.Fatalf("expected 2 distinct days, got %d", len(seenDays))
	}
}

func TestLabeledFeaturesRecentDays_RejectsNonPositiveArgs(t *testing.T) {
	ctx := context.Background()
	st := openTemp(t)
	sym, err := st.UpsertSymbol(ctx, "SPY", md.Stocks, "")
	if err != nil {
		t.Fatalf("UpsertSymbol: %v", err)
	}

	// Seed one valid row so the function has something to query
	mustPredict(t, st, sym.ID, md.H1d, 86400, 0.5, map[string]float64{"pressure_score": 0.4, "pred_raw": 0.7})
	if err := st.ResolvePrediction(ctx, sym.ID, md.H1d, 86400, 0.02); err != nil {
		t.Fatalf("ResolvePrediction: %v", err)
	}

	testCases := []struct {
		name    string
		days    int
		maxRows int
	}{
		{"days=0", 0, 10},
		{"days=-1", -1, 10},
		{"maxRows=0", 5, 0},
		{"maxRows=-1", 5, -1},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := st.LabeledFeaturesRecentDays(ctx, md.H1d, tc.days, tc.maxRows)
			if err == nil {
				t.Fatalf("expected error for %s, got nil", tc.name)
			}
		})
	}
}

func TestLabeledFeaturesRecentDays_ExcludesUnresolvedAndOtherHorizons(t *testing.T) {
	ctx := context.Background()
	st := openTemp(t)
	sym, err := st.UpsertSymbol(ctx, "SPY", md.Stocks, "")
	if err != nil {
		t.Fatalf("UpsertSymbol: %v", err)
	}

	ts := int64(86400) // day 1

	// Resolved H1d row (should be returned)
	mustPredict(t, st, sym.ID, md.H1d, ts, 0.6, map[string]float64{"pressure_score": 0.4, "pred_raw": 0.7})
	if err := st.ResolvePrediction(ctx, sym.ID, md.H1d, ts, 0.02); err != nil {
		t.Fatalf("ResolvePrediction resolved H1d: %v", err)
	}

	// Unresolved H1d row (predict but never resolve — should NOT be returned)
	mustPredict(t, st, sym.ID, md.H1d, ts+3600, 0.7, map[string]float64{"pressure_score": 0.5, "pred_raw": 0.8})
	// Note: deliberately NOT calling ResolvePrediction

	// Resolved H1w row (different horizon — should NOT be returned)
	mustPredict(t, st, sym.ID, md.H1w, ts, 0.55, map[string]float64{"pressure_score": 0.3, "pred_raw": 0.6})
	if err := st.ResolvePrediction(ctx, sym.ID, md.H1w, ts, 0.03); err != nil {
		t.Fatalf("ResolvePrediction resolved H1w: %v", err)
	}

	// Query H1d — only the single resolved H1d row should appear
	rows, ceilingBound, err := st.LabeledFeaturesRecentDays(ctx, md.H1d, 10, 100)
	if err != nil {
		t.Fatalf("LabeledFeaturesRecentDays: %v", err)
	}
	if ceilingBound {
		t.Fatalf("ceilingBound=true, expected false")
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row (resolved H1d only), got %d", len(rows))
	}
	if rows[0].Horizon != md.H1d {
		t.Fatalf("returned row has horizon %v, expected H1d", rows[0].Horizon)
	}
}
