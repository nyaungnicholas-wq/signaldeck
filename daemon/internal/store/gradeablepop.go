package store

import (
	"context"
	"strconv"
)

// The ONE Go definition of "a gradeable directional row".
//
// WHY THIS FILE EXISTS. tools/accuracy_registry.py grades a FILTERED population
// — survivorship epoch, settlement quarantine, stale-feed exclusion, one
// observation per (symbol, horizon, trading-day). Every Go surface graded an
// unfiltered one. Measured 2026-08-12 on the live corpus:
//
//	                       1d                 1w
//	grader (published)     n=2399 acc=.4152   n=2873 acc=.3648
//	Go surfaces            n=17099 acc=.4766  n=16183 acc=.4749
//
// i.e. symbol pages overstated the evidence 7.1x and the accuracy by 6.1pp
// while the registry published FAILED on the same predictor. The two must not
// be able to disagree again, so the SQL below is the single source and callers
// wrap it rather than writing their own population.
//
// The fragments mirror accuracy_registry.py one-for-one; keep them in sync.

// SurvivorshipEpochTS is 2026-07-24T00:00:00Z — accuracy_registry.py
// SURVIVORSHIP_EPOCH_TS. symbols.delisted_at only exists from that wave on, so
// everything earlier is survivor-seeded and is not gradeable evidence.
const SurvivorshipEpochTS = 1784851200

const (
	// tradingDayOffsetSecs / secondsPerDay mirror the grader's trading_day():
	// (ts - 5h) // 86400. Go cannot call that Python callback, so the fold is
	// inlined as integer SQL. SQLite's / truncates toward zero, which equals
	// floor for every ts >= the offset — true for the whole corpus (post-1970).
	// Verified: the inlined form reproduces the callback population exactly.
	tradingDayOffsetSecs = 5 * 3600
	secondsPerDay        = 86400

	// SETTLEMENT_CLOSE_SECS. A stock bar is final at the 16:00 close; a crypto
	// bar runs the full 24h. Leaving these at a single value (or at 0) shifts
	// every settlement instant and silently re-inflates n.
	settlementCloseStocksSecs = 16 * 3600
	settlementCloseCryptoSecs = 24 * 3600
)

// dayFold folds a timestamp expression to its trading-day index. The column is
// passed in so the same fragment serves prediction_outcomes.ts and dq_events.ts
// without relying on which table an unqualified `ts` happens to bind to.
func dayFold(tsExpr string) string {
	return "((" + tsExpr + " - " + strconv.Itoa(tradingDayOffsetSecs) + ") / " +
		strconv.Itoa(secondsPerDay) + ")"
}

// settleDayFold is the SQL form of md.SettleDay and of the grader's
// settle_day() — the unit of INDEPENDENT EVIDENCE, which is not the calendar
// day. Predictions issued Friday, Saturday and Sunday resolve against ONE
// settled move (Friday→Monday), yet a calendar fold counts three independent
// observations: measured, a 1.41x overstatement of effective N, which narrows
// every published interval by ~19% in the flattering direction.
//
// settle_ts is the base bar the row was graded from, and two predictions share
// an outcome exactly when they share a base bar. NULL or <= 0 means the settled
// move is unknown (column predates the row, or no bar at/before it), and falls
// back to the calendar day rather than dropping the row — so this improves
// monotonically as the backfill drains. In SQL, `NULL > 0` is NULL, so a NULL
// settle_ts takes the ELSE branch, matching the Python guard exactly.
//
// The two implementations must not drift: accuracy_registry.py folds the same
// way, and the Go/Python populations are compared on the live corpus.
func settleDayFold(settleExpr, tsExpr string) string {
	return dayFold("(CASE WHEN " + settleExpr + " > 0 THEN " + settleExpr +
		" ELSE " + tsExpr + " END)")
}

