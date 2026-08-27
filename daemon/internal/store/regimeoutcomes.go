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
	"sort"
	"strconv"
	"strings"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
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

// RegimeForecastCalls returns the current regime forecasts for symbols the
// system still tracks, with their symbol id (RegimeForecasts joins for display;
// this is the grading read).
//
// Restricted to active, non-delisted symbols. Without the join it returned
// stale forecasts for inactive S&P names whose daily bars stopped updating, and
// a naive-persistence baseline is not computable from a series that no longer
// advances. Those 8 rows made the unmatched-null refusal fire on EVERY pass
// forever — a permanently red worker cannot signal a new gap, which is the only
// thing that refusal exists to do. Scoring calls for symbols the product no
// longer covers was the actual defect; the refusal was working correctly.
func (s *Store) RegimeForecastCalls(ctx context.Context) ([]RegimeCall, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT f.symbol_id, f.kind, f.ts, f.horizon_days, f.regime, f.conviction,
		       f.historical_accuracy, f.rank
		FROM regime_forecasts f
		JOIN symbols sy ON sy.id = f.symbol_id
		WHERE sy.active = 1 AND sy.delisted_at IS NULL
		ORDER BY f.symbol_id, f.kind`)
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
// It counts ONLY the kinds the write guard actually requires a label for. The
// two disagreed: the guard refuses a label-less row when structuralNullKinds
// covers its kind, but this counter counted every kind. filingsdrift21 is
// deliberately outside that set -- no resolver can compute a null for it, and
// the registry grades it NO BASELINE -- so rows the writer was ENTITLED to write
// were being read as proof that "the deployed binary differs from source".
// On the live store that was 7 of 15 unmatched rows, and regime-outcome-runner
// refused to run because of them, blocking every structural grade indefinitely.
// A guard that fires on output its own writer is allowed to produce cannot be
// satisfied by any correct binary; that is a broken guard, not a strict one.
func (s *Store) UnmatchedNullCount(ctx context.Context) (int, error) {
	kinds := make([]string, 0, len(structuralNullKinds))
	for k := range structuralNullKinds {
		kinds = append(kinds, string(k))
	}
	sort.Strings(kinds) // deterministic SQL for stable query plans and logs
	args := make([]any, 0, len(kinds)+1)
	args = append(args, NullAmendmentEpoch)
	for _, k := range kinds {
		args = append(args, k)
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(kinds)), ",")

	var n int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM regime_outcomes o
		WHERE o.ts >= ? AND o.naive_label IS NULL AND o.superseded_by IS NULL
		  AND o.kind IN (`+ph+`)
		  AND o.id NOT IN (SELECT outcome_id FROM regime_outcome_quarantine)`,
		args...).Scan(&n)
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

// ExtendNullQuarantine admits a NAMED set of already-written rows into the
// exempt set and re-freezes the manifest over the new membership.
//
// FreezeNullQuarantine is one-shot on purpose: a freely growable exemption is
// the guard switched off on a delay. But that design assumed divergence is
// transient — deploy the correct binary and no new unmatched rows appear. It has
// no remedy for rows ALREADY written during a divergence window, and eight of
// them (vol21/liquidity21, 2026-07-28..29, from a binary predating revision
// stamping) permanently blocked regime-outcome-runner. Blocked forever means no
// structural call is ever graded, which is the entire point of the system.
//
// The safety property is that the caller must name every row EXACTLY. If the
// live unmatched set differs from expectIDs in either direction — one row more,
// one row fewer, one different id — this refuses and changes nothing. So it
// cannot be used to wave through "whatever is currently broken": the operator
// has to enumerate the damage, and any drift between deciding and executing
// aborts the amendment. Nothing is relabelled or backfilled; the rows keep their
// NULL naive_label and keep grading NO BASELINE.
func (s *Store) ExtendNullQuarantine(ctx context.Context, now int64, expectIDs []int64) (QuarantineManifest, error) {
	if len(expectIDs) == 0 {
		return QuarantineManifest{}, errors.New("extend null quarantine: no ids named; " +
			"an amnesty must enumerate exactly what it forgives")
	}
	if _, ok, err := s.NullQuarantineManifest(ctx); err != nil {
		return QuarantineManifest{}, err
	} else if !ok {
		return QuarantineManifest{}, errors.New("extend null quarantine: nothing frozen yet; " +
			"call FreezeNullQuarantine first")
	}

	actual, err := s.unmatchedNullIDs(ctx)
	if err != nil {
		return QuarantineManifest{}, err
	}
	want := append([]int64(nil), expectIDs...)
	sort.Slice(want, func(i, j int) bool { return want[i] < want[j] })
	if len(actual) != len(want) {
		return QuarantineManifest{}, fmt.Errorf("extend null quarantine: named %d row(s) but %d are "+
			"unmatched right now; refusing to amend a set that moved since it was reviewed",
			len(want), len(actual))
	}
	for i := range actual {
		if actual[i] != want[i] {
			return QuarantineManifest{}, fmt.Errorf("extend null quarantine: named id %d where the "+
				"live set holds %d; the exempt set must be enumerated exactly", want[i], actual[i])
		}
	}

	for _, id := range actual {
		if _, err := s.w.ExecContext(ctx, `
			INSERT OR IGNORE INTO regime_outcome_quarantine (outcome_id, symbol_id, kind, day, frozen_ts)
			SELECT id, symbol_id, kind, day, ? FROM regime_outcomes WHERE id = ?`, now, id); err != nil {
			return QuarantineManifest{}, err
		}
	}
	members, err := s.quarantineMembers(ctx)
	if err != nil {
		return QuarantineManifest{}, err
	}
	m := QuarantineManifest{
		Digest: quarantineDigest(members, len(members)), NRows: len(members), FrozenTs: now,
	}
	if _, err := s.w.ExecContext(ctx, `
		UPDATE regime_outcome_quarantine_manifest SET digest=?, n_rows=?, frozen_ts=? WHERE id=1`,
		m.Digest, m.NRows, m.FrozenTs); err != nil {
		return QuarantineManifest{}, err
	}
	return m, nil
}

