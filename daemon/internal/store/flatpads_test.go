package store

import (
	"context"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// seedFlat writes daily bars for one symbol. flat bars are open=high=low=close.
func seedFlat(t *testing.T, st *Store, id int64, from, to int64, px, vol float64) {
	t.Helper()
	var bars []md.Bar
	for d := from; d <= to; d++ {
		bars = append(bars, md.Bar{
			SymbolID: id, TF: md.TF1d, Ts: d * 86400,
			Open: px, High: px, Low: px, Close: px, Volume: vol,
		})
	}
	if err := st.UpsertBars(context.Background(), bars); err != nil {
		t.Fatalf("seed flat: %v", err)
	}
}

func seedReal(t *testing.T, st *Store, id int64, from, to int64) {
	t.Helper()
	var bars []md.Bar
	for d := from; d <= to; d++ {
		px := 100 + float64(d%7)
		bars = append(bars, md.Bar{
			SymbolID: id, TF: md.TF1d, Ts: d * 86400,
			Open: px, High: px * 1.02, Low: px * 0.98, Close: px * 1.01, Volume: 250_000,
		})
	}
	if err := st.UpsertBars(context.Background(), bars); err != nil {
		t.Fatalf("seed real: %v", err)
	}
}

// The predicate must catch a genuine constant-price pad and NOTHING ELSE. Each
// negative case below corresponds to one clause; delete a clause and one of them
// starts failing.
func TestFlatPadRuns_CatchesPadsAndSparesEverythingElse(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "AAA", md.Stocks, "")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	seedReal(t, st, sym.ID, 1, 10)           // real trading
	seedFlat(t, st, sym.ID, 11, 40, 50.0, 0) // THE PAD: 30 sessions, one price, no volume
	seedReal(t, st, sym.ID, 41, 50)          // real again — the pad is INTERIOR
	seedFlat(t, st, sym.ID, 55, 55, 61.0, 0) // isolated thin session: a run of one
	for d := int64(60); d <= 79; d++ {       // 20 flat zero-volume days at VARYING prices
		seedFlat(t, st, sym.ID, d, d, 70.0+float64(d), 0)
	}
	seedFlat(t, st, sym.ID, 90, 119, 88.0, 400_000) // 30 flat days that DID trade

	runs, err := st.FlatPadRuns(ctx, string(md.TF1d), 20)
	if err != nil {
		t.Fatalf("FlatPadRuns: %v", err)
	}
	if len(runs) != 1 {
		for _, r := range runs {
			t.Logf("run: %s %d bars at %.2f, %d..%d", r.Symbol, r.Bars, r.Close, r.FromTs/86400, r.ToTs/86400)
		}
		t.Fatalf("got %d run(s), want exactly 1 — the pad", len(runs))
	}
	got := runs[0]
	if got.Bars != 30 || got.Close != 50.0 {
		t.Fatalf("run = %d bars at %.2f, want 30 at 50.00", got.Bars, got.Close)
	}
	if got.FromTs != 11*86400 || got.ToTs != 40*86400 {
		t.Fatalf("run spans days %d..%d, want 11..40 — a run must not bleed past a real bar",
			got.FromTs/86400, got.ToTs/86400)
	}
}

// A single flat zero-volume day is an ordinary thin session, so minRun must be
// meaningful and must be refused below 2.
func TestFlatPadRuns_RefusesADegenerateMinRun(t *testing.T) {
	st := openTestStore(t)
	if _, err := st.FlatPadRuns(context.Background(), string(md.TF1d), 1); err == nil {
		t.Fatal("minRun=1 must be refused: it would quarantine every thin session")
	}
}

// THE INVARIANT THAT MATTERS. The dry run reports one set and the apply moves
// another only if their predicates drift — and they are two separate SQL
// statements, so nothing but a test keeps them together.
func TestQuarantineFlatPads_MovesExactlyWhatTheDryRunReported(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	a, _ := st.UpsertSymbol(ctx, "AAA", md.Stocks, "")
	b, _ := st.UpsertSymbol(ctx, "BBB", md.Stocks, "")

	seedReal(t, st, a.ID, 1, 10)
	seedFlat(t, st, a.ID, 11, 40, 50.0, 0) // 30-bar pad
	seedReal(t, st, a.ID, 41, 50)
	seedFlat(t, st, a.ID, 55, 55, 61.0, 0) // isolated: must survive
	seedReal(t, st, b.ID, 1, 5)
	seedFlat(t, st, b.ID, 6, 30, 12.5, 0) // 25-bar pad on a second symbol

	runs, err := st.FlatPadRuns(ctx, string(md.TF1d), 20)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	reported := 0
	for _, r := range runs {
		reported += r.Bars
	}
	if reported != 55 {
		t.Fatalf("dry run reported %d bar(s) across %d run(s), want 55", reported, len(runs))
	}

	before := countBars(t, st)
	moved, err := st.QuarantineFlatPads(ctx, string(md.TF1d), 20, "run-1", "vendor flat pad", 1_700_000_000)
	if err != nil {
		t.Fatalf("quarantine: %v", err)
	}
	if int(moved) != reported {
		t.Fatalf("apply moved %d bar(s) but the dry run reported %d — the two predicates have drifted, "+
			"so the report describes a different set than the one that moves", moved, reported)
	}
	if after := countBars(t, st); after != before-reported {
		t.Fatalf("bars went %d -> %d, want a drop of exactly %d", before, after, reported)
	}

	// The isolated thin session and every real bar must still be there.
	if n := countBarsFor(t, st, a.ID, 55*86400); n != 1 {
		t.Fatal("the isolated flat zero-volume day was quarantined: a run of one is a thin session, not a pad")
	}
	if n := countBarsFor(t, st, a.ID, 45*86400); n != 1 {
		t.Fatal("a real bar was quarantined")
	}
	// And a second pass must find nothing left to move.
	again, err := st.FlatPadRuns(ctx, string(md.TF1d), 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 0 {
		t.Fatalf("%d run(s) still qualify after the apply", len(again))
	}

	// Quarantine is reversible — that is the whole reason it was chosen over DELETE.
	restored, err := st.RestoreQuarantinedBars(ctx, "run-1")
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if int(restored) != reported {
		t.Fatalf("restored %d, want %d", restored, reported)
	}
	if after := countBars(t, st); after != before {
		t.Fatalf("bars after restore = %d, want the original %d", after, before)
	}
}

func countBars(t *testing.T, st *Store) int {
	t.Helper()
	var n int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM bars WHERE tf='1d'`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

func countBarsFor(t *testing.T, st *Store, symbolID, ts int64) int {
	t.Helper()
	var n int
	if err := st.DB().QueryRow(
		`SELECT COUNT(*) FROM bars WHERE tf='1d' AND symbol_id=? AND ts=?`, symbolID, ts).Scan(&n); err != nil {
		t.Fatalf("count one: %v", err)
	}
	return n
}
