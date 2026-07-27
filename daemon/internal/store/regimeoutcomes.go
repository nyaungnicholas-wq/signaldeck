// Persistence for the LIVE regime-forecast grading loop (credibility wave):
// frozen regime calls (regime_outcomes), their later resolutions, and the
// plain-English postmortems for high-conviction misses (regime_postmortems).
// See schema.sql banners for the honesty contract; math lives in
// internal/structregime (resolve helpers) and the worker in
// internal/pipeline/regimeoutcomes.go.
package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"

	"github.com/nyaungnicholas-wq/signaldeck/internal/structregime"
)

// RegimeCall is one current regime_forecasts row with its symbol id — the
// snapshot source the outcome worker freezes from.
type RegimeCall struct {
	SymbolID           int64
	Kind               structregime.Kind
	Ts                 int64
	HorizonDays        int
	Regime             string
	Conviction         float64
	HistoricalAccuracy float64
	Rank               float64
	// NaiveLabel is the frozen naive-persistence baseline for this call — the
	// "nothing changes" guess computed from the call bar (structregime.Naive*At).
	// Empty means no baseline was computable; it is stored as NULL and excluded
	// from the benchmark tally rather than counted as a miss.
	NaiveLabel string
}

// RegimeForecastCalls returns every current regime forecast with its symbol id
// (RegimeForecasts joins for display; this is the grading read).
func (s *Store) RegimeForecastCalls(ctx context.Context) ([]RegimeCall, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT symbol_id, kind, ts, horizon_days, regime, conviction,
		       historical_accuracy, rank
		FROM regime_forecasts ORDER BY symbol_id, kind`)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []RegimeCall
	for rows.Next() {
		var c RegimeCall
		var kind string
		if err := rows.Scan(&c.SymbolID, &kind, &c.Ts, &c.HorizonDays, &c.Regime,
			&c.Conviction, &c.HistoricalAccuracy, &c.Rank); err != nil {
			return nil, err
		}
		c.Kind = structregime.Kind(kind)
		out = append(out, c)
	}
	return out, rows.Err()
}

// HasNaiveLabelColumn reports whether regime_outcomes carries the frozen
// naive-persistence baseline column. A deployed binary older than the migration
// would keep writing baseline-less rows forever and the grader could then never
// hand out a NO SKILL verdict, so the outcome worker refuses to run without it
// rather than silently accumulating an unfalsifiable record.
func (s *Store) HasNaiveLabelColumn(ctx context.Context) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM pragma_table_info('regime_outcomes') WHERE name='naive_label'`).Scan(&n)
	return n > 0, err
}

// NullAmendmentEpoch is the UTC instant from which every frozen structural
// call must carry a matched naive-persistence baseline (2026-07-27, the
// amendment that introduced regime_outcomes.naive_label). Rows older than it
// predate the null and grade NO BASELINE; they are never backfilled, because a
// persistence label computed after the outcome is known is a hindsight
// baseline, not a null.
const NullAmendmentEpoch int64 = 1785110400 // 2026-07-27T00:00:00Z

// structuralNullKinds are the regime kinds whose write path REQUIRES a frozen
// naive-persistence baseline. These are exactly the kinds the outcome worker
// can compute a null for (structregime.Naive*At); anything else (retired
// gapfill rows, future kinds with no resolver) is not benchmarkable and is not
// silently admitted into the skill tally either — the registry grades it
// NO BASELINE.
var structuralNullKinds = map[structregime.Kind]bool{
	structregime.KindTrend21:           true,
	structregime.KindTrend63:           true,
	structregime.KindVol21:             true,
	structregime.KindLiquidity21:       true,
	structregime.KindTrendCrypto21:     true,
	structregime.KindLiquidityCrypto21: true,
}

