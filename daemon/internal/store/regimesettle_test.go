package store

import (
	"context"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/structregime"
)

// TestRegimeOutcomeDedupesOnTheSettledMove pins the WRITE-path half of the
// settled-move fold. regime_outcomes.day is a unique dedup key, so the fold
// decides what gets stored, not merely what gets counted: a Friday-evening,
// Saturday and Sunday call all describe the same Friday base bar and must
// collapse to ONE frozen row.
func TestRegimeOutcomeDedupesOnTheSettledMove(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()

	sym, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	if err != nil {
		t.Fatalf("UpsertSymbol: %v", err)
	}
	// ONE daily bar: the settled move all three calls describe.
	const fridayBar = int64(1784001600)
	if err := st.UpsertBars(ctx, []md.Bar{{
		SymbolID: sym.ID, TF: md.TF1d, Ts: fridayBar,
		Open: 100, High: 101, Low: 99, Close: 100, Volume: 1000,
	}}); err != nil {
		t.Fatalf("UpsertBars: %v", err)
	}

	friCall := int64(1784042580) // Friday, after the close
	base := RegimeCall{
		SymbolID: sym.ID, Kind: structregime.Kind("trend21"), HorizonDays: 21,
		Regime: "uptrend", Conviction: 0.9, HistoricalAccuracy: 0.62, Rank: 0.8,
		NaiveLabel: "uptrend",
	}

	fresh := 0
	for i := 0; i < 3; i++ { // Friday evening, Saturday, Sunday
		c := base
		c.Ts = friCall + int64(i)*86400
		ok, err := st.InsertRegimeOutcome(ctx, c)
		if err != nil {
			t.Fatalf("InsertRegimeOutcome(%d): %v", i, err)
		}
		if ok {
			fresh++
		}
	}
	if fresh != 1 {
		t.Fatalf("froze %d rows, want 1: Fri/Sat/Sun describe ONE Friday base bar; "+
			"%d means the calendar fold is back and the dedup key admits duplicates", fresh, fresh)
	}

	var n int
	if err := st.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM regime_outcomes WHERE superseded_by IS NULL`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("%d live rows, want 1", n)
	}

	// The stored row carries the key it was deduped under.
	var settle, day int64
	if err := st.db.QueryRowContext(ctx,
		`SELECT settle_ts, day FROM regime_outcomes`).Scan(&settle, &day); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if settle != fridayBar {
		t.Fatalf("settle_ts = %d, want %d", settle, fridayBar)
	}
	if want := md.SettleDay(fridayBar, friCall); day != want {
		t.Fatalf("day = %d, want %d", day, want)
	}
}

// TestRegimeSettleMigrationSupersedesRatherThanDeletes pins the invariant that
// makes the re-fold safe to run on the pre-registration audit trail: a row that
// loses the corrected dedup keeps its bytes and is marked superseded_by. If it
// were deleted instead, the table would be a file drawer — ungraded forecasts
// dropped because the key that admitted them was wrong.
func TestRegimeSettleMigrationSupersedesRatherThanDeletes(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()

	sym, err := st.UpsertSymbol(ctx, "MSFT", md.Stocks, "Microsoft")
	if err != nil {
		t.Fatalf("UpsertSymbol: %v", err)
	}
	const fridayBar = int64(1784001600)
	if err := st.UpsertBars(ctx, []md.Bar{{
		SymbolID: sym.ID, TF: md.TF1d, Ts: fridayBar,
		Open: 100, High: 101, Low: 99, Close: 100, Volume: 1000,
	}}); err != nil {
		t.Fatalf("UpsertBars: %v", err)
	}

	// Write three rows straight past the dedup index, as the OLD calendar fold
	// would have: distinct trading days, one settled move.
	friCall := int64(1784042580)
	for i := 0; i < 3; i++ {
		ts := friCall + int64(i)*86400
		if _, err := st.w.ExecContext(ctx, `
			INSERT INTO regime_outcomes
			  (symbol_id, kind, ts, day, horizon_days, regime, conviction,
			   historical_accuracy, rank, naive_label)
			VALUES (?,?,?,?,?,?,?,?,?,?)`,
			sym.ID, "trend21", ts, md.TradingDay(ts), 21, "uptrend",
			0.9, 0.62, 0.8, "uptrend"); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}

	// Re-run the fold the migration performs.
	if _, err := st.w.ExecContext(ctx, `DROP INDEX IF EXISTS idx_regime_outcomes_dedup`); err != nil {
		t.Fatalf("drop index: %v", err)
	}
	if _, err := st.w.ExecContext(ctx, `
		UPDATE regime_outcomes SET settle_ts = (
		  SELECT MAX(b.ts) FROM bars b
		   WHERE b.symbol_id = regime_outcomes.symbol_id
		     AND b.tf='1d' AND b.ts <= regime_outcomes.ts)`); err != nil {
		t.Fatalf("settle_ts: %v", err)
	}
	if _, err := st.w.ExecContext(ctx,
		`UPDATE regime_outcomes SET day = settle_day(settle_ts, ts)`); err != nil {
		t.Fatalf("refold: %v", err)
	}
	if _, err := st.w.ExecContext(ctx, `
		CREATE TEMP TABLE k AS
		  SELECT id FROM (SELECT id, ROW_NUMBER() OVER (
		    PARTITION BY symbol_id, kind, day ORDER BY ts ASC, id ASC) rn
		    FROM regime_outcomes WHERE superseded_by IS NULL) WHERE rn=1`); err != nil {
		t.Fatalf("winners: %v", err)
	}
	if _, err := st.w.ExecContext(ctx, `
		UPDATE regime_outcomes AS r SET superseded_by = (
		  SELECT k.id FROM k JOIN regime_outcomes kw ON kw.id=k.id
		   WHERE kw.symbol_id=r.symbol_id AND kw.kind=r.kind AND kw.day=r.day)
		 WHERE r.superseded_by IS NULL AND r.id NOT IN (SELECT id FROM k)`); err != nil {
		t.Fatalf("supersede: %v", err)
	}

	var total, live int
	if err := st.db.QueryRowContext(ctx, `
		SELECT COUNT(*), SUM(CASE WHEN superseded_by IS NULL THEN 1 ELSE 0 END)
		  FROM regime_outcomes`).Scan(&total, &live); err != nil {
		t.Fatalf("count: %v", err)
	}
	if total != 3 {
		t.Fatalf("total = %d, want 3: the re-fold must not DELETE an audit-trail row", total)
	}
	if live != 1 {
		t.Fatalf("live = %d, want 1: the settled move is one observation", live)
	}

	// The winner is the EARLIEST call, reproducing what INSERT OR IGNORE would
	// have written had the key been right from the start.
	var winnerTs int64
	if err := st.db.QueryRowContext(ctx,
		`SELECT ts FROM regime_outcomes WHERE superseded_by IS NULL`).Scan(&winnerTs); err != nil {
		t.Fatalf("winner: %v", err)
	}
	if winnerTs != friCall {
		t.Fatalf("winner ts = %d, want the earliest call %d", winnerTs, friCall)
	}
}