// Settlement building blocks — accuracy_registry.py _HORIZON_SECS / _BASE_TS /
// _FWD_TS / _CLOSE_OFFSET / SETTLEMENT_AT, verbatim.
const (
	horizonSecsSQL = "(CASE WHEN po.horizon LIKE '1w%' THEN 604800 ELSE 86400 END)"
	baseTsSQL      = "(SELECT MAX(b.ts) FROM bars b WHERE b.symbol_id = po.symbol_id" +
		" AND b.tf = '1d' AND b.ts <= po.ts)"
	// dstStampSlackSQL mirrors pipeline.dstStampSlackSecs. US daily bars are
	// stamped at ET midnight, so a fixed +604800 on an EST-stamped base lands an
	// hour PAST the EDT-stamped bar seven days later and this SELECT skips it,
	// grading an 8-session move as "1w". 6h cannot reach the prior session.
	dstStampSlackSQL = "21600"
	fwdTsSQL         = "(SELECT MIN(f.ts) FROM bars f WHERE f.symbol_id = po.symbol_id" +
		" AND f.tf = '1d' AND f.ts >= " + baseTsSQL + " + " + horizonSecsSQL +
		" - " + dstStampSlackSQL + ")"
)

// closeOffsetSQL branches on the instrument class, like _CLOSE_OFFSET.
func closeOffsetSQL() string {
	return "(CASE WHEN (SELECT sy.market FROM symbols sy WHERE sy.id = po.symbol_id) = 'crypto'" +
		" THEN " + strconv.Itoa(settlementCloseCryptoSecs) +
		" ELSE " + strconv.Itoa(settlementCloseStocksSecs) + " END)"
}

// settlementAtSQL is the instant the row's forward bar became final, or NULL
// when the resolver's own reconstruction no longer holds. NULL is UNVERIFIABLE:
// `resolved_at >= NULL` is NULL, so those rows are quarantined too.
func settlementAtSQL() string {
	return "(SELECT CASE WHEN " + fwdTsSQL + " - (" + baseTsSQL + " + " + horizonSecsSQL + ")" +
		" > 3 * " + horizonSecsSQL + " THEN NULL ELSE " + fwdTsSQL + " + " + closeOffsetSQL() + " END)"
}

// gradeableDedupSQL returns the CTE + dedup body reproducing the grader's
// population. rn = 1 is one observation per (symbol, horizon, trading-day),
// latest call wins. Callers wrap it:
//
//	SELECT ... FROM (<gradeableDedupSQL(...)>) WHERE rn = 1 AND horizon = ?
//
// The horizon is ALWAYS bound as `?` by the caller; nothing here interpolates
// caller input — only the fixed constants above appear in the SQL text.
//
// withReconstruction gates the settlement and stale-feed predicates. A filter
// that CANNOT BE EVALUATED is a non-exclusion, not a total exclusion: with an
// empty bars table every settlement subquery is NULL, `resolved_at >= NULL` is
// never true, and the whole population would silently vanish — a fixture would
// read "0 observations" as though the model had never been graded. The grader
// takes the same position for the same reason (settlement_clause() returns ""
// when there is nothing to reconstruct against). See SettlementApplicable.
func gradeableDedupSQL(withReconstruction bool) string {
	q := "SELECT symbol_id, horizon, prob, up, ts, settle_ts," +
		" ROW_NUMBER() OVER (PARTITION BY symbol_id, horizon, " +
		settleDayFold("po.settle_ts", "po.ts") +
		" ORDER BY ts DESC) rn" +
		" FROM prediction_outcomes po" +
		" WHERE resolved_at IS NOT NULL AND up IS NOT NULL AND prob IS NOT NULL" +
		" AND ts >= " + strconv.Itoa(SurvivorshipEpochTS)

	if !withReconstruction {
		return q
	}
	// Stale feed: drop observations minted on a trading day the daemon had
	// already flagged that symbol's own feed stale (dq_events kind='stale').
	q += " AND NOT EXISTS (SELECT 1 FROM stale_feed f WHERE f.symbol_id = po.symbol_id" +
		" AND f.d = " + dayFold("po.ts") + ")"
	q += " AND po.resolved_at >= " + settlementAtSQL()

	return "WITH stale_feed AS (SELECT DISTINCT symbol_id, " + dayFold("ts") +
		" AS d FROM dq_events WHERE kind = 'stale') " + q
}

// SettlementApplicable reports whether the settlement/stale-feed filters can be
// reconstructed at all, i.e. whether any bar exists. False on a fresh schema or
// an in-memory fixture, where the filters must be omitted rather than allowed
// to empty the population. See gradeableDedupSQL.
func (s *Store) SettlementApplicable(ctx context.Context) (bool, error) {
	var ok bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM bars)`).Scan(&ok)
	return ok, err
}
