// Store methods for the anomaly layer (Signal8 wave — Stage 3; see
// internal/anomaly). Kept in this file — separate from store.go — so the
// anomaly wave never touches the core write paths.
//
// Dedup contract: idx_anomalies_dedup is UNIQUE on (symbol_id, kind,
// hour_bucket) where hour_bucket = ts/3600, so a condition that persists
// across successive 5-minute scans becomes ONE stored anomaly per hour, and
// a re-run of the same scan is a no-op (INSERT OR IGNORE) — the exact
// idempotency pattern idx_alerts_dedup uses for alerts.
package store

import (
	"context"
	"database/sql"
)

// AnomalyRow is one detected anomaly (a DESCRIPTIVE statistic, never a
// prediction — Detail states the window + baseline the z was measured over).
type AnomalyRow struct {
	ID       int64   `json:"id"`
	SymbolID int64   `json:"-"`
	Symbol   string  `json:"symbol,omitempty"`
	Market   string  `json:"market,omitempty"`
	Ts       int64   `json:"ts"`
	Kind     string  `json:"kind"` // anomaly_imbalance | anomaly_vol | anomaly_volume
	Z        float64 `json:"z"`
	Detail   string  `json:"detail"`
}

// InsertAnomaly stores one anomaly row; reports whether a NEW row was
// written (false = deduped: same symbol+kind already recorded this hour).
func (s *Store) InsertAnomaly(ctx context.Context, a AnomalyRow) (bool, error) {
	res, err := s.w.ExecContext(ctx, `
		INSERT OR IGNORE INTO anomalies (symbol_id, ts, kind, z, detail, hour_bucket)
		VALUES (?,?,?,?,?,?)`,
		a.SymbolID, a.Ts, a.Kind, a.Z, a.Detail, a.Ts/3600)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// Anomalies returns recent anomalies newest-first. symbolID=0 ⇒ fleet-wide;
// kind="" ⇒ all kinds.
func (s *Store) Anomalies(ctx context.Context, symbolID int64, kind string, limit int) ([]AnomalyRow, error) {
	q := `SELECT a.id, a.symbol_id, COALESCE(sym.symbol,''), COALESCE(sym.market,''),
	             a.ts, a.kind, a.z, a.detail
	      FROM anomalies a LEFT JOIN symbols sym ON sym.id = a.symbol_id
	      WHERE 1=1`
	args := []any{}
	if symbolID != 0 {
		q += ` AND a.symbol_id=?`
		args = append(args, symbolID)
	}
	if kind != "" {
		q += ` AND a.kind=?`
		args = append(args, kind)
	}
	q += ` ORDER BY a.ts DESC, a.id DESC LIMIT ?`
	args = append(args, limit)
	return s.scanAnomalies(ctx, q, args...)
}

// AnomaliesAfterID returns anomalies with id > afterID (ascending id), at or
// after sinceTs (0 = no time floor) — the alert engine's gap-free sweep
// source, mirroring BreakoutsAfterID.
func (s *Store) AnomaliesAfterID(ctx context.Context, afterID, sinceTs int64, limit int) ([]AnomalyRow, error) {
	return s.scanAnomalies(ctx, `
		SELECT a.id, a.symbol_id, COALESCE(sym.symbol,''), COALESCE(sym.market,''),
		       a.ts, a.kind, a.z, a.detail
		FROM anomalies a LEFT JOIN symbols sym ON sym.id = a.symbol_id
		WHERE a.id > ? AND a.ts >= ?
		ORDER BY a.id LIMIT ?`, afterID, sinceTs, limit)
}

// MaxAnomalyID returns the current max anomalies.id (0 when empty) — the
// first-sweep cursor seed, mirroring MaxBreakoutID.
func (s *Store) MaxAnomalyID(ctx context.Context) (int64, error) {
	var id sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT MAX(id) FROM anomalies`).Scan(&id)
	return id.Int64, err
}

// AnomaliesBelow returns up to limit anomalies with ts < cutoff, ordered by
// ts ascending (oldest first) — the archive-before-prune read for the
// retention tier, same contract as BarsBelow/SnapsBelow: the caller archives
// the returned rows, then prunes the SAME [<cutoff) predicate.
func (s *Store) AnomaliesBelow(ctx context.Context, cutoff int64, limit int) ([]AnomalyRow, error) {
	if limit <= 0 {
		limit = 1 << 30
	}
	return s.scanAnomalies(ctx, `
		SELECT a.id, a.symbol_id, COALESCE(sym.symbol,''), COALESCE(sym.market,''),
		       a.ts, a.kind, a.z, a.detail
		FROM anomalies a LEFT JOIN symbols sym ON sym.id = a.symbol_id
		WHERE a.ts < ? ORDER BY a.ts, a.id LIMIT ?`, cutoff, limit)
}

// PruneAnomalies deletes anomalies older than cutoff (retention). Callers
// MUST have durably archived the rows first (Downsampler fail-safe).
func (s *Store) PruneAnomalies(ctx context.Context, cutoff int64) (int64, error) {
	res, err := s.w.ExecContext(ctx, `DELETE FROM anomalies WHERE ts < ?`, cutoff)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (s *Store) scanAnomalies(ctx context.Context, q string, args ...any) ([]AnomalyRow, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	out := make([]AnomalyRow, 0, 8)
	for rows.Next() {
		var a AnomalyRow
		if err := rows.Scan(&a.ID, &a.SymbolID, &a.Symbol, &a.Market, &a.Ts, &a.Kind, &a.Z, &a.Detail); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
