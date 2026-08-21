// Store methods for the CONFLUENCE GATE + MONEY SCOREBOARD wave (see
// internal/confluence + pipeline/confluence.go). Kept in this file — separate
// from store.go — so the wave never touches the core write paths. All writes go
// through the single write connection s.w; reads use s.db.
//
// HONESTY: a setup row is a strict AND over INDEPENDENT signal families (no
// manufactured edge); outcomes are FORWARD-TRACKED with no lookahead (fwd_return
// filled only once the forward bar exists); events are day-deduped exactly like
// smart-money / anomalies.
package store

import (
	"context"
	"database/sql"
	"fmt"
)

// ConfluenceSetup is one stored per-symbol confluence assessment (latest only —
// the table is PK'd on symbol_id and upserted). Payload is the votes JSON the
// API re-encodes typed. Symbol/Market are populated only by joined queries.
type ConfluenceSetup struct {
	SymbolID  int64   `json:"-"`
	Symbol    string  `json:"symbol,omitempty"`
	Market    string  `json:"market,omitempty"`
	Ts        int64   `json:"ts"`
	Direction int     `json:"direction"`
	Agree     int     `json:"agree"`
	Dissent   int     `json:"dissent"`
	Score     float64 `json:"score"`
	IsSetup   bool    `json:"isSetup"`
	Payload   string  `json:"-"`
}

// ConfluenceOutcome is one FORWARD-TRACKED flagged setup. FwdReturn/Win/ResolvedAt
// are nil until the resolver grades it against realized bars (no lookahead).
type ConfluenceOutcome struct {
	SymbolID  int64
	Symbol    string
	Market    string
	Ts        int64
	Horizon   string
	Direction int
	Agree     int
	EntryPx   float64
	FwdReturn float64
	Win       int
	// SettleTs is the base bar this outcome was graded from — the unit of
	// independent evidence. 0 means unknown (not yet backfilled, or no bar at or
	// before it); md.SettleDay falls back to the calendar day for those.
	SettleTs int64
	// EntryTs is the bar the entry leg of FwdReturn was actually read from.
	// 0 means unrecorded (graded before the column existed).
	EntryTs int64
	// EpisodeTs is the ts of the first outcome in this continuous setup episode.
	// 0 means unclassified. Rows sharing an EpisodeTs are ONE bet held across
	// days, not several independent ones.
	EpisodeTs int64
	// EntryClose is the graded entry leg's price LEVEL — what EntryPx would be if
	// it had been read from the same basis as the exit. 0 = unrecorded.
	// ExitLow/ExitHigh are the exit bar's extremes, the intra-window excursion a
	// stop would have been hit at. 0 = unrecorded.
	EntryClose float64
	ExitLow    float64
	ExitHigh   float64
}

// ConfluenceEpisodeGapSecs is the largest gap between two same-direction
// outcomes that still counts as ONE continuous episode.
//
// Four days, because outcomes are bucketed by calendar day but only exist on
// trading days: Friday to Monday is three, and Friday to Tuesday across a
// Monday holiday is four. A fifth day means the setup genuinely lapsed for a
// whole session and re-formed, which is a new bet.
const ConfluenceEpisodeGapSecs = int64(4 * 86400)

// ConfluenceEvent is one "confluence setup" detection to persist (day-deduped).
type ConfluenceEvent struct {
	SymbolID  int64
	Ts        int64
	Kind      string
	Detail    string
	DayBucket string
}

// ConfluenceEventRow is one stored event with its rowid + joined symbol (the
// alert sweep's fan-out source, mirroring SmartMoneyEventRow).
type ConfluenceEventRow struct {
	ID       int64  `json:"id"`
	SymbolID int64  `json:"-"`
	Symbol   string `json:"symbol,omitempty"`
	Market   string `json:"market,omitempty"`
	Ts       int64  `json:"ts"`
	Kind     string `json:"kind"`
	Detail   string `json:"detail"`
}

