package store

import (
	"context"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// The defect: one symbols row holding two securities, because an exchange
// recycled the ticker. These pin that the split moves exactly the dead
// company's rows, leaves the live company addressable under the ticker its feed
// uses, and REFUSES rather than guesses whenever the plan and the database
// disagree — a migration that proceeds on a stale plan is how you lose a
// company.

func splitFixture(t *testing.T, st *Store) (id int64, delistedAt int64) {
	t.Helper()
	ctx := context.Background()
	s, err := st.UpsertSymbol(ctx, "ATC", md.Stocks, "Second Co on a recycled ticker")
	if err != nil {
		t.Fatal(err)
	}
	for i := int64(0); i < 4; i++ { // dead company, days 0..3
		bar(t, st, s.ID, d(i))
	}
	delistedAt = d(3)
	if err := st.MarkDelisted(ctx, s.ID, delistedAt); err != nil {
		t.Fatal(err)
	}
	for i := int64(400); i < 404; i++ { // new company, ~13 months later
		bar(t, st, s.ID, d(i))
	}
	// One row in each auxiliary shape the real case had. news.id is the
	// PROVIDER's article id (TEXT), not an autoincrement, so it is supplied.
	const newsID = "article-dead-co-1"
	if _, err := st.w.Exec(`INSERT INTO news (id, symbol_id, ts, headline, url, source, sentiment, score)
	                        VALUES (?,?,?,?,?,?,?,?)`,
		newsID, s.ID, d(1), "dead co news", "u1", "s", "neutral", 0.0); err != nil {
		t.Fatal(err)
	}
	if _, err := st.w.Exec(`INSERT INTO news_symbols (news_id, symbol_id) VALUES (?,?)`, newsID, s.ID); err != nil {
		t.Fatal(err)
	}
	// A pre-boundary article about the dead company that is OWNED by a
	// different symbol. news_symbols is many-to-many; on the real data three of
	// eight rows looked like this, and a subquery that also required
	// news.symbol_id = the split symbol left all three behind.
	other, err := st.UpsertSymbol(ctx, "OTHER", md.Stocks, "Other Co")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.w.Exec(`INSERT INTO news (id, symbol_id, ts, headline, url, source, sentiment, score)
	                        VALUES (?,?,?,?,?,?,?,?)`,
		"article-shared-1", other.ID, d(2), "shared coverage", "u2", "s", "neutral", 0.0); err != nil {
		t.Fatal(err)
	}
	if _, err := st.w.Exec(`INSERT INTO news_symbols (news_id, symbol_id) VALUES (?,?)`,
		"article-shared-1", s.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.w.Exec(`INSERT INTO expectancy (symbol_id, horizon, state_key, n, mean_fwd, median_fwd, hit_rate, stdev, updated_at)
	                        VALUES (?,?,?,?,?,?,?,?,?)`, s.ID, "1d", "k", 9, 0.1, 0.1, 0.5, 0.1, d(3)); err != nil {
		t.Fatal(err)
	}
	return s.ID, delistedAt
}

func spec(id, delistedAt int64) ReusedTickerSplit {
	return ReusedTickerSplit{
		SymbolID:         id,
		Symbol:           "ATC",
		HistoricalTicker: "ATC~DEAD",
		HistoricalName:   "First Co (delisted; ticker later reused)",
		DelistedAt:       delistedAt,
		LiveAddedAt:      d(400),
	}
}

func count(t *testing.T, st *Store, q string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := st.db.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestSplitMovesOnlyTheDeadCompany(t *testing.T) {
	ctx := context.Background()
	st := newPITStore(t)
	id, del := splitFixture(t, st)

	res, err := st.SplitReusedTicker(ctx, spec(id, del))
	if err != nil {
		t.Fatal(err)
	}
	if res.NewSymbolID == 0 || res.NewSymbolID == id {
		t.Fatalf("no distinct historical row created: %+v", res)
	}
	if got := count(t, st, `SELECT COUNT(*) FROM bars WHERE symbol_id=?`, res.NewSymbolID); got != 4 {
		t.Fatalf("historical row holds %d bars, want 4", got)
	}
	if got := count(t, st, `SELECT COUNT(*) FROM bars WHERE symbol_id=?`, id); got != 4 {
		t.Fatalf("live row holds %d bars, want 4 (the new company's)", got)
	}
	// The live row must still be reachable under the ticker its feed addresses.
	sym, err := st.GetSymbol(ctx, "ATC", md.Stocks)
	if err != nil {
		t.Fatal(err)
	}
	if sym.ID != id {
		t.Fatalf("ticker ATC now resolves to %d, want the live row %d — the feed would break", sym.ID, id)
	}
}

func TestSplitClearsTheLiveRowsDelisting(t *testing.T) {
	ctx := context.Background()
	st := newPITStore(t)
	id, del := splitFixture(t, st)
	if _, err := st.SplitReusedTicker(ctx, spec(id, del)); err != nil {
		t.Fatal(err)
	}
	if n := count(t, st, `SELECT COUNT(*) FROM symbols WHERE id=? AND delisted_at IS NULL`, id); n != 1 {
		t.Fatal("live row is still marked delisted — it would stay out of every point-in-time universe")
	}
	if n := count(t, st, `SELECT added_at FROM symbols WHERE id=?`, id); n != d(400) {
		t.Fatalf("live row added_at=%d, want its own first bar %d", n, d(400))
	}
}

// After the split the PIT rebuild must admit the live security again — the
// whole reason the split matters.
func TestSplitRestoresTheLiveSecurityToThePITUniverse(t *testing.T) {
	ctx := context.Background()
	st := newPITStore(t)
	id, del := splitFixture(t, st)

	before, err := st.RebuildUniverseMembership(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if before.ReusedTickerDays != 4 {
		t.Fatalf("pre-split rebuild counted %d reused-ticker days, want 4", before.ReusedTickerDays)
	}
	if ids, _ := st.UniverseAt(ctx, d(401)); len(ids) != 0 {
		t.Fatalf("pre-split, day 401 should be empty (the live security is excluded), got %v", ids)
	}

	if _, err := st.SplitReusedTicker(ctx, spec(id, del)); err != nil {
		t.Fatal(err)
	}
	after, err := st.RebuildUniverseMembership(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after.ReusedTickerDays != 0 {
		t.Fatalf("post-split rebuild still counts %d reused-ticker days", after.ReusedTickerDays)
	}
	ids, err := st.UniverseAt(ctx, d(401))
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != id {
		t.Fatalf("day 401 = %v, want the live security %d back in the universe", ids, id)
	}
	if after.Rows != before.Rows+4 {
		t.Fatalf("membership %d -> %d, want +4 (the recovered live days)", before.Rows, after.Rows)
	}
}

func TestSplitMovesNewsAndDeletesSplicedFits(t *testing.T) {
	ctx := context.Background()
	st := newPITStore(t)
	id, del := splitFixture(t, st)
	res, err := st.SplitReusedTicker(ctx, spec(id, del))
	if err != nil {
		t.Fatal(err)
	}
	if n := count(t, st, `SELECT COUNT(*) FROM news WHERE symbol_id=?`, res.NewSymbolID); n != 1 {
		t.Fatalf("news did not follow the dead company: %d", n)
	}
	// Both rows must follow: the article the dead company owns, AND the
	// pre-boundary article about it that another symbol owns.
	if n := count(t, st, `SELECT COUNT(*) FROM news_symbols WHERE symbol_id=?`, res.NewSymbolID); n != 2 {
		t.Fatalf("news_symbols did not follow the dead company: %d, want 2 "+
			"(a shared many-to-many article must move too)", n)
	}
	if n := count(t, st, `SELECT COUNT(*) FROM news_symbols WHERE symbol_id=?`, id); n != 0 {
		t.Fatalf("%d news_symbols row(s) about the dead company stayed on the live row", n)
	}
	if n := count(t, st, `SELECT COUNT(*) FROM expectancy WHERE symbol_id=?`, id); n != 0 {
		t.Fatal("expectancy fitted over the spliced series survived; it must be deleted to regenerate")
	}
	if n := count(t, st, `SELECT COUNT(*) FROM expectancy WHERE symbol_id=?`, res.NewSymbolID); n != 0 {
		t.Fatal("expectancy was MOVED rather than deleted — it was computed from both companies")
	}
}

func TestSplitRefusesWhenThePlanDisagreesWithTheDatabase(t *testing.T) {
	ctx := context.Background()
	st := newPITStore(t)
	id, del := splitFixture(t, st)

	bad := spec(id, del)
	bad.Symbol = "NOTATC"
	if _, err := st.SplitReusedTicker(ctx, bad); err == nil {
		t.Fatal("split proceeded on a plan whose ticker does not match the row")
	}

	bad = spec(id, del)
	bad.DelistedAt = del + 86400
	if _, err := st.SplitReusedTicker(ctx, bad); err == nil {
		t.Fatal("split proceeded on a plan whose delisting boundary does not match the row")
	}

	// A refused split must have changed nothing.
	if n := count(t, st, `SELECT COUNT(*) FROM bars WHERE symbol_id=?`, id); n != 8 {
		t.Fatalf("a refused split still moved rows: live row holds %d bars, want 8", n)
	}
	if n := count(t, st, `SELECT COUNT(*) FROM symbols WHERE symbol=?`, "ATC~DEAD"); n != 0 {
		t.Fatal("a refused split still created the historical symbol row")
	}
}

func TestSplitRefusesAnOccupiedHistoricalTicker(t *testing.T) {
	ctx := context.Background()
	st := newPITStore(t)
	id, del := splitFixture(t, st)
	if _, err := st.UpsertSymbol(ctx, "ATC~DEAD", md.Stocks, "already taken"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SplitReusedTicker(ctx, spec(id, del)); err == nil {
		t.Fatal("split overwrote an existing symbol row")
	}
}

// The post-boundary rows that look like the live security's own but were
// computed across the splice, and that no refresh path can overwrite because
// every one of them gates on more bars than the live security has.
func TestSplitPurgesSplicedLatestRows(t *testing.T) {
	ctx := context.Background()
	st := newPITStore(t)
	id, del := splitFixture(t, st)
	// A forecast written AFTER the boundary from a window that reached back
	// across it — the ATC case, where n_train needed more bars than the live
	// security has ever had.
	if _, err := st.w.Exec(`INSERT INTO forecasts (symbol_id, horizon, ts, prob, accuracy, brier, auc, base_rate, lift, n_train, n_eval)
	                        VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		id, "1d", d(401), 0.71, 0.6, 0.2, 0.6, 0.5, 0.1, 389, 40); err != nil {
		t.Fatal(err)
	}
	if _, err := st.w.Exec(`INSERT INTO regime_state (symbol_id, ts, label, strength, note) VALUES (?,?,?,?,?)`,
		id, d(401), "bull", 0.7, "sma50 drawn from the dead company's tape"); err != nil {
		t.Fatal(err)
	}

	res, err := st.SplitReusedTicker(ctx, spec(id, del))
	if err != nil {
		t.Fatal(err)
	}
	if res.Deleted["forecasts"] != 1 || res.Deleted["regime_state"] != 1 {
		t.Fatalf("spliced latest-state rows were not purged: %+v", res.Deleted)
	}
	if n := count(t, st, `SELECT COUNT(*) FROM forecasts WHERE symbol_id=?`, id); n != 0 {
		t.Fatal("a forecast fitted across the splice survived; no refresh path can overwrite it")
	}
	// They must be DELETED, not handed to the dead company either.
	if n := count(t, st, `SELECT COUNT(*) FROM forecasts WHERE symbol_id=?`, res.NewSymbolID); n != 0 {
		t.Fatal("spliced forecast was moved to the historical row rather than deleted")
	}
}

// The root cause: an exchange recycles a ticker, the feed upserts it, and an
// unguarded ON CONFLICT resurrects the dead row and re-splices the series.
func TestUpsertSymbolWillNotResurrectADelistedRow(t *testing.T) {
	ctx := context.Background()
	st := newPITStore(t)
	s, err := st.UpsertSymbol(ctx, "GONE", md.Stocks, "Dead Co")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.MarkDelisted(ctx, s.ID, d(10)); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSymbolActive(ctx, s.ID, false); err != nil {
		t.Fatal(err)
	}

	// The feed sees the recycled ticker and upserts it, exactly as a poll would.
	again, err := st.UpsertSymbol(ctx, "GONE", md.Stocks, "New Co on the recycled ticker")
	if err != nil {
		t.Fatal(err)
	}
	if again.Active {
		t.Fatal("upsert reactivated a delisted symbol — the next poll would splice two securities onto one row")
	}
	if n := count(t, st, `SELECT COUNT(*) FROM symbols WHERE id=? AND delisted_at IS NOT NULL`, s.ID); n != 1 {
		t.Fatal("upsert cleared the delisting stamp")
	}
	// A live symbol must still be reactivated normally — the guard is narrow.
	live, _ := st.UpsertSymbol(ctx, "ALIVE", md.Stocks, "Live Co")
	if err := st.SetSymbolActive(ctx, live.ID, false); err != nil {
		t.Fatal(err)
	}
	back, err := st.UpsertSymbol(ctx, "ALIVE", md.Stocks, "Live Co")
	if err != nil {
		t.Fatal(err)
	}
	if !back.Active {
		t.Fatal("guard is too wide: it blocked reactivating a symbol that was never delisted")
	}
}

// A stamp one settlement day early is not a second company.
func TestNudgeDelistingCorrectsAnEarlyStampButRefusesAWideGap(t *testing.T) {
	ctx := context.Background()
	st := newPITStore(t)
	s, _ := st.UpsertSymbol(ctx, "ACACU", md.Stocks, "Early stamp Co")
	bar(t, st, s.ID, d(10))
	bar(t, st, s.ID, d(11))
	if err := st.MarkDelisted(ctx, s.ID, d(10)); err != nil {
		t.Fatal(err)
	}

	moved, err := st.NudgeDelisting(ctx, s.ID, d(11), 5)
	if err != nil || !moved {
		t.Fatalf("nudge refused a one-day settlement lag: moved=%v err=%v", moved, err)
	}
	if got := count(t, st, `SELECT delisted_at FROM symbols WHERE id=?`, s.ID); got != d(11) {
		t.Fatalf("delisted_at=%d, want %d", got, d(11))
	}
	res, err := st.RebuildUniverseMembership(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.ReusedTickerDays != 0 {
		t.Fatalf("still %d reused-ticker days after the nudge", res.ReusedTickerDays)
	}

	// Backwards is never right, and a wide gap is reuse, not a lag.
	if moved, _ := st.NudgeDelisting(ctx, s.ID, d(5), 5); moved {
		t.Fatal("nudge moved a delisting stamp BACKWARD")
	}
	if _, err := st.NudgeDelisting(ctx, s.ID, d(400), 5); err == nil {
		t.Fatal("nudge accepted a 389-day gap; that is ticker reuse and needs a split")
	}
}