// UnmatchedNullCount returns how many post-amendment regime_outcomes rows carry
// no frozen naive-persistence baseline AND are not members of the frozen
// quarantine manifest. It must be zero: a non-zero count means a deployed binary
// is writing rows the store-level guard below would have refused, i.e. the
// running code and this source have diverged.
//
// The quarantine exclusion is a FIXED, hash-chained set of historical rows (see
// FreezeNullQuarantine) — not an escape hatch. It cannot grow, the quarantined
// rows keep grading as NO BASELINE, and every future row still has to satisfy
// the guard exactly as before.
func (s *Store) UnmatchedNullCount(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM regime_outcomes o
		WHERE o.ts >= ? AND o.naive_label IS NULL
		  AND o.id NOT IN (SELECT outcome_id FROM regime_outcome_quarantine)`,
		NullAmendmentEpoch).Scan(&n)
	return n, err
}

// QuarantineManifest is the frozen record of the historical unmatched-null set.
type QuarantineManifest struct {
	Digest   string `json:"digest"`
	NRows    int    `json:"nRows"`
	FrozenTs int64  `json:"frozenTs"`
}

// quarantineDigest renders the quarantined membership byte-stably and hashes it.
// Ordering is by outcome_id so the digest depends on content, not on row order.
func quarantineDigest(members []string, n int) string {
	h := sha256.New()
	h.Write([]byte("regime-outcome-null-quarantine|epoch=" +
		strconv.FormatInt(NullAmendmentEpoch, 10) + "|n=" + strconv.Itoa(n)))
	for _, m := range members {
		h.Write([]byte("\x1e"))
		h.Write([]byte(m))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// quarantineMembers reads the canonical rendering of the current quarantine
// table contents, ordered by outcome_id.
func (s *Store) quarantineMembers(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT outcome_id, symbol_id, kind, day FROM regime_outcome_quarantine
		ORDER BY outcome_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []string
	for rows.Next() {
		var id, symID, day int64
		var kind string
		if err := rows.Scan(&id, &symID, &kind, &day); err != nil {
			return nil, err
		}
		out = append(out, strconv.FormatInt(id, 10)+":"+strconv.FormatInt(symID, 10)+
			":"+kind+":"+strconv.FormatInt(day, 10))
	}
	return out, rows.Err()
}

// NullQuarantineManifest returns the stored manifest, ok=false when the set has
// never been frozen.
func (s *Store) NullQuarantineManifest(ctx context.Context) (QuarantineManifest, bool, error) {
	var m QuarantineManifest
	err := s.db.QueryRowContext(ctx,
		`SELECT digest, n_rows, frozen_ts FROM regime_outcome_quarantine_manifest WHERE id = 1`).
		Scan(&m.Digest, &m.NRows, &m.FrozenTs)
	if err == sql.ErrNoRows {
		return QuarantineManifest{}, false, nil
	}
	if err != nil {
		return QuarantineManifest{}, false, err
	}
	return m, true, nil
}

// FreezeNullQuarantine populates the quarantine table ONCE from the exact set of
// post-epoch rows that currently carry a NULL naive_label, and writes the
// manifest digest over that membership. It is a no-op after the first call:
// the set is deliberately NOT growable, because a growable exemption is just the
// guard switched off on a delay. A row that becomes unmatched after the freeze
// is a live divergence and must fail the worker.
//
// Nothing is relabelled and nothing is backfilled — the quarantined rows keep
// their NULL naive_label and keep grading as NO BASELINE.
func (s *Store) FreezeNullQuarantine(ctx context.Context, now int64) (QuarantineManifest, bool, error) {
	if m, ok, err := s.NullQuarantineManifest(ctx); err != nil || ok {
		return m, false, err
	}
	if _, err := s.w.ExecContext(ctx, `
		INSERT OR IGNORE INTO regime_outcome_quarantine (outcome_id, symbol_id, kind, day, frozen_ts)
		SELECT id, symbol_id, kind, day, ? FROM regime_outcomes
		WHERE ts >= ? AND naive_label IS NULL`, now, NullAmendmentEpoch); err != nil {
		return QuarantineManifest{}, false, err
	}
	members, err := s.quarantineMembers(ctx)
	if err != nil {
		return QuarantineManifest{}, false, err
	}
	m := QuarantineManifest{
		Digest: quarantineDigest(members, len(members)), NRows: len(members), FrozenTs: now,
	}
	if _, err := s.w.ExecContext(ctx, `
		INSERT INTO regime_outcome_quarantine_manifest (id, digest, n_rows, frozen_ts)
		VALUES (1, ?, ?, ?)`, m.Digest, m.NRows, m.FrozenTs); err != nil {
		return QuarantineManifest{}, false, err
	}
	return m, true, nil
}

// VerifyNullQuarantine recomputes the digest over the quarantine table's current
// contents and compares it to the frozen manifest. Any extension, deletion or
// edit of the exempt set changes the digest and fails here — which is what makes
// the exemption externally fixed rather than merely conventional. ok=false with
// a nil error means the set has never been frozen (nothing is exempt yet).
func (s *Store) VerifyNullQuarantine(ctx context.Context) (QuarantineManifest, bool, error) {
	m, ok, err := s.NullQuarantineManifest(ctx)
	if err != nil || !ok {
		return m, false, err
	}
	members, err := s.quarantineMembers(ctx)
	if err != nil {
		return m, false, err
	}
	if got := quarantineDigest(members, len(members)); got != m.Digest {
		return m, false, fmt.Errorf("null-quarantine manifest digest mismatch: table now digests "+
			"%s over %d rows, manifest froze %s over %d — the exempt set was altered after freezing",
			got, len(members), m.Digest, m.NRows)
	}
	return m, true, nil
}

// InsertRegimeOutcome freezes one call as an ungraded outcome row, at most once
// per (symbol, kind, UTC-day of ts) — INSERT OR IGNORE on the dedup unique
// index. Returns whether a new row was written.
//
// HARD REFUSAL at the WRITE path: a structural-kind call with no frozen naive
// baseline is rejected outright. The worker also refuses at the end of a pass,
// but that is a property of one binary's control flow; this makes "every
// post-amendment structural row has a matched null" a property of the table
// itself, which is the only version a grader can rely on. The guard is strictly
// restrictive — it can only block a write, never admit a row or raise a number.
func (s *Store) InsertRegimeOutcome(ctx context.Context, c RegimeCall) (bool, error) {
	if c.NaiveLabel == "" && structuralNullKinds[c.Kind] && c.Ts >= NullAmendmentEpoch {
		return false, fmt.Errorf("refusing to freeze %s call for symbol %d at ts %d with no "+
			"naive-persistence baseline: the null would be unmatched", c.Kind, c.SymbolID, c.Ts)
	}
	res, err := s.w.ExecContext(ctx, `
		INSERT OR IGNORE INTO regime_outcomes
		  (symbol_id, kind, ts, day, horizon_days, regime, conviction,
		   historical_accuracy, rank, naive_label, revision)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		c.SymbolID, string(c.Kind), c.Ts, c.Ts/86400, c.HorizonDays, c.Regime,
		c.Conviction, c.HistoricalAccuracy, c.Rank, nullString(c.NaiveLabel),
		nullString(CodeRevision()))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if n > 0 {
		return true, nil
	}
	// The write was IGNORED — a row for this (symbol, kind, day) already exists.
	// Normally that is plain idempotence. But if the STORED row has no baseline
	// and this call carries one, the ignore silently discards the only null that
	// row will ever get, and the outcome stays permanently ungradable against a
	// benchmark. That is exactly the mechanism that manufactured the quarantined
	// historical rows, and as a bare (false, nil) it is unfalsifiable — nothing
	// downstream can tell a dedup from a dropped baseline. Make it loud instead.
	var stored sql.NullString
	if err := s.db.QueryRowContext(ctx,
		`SELECT naive_label FROM regime_outcomes WHERE symbol_id=? AND kind=? AND day=?`,
		c.SymbolID, string(c.Kind), c.Ts/86400).Scan(&stored); err != nil {
		return false, err
	}
	if !stored.Valid && c.NaiveLabel != "" {
		return false, fmt.Errorf("%w: %s call for symbol %d on day %d is stored with a NULL "+
			"naive_label and this freeze carries baseline %q; the dedup would drop it",
			ErrNaiveLabelDropped, c.Kind, c.SymbolID, c.Ts/86400, c.NaiveLabel)
	}
	return false, nil
}

