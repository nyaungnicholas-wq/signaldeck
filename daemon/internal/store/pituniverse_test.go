package store

import (
	"context"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// The defect these pin: universe_membership held zero rows, so every
// cross-sectional denominator was reconstructed at read time from stamps that
// have been wrong before. The three properties a point-in-time denominator has
// to have are (1) no symbol in a day before it traded, (2) no symbol in a day
// after it stopped, (3) the count actually moves over time — a membership that
// is constant is today's universe wearing a date.

const day = int64(86400)

// d returns the UTC-midnight timestamp n days after 2020-01-01.
func d(n int64) int64 { return 1577836800 + n*day }

func newPITStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(t.TempDir() + "/s.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// bar writes one daily bar directly through the write connection. The public
// bar-ingest path carries vendor plumbing these tests do not need; the table is
// the contract here.
func bar(t *testing.T, st *Store, symbolID, ts int64) {
	t.Helper()
	if _, err := st.w.Exec(
		`INSERT OR REPLACE INTO bars (symbol_id, tf, ts, open, high, low, close, volume)
		 VALUES (?, '1d', ?, 1, 1, 1, 1, 1)`, symbolID, ts); err != nil {
		t.Fatal(err)
	}
}

// fixture: OLD trades days 0..9, NEW lists on day 5 and trades 5..9, DEAD
// trades 0..5 and then stops. The one-day overlap on day 5 is deliberate — it
// is what makes the universe size actually move (2, then 3, then 2), which a
// fixture where a name dies exactly as another lists would hide.
func pitFixture(t *testing.T, st *Store) (old, neu, dead int64) {
	t.Helper()
	ctx := context.Background()
	o, _ := st.UpsertSymbol(ctx, "OLD", md.Stocks, "Old Co")
	n, _ := st.UpsertSymbol(ctx, "NEW", md.Stocks, "New Co")
	x, _ := st.UpsertSymbol(ctx, "DEAD", md.Stocks, "Dead Co")
	for i := int64(0); i < 10; i++ {
		bar(t, st, o.ID, d(i))
		if i >= 5 {
			bar(t, st, n.ID, d(i))
		}
		if i <= 5 {
			bar(t, st, x.ID, d(i))
		}
	}
	return o.ID, n.ID, x.ID
}

func has(ids []int64, want int64) bool {
	for _, v := range ids {
		if v == want {
			return true
		}
	}
	return false
}

func TestRebuildPopulatesMembership(t *testing.T) {
	ctx := context.Background()
	st := newPITStore(t)

	span, err := st.UniverseSpan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if span.Rows != 0 {
		t.Fatalf("fresh database should hold no membership, got %d rows", span.Rows)
	}

	pitFixture(t, st)
	res, err := st.RebuildUniverseMembership(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// OLD 10 days + NEW 5 + DEAD 6 = 21.
	if res.Rows != 21 {
		t.Fatalf("membership rows = %d, want 21", res.Rows)
	}
	if res.ReusedTickerDays != 0 {
		t.Fatalf("clean fixture reported %d reused-ticker days", res.ReusedTickerDays)
	}
	span, err = st.UniverseSpan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if span.Days != 10 || span.Symbols != 3 || span.FirstDay != d(0) || span.LastDay != d(9) {
		t.Fatalf("span = %+v, want 10 days / 3 symbols / %d..%d", span, d(0), d(9))
	}
}

// No future symbol in a historical denominator. NEW's first print is day 5; it
// must be absent from day 4 even though its symbols row exists the whole time.
func TestNoFutureSymbolInAHistoricalDay(t *testing.T) {
	ctx := context.Background()
	st := newPITStore(t)
	_, neu, _ := pitFixture(t, st)
	if _, err := st.RebuildUniverseMembership(ctx); err != nil { //nolint:errcheck
		t.Fatal(err)
	}
	for i := int64(0); i < 5; i++ {
		ids, err := st.UniverseAt(ctx, d(i))
		if err != nil {
			t.Fatal(err)
		}
		if has(ids, neu) {
			t.Fatalf("day %d contains NEW, which had not listed yet — look-ahead in the denominator", i)
		}
	}
	ids, err := st.UniverseAt(ctx, d(5))
	if err != nil {
		t.Fatal(err)
	}
	if !has(ids, neu) {
		t.Fatal("NEW missing from the first day it traded")
	}
}

// No dead symbol after its exit. DEAD's last print is day 5.
func TestNoDeadSymbolAfterExit(t *testing.T) {
	ctx := context.Background()
	st := newPITStore(t)
	_, _, dead := pitFixture(t, st)
	if _, err := st.RebuildUniverseMembership(ctx); err != nil { //nolint:errcheck
		t.Fatal(err)
	}
	for i := int64(6); i < 10; i++ {
		ids, err := st.UniverseAt(ctx, d(i))
		if err != nil {
			t.Fatal(err)
		}
		if has(ids, dead) {
			t.Fatalf("day %d still contains DEAD, which stopped trading on day 5", i)
		}
	}
}

// A membership whose count never moves is today's universe wearing a date.
func TestMembershipCountChangesOverTime(t *testing.T) {
	ctx := context.Background()
	st := newPITStore(t)
	pitFixture(t, st)
	if _, err := st.RebuildUniverseMembership(ctx); err != nil { //nolint:errcheck
		t.Fatal(err)
	}
	seen := map[int]bool{}
	for i := int64(0); i < 10; i++ {
		ids, err := st.UniverseAt(ctx, d(i))
		if err != nil {
			t.Fatal(err)
		}
		seen[len(ids)] = true
	}
	if len(seen) < 2 {
		t.Fatalf("universe size never changed across 10 days (%v) — membership is not point-in-time", seen)
	}
}

// The day key is the floor of the UTC day: daily bars here carry 00:00, 04:00
// and 05:00 UTC alignments, and all three are the same trading day.
func TestIntradayAlignmentsCollapseToOneDay(t *testing.T) {
	ctx := context.Background()
	st := newPITStore(t)
	s, _ := st.UpsertSymbol(ctx, "TZ", md.Stocks, "Alignment Co")
	bar(t, st, s.ID, d(3))
	bar(t, st, s.ID, d(3)+4*3600)
	bar(t, st, s.ID, d(3)+5*3600)

	res, err := st.RebuildUniverseMembership(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Rows != 1 {
		t.Fatalf("three alignments of one trading day produced %d membership rows, want 1", res.Rows)
	}
	ids, err := st.UniverseAt(ctx, d(3)+4*3600)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != s.ID {
		t.Fatalf("UniverseAt on an 04:00 ts = %v, want the symbol", ids)
	}
}

// Rebuilding is idempotent and drops days whose bars were corrected away —
// the reason it is a rebuild and not an append.
func TestRebuildIsIdempotentAndFollowsCorrections(t *testing.T) {
	ctx := context.Background()
	st := newPITStore(t)
	old, _, _ := pitFixture(t, st)

	first, err := st.RebuildUniverseMembership(ctx)
	if err != nil {
		t.Fatal(err)
	}
	again, err := st.RebuildUniverseMembership(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if first.Rows != again.Rows {
		t.Fatalf("rebuild is not idempotent: %d then %d", first.Rows, again.Rows)
	}

	if _, err := st.w.Exec(`DELETE FROM bars WHERE symbol_id=? AND tf='1d' AND ts=?`, old, d(9)); err != nil {
		t.Fatal(err)
	}
	after, err := st.RebuildUniverseMembership(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after.Rows != first.Rows-1 {
		t.Fatalf("rebuild kept a day whose bar was corrected away: %d, want %d", after.Rows, first.Rows-1)
	}
}

// TICKER REUSE. A row that holds a dead company's history and then starts
// printing again under a recycled ticker must not contribute the second
// segment: that would splice two securities into one name inside the
// cross-sectional denominator. The count is reported, not swallowed.
func TestReusedTickerSegmentIsExcludedAndCounted(t *testing.T) {
	ctx := context.Background()
	st := newPITStore(t)
	s, _ := st.UpsertSymbol(ctx, "ATC", md.Stocks, "First Co, then someone else")
	for i := int64(0); i < 4; i++ {
		bar(t, st, s.ID, d(i)) // the dead company
	}
	if err := st.MarkDelisted(ctx, s.ID, d(3)); err != nil {
		t.Fatal(err)
	}
	for i := int64(20); i < 23; i++ {
		bar(t, st, s.ID, d(i)) // the new company on the recycled ticker
	}

	res, err := st.RebuildUniverseMembership(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Rows != 4 {
		t.Fatalf("membership rows = %d, want 4 (only the pre-delisting segment)", res.Rows)
	}
	if res.ReusedTickerDays != 3 {
		t.Fatalf("reused-ticker days = %d, want 3 — the conflict must be counted, not hidden",
			res.ReusedTickerDays)
	}
	ids, err := st.UniverseAt(ctx, d(21))
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 0 {
		t.Fatalf("day 21 admitted the recycled ticker: %v", ids)
	}
}

// A row written under another provenance is not this function's to delete.
func TestRebuildLeavesForeignProvenanceAlone(t *testing.T) {
	ctx := context.Background()
	st := newPITStore(t)
	pitFixture(t, st)
	if _, err := st.w.Exec(
		`INSERT INTO universe_membership (day, symbol_id, source) VALUES (?,?,?)`,
		d(50), 1, "vendor-index"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.RebuildUniverseMembership(ctx); err != nil { //nolint:errcheck
		t.Fatal(err)
	}
	var n int
	if err := st.db.QueryRow(
		`SELECT COUNT(*) FROM universe_membership WHERE source='vendor-index'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("rebuild deleted a foreign-provenance row (%d left)", n)
	}
}
