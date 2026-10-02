// Intraday compaction for the DERIVED score tables — persistence for the
// scores-compactor (maintain). Complements retention.go's DerivedRetention
// (age-based archive+prune at the far 90d tier): the bloat that actually
// dominates the file TODAY is the 10-minute full-universe cadence INSIDE the
// window — ~1KB components JSON per scores row and ~3KB payload per
// composite_scores row that only the LATEST row per symbol ever renders
// (measured 2026-07-16: scores = 3.2GB of a 5.4GB file). The compactor
// strips those blobs past a short window and downsamples intraday rows past a
// longer one to one row per (symbol, horizon, UTC-day) — ALWAYS
// archive-before-transform, the house fail-safe.
//
// Writes go through s.w, reads through the pooled s.db, as everywhere else.
package store

import (
	"context"
	"database/sql"
	"strings"
)

// CompositeArchiveRow is one full composite_scores row bound for the cold
// archive before its payload is stripped (or the row downsampled away).
// (scores rows reuse retention.go's ScoreRow + archive.ArchiveScores.)
type CompositeArchiveRow struct {
	SymbolID int64
	Ts       int64
	Horizon  string
	Score    int64
	CurvePct float64
	Edge     float64
	Payload  string
}

// ScoresHeavyBelow returns up to limit scores rows older than cutoff that
// still carry a components blob, oldest first — the compactor's strip feed.
// Each (symbol, horizon)'s NEWEST row is carved out (never selected, never
// stripped): a delisted or long-stalled symbol keeps its last rendered
// decomposition instead of a blanked panel.
func (s *Store) ScoresHeavyBelow(ctx context.Context, cutoff int64, limit int) ([]ScoreRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT symbol_id, horizon, ts, score, components FROM scores
		WHERE ts < ? AND components != '[]'
		  AND EXISTS (SELECT 1 FROM scores s3
		              WHERE s3.symbol_id=scores.symbol_id AND s3.horizon=scores.horizon
		                AND s3.ts>scores.ts)
		ORDER BY ts ASC LIMIT ?`, cutoff, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []ScoreRow
	for rows.Next() {
		var r ScoreRow
		if err := rows.Scan(&r.SymbolID, &r.Horizon, &r.Ts, &r.Score, &r.Components); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// StripScoreComponents empties the components blob for EXACTLY the rows given
// (by primary key), which the caller has just durably archived.
//
// KEY-SET, NOT WINDOW (council round 2): the earlier form stripped a [from,to)
// ts window and RE-EVALUATED the newest-row carve-out predicate at UPDATE time.
// That predicate is mutable — a stalled symbol resuming its normal scoring
// cadence inserts a newer row, flipping a carved-out (never-archived) row into
// the strip set. Two advisors reproduced that TOCTOU with probes. Stripping the
// archived key set makes strip-set == archive-set BY CONSTRUCTION: no
// concurrent write can add a row to it, so the archive-before-transform
// invariant holds structurally, not by timing luck.
func (s *Store) StripScoreComponents(ctx context.Context, rows []ScoreRow) (int64, error) {
	var total int64
	for i := 0; i < len(rows); i += stripKeyChunk {
		end := i + stripKeyChunk
		if end > len(rows) {
			end = len(rows)
		}
		batch := rows[i:end]
		var sb strings.Builder
		sb.WriteString(`UPDATE scores SET components='[]' WHERE (symbol_id, horizon, ts) IN (VALUES `)
		args := make([]any, 0, len(batch)*3)
		for j, r := range batch {
			if j > 0 {
				sb.WriteString(",")
			}
			sb.WriteString("(?,?,?)")
			args = append(args, r.SymbolID, r.Horizon, r.Ts)
		}
		sb.WriteString(")")
		res, err := s.w.ExecContext(ctx, sb.String(), args...)
		if err != nil {
			return total, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return total, err
		}
		total += n
	}
	return total, nil
}

// stripKeyChunk bounds the row-value tuples per UPDATE (SQLite caps host
// parameters; 500 keys = 1500 params, comfortably inside the default 32k).
const stripKeyChunk = 500

// PruneScoresKeepDailyLast deletes intraday scores rows older than cutoff,
// keeping the LAST row per (symbol, horizon, UTC-day) so daily-resolution
// history (charts, overlays, evolution panels) stays hot.
//
// STRUCTURAL FAIL-SAFE (council-mandated): the DELETE refuses any row still
// carrying its components blob (components != '[]'). A stripped row is BY
// CONSTRUCTION a row whose full form was durably archived (the strip only ever
// runs after ArchiveScores succeeds), so the archive-before-destroy invariant
// is enforced in SQL — a sustained archive failure or a window misconfig can
// delay downsampling, but can never destroy an un-archived blob.
//
// ONE UTC DAY AT A TIME, IN SHORT BATCHES (pruneKeepDailyLast). As a single
// DELETE it walked every row below the cutoff inside one write: 1m17s holding
// the write lock on 2026-10-01, with a sign-in waiting 10.3 s behind it (12.7 s
// behind the strip pass). Between batches the priority gate lets account
// writes in.
func (s *Store) PruneScoresKeepDailyLast(ctx context.Context, cutoff int64) (int64, error) {
	return s.pruneKeepDailyLast(ctx, "scores", "components = '[]'", cutoff)
}

// pruneKeepDailyLast is the keep-the-last DELETE shared by scores and
// composite_scores, one UTC day at a time and, within a day, in deleteInBatches'
// short statements. A whole day as one statement still held the write lock
// 1m36.8s (scores, 2026-10-01 22:45). Any batching ends at the same table: a
// row is deleted only while a newer row of its (symbol, horizon, day) exists,
// and the newest one never qualifies, so it is never deleted.
//
// The same-day test is a range (k.ts below the next midnight), not
// k.ts/86400 = ts/86400: the division cannot seek, so for each day's newest
// row the probe walked every later row of its (symbol, horizon).
//
// table and stripped are fixed literals from the two callers (stripped is the
// archive-before-destroy sentinel), never input.
func (s *Store) pruneKeepDailyLast(ctx context.Context, table, stripped string, cutoff int64) (int64, error) {
	var lo sql.NullInt64
	if err := s.db.QueryRowContext(ctx,
		`SELECT MIN(ts) FROM `+table+` WHERE ts < ? AND `+stripped, cutoff).Scan(&lo); err != nil {
		return 0, err
	}
	if !lo.Valid {
		return 0, nil
	}
	del := `DELETE FROM ` + table + ` WHERE (symbol_id, horizon, ts) IN (
		SELECT r.symbol_id, r.horizon, r.ts FROM ` + table + ` r
		WHERE r.ts >= ? AND r.ts < ? AND r.` + stripped + ` AND EXISTS (
			SELECT 1 FROM ` + table + ` k
			WHERE k.symbol_id = r.symbol_id AND k.horizon = r.horizon
			  AND k.ts > r.ts AND k.ts < (r.ts/86400 + 1) * 86400)
		LIMIT ?)`
	var total int64
	for day := lo.Int64 - lo.Int64%86400; day < cutoff; day += 86400 {
		n, err := s.deleteInBatches(ctx, del, day, min(day+86400, cutoff))
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

// CompositeHeavyBelow / StripCompositePayload / PruneCompositeKeepDailyLast
// mirror the scores trio for composite_scores (payload sentinel '{}').

func (s *Store) CompositeHeavyBelow(ctx context.Context, cutoff int64, limit int) ([]CompositeArchiveRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT symbol_id, ts, horizon, score, curve_pct, edge, payload FROM composite_scores
		WHERE ts < ? AND payload != '{}'
		  AND EXISTS (SELECT 1 FROM composite_scores c3
		              WHERE c3.symbol_id=composite_scores.symbol_id AND c3.horizon=composite_scores.horizon
		                AND c3.ts>composite_scores.ts)
		ORDER BY ts ASC LIMIT ?`, cutoff, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []CompositeArchiveRow
	for rows.Next() {
		var r CompositeArchiveRow
		if err := rows.Scan(&r.SymbolID, &r.Ts, &r.Horizon, &r.Score, &r.CurvePct, &r.Edge, &r.Payload); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// StripCompositePayload is StripScoreComponents's twin: key-set strip of
// exactly the archived rows (same TOCTOU reasoning).
func (s *Store) StripCompositePayload(ctx context.Context, rows []CompositeArchiveRow) (int64, error) {
	var total int64
	for i := 0; i < len(rows); i += stripKeyChunk {
		end := i + stripKeyChunk
		if end > len(rows) {
			end = len(rows)
		}
		batch := rows[i:end]
		var sb strings.Builder
		sb.WriteString(`UPDATE composite_scores SET payload='{}' WHERE (symbol_id, ts, horizon) IN (VALUES `)
		args := make([]any, 0, len(batch)*3)
		for j, r := range batch {
			if j > 0 {
				sb.WriteString(",")
			}
			sb.WriteString("(?,?,?)")
			args = append(args, r.SymbolID, r.Ts, r.Horizon)
		}
		sb.WriteString(")")
		res, err := s.w.ExecContext(ctx, sb.String(), args...)
		if err != nil {
			return total, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return total, err
		}
		total += n
	}
	return total, nil
}

// PruneCompositeKeepDailyLast is PruneScoresKeepDailyLast for composite_scores
// (payload sentinel '{}'), one UTC day per statement for the same reason: as one
// DELETE it held the write lock 35.4 s on 2026-10-01 23:30.
func (s *Store) PruneCompositeKeepDailyLast(ctx context.Context, cutoff int64) (int64, error) {
	return s.pruneKeepDailyLast(ctx, "composite_scores", "payload = '{}'", cutoff)
}

// Path returns the database file path (for disk-headroom checks by callers
// that must never guess where the DB lives).
func (s *Store) Path() string { return s.path }

// ─────────────────────────────────────────────────────────────────────────
// RESEARCH_WEEKS RETENTION (appended block — unmanaged-table sweep, phase 3).
//
// research_weeks is ALREADY at the downsampled grain the reaudit asked for —
// PRIMARY KEY (symbol_id, week) structurally guarantees one row per (symbol,
// week). What was missing is any bound on the WINDOW: the evidence base grows
// a full universe-width band of ~1KB vec rows every week, forever (measured
// 165MB live). These helpers give rows past the active research window the
// house archive-before-prune contract. Two coupled honesty constraints:
//
//   - The hist-backfill worker recomputes the base DAILY from rowsFrom onward
//     (REPLACE on the key), so the prune floor and the recompute floor MUST
//     move in lockstep — pipeline.HistoryBackfillWorker clamps its rowsFrom
//     to maintain.RetentionResearchWeeksDays(); pruning without that clamp
//     would resurrect rows daily and re-archive them hourly (churn loop).
//   - Nothing is ever lost: every pruned row is archived first, AND the base
//     is recompute-idempotent from daily bars, which are NEVER pruned — so
//     widening the window later rebuilds the hot rows from source.
// ─────────────────────────────────────────────────────────────────────────

// ResearchWeekArchiveRow is one research_weeks row for the cold archive. The
// vec JSON is carried verbatim so the archived evidence base round-trips
// exactly into offline research (DuckDB/pandas).
type ResearchWeekArchiveRow struct {
	SymbolID  int64
	Week      int64
	Ts        int64
	Vec       string
	FwdReturn float64
	Up        int
	Era       string
	HighVol   int
	CreatedAt int64
}

// ResearchWeeksBefore returns up to limit research_weeks rows with ts <
// cutoff (the anchor daily-bar ts), ts ascending — archive-before-prune read,
// same contract as ScoresBefore in retention.go.
func (s *Store) ResearchWeeksBefore(ctx context.Context, cutoff int64, limit int) ([]ResearchWeekArchiveRow, error) {
	if limit <= 0 {
		limit = 1 << 30
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT symbol_id, week, ts, vec, fwd_return, up, era, high_vol, created_at
		FROM research_weeks WHERE ts < ? ORDER BY ts, symbol_id LIMIT ?`, cutoff, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	out := make([]ResearchWeekArchiveRow, 0, 8)
	for rows.Next() {
		var r ResearchWeekArchiveRow
		if err := rows.Scan(&r.SymbolID, &r.Week, &r.Ts, &r.Vec, &r.FwdReturn, &r.Up, &r.Era, &r.HighVol, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// DeleteResearchWeeksBefore deletes research_weeks rows with ts < cutoff
// (retention). Callers MUST have durably archived the rows first.
func (s *Store) DeleteResearchWeeksBefore(ctx context.Context, cutoff int64) (int64, error) {
	return s.deleteInBatches(ctx, `
		DELETE FROM research_weeks WHERE (symbol_id, week) IN (
		  SELECT symbol_id, week FROM research_weeks WHERE ts < ? LIMIT ?)`, cutoff)
}