// ErrNaiveLabelDropped marks the collision where an already-stored outcome row
// has no frozen naive-persistence baseline while the incoming freeze does have
// one. Typed so callers can record it distinctly rather than reading it as
// ordinary same-day deduplication.
var ErrNaiveLabelDropped = errors.New("naive baseline dropped by outcome dedup")

// RegimeOutcomeRow is one frozen call, graded or not.
type RegimeOutcomeRow struct {
	ID                 int64
	SymbolID           int64
	Kind               structregime.Kind
	Ts                 int64
	HorizonDays        int
	Regime             string
	Conviction         float64
	HistoricalAccuracy float64
	Rank               float64
	ResolvedAt         int64 // 0 = unresolved
	Actual             string
	Correct            int    // -1 unresolved, else 0/1
	NaiveLabel         string // frozen naive-persistence baseline ("" = none frozen)
}

// nullString stores "" as SQL NULL — an absent naive baseline must read as
// absent, not as an empty label that could be compared against a real one.
func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// DueRegimeOutcomes returns unresolved outcomes whose approximate forward
// window has elapsed: ts + horizon_days*1.45 calendar days <= now (trading→
// calendar approximation for stocks; the worker additionally requires enough
// NEWER daily bars before grading). Oldest first, capped.
func (s *Store) DueRegimeOutcomes(ctx context.Context, now int64, limit int) ([]RegimeOutcomeRow, error) {
	if limit <= 0 {
		limit = 5000
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, symbol_id, kind, ts, horizon_days, regime, conviction,
		       historical_accuracy, rank, COALESCE(naive_label, '')
		FROM regime_outcomes
		WHERE resolved_at IS NULL
		  AND ts + CAST(horizon_days * 1.45 * 86400 AS INTEGER) <= ?
		ORDER BY ts ASC LIMIT ?`, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []RegimeOutcomeRow
	for rows.Next() {
		var r RegimeOutcomeRow
		var kind string
		if err := rows.Scan(&r.ID, &r.SymbolID, &kind, &r.Ts, &r.HorizonDays,
			&r.Regime, &r.Conviction, &r.HistoricalAccuracy, &r.Rank,
			&r.NaiveLabel); err != nil {
			return nil, err
		}
		r.Kind = structregime.Kind(kind)
		r.Correct = -1
		out = append(out, r)
	}
	return out, rows.Err()
}

// ResolveRegimeOutcome grades one frozen call with its realized label.
func (s *Store) ResolveRegimeOutcome(ctx context.Context, id int64, actual string, correct bool, resolvedAt int64) error {
	c := 0
	if correct {
		c = 1
	}
	_, err := s.w.ExecContext(ctx, `
		UPDATE regime_outcomes SET resolved_at=?, actual=?, correct=?
		WHERE id=? AND resolved_at IS NULL`, resolvedAt, actual, c, id)
	return err
}

// ResolvedRegimeOutcomes returns graded outcomes (newest call first, capped)
// for the track-record's regimes section. Rows are unique per (symbol, kind,
// UTC-day) by the dedup index, so they already ARE the independent set.
func (s *Store) ResolvedRegimeOutcomes(ctx context.Context, limit int) ([]RegimeOutcomeRow, error) {
	if limit <= 0 {
		limit = 50000
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, symbol_id, kind, ts, horizon_days, regime, conviction,
		       historical_accuracy, rank, resolved_at, actual, correct,
		       COALESCE(naive_label, '')
		FROM regime_outcomes
		WHERE resolved_at IS NOT NULL
		ORDER BY ts DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []RegimeOutcomeRow
	for rows.Next() {
		var r RegimeOutcomeRow
		var kind string
		if err := rows.Scan(&r.ID, &r.SymbolID, &kind, &r.Ts, &r.HorizonDays,
			&r.Regime, &r.Conviction, &r.HistoricalAccuracy, &r.Rank,
			&r.ResolvedAt, &r.Actual, &r.Correct, &r.NaiveLabel); err != nil {
			return nil, err
		}
		r.Kind = structregime.Kind(kind)
		out = append(out, r)
	}
	return out, rows.Err()
}

// RegimePostmortem is one stored high-conviction-miss narrative.
type RegimePostmortem struct {
	OutcomeID       int64   `json:"outcomeId"`
	Symbol          string  `json:"symbol"`
	Kind            string  `json:"kind"`
	Ts              int64   `json:"ts"`
	Regime          string  `json:"regime"`
	Conviction      float64 `json:"conviction"`
	ClaimedAccuracy float64 `json:"claimedAccuracy"`
	Actual          string  `json:"actual"`
	KeyName         string  `json:"keyName"`
	KeyValue        float64 `json:"keyValue"`
	Narrative       string  `json:"narrative"`
	CreatedAt       int64   `json:"createdAt"`
}

// InsertRegimePostmortem stores one miss narrative, at most once per outcome
// (INSERT OR IGNORE on the UNIQUE outcome_id).
func (s *Store) InsertRegimePostmortem(ctx context.Context, outcomeID, symbolID int64,
	o RegimeOutcomeRow, keyName string, keyValue float64, narrative string, now int64,
) error {
	_, err := s.w.ExecContext(ctx, `
		INSERT OR IGNORE INTO regime_postmortems
		  (outcome_id, symbol_id, kind, ts, regime, conviction, claimed_accuracy,
		   actual, key_name, key_value, narrative, created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		outcomeID, symbolID, string(o.Kind), o.Ts, o.Regime, o.Conviction,
		o.HistoricalAccuracy, o.Actual, keyName, keyValue, narrative, now)
	return err
}

// RecentRegimePostmortems returns the newest stored regime postmortems, capped.
func (s *Store) RecentRegimePostmortems(ctx context.Context, limit int) ([]RegimePostmortem, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT pm.outcome_id, sym.symbol, pm.kind, pm.ts, pm.regime, pm.conviction,
		       pm.claimed_accuracy, pm.actual, pm.key_name, pm.key_value,
		       pm.narrative, pm.created_at
		FROM regime_postmortems pm
		JOIN symbols sym ON sym.id = pm.symbol_id
		ORDER BY pm.created_at DESC, pm.id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []RegimePostmortem
	for rows.Next() {
		var p RegimePostmortem
		if err := rows.Scan(&p.OutcomeID, &p.Symbol, &p.Kind, &p.Ts, &p.Regime,
			&p.Conviction, &p.ClaimedAccuracy, &p.Actual, &p.KeyName, &p.KeyValue,
			&p.Narrative, &p.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// LatestPeriodicFiling returns one symbol's most recent 10-Q/10-K (amendments
// excluded, matching LatestPeriodicFilingAll) — the per-symbol earnings-window
// read. ok=false when no periodic filing is stored (an honest unknown).
func (s *Store) LatestPeriodicFiling(ctx context.Context, symbolID int64) (PeriodicFiling, bool, error) {
	var p PeriodicFiling
	err := s.db.QueryRowContext(ctx, `
		SELECT form, filed_ts FROM filings
		WHERE symbol_id = ? AND form IN ('10-Q','10-K')
		ORDER BY filed_ts DESC LIMIT 1`, symbolID).Scan(&p.Form, &p.FiledTs)
	if err == sql.ErrNoRows {
		return PeriodicFiling{}, false, nil
	}
	if err != nil {
		return PeriodicFiling{}, false, err
	}
	return p, true, nil
}

// ═══ WEEKLY DIGEST WAVE (appended block — keep at END of file so parallel
// edits by other agents never collide) ═══════════════════════════════════════

// RegimeWeekCall is one frozen regime call joined to its ACTIVE symbol — the
// digest's raw material for "what changed this week".
type RegimeWeekCall struct {
	Symbol string
	Kind   structregime.Kind
	Ts     int64
	Regime string
}

// RegimeOutcomeCallsSince returns every frozen regime call with ts >= since
// for currently-active symbols, oldest first — the digest compares each
// (symbol, kind)'s EARLIEST frozen call of the week against the CURRENT
// forecast to find regime changes.
func (s *Store) RegimeOutcomeCallsSince(ctx context.Context, since int64) ([]RegimeWeekCall, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT sym.symbol, o.kind, o.ts, o.regime
		FROM regime_outcomes o JOIN symbols sym ON sym.id = o.symbol_id
		WHERE o.ts >= ? AND sym.active = 1
		ORDER BY o.ts ASC`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []RegimeWeekCall
	for rows.Next() {
		var c RegimeWeekCall
		var kind string
		if err := rows.Scan(&c.Symbol, &kind, &c.Ts, &c.Regime); err != nil {
			return nil, err
		}
		c.Kind = structregime.Kind(kind)
		out = append(out, c)
	}
	return out, rows.Err()
}
