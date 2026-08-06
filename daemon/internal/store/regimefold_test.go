package store

import (
	"context"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// insertRawOutcome writes one regime_outcomes row with an EXPLICIT day, so a
// test can reproduce the pre-migration shape (day = ts/86400) that the fold has
// to repair.
func insertRawOutcome(t *testing.T, st *Store, symbolID int64, kind string, ts, day int64, regime string) int64 {
	t.Helper()
	res, err := st.w.ExecContext(context.Background(), `
		INSERT INTO regime_outcomes
		  (symbol_id, kind, ts, day, horizon_days, regime, conviction,
		   historical_accuracy, rank, naive_label)
		VALUES (?,?,?,?,?,?,?,?,?,?)`,
		symbolID, kind, ts, day, 21, regime, 0.5, 0.6, 1.0, "flat")
	if err != nil {
		t.Fatalf("insert ts=%d day=%d: %v", ts, day, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// TestRegimeOutcomesTradingDayFold_KeepsBothRows is the guard on the one
// migration in this tree that could destroy evidence.
//
// A 21:00Z call and a 01:00Z call are the SAME trading day, and under the old
// ts/86400 key they were frozen as two rows. Re-folding collides them. The
// migration must merge the OBSERVATION without deleting the RECORD: this table
// is the pre-registration audit trail, so a repair that drops a frozen row is a
// worse defect than the miscount it fixes.
func TestRegimeOutcomesTradingDayFold_KeepsBothRows(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	sym, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}

	// A fresh store already has the post-fold shape from schema.sql, so the
	// migration would no-op and this test would prove nothing. Put the table
	// back into the PRE-fold shape — no superseded_by, total unique index — so
	// what runs below is the real upgrade path an existing database takes.
	for _, s := range []string{
		`DROP INDEX IF EXISTS idx_regime_outcomes_dedup`,
		`ALTER TABLE regime_outcomes DROP COLUMN superseded_by`,
		`CREATE UNIQUE INDEX idx_regime_outcomes_dedup
		   ON regime_outcomes (symbol_id, kind, day)`,
	} {
		if _, err := st.w.ExecContext(ctx, s); err != nil {
			t.Fatalf("downgrade to pre-fold shape (%s): %v", s, err)
		}
	}

	base := int64(20000) * md.SecondsPerDay
	early := base + 21*3600      // 21:00Z — trading day 20000
	late := base + 24*3600 + 900 // 00:15Z next UTC day — SAME trading day
	if md.TradingDay(early) != md.TradingDay(late) {
		t.Fatalf("fixture broken: %d and %d are different trading days", early, late)
	}

	earlyID := insertRawOutcome(t, st, sym.ID, "trend21", early, early/md.SecondsPerDay, "uptrend")
	lateID := insertRawOutcome(t, st, sym.ID, "trend21", late, late/md.SecondsPerDay, "uptrend")
	// A row on a genuinely different trading day must be left alone.
	otherID := insertRawOutcome(t, st, sym.ID, "trend21",
		base+3*md.SecondsPerDay+18*3600, (base+3*md.SecondsPerDay+18*3600)/md.SecondsPerDay, "downtrend")

	if err := migrateRegimeOutcomesToTradingDay(st.w); err != nil {
		t.Fatalf("migration: %v", err)
	}

	// NOTHING may be deleted.
	var n int
	if err := st.w.QueryRowContext(ctx, `SELECT COUNT(*) FROM regime_outcomes`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("migration left %d rows, want all 3 — it must never delete a frozen row", n)
	}

	// The EARLIER call is the observation; the later one is superseded by it.
	var earlySup, lateSup, otherSup any
	row := func(id int64, dst *any) {
		if err := st.w.QueryRowContext(ctx,
			`SELECT superseded_by FROM regime_outcomes WHERE id=?`, id).Scan(dst); err != nil {
			t.Fatal(err)
		}
	}
	row(earlyID, &earlySup)
	row(lateID, &lateSup)
	row(otherID, &otherSup)
	if earlySup != nil {
		t.Fatalf("the earliest call of the trading day must survive as the observation, got superseded_by=%v", earlySup)
	}
	if lateSup == nil {
		t.Fatal("the later call of the same trading day must be marked superseded")
	}
	if got, ok := lateSup.(int64); !ok || got != earlyID {
		t.Fatalf("superseded_by = %v, want the earlier row's id %d", lateSup, earlyID)
	}
	if otherSup != nil {
		t.Fatalf("a row on its own trading day must not be superseded, got %v", otherSup)
	}

	// The day column is re-folded for every row, superseded ones included.
	var earlyDay, lateDay int64
	if err := st.w.QueryRowContext(ctx, `SELECT day FROM regime_outcomes WHERE id=?`, earlyID).Scan(&earlyDay); err != nil {
		t.Fatal(err)
	}
	if err := st.w.QueryRowContext(ctx, `SELECT day FROM regime_outcomes WHERE id=?`, lateID).Scan(&lateDay); err != nil {
		t.Fatal(err)
	}
	if earlyDay != md.TradingDay(early) || lateDay != md.TradingDay(late) {
		t.Fatalf("day not re-folded: early=%d late=%d, want %d for both",
			earlyDay, lateDay, md.TradingDay(early))
	}

	// The partial index must still forbid a SECOND live row on that key...
	if _, err := st.w.ExecContext(ctx, `
		INSERT INTO regime_outcomes
		  (symbol_id, kind, ts, day, horizon_days, regime, conviction, historical_accuracy, rank)
		VALUES (?,?,?,?,?,?,?,?,?)`,
		sym.ID, "trend21", early+60, md.TradingDay(early), 21, "uptrend", 0.5, 0.6, 1.0); err == nil {
		t.Fatal("the partial unique index must still reject a second LIVE row on (symbol, kind, day)")
	}

	// ...and running the migration again must be a no-op.
	if err := migrateRegimeOutcomesToTradingDay(st.w); err != nil {
		t.Fatalf("second run must be idempotent: %v", err)
	}
	if err := st.w.QueryRowContext(ctx, `SELECT COUNT(*) FROM regime_outcomes`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("re-running the migration changed the row count to %d", n)
	}
}
