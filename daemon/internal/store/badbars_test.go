package store

import (
	"context"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// barAt writes one daily bar with an explicit close, so a test can plant the
// vendor's zero-priced rows.
func barAt(t *testing.T, st *Store, symbolID, ts int64, close, vol float64) {
	t.Helper()
	if _, err := st.w.Exec(
		`INSERT OR REPLACE INTO bars (symbol_id, tf, ts, open, high, low, close, volume)
		 VALUES (?, '1d', ?, ?, ?, ?, ?, ?)`,
		symbolID, ts, close, close, close, close, vol); err != nil {
		t.Fatal(err)
	}
}

func TestPurgeRemovesPricelessBarsAndRedatesTheSymbol(t *testing.T) {
	ctx := context.Background()
	st := newPITStore(t)
	s, _ := st.UpsertSymbol(ctx, "LDTC", md.Stocks, "Shell then real company")
	// The LDTC shape: volume-bearing, price-less shell bars, then the real series.
	for i := int64(0); i < 3; i++ {
		barAt(t, st, s.ID, d(i), 0.0, 800)
	}
	for i := int64(3); i < 6; i++ {
		barAt(t, st, s.ID, d(i), 6.08, 8812)
	}
	if _, err := st.w.Exec(`UPDATE symbols SET added_at=? WHERE id=?`, d(0), s.ID); err != nil {
		t.Fatal(err)
	}

	deleted, redated, err := st.PurgeNonPositiveBars(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 3 || redated != 1 {
		t.Fatalf("deleted=%d redated=%d, want 3 and 1", deleted, redated)
	}
	if n := count(t, st, `SELECT COUNT(*) FROM bars WHERE symbol_id=?`, s.ID); n != 3 {
		t.Fatalf("%d bars left, want the 3 real ones", n)
	}
	// added_at must now be the first REAL bar, not the first priceless one —
	// otherwise the symbol sits in historical universes on days it had no price.
	if got := count(t, st, `SELECT added_at FROM symbols WHERE id=?`, s.ID); got != d(3) {
		t.Fatalf("added_at=%d, want the first priced bar %d", got, d(3))
	}
}

func TestPurgeIsIdempotentAndLeavesGoodBarsAlone(t *testing.T) {
	ctx := context.Background()
	st := newPITStore(t)
	s, _ := st.UpsertSymbol(ctx, "GOOD", md.Stocks, "Clean series")
	for i := int64(0); i < 4; i++ {
		barAt(t, st, s.ID, d(i), 10.0, 100)
	}
	before := count(t, st, `SELECT added_at FROM symbols WHERE id=?`, s.ID)

	deleted, redated, err := st.PurgeNonPositiveBars(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 0 || redated != 0 {
		t.Fatalf("a clean database lost %d bars and re-dated %d symbols", deleted, redated)
	}
	if count(t, st, `SELECT added_at FROM symbols WHERE id=?`, s.ID) != before {
		t.Fatal("purge moved added_at on a symbol it should not have touched")
	}
	if n := count(t, st, `SELECT COUNT(*) FROM bars WHERE symbol_id=?`, s.ID); n != 4 {
		t.Fatalf("%d bars survive, want 4", n)
	}
}

// A symbol whose bars are ALL priceless must not be re-dated to the epoch,
// which would put it in every historical universe it never traded in.
func TestPurgeDoesNotEpochDateASymbolLeftWithNoBars(t *testing.T) {
	ctx := context.Background()
	st := newPITStore(t)
	s, _ := st.UpsertSymbol(ctx, "EMPTY", md.Stocks, "All priceless")
	for i := int64(0); i < 3; i++ {
		barAt(t, st, s.ID, d(i), 0.0, 500)
	}
	if _, err := st.w.Exec(`UPDATE symbols SET added_at=? WHERE id=?`, d(0), s.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.PurgeNonPositiveBars(ctx, 0); err != nil {
		t.Fatal(err)
	}
	if got := count(t, st, `SELECT added_at FROM symbols WHERE id=?`, s.ID); got != d(0) {
		t.Fatalf("added_at=%d — a symbol with no surviving bars was re-dated", got)
	}
}
