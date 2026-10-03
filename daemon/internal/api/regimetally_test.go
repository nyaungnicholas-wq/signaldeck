package api

import (
	"fmt"
	"math"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// The regime section of /api/track-record read graded calls through
// ResolvedRegimeOutcomes(ctx, 50000), newest first, so past 50,000 graded
// calls (42,333 on 2026-10-01) every per-kind count undercounted. It now
// tallies in SQL. This fixture holds 50,400 graded calls, more than the old
// cap, plus open calls and one superseded duplicate that must not count.
func TestRegimeTrackRecordCountsEveryGradedCall(t *testing.T) {
	_, st, d := newTestServer(t, nil)
	ctx := t.Context()
	dayBase := store.GradingEpoch / 86400
	kinds := []string{"trend21", "liquidity21", "vol21"}

	var syms []md.Symbol
	for i := 0; i < 60; i++ {
		s, err := st.UpsertSymbol(ctx, fmt.Sprintf("RT%02d", i), md.Stocks, "")
		if err != nil {
			t.Fatal(err)
		}
		syms = append(syms, s)
	}

	const ins = `INSERT INTO regime_outcomes (symbol_id, kind, ts, day, horizon_days, regime,
		conviction, historical_accuracy, rank, resolved_at, actual, correct, naive_label, superseded_by)
		VALUES (?,?,?,?,21,'uptrend',0.9,0.8,0.9,?,?,?,'uptrend',?)`
	tx, err := st.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	stmt, err := tx.PrepareContext(ctx, ins)
	if err != nil {
		t.Fatal(err)
	}
	wantN, wantCorrect := map[string]int{}, map[string]int{}
	for dd := 0; dd < 280; dd++ {
		day := dayBase + int64(dd)
		ts := day*86400 + 3600
		for s := range syms {
			for k, kind := range kinds {
				correct := 0
				if (dd+s+k)%4 != 0 {
					correct = 1
				}
				if _, err := stmt.ExecContext(ctx, syms[s].ID, kind, ts, day, ts+30*86400, "uptrend", correct, nil); err != nil {
					t.Fatal(err)
				}
				wantN[kind]++
				wantCorrect[kind] += correct
			}
		}
	}
	// Open calls: not graded, not counted.
	openDay := dayBase + 300
	for s := 0; s < 5; s++ {
		if _, err := stmt.ExecContext(ctx, syms[s].ID, "trend21", openDay*86400+3600, openDay, nil, nil, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	stmt.Close() //nolint:errcheck,gosec
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	// A superseded duplicate of (RT00, trend21, dayBase): an hour NEWER than the
	// winner, as in production (the dedup keeps the earliest call), and graded
	// the other way. One call per (symbol, kind, day), and never the retired one.
	var winner int64
	if err := st.DB().QueryRowContext(ctx, `SELECT id FROM regime_outcomes
		WHERE symbol_id=? AND kind='trend21' AND day=? AND superseded_by IS NULL`, syms[0].ID, dayBase).Scan(&winner); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().ExecContext(ctx, ins, syms[0].ID, "trend21", dayBase*86400+7200, dayBase,
		dayBase*86400+30*86400, "downtrend", 1, winner); err != nil {
		t.Fatal(err)
	}

	// The premise: the old read could not see every graded call.
	if rows, err := st.ResolvedRegimeOutcomes(ctx, 50000); err != nil || len(rows) != 50000 {
		t.Fatalf("ResolvedRegimeOutcomes(50000) = %d rows, %v; want the cap, 50000", len(rows), err)
	}

	m := d.regimeTrackRecord(ctx)
	byKind, ok := m["kinds"].(map[string]any)
	if m["available"] != true || !ok {
		t.Fatalf("regime section unavailable: %v", m)
	}
	total := 0
	for _, kind := range kinds {
		e, _ := byKind[kind].(map[string]any)
		n, _ := e["resolvedN"].(int)
		total += n
		if n != wantN[kind] {
			t.Errorf("%s resolvedN = %d, want %d", kind, n, wantN[kind])
		}
		acc, _ := e["liveAccuracy"].(float64)
		if want := float64(wantCorrect[kind]) / float64(wantN[kind]); math.Abs(acc-want) > 1e-12 {
			t.Errorf("%s liveAccuracy = %v, want %v", kind, acc, want)
		}
	}
	if total != 50400 {
		t.Errorf("resolvedN over the three kinds = %d, want 50400 (every graded call, past the old 50,000 cap)", total)
	}
}