// UpsertConfluenceSetup stores one symbol's latest assessment; INSERT OR REPLACE
// on the symbol_id PK makes a re-run of the same pass idempotent.
func (s *Store) UpsertConfluenceSetup(ctx context.Context, c ConfluenceSetup) error {
	_, err := s.w.ExecContext(ctx, `
		INSERT OR REPLACE INTO confluence_setups
		  (symbol_id, ts, direction, agree, dissent, score, is_setup, payload)
		VALUES (?,?,?,?,?,?,?,?)`,
		c.SymbolID, c.Ts, c.Direction, c.Agree, c.Dissent, c.Score, boolToInt(c.IsSetup), c.Payload)
	return err
}

// LatestConfluenceSetup returns a symbol's stored assessment (ok=false when the
// symbol has never been assessed — honest absence, not an error).
func (s *Store) LatestConfluenceSetup(ctx context.Context, symbolID int64) (ConfluenceSetup, bool, error) {
	c := ConfluenceSetup{SymbolID: symbolID}
	var isSetup int
	err := s.db.QueryRowContext(ctx, `
		SELECT ts, direction, agree, dissent, score, is_setup, payload
		FROM confluence_setups WHERE symbol_id=?`, symbolID).
		Scan(&c.Ts, &c.Direction, &c.Agree, &c.Dissent, &c.Score, &isSetup, &c.Payload)
	if err == sql.ErrNoRows {
		return c, false, nil
	}
	c.IsSetup = isSetup == 1
	return c, err == nil, err
}

// TopConfluenceSetups returns stored assessments joined to their symbols, flagged
// setups first then strongest agreement (|score|) first. market "" means both
// markets; onlySetups restricts to rows that cleared the gate; limit <= 0 = all.
func (s *Store) TopConfluenceSetups(ctx context.Context, market string, limit int, onlySetups bool) ([]ConfluenceSetup, error) {
	q := `
		SELECT c.symbol_id, sy.symbol, sy.market, c.ts, c.direction, c.agree, c.dissent, c.score, c.is_setup, c.payload
		FROM confluence_setups c
		JOIN symbols sy ON sy.id = c.symbol_id AND sy.active = 1`
	var conds []string
	var args []any
	if market != "" {
		conds = append(conds, "sy.market = ?")
		args = append(args, market)
	}
	if onlySetups {
		conds = append(conds, "c.is_setup = 1")
	}
	for i, cond := range conds {
		if i == 0 {
			q += " WHERE " + cond
		} else {
			q += " AND " + cond
		}
	}
	q += ` ORDER BY c.is_setup DESC, ABS(c.score) DESC, sy.symbol`
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	out := []ConfluenceSetup{}
	for rows.Next() {
		var c ConfluenceSetup
		var isSetup int
		if err := rows.Scan(&c.SymbolID, &c.Symbol, &c.Market, &c.Ts, &c.Direction,
			&c.Agree, &c.Dissent, &c.Score, &isSetup, &c.Payload); err != nil {
			return nil, err
		}
		c.IsSetup = isSetup == 1
		out = append(out, c)
	}
	return out, rows.Err()
}

// InsertConfluenceOutcome forward-tracks a flagged setup; INSERT OR IGNORE on the
// (symbol_id, ts, horizon) PK makes it idempotent (a re-run of the same pass, or
// two setups the same rounded ts, never double-inserts).
// It also assigns episode_ts in the same statement: the episode of the most
// recent same-direction row within ConfluenceEpisodeGapSecs, or this row's own
// ts when the setup is newly formed. Doing it in the INSERT rather than in a
// follow-up UPDATE keeps the classification atomic with the row it describes —
// a crash between the two would otherwise leave a bet no episode owns, and an
// unowned row is exactly what the published population must not contain.
func (s *Store) InsertConfluenceOutcome(ctx context.Context, o ConfluenceOutcome) error {
	_, err := s.w.ExecContext(ctx, `
		INSERT OR IGNORE INTO confluence_outcomes
		  (symbol_id, ts, horizon, direction, agree, entry_px, entry_ts, episode_ts)
		VALUES (?,?,?,?,?,?,?,
		  COALESCE(
		    (SELECT COALESCE(p.episode_ts, p.ts) FROM confluence_outcomes p
		      WHERE p.symbol_id = ? AND p.horizon = ? AND p.direction = ?
		        AND p.ts < ? AND p.ts >= ?
		      ORDER BY p.ts DESC LIMIT 1),
		    ?))`,
		o.SymbolID, o.Ts, o.Horizon, o.Direction, o.Agree, o.EntryPx, nullableTs(o.EntryTs),
		o.SymbolID, o.Horizon, o.Direction, o.Ts, o.Ts-ConfluenceEpisodeGapSecs, o.Ts)
	return err
}

