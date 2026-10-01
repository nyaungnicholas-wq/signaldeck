package store

import (
	"context"
	"reflect"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// limit < 0 reads the whole graded window; a positive limit still caps it. The
// published track record passes -1: a fixed 120,000 cap had become binding and
// silently trimmed the oldest graded days. 20,001 rows clears the old 20,000
// default that a non-positive limit used to fall back to.
func TestResolvedPredictionOutcomesWholeWindow(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "WIN", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}
	const n = 20001
	if _, err := st.w.ExecContext(ctx, `
		INSERT INTO prediction_outcomes (symbol_id, horizon, ts, prob, up, fwd_return, resolved_at)
		WITH RECURSIVE c(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM c WHERE i < ?)
		SELECT ?, '1w', ? + i*60, 0.6, 1, 0.01, ? + i*60 FROM c`,
		n, sym.ID, GradingEpochTS, GradingEpochTS+604800); err != nil {
		t.Fatal(err)
	}
	all, err := st.ResolvedPredictionOutcomes(ctx, md.H1w, -1)
	if err != nil || len(all) != n {
		t.Fatalf("whole window = %d rows, %v; want %d", len(all), err, n)
	}
	two, err := st.ResolvedPredictionOutcomes(ctx, md.H1w, 2)
	if err != nil || len(two) != 2 || two[0].Ts < two[1].Ts {
		t.Fatalf("capped = %d rows, %v; want the 2 newest", len(two), err)
	}
}

// SD-48: the track record and fleetEdgeSkill read the graded window collapsed
// IN SQL. IndependentPredictionOutcomes must be exactly what those callers used
// to build in Go from ResolvedPredictionOutcomes(h, -1) — the first (newest) row
// per (symbol, md.SettleDay), in the same order — with rawN the old len(rows),
// and no row cap. The fixture has several rows per symbol per settle day, ts
// ties across symbols (ids not in name order), settle_ts both NULL and folding
// two later days onto an earlier bar, every excluded row shape, and 20,001 bulk
// rows (past the old 20,000 default).
func TestIndependentPredictionOutcomesIsTheGoCollapse(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()
	ins := func(symID int64, h string, ts int64, settle, resolved, up any) {
		t.Helper()
		if _, err := st.w.ExecContext(ctx, `
			INSERT INTO prediction_outcomes (symbol_id, horizon, ts, prob, up, fwd_return, resolved_at, settle_ts)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			symID, h, ts, 0.3+float64(ts%37)/100, up, float64(ts%23-11)/1000, resolved, settle); err != nil {
			t.Fatal(err)
		}
	}
	var ids []int64
	for _, name := range []string{"ZZZ", "AAA", "MMM", "BULK"} {
		sym, err := st.UpsertSymbol(ctx, name, md.Stocks, "")
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, sym.ID)
	}
	monday := int64(GradingEpochTS) + 3*86400 // 2026-08-10 00:00 UTC
	wantRaw := 0
	for d := int64(0); d < 5; d++ {
		bar := monday + d*86400 + 13*3600 + 1800
		for _, off := range []int64{14 * 3600, 16 * 3600, 19 * 3600, 22*3600 + 2400} {
			ts := monday + d*86400 + off
			for i, id := range ids[:3] {
				var settle any // ZZZ: NULL, folds on the calendar day
				switch {
				case i == 1:
					settle = bar
				case i == 2 && d >= 3:
					settle = monday + 2*86400 + 13*3600 + 1800 // MMM days 3-4 settle on day 2's bar
				case i == 2:
					settle = bar
				}
				ins(id, "1w", ts, settle, ts+604800, int(ts%2))
				wantRaw++
			}
		}
	}
	// Excluded from both paths: unresolved, resolved without an outcome,
	// pre-epoch, another horizon, and a row whose symbol does not exist.
	ins(ids[0], "1w", monday+5*86400+50000, nil, nil, nil)
	ins(ids[0], "1w", monday+5*86400+50060, nil, monday+6*86400, nil)
	ins(ids[0], "1w", int64(GradingEpochTS)-60, nil, monday, 1)
	ins(ids[0], "1d", monday+50000, nil, monday+86400, 1)
	ins(999999, "1w", monday+50000, nil, monday+604800, 1)

	const bulk = 20001
	if _, err := st.w.ExecContext(ctx, `
		INSERT INTO prediction_outcomes (symbol_id, horizon, ts, prob, up, fwd_return, resolved_at)
		WITH RECURSIVE c(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM c WHERE i < ?)
		SELECT ?, '1w', ? + i*60, 0.4 + (i % 20) / 100.0, i % 2, (i % 7 - 3) / 100.0, ? + i*60 FROM c`,
		bulk, ids[3], GradingEpochTS, GradingEpochTS+604800); err != nil {
		t.Fatal(err)
	}
	wantRaw += bulk
	bulkDays := map[int64]bool{}
	for i := int64(1); i <= bulk; i++ {
		bulkDays[md.TradingDay(int64(GradingEpochTS)+i*60)] = true
	}

	got, rawN, err := st.IndependentPredictionOutcomes(ctx, md.H1w)
	if err != nil {
		t.Fatal(err)
	}
	if rawN != wantRaw {
		t.Fatalf("rawN = %d, want %d graded rows (every one, no cap)", rawN, wantRaw)
	}
	perSym := map[int64]int{}
	for _, o := range got {
		perSym[o.SymbolID]++
	}
	want := map[int64]int{ids[0]: 5, ids[1]: 5, ids[2]: 3, ids[3]: len(bulkDays)}
	if !reflect.DeepEqual(perSym, want) {
		t.Fatalf("independent rows per symbol = %v, want %v", perSym, want)
	}

	// The old path, verbatim: whole window, then first-seen per key in Go.
	raw, err := st.ResolvedPredictionOutcomes(ctx, md.H1w, -1)
	if err != nil || len(raw) != wantRaw {
		t.Fatalf("old path read %d rows, %v; want %d", len(raw), err, wantRaw)
	}
	seen := map[[2]int64]bool{}
	var old []ResolvedPredictionOutcome
	for _, o := range raw {
		key := [2]int64{o.SymbolID, md.SettleDay(o.SettleTs, o.Ts)}
		if seen[key] {
			continue
		}
		seen[key] = true
		old = append(old, o)
	}
	if len(got) != len(old) {
		t.Fatalf("SQL collapse = %d rows, Go collapse = %d", len(got), len(old))
	}
	for i := range old {
		if got[i] != old[i] {
			t.Fatalf("row %d: SQL collapse %+v, Go collapse %+v", i, got[i], old[i])
		}
	}
}