// unmatchedNullIDs lists, ascending, the post-epoch rows of a guarded kind that
// carry no baseline and are not already exempt — the exact set UnmatchedNullCount
// counts.
func (s *Store) unmatchedNullIDs(ctx context.Context) ([]int64, error) {
	kinds := make([]string, 0, len(structuralNullKinds))
	for k := range structuralNullKinds {
		kinds = append(kinds, string(k))
	}
	sort.Strings(kinds)
	args := make([]any, 0, len(kinds)+1)
	args = append(args, NullAmendmentEpoch)
	for _, k := range kinds {
		args = append(args, k)
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(kinds)), ",")
	rows, err := s.db.QueryContext(ctx, `
		SELECT o.id FROM regime_outcomes o
		WHERE o.ts >= ? AND o.naive_label IS NULL AND o.superseded_by IS NULL
		  AND o.kind IN (`+ph+`)
		  AND o.id NOT IN (SELECT outcome_id FROM regime_outcome_quarantine)
		ORDER BY o.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
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
// per (symbol, kind, SETTLED MOVE of ts) — INSERT OR IGNORE on the dedup unique
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
	// Folded on the SETTLED MOVE like every other outcome table. `day` is the
	// dedup key, so the fold decides what gets WRITTEN, not merely what gets
	// counted: under the calendar fold a Friday-evening, Saturday and Sunday call
	// all describing the same Friday base bar were admitted as three observations.
	// migrateRegimeOutcomesToSettleDay re-folded the history the same way and
	// superseded the losers rather than deleting them.
	//
	// settle_ts is resolved here rather than left NULL so the row carries the key
	// it was deduped under. A call with no 1d bar at or before it keeps settle_ts
	// NULL and md.SettleDay falls back to the trading day — the honest
	// degradation, and measured as never occurring on the live corpus.
	settleTs, err := s.settleBarFor(ctx, c.SymbolID, c.Ts)
	if err != nil {
		return false, err
	}
	// Resolved ONCE and reused by the dedup-collision probe below. Recomputing it
	// there is how the trading-day fold broke: the probe looked the stored row up
	// under a different fold than the INSERT wrote it with, found nothing, and
	// turned every ordinary dedup into "sql: no rows in result set".
	day := md.SettleDay(settleTs, c.Ts)
	res, err := s.w.ExecContext(ctx, `
		INSERT OR IGNORE INTO regime_outcomes
		  (symbol_id, kind, ts, day, settle_ts, horizon_days, regime, conviction,
		   historical_accuracy, rank, naive_label, revision)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		c.SymbolID, string(c.Kind), c.Ts, day, nullInt64(settleTs),
		c.HorizonDays, c.Regime,
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
		`SELECT naive_label FROM regime_outcomes
		   WHERE symbol_id=? AND kind=? AND day=? AND superseded_by IS NULL`,
		c.SymbolID, string(c.Kind), day).Scan(&stored); err != nil {
		return false, err
	}
	if !stored.Valid && c.NaiveLabel != "" {
		return false, fmt.Errorf("%w: %s call for symbol %d on day %d is stored with a NULL "+
			"naive_label and this freeze carries baseline %q; the dedup would drop it",
			ErrNaiveLabelDropped, c.Kind, c.SymbolID, day, c.NaiveLabel)
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
	// Day is the STORED settled-move key this row was deduped under. Read rather
	// than recomputed from Ts: the dedup index is built on this column, so a
	// consumer that re-derives the fold can disagree with the table about which
	// rows are the same observation.
	Day int64
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
		SELECT id, symbol_id, kind, ts, day, horizon_days, regime, conviction,
		       historical_accuracy, rank, COALESCE(naive_label, '')
		FROM regime_outcomes
		WHERE resolved_at IS NULL AND superseded_by IS NULL AND ungradable IS NULL
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
		if err := rows.Scan(&r.ID, &r.SymbolID, &kind, &r.Ts, &r.Day, &r.HorizonDays,
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

// MarkRegimeOutcomeUngradable records WHY a due call can never be graded, so it
// leaves the due queue instead of being retried forever.
//
// This exists because "retried later" was a lie for any symbol the universe
// sweep pruned: an inactive symbol stops receiving daily bars, so a row short of
// its horizon can never reach it. Measured 2026-08-27, 2,267 of 2,288 due rows
// sat on inactive symbols and NONE had enough forward bars, while the worker
// reported ok/resolved 0 for 31 days.
//
// The row is KEPT with all its frozen bytes — the call, the conviction, the
// baseline — exactly as confluence_outcomes.ungradable keeps its entry_px. An
// excluded observation that is counted and reasoned is evidence; one that is
// silently retried is a survivorship hole.
//
// Never overwrites: a row that already carries a reason, or that got graded in
// the meantime, is left alone.
func (s *Store) MarkRegimeOutcomeUngradable(ctx context.Context, id int64, reason string) error {
	_, err := s.w.ExecContext(ctx, `
		UPDATE regime_outcomes SET ungradable=?
		WHERE id=? AND resolved_at IS NULL AND ungradable IS NULL`, reason, id)
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
		SELECT id, symbol_id, kind, ts, day, horizon_days, regime, conviction,
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
		if err := rows.Scan(&r.ID, &r.SymbolID, &kind, &r.Ts, &r.Day, &r.HorizonDays,
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