// nullableTs maps a zero timestamp to SQL NULL. A stored 0 would read as the
// Unix epoch — "1970" is a value, "we did not record it" is not.
func nullableTs(ts int64) any {
	if ts <= 0 {
		return nil
	}
	return ts
}

// UnresolvedConfluenceOutcomes returns pending outcomes with ts <= before (the
// resolver's maturity cutoff), oldest first. entry_px is frozen at flag time.
func (s *Store) UnresolvedConfluenceOutcomes(ctx context.Context, before int64, limit int) ([]ConfluenceOutcome, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT symbol_id, ts, horizon, direction, agree, entry_px
		FROM confluence_outcomes
		WHERE resolved_at IS NULL AND ungradable IS NULL AND ts <= ?
		ORDER BY ts LIMIT ?`, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	out := []ConfluenceOutcome{}
	for rows.Next() {
		var o ConfluenceOutcome
		if err := rows.Scan(&o.SymbolID, &o.Ts, &o.Horizon, &o.Direction, &o.Agree, &o.EntryPx); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// ResolveConfluenceOutcome records the realized forward return + win flag for a
// matured setup (called only once the forward bar exists — no lookahead).
// px carries the price LEVELS the constrained basis needs; zero fields store NULL.
func (s *Store) ResolveConfluenceOutcome(ctx context.Context, symbolID, ts int64, horizon string, fwdReturn float64, win bool, entryTs int64, px ConfluenceGradePrices) error {
	// settle_ts stamped at grade time — the independence unit (md.SettleDay).
	// Derivation identical to BackfillConfluenceSettleTs so the two agree.
	_, err := s.w.ExecContext(ctx, `
		UPDATE confluence_outcomes SET fwd_return=?, win=?, entry_ts=?,
		  entry_close=?, exit_low=?, exit_high=?, resolved_at=strftime('%s','now'),
		  settle_ts = (
		    SELECT MAX(b.ts) FROM bars b
		    WHERE b.symbol_id = confluence_outcomes.symbol_id
		      AND b.tf = '1d' AND b.ts <= confluence_outcomes.ts
		  )
		WHERE symbol_id=? AND ts=? AND horizon=?`,
		fwdReturn, boolToInt(win), nullableTs(entryTs),
		nullablePx(px.EntryClose), nullablePx(px.ExitLow), nullablePx(px.ExitHigh),
		symbolID, ts, horizon)
	return err
}

// ConfluenceGradePrices are the price levels stamped on a row when it is graded.
type ConfluenceGradePrices struct {
	EntryClose float64
	ExitLow    float64
	ExitHigh   float64
}

// nullablePx maps a non-positive price to SQL NULL. A stored 0 would read as a
// free instrument; "we did not record it" is a different statement.
func nullablePx(px float64) any {
	if px <= 0 {
		return nil
	}
	return px
}

// ResolvedConfluenceOutcomes returns graded outcomes joined to their symbols,
// newest first — the money scoreboard's source. limit <= 0 means all.
func (s *Store) ResolvedConfluenceOutcomes(ctx context.Context, limit int) ([]ConfluenceOutcome, error) {
	q := `
		SELECT o.symbol_id, sy.symbol, sy.market, o.ts, o.horizon, o.direction, o.agree, o.entry_px, o.fwd_return, o.win, o.settle_ts,
		       o.entry_ts, o.episode_ts, o.entry_close, o.exit_low, o.exit_high
		FROM confluence_outcomes o
		JOIN symbols sy ON sy.id = o.symbol_id
		WHERE o.resolved_at IS NOT NULL AND o.ungradable IS NULL
		ORDER BY o.ts DESC`
	var args []any
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	out := []ConfluenceOutcome{}
	for rows.Next() {
		var o ConfluenceOutcome
		var fwd sql.NullFloat64
		var win sql.NullInt64
		var settle, entryTs, episodeTs sql.NullInt64
		var entryClose, exitLow, exitHigh sql.NullFloat64
		if err := rows.Scan(&o.SymbolID, &o.Symbol, &o.Market, &o.Ts, &o.Horizon,
			&o.Direction, &o.Agree, &o.EntryPx, &fwd, &win, &settle, &entryTs, &episodeTs,
			&entryClose, &exitLow, &exitHigh); err != nil {
			return nil, err
		}
		o.EntryClose, o.ExitLow, o.ExitHigh = entryClose.Float64, exitLow.Float64, exitHigh.Float64
		o.FwdReturn = fwd.Float64
		o.Win = int(win.Int64)
		o.SettleTs = settle.Int64 // 0 when NULL — md.SettleDay reads that as unknown
		o.EntryTs = entryTs.Int64
		o.EpisodeTs = episodeTs.Int64
		out = append(out, o)
	}
	return out, rows.Err()
}

// InsertConfluenceEvent stores one event; reports whether a NEW row was written
// (false = deduped: same symbol+kind already recorded for this day_bucket).
func (s *Store) InsertConfluenceEvent(ctx context.Context, e ConfluenceEvent) (bool, error) {
	res, err := s.w.ExecContext(ctx, `
		INSERT OR IGNORE INTO confluence_events (symbol_id, ts, kind, detail, day_bucket)
		VALUES (?,?,?,?,?)`,
		e.SymbolID, e.Ts, e.Kind, e.Detail, e.DayBucket)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// MaxConfluenceEventID returns the current max confluence_events.id (0 when
// empty) — the first-sweep cursor seed, mirroring MaxSmartMoneyEventID.
func (s *Store) MaxConfluenceEventID(ctx context.Context) (int64, error) {
	var id sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT MAX(id) FROM confluence_events`).Scan(&id)
	return id.Int64, err
}

