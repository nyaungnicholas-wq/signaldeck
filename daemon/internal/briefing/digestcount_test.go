package briefing

import (
	"fmt"
	"testing"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// The digest's per-kind record read ResolvedRegimeOutcomes(ctx, 50000) and
// counted raw rows: past 50,000 graded calls it undercounted, and a superseded
// row counted twice. It now reads the track record's deduplicated tallies.
// This fixture holds 50,400 graded calls (more than the old cap), open calls,
// and one superseded duplicate graded the other way inside the week.
func TestDigestCountsEveryGradedCallOnce(t *testing.T) {
	st := digestStore(t)
	ctx := t.Context()
	dayBase := store.GradingEpoch / 86400
	kinds := []string{"trend21", "liquidity21", "vol21"}
	// The week ends 30 days after the last call day, so calls of the last 7 call
	// days resolve inside it.
	now := time.Unix((dayBase+280+30)*86400, 0).UTC()
	since := now.Add(-7 * 24 * time.Hour).Unix()

	var syms []md.Symbol
	for i := 0; i < 60; i++ {
		s, err := st.UpsertSymbol(ctx, fmt.Sprintf("DG%02d", i), md.Stocks, "")
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
	want := map[string]DigestKindRecord{}
	for dd := 0; dd < 280; dd++ {
		day := dayBase + int64(dd)
		ts, resolved := day*86400+3600, day*86400+3600+30*86400
		for s := range syms {
			for k, kind := range kinds {
				correct := 0
				if (dd+s+k)%4 != 0 {
					correct = 1
				}
				if _, err := stmt.ExecContext(ctx, syms[s].ID, kind, ts, day, resolved, "uptrend", correct, nil); err != nil {
					t.Fatal(err)
				}
				w := want[kind]
				w.Kind, w.Resolved, w.Correct = kind, w.Resolved+1, w.Correct+correct
				if resolved >= since {
					w.NewThisWk++
				}
				want[kind] = w
			}
		}
	}
	for s := 0; s < 5; s++ { // open calls: not graded, not counted
		day := dayBase + 300
		if _, err := stmt.ExecContext(ctx, syms[s].ID, "trend21", day*86400+3600, day, nil, nil, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	stmt.Close() //nolint:errcheck,gosec
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	// A superseded duplicate of (DG00, trend21, last call day): an hour NEWER
	// than the winner, as in production (the dedup keeps the earliest call),
	// graded the other way, resolved inside the week.
	lastDay := dayBase + 279
	var winner int64
	if err := st.DB().QueryRowContext(ctx, `SELECT id FROM regime_outcomes
		WHERE symbol_id=? AND kind='trend21' AND day=? AND superseded_by IS NULL`, syms[0].ID, lastDay).Scan(&winner); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().ExecContext(ctx, ins, syms[0].ID, "trend21", lastDay*86400+7200, lastDay,
		lastDay*86400+30*86400, "downtrend", 0, winner); err != nil {
		t.Fatal(err)
	}
	if rows, err := st.ResolvedRegimeOutcomes(ctx, 50000); err != nil || len(rows) != 50000 {
		t.Fatalf("ResolvedRegimeOutcomes(50000) = %d rows, %v; want the cap, 50000", len(rows), err)
	}

	f, err := CollectDigestFacts(ctx, st, now)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]DigestKindRecord{}
	for _, r := range f.Records {
		got[r.Kind] = r
	}
	for _, kind := range kinds {
		if got[kind] != want[kind] {
			t.Errorf("%s record = %+v, want %+v", kind, got[kind], want[kind])
		}
	}
	if want["trend21"].Resolved != 16800 || want["trend21"].NewThisWk != 7*60 {
		t.Fatalf("fixture drifted: %+v", want["trend21"])
	}
}
