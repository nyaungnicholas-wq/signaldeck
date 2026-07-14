// Self-audit store methods (self-audit / drift-watchdog wave). The self-audit
// worker MEASURES the platform's own honesty from resolved history and records
// each finding here; GET /api/self-audit reads the latest finding per metric.
// Writes go through the single writer s.w; reads use s.db.
//
// HONESTY: every finding carries a status; below the sample-size gate the
// status is 'insufficient' with value 0, so a thin measurement never masquerades
// as a real one. LastSelfAuditValue deliberately skips 'insufficient' rows so a
// drift comparison is never made against a placeholder zero.
package store

import (
	"context"
	"database/sql"
)

// SelfAuditRow is one recorded self-audit finding.
type SelfAuditRow struct {
	ID       int64   `json:"-"`
	Ts       int64   `json:"ts"`
	Metric   string  `json:"metric"`
	SymbolID *int64  `json:"symbolId,omitempty"`
	Value    float64 `json:"value"`
	Status   string  `json:"status"`
	Detail   string  `json:"detail"`
}

// InsertSelfAudit appends one finding.
func (s *Store) InsertSelfAudit(ctx context.Context, r SelfAuditRow) error {
	var sid sql.NullInt64
	if r.SymbolID != nil {
		sid = sql.NullInt64{Int64: *r.SymbolID, Valid: true}
	}
	_, err := s.w.ExecContext(ctx, `
		INSERT INTO self_audit (ts, metric, symbol_id, value, status, detail)
		VALUES (?,?,?,?,?,?)`, r.Ts, r.Metric, sid, r.Value, r.Status, r.Detail)
	return err
}

// LatestSelfAudit returns the newest finding for each metric, ordered by metric.
// This is the /api/self-audit payload: one honest current status per check.
func (s *Store) LatestSelfAudit(ctx context.Context) ([]SelfAuditRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT s.id, s.ts, s.metric, s.symbol_id, s.value, s.status, s.detail
		FROM self_audit s
		JOIN (SELECT metric, MAX(ts) AS mt FROM self_audit GROUP BY metric) m
		  ON m.metric = s.metric AND m.mt = s.ts
		ORDER BY s.metric, s.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []SelfAuditRow
	for rows.Next() {
		var r SelfAuditRow
		var sid sql.NullInt64
		if err := rows.Scan(&r.ID, &r.Ts, &r.Metric, &sid, &r.Value, &r.Status, &r.Detail); err != nil {
			return nil, err
		}
		if sid.Valid {
			r.SymbolID = &sid.Int64
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// LastSelfAuditValue returns the value of the most recent MEASURED finding for a
// metric (status != 'insufficient'), so a drift/sign comparison is made against
// a real prior, never a placeholder zero. ok=false when there is no prior.
func (s *Store) LastSelfAuditValue(ctx context.Context, metric string) (float64, bool) {
	var v float64
	err := s.db.QueryRowContext(ctx, `
		SELECT value FROM self_audit
		WHERE metric=? AND status != 'insufficient'
		ORDER BY ts DESC, id DESC LIMIT 1`, metric).Scan(&v)
	if err != nil {
		return 0, false
	}
	return v, true
}