// ConfluenceEventsAfterID returns events with id > afterID (and, when sinceTs >
// 0, ts >= sinceTs), ascending id — the alert engine's gap-free sweep source,
// mirroring SmartMoneyEventsAfterID.
func (s *Store) ConfluenceEventsAfterID(ctx context.Context, afterID, sinceTs int64, limit int) ([]ConfluenceEventRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT e.id, e.symbol_id, COALESCE(sym.symbol,''), COALESCE(sym.market,''),
		       e.ts, e.kind, e.detail
		FROM confluence_events e LEFT JOIN symbols sym ON sym.id = e.symbol_id
		WHERE e.id > ? AND e.ts >= ?
		ORDER BY e.id LIMIT ?`, afterID, sinceTs, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	out := make([]ConfluenceEventRow, 0, 8)
	for rows.Next() {
		var e ConfluenceEventRow
		if err := rows.Scan(&e.ID, &e.SymbolID, &e.Symbol, &e.Market, &e.Ts, &e.Kind, &e.Detail); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// boolToInt is the 1/0 encoding for SQLite integer-boolean columns.
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// BackfillConfluenceEpisodes recomputes episode_ts for EVERY outcome row from
// the stored (symbol, direction, ts) sequence, and returns how many rows now
// carry one.
//
// It is a pure re-derivation, not a repair with judgement in it: the same table
// always produces the same assignment, so running it twice changes nothing and
// running it after new rows arrive re-anchors only the runs those rows extend.
// That is deliberate — the alternative, stamping episodes only on NULL rows,
// would let a row inserted out of order split an episode permanently.
//
// The gap rule is ConfluenceEpisodeGapSecs. LAG returns NULL on a partition's
// first row and NULL <= x is NULL, so the CASE falls to ELSE and the first row
// of every symbol/direction correctly starts an episode.
func (s *Store) BackfillConfluenceEpisodes(ctx context.Context) (int64, error) {
	res, err := s.w.ExecContext(ctx, `
WITH ordered AS (
  SELECT symbol_id, horizon, direction, ts,
         CASE WHEN ts - LAG(ts) OVER (PARTITION BY symbol_id, horizon, direction ORDER BY ts) <= ?
              THEN 0 ELSE 1 END AS is_start
  FROM confluence_outcomes WHERE ungradable IS NULL
),
grp AS (
  SELECT symbol_id, horizon, direction, ts,
         SUM(is_start) OVER (PARTITION BY symbol_id, horizon, direction ORDER BY ts) AS g
  FROM ordered
),
ep AS (
  SELECT symbol_id, horizon, direction, ts,
         MIN(ts) OVER (PARTITION BY symbol_id, horizon, direction, g) AS episode_ts
  FROM grp
)
UPDATE confluence_outcomes AS o
   SET episode_ts = (SELECT e.episode_ts FROM ep e
                      WHERE e.symbol_id = o.symbol_id AND e.horizon = o.horizon
                        AND e.direction = o.direction AND e.ts = o.ts)
 WHERE o.ungradable IS NULL`,
		ConfluenceEpisodeGapSecs)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ConfluenceOutcomesForSymbol returns every outcome for one symbol and horizon,
// oldest first, resolved or not. It exists for episode inspection and for the
// migration report — the published scoreboard reads ResolvedConfluenceOutcomes.
func (s *Store) ConfluenceOutcomesForSymbol(ctx context.Context, symbolID int64, horizon string) ([]ConfluenceOutcome, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT symbol_id, ts, horizon, direction, agree, entry_px, fwd_return, win, entry_ts, episode_ts
		FROM confluence_outcomes
		WHERE symbol_id = ? AND horizon = ?
		ORDER BY ts`, symbolID, horizon)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	out := []ConfluenceOutcome{}
	for rows.Next() {
		var o ConfluenceOutcome
		var fwd sql.NullFloat64
		var win, entryTs, episodeTs sql.NullInt64
		if err := rows.Scan(&o.SymbolID, &o.Ts, &o.Horizon, &o.Direction, &o.Agree,
			&o.EntryPx, &fwd, &win, &entryTs, &episodeTs); err != nil {
			return nil, err
		}
		o.FwdReturn, o.Win = fwd.Float64, int(win.Int64)
		o.EntryTs, o.EpisodeTs = entryTs.Int64, episodeTs.Int64
		out = append(out, o)
	}
	return out, rows.Err()
}

// MarkUngradableConfluenceOutcomes retires every row whose entry leg cannot come
// from its own bucket day, and returns how many it retired.
//
// WHY THESE ROWS EXIST. The scorer bucketed outcomes by UTC calendar day and ran
// every 30 minutes, weekends included, so a setup that persisted over a weekend
// opened Friday, Saturday and Sunday rows. The resolver then graded all three
// from the same pair of bars, because its entry read reaches BACKWARD and its
// forward read reaches FORWARD and there is no bar in between. Measured on the
// live table: 1,430 of 5,428 resolved rows have an entry bar outside their own
// bucket, and 1,429 of those are Saturday or Sunday. RNWWW carried the identical
// +93.33% on three consecutive buckets.
//
// They cannot simply be left graded. Collapsing a persistent setup into one
// episode COMPOUNDS its days, and compounding a duplicated day squares the move
// that was never made twice — RNWWW's three copies compound to +622%. Nor can
// they be deleted: a bet that was placed is a fact about what this system did.
//
// So the row stays, keeps its entry_px audit value, and is marked ungradable.
// fwd_return, win and resolved_at are cleared because they were computed from
// the wrong pair of bars and no reader should be able to find them.
//
// It also retires a RESOLVED row with no exit bar inside its grading window. One
// such row survives on the live table — BURU 2026-07-17, carrying fwd_return 0.0
// with zero bars in the window, because the bar it was graded against has since
// been quarantined. A return no bar supports is the same unsupported number as a
// stale entry, at the other leg.
//
// FORWARD-SAFE: the scorer no longer opens a bucket on a non-trading day and the
// resolver refuses an entry bar from outside the bucket, so this can only ever
// have historical rows to act on. Re-running it is a no-op.
func (s *Store) MarkUngradableConfluenceOutcomes(ctx context.Context, reason string) (int64, error) {
	if reason == "" {
		return 0, fmt.Errorf("reason must not be empty: an unexplained retirement is indistinguishable from data loss")
	}
	res, err := s.w.ExecContext(ctx, `
UPDATE confluence_outcomes
   SET ungradable = ?, fwd_return = NULL, win = NULL, resolved_at = NULL, episode_ts = NULL
 WHERE ungradable IS NULL
   AND (
     -- (a) the entry leg cannot come from this row's own bucket day.
     COALESCE((SELECT MAX(b.ts) FROM bars b
                WHERE b.symbol_id = confluence_outcomes.symbol_id
                  AND b.tf = '1d' AND b.ts <= confluence_outcomes.ts + 86399), -1) < confluence_outcomes.ts
     -- (b) the row is GRADED but no exit bar exists in its window. An unresolved
     -- row with no exit bar is simply pending and must not be caught here; a
     -- RESOLVED one is carrying a return no bar of this symbol supports, which
     -- is the same unsupported-number problem as (a) at the other leg.
     OR (resolved_at IS NOT NULL AND NOT EXISTS (
           SELECT 1 FROM bars b
            WHERE b.symbol_id = confluence_outcomes.symbol_id AND b.tf = '1d'
              AND b.ts >= confluence_outcomes.ts + 86400
              AND b.ts <= confluence_outcomes.ts + 4 * 86400))
   )`,
		reason)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// UngradableConfluenceCount reports how many rows are currently retired, so the
// number is published rather than inferred from an absence.
func (s *Store) UngradableConfluenceCount(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM confluence_outcomes WHERE ungradable IS NOT NULL`).Scan(&n)
	return n, err
}

// ConfluencePopulation is the shape of the graded record, published so a reader
// never has to infer it from an absence.
type ConfluencePopulation struct {
	Rows       int `json:"rows"`
	Resolved   int `json:"resolved"`
	Ungradable int `json:"ungradable"`
	Episodes   int `json:"episodes"`
	// StaleEntry counts resolved rows still graded from a bar outside their own
	// bucket day. After the repair this must be zero, and it is checked rather
	// than assumed.
	StaleEntry int `json:"staleEntry"`
}

// ConfluencePopulation measures the table in one pass.
func (s *Store) ConfluencePopulation(ctx context.Context) (ConfluencePopulation, error) {
	var p ConfluencePopulation
	err := s.db.QueryRowContext(ctx, `
SELECT (SELECT COUNT(*) FROM confluence_outcomes),
       (SELECT COUNT(*) FROM confluence_outcomes WHERE resolved_at IS NOT NULL AND ungradable IS NULL),
       (SELECT COUNT(*) FROM confluence_outcomes WHERE ungradable IS NOT NULL),
       (SELECT COUNT(*) FROM (SELECT DISTINCT symbol_id, episode_ts FROM confluence_outcomes
                               WHERE episode_ts IS NOT NULL AND ungradable IS NULL)),
       (SELECT COUNT(*) FROM confluence_outcomes o
         WHERE o.resolved_at IS NOT NULL AND o.ungradable IS NULL
           AND COALESCE((SELECT MAX(b.ts) FROM bars b
                          WHERE b.symbol_id = o.symbol_id AND b.tf = '1d'
                            AND b.ts <= o.ts + 86399), -1) < o.ts)`).
		Scan(&p.Rows, &p.Resolved, &p.Ungradable, &p.Episodes, &p.StaleEntry)
	return p, err
}

// BackfillConfluenceGradePrices stamps entry_close, exit_low and exit_high on
// rows that were graded before those columns existed.
//
// WHY IT IS NEEDED. The CONSTRAINED basis needs price LEVELS, not just a ratio:
// a tradable-minimum test cannot be run on a return, and a stop cannot be placed
// without knowing how far the position actually traded against itself. Without
// this backfill the whole historical record reports noPriceLevels and the
// account-level number is empty — honest, but useless.
//
// THE DERIVATION IS THE RESOLVER'S, NOT A NEW ONE. Entry is the last bar inside
// the row's own bucket day (the same lower-bounded read the resolver now uses).
// Exit is the first bar at or after ts+86400, refused beyond three horizons —
// byte-for-byte the same window ConfluenceResolver applies, so a backfilled row
// and a freshly graded one cannot describe different trades.
//
// Rows whose bars have since been quarantined get NULL and stay NULL: the
// constrained basis then counts them under noPriceLevels rather than guessing.
// Only rows missing entry_close are touched, so this is idempotent and can never
// overwrite a value the resolver stamped.
func (s *Store) BackfillConfluenceGradePrices(ctx context.Context) (int64, error) {
	res, err := s.w.ExecContext(ctx, `
UPDATE confluence_outcomes AS o
   SET entry_close = (SELECT b.close FROM bars b
                       WHERE b.symbol_id = o.symbol_id AND b.tf = '1d'
                         AND b.ts >= o.ts AND b.ts <= o.ts + 86399
                       ORDER BY b.ts DESC LIMIT 1),
       exit_low    = (SELECT b.low FROM bars b
                       WHERE b.symbol_id = o.symbol_id AND b.tf = '1d'
                         AND b.ts >= o.ts + 86400 AND b.ts <= o.ts + 4 * 86400
                       ORDER BY b.ts ASC LIMIT 1),
       exit_high   = (SELECT b.high FROM bars b
                       WHERE b.symbol_id = o.symbol_id AND b.tf = '1d'
                         AND b.ts >= o.ts + 86400 AND b.ts <= o.ts + 4 * 86400
                       ORDER BY b.ts ASC LIMIT 1)
 WHERE o.resolved_at IS NOT NULL AND o.ungradable IS NULL AND o.entry_close IS NULL`)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ConfluencePriceLevelCoverage reports how many gradable resolved rows carry the
// price levels the constrained basis needs, and how many of those reproduce
// their own stored fwd_return from those levels.
//
// The second number is the one that matters: it proves the backfilled entry and
// exit are the SAME pair the row was graded from, rather than a plausible pair
// that happens to exist. A mismatch means the bars moved under the row.
func (s *Store) ConfluencePriceLevelCoverage(ctx context.Context) (withLevels, reproducing, total int, err error) {
	err = s.db.QueryRowContext(ctx, `
SELECT COUNT(*),
       SUM(CASE WHEN entry_close IS NOT NULL THEN 1 ELSE 0 END),
       SUM(CASE WHEN entry_close IS NOT NULL AND entry_close > 0
                 AND ABS((SELECT b.close FROM bars b
                           WHERE b.symbol_id = o.symbol_id AND b.tf = '1d'
                             AND b.ts >= o.ts + 86400 AND b.ts <= o.ts + 4 * 86400
                           ORDER BY b.ts ASC LIMIT 1) / entry_close - 1 - fwd_return) < 1e-6
                THEN 1 ELSE 0 END)
FROM confluence_outcomes o
WHERE resolved_at IS NOT NULL AND ungradable IS NULL`).Scan(&total, &withLevels, &reproducing)
	return withLevels, reproducing, total, err
}

// RegradeConfluenceFromCurrentBars recomputes every gradable resolved row from
// the bars as they stand, on ONE basis, and returns how many rows changed.
//
// WHY 85% OF THE RECORD NEEDED THIS. Commit 72007b3 changed the resolver to
// divide two prices read from the SAME series, because the previous version
// divided a live exit close by entry_px — a price frozen when the setup was
// flagged, on a basis a later re-backfill may have rescaled. That fix applied
// only to rows graded AFTER it. Measured on the live table 2026-08-21: of 3,998
// gradable resolved rows, only 590 reproduce their own stored fwd_return from
// the current bars. The other 3,408 still carry the superseded frozen-entry
// number, which is the defect DFNS published at +8541%.
//
// This is a RECOMPUTATION, not a correction with judgement in it: it applies the
// shipped resolver's own derivation — last bar inside the bucket day for the
// entry, first bar at or after one horizon for the exit, refused beyond three
// horizons — to rows the superseded resolver graded. Running it twice changes
// nothing.
//
// A row whose entry or exit bar no longer exists (quarantined, purged) is left
// untouched with its old value and shows up in ConfluencePriceLevelCoverage as
// not reproducing, rather than being silently zeroed.
func (s *Store) RegradeConfluenceFromCurrentBars(ctx context.Context) (int64, error) {
	res, err := s.w.ExecContext(ctx, `
WITH lv AS (
  SELECT o.symbol_id, o.ts, o.horizon, o.direction,
         (SELECT b.close FROM bars b
           WHERE b.symbol_id = o.symbol_id AND b.tf = '1d'
             AND b.ts >= o.ts AND b.ts <= o.ts + 86399
           ORDER BY b.ts DESC LIMIT 1) AS ec,
         (SELECT b.close FROM bars b
           WHERE b.symbol_id = o.symbol_id AND b.tf = '1d'
             AND b.ts >= o.ts + 86400 AND b.ts <= o.ts + 4 * 86400
           ORDER BY b.ts ASC LIMIT 1) AS xc,
         (SELECT b.low FROM bars b
           WHERE b.symbol_id = o.symbol_id AND b.tf = '1d'
             AND b.ts >= o.ts + 86400 AND b.ts <= o.ts + 4 * 86400
           ORDER BY b.ts ASC LIMIT 1) AS xl,
         (SELECT b.high FROM bars b
           WHERE b.symbol_id = o.symbol_id AND b.tf = '1d'
             AND b.ts >= o.ts + 86400 AND b.ts <= o.ts + 4 * 86400
           ORDER BY b.ts ASC LIMIT 1) AS xh,
         (SELECT b.ts FROM bars b
           WHERE b.symbol_id = o.symbol_id AND b.tf = '1d'
             AND b.ts >= o.ts AND b.ts <= o.ts + 86399
           ORDER BY b.ts DESC LIMIT 1) AS ets
  FROM confluence_outcomes o
  WHERE o.resolved_at IS NOT NULL AND o.ungradable IS NULL
)
UPDATE confluence_outcomes AS o
   SET fwd_return  = (SELECT lv.xc / lv.ec - 1 FROM lv
                       WHERE lv.symbol_id = o.symbol_id AND lv.ts = o.ts AND lv.horizon = o.horizon),
       win         = (SELECT CASE WHEN (lv.direction > 0 AND lv.xc / lv.ec - 1 > 0)
                                    OR (lv.direction < 0 AND lv.xc / lv.ec - 1 < 0)
                                  THEN 1 ELSE 0 END FROM lv
                       WHERE lv.symbol_id = o.symbol_id AND lv.ts = o.ts AND lv.horizon = o.horizon),
       entry_close = (SELECT lv.ec FROM lv WHERE lv.symbol_id = o.symbol_id AND lv.ts = o.ts AND lv.horizon = o.horizon),
       entry_ts    = (SELECT lv.ets FROM lv WHERE lv.symbol_id = o.symbol_id AND lv.ts = o.ts AND lv.horizon = o.horizon),
       exit_low    = (SELECT lv.xl FROM lv WHERE lv.symbol_id = o.symbol_id AND lv.ts = o.ts AND lv.horizon = o.horizon),
       exit_high   = (SELECT lv.xh FROM lv WHERE lv.symbol_id = o.symbol_id AND lv.ts = o.ts AND lv.horizon = o.horizon)
 WHERE o.resolved_at IS NOT NULL AND o.ungradable IS NULL
   AND EXISTS (SELECT 1 FROM lv
                WHERE lv.symbol_id = o.symbol_id AND lv.ts = o.ts AND lv.horizon = o.horizon
                  AND lv.ec IS NOT NULL AND lv.ec > 0 AND lv.xc IS NOT NULL AND lv.xc > 0
                  AND ABS(lv.xc / lv.ec - 1 - o.fwd_return) >= 1e-9)`)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
