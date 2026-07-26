// Evidence Engine persistence — claims and their machine-checkable evidence
// items (see internal/evidence for the types, rules and sweep).
//
// The store is deliberately dumb here: it round-trips rows and flips
// status/tier under the sweep's instruction. All judgement — whether a tier is
// justified, whether evidence refutes, when a claim is stale — lives in
// internal/evidence, so the rules are testable without a database and the
// database cannot end up holding a claim the rules would reject (PutEvidence
// callers validate first; the API only reads).
//
// Reads use the pooled s.db handle; writes go through s.w, matching the
// store's single-writer discipline.
package store

import (
	"context"
	"database/sql"
	"time"
)

// EvidenceClaimRow is one persisted claim, items attached.
type EvidenceClaimRow struct {
	ID            string
	Text          string
	ScopeJSON     string
	Tier          string
	Status        string
	LastValidated int64
	RevalidateBy  int64
	LineageJSON   string
	Seeded        bool
	UpdatedAt     int64
	Items         []EvidenceItemRow
}

// EvidenceItemRow is one measured piece of support for a claim.
type EvidenceItemRow struct {
	Kind       string
	Value      float64
	NEffective float64
	Method     string
	Correction string
	CILow      *float64
	CIHigh     *float64
	Baseline   *float64
	SourceRef  string
}

// PutEvidenceClaim writes a claim and its items in ONE transaction, replacing
// any prior version of the same id. Validation is the caller's job
// (evidence.Validate) — this layer only persists.
func (s *Store) PutEvidenceClaim(ctx context.Context, c EvidenceClaimRow) error {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO evidence_claims
		  (id, text, scope_json, tier, status, last_validated, revalidate_by, lineage_json, seeded, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
		  text=excluded.text, scope_json=excluded.scope_json, tier=excluded.tier,
		  status=excluded.status, last_validated=excluded.last_validated,
		  revalidate_by=excluded.revalidate_by, lineage_json=excluded.lineage_json,
		  seeded=excluded.seeded, updated_at=excluded.updated_at`,
		c.ID, c.Text, c.ScopeJSON, c.Tier, c.Status, c.LastValidated,
		c.RevalidateBy, c.LineageJSON, boolInt(c.Seeded), time.Now().Unix()); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM evidence_items WHERE claim_id=?`, c.ID); err != nil {
		return err
	}
	st, err := tx.PrepareContext(ctx, `
		INSERT INTO evidence_items
		  (claim_id, idx, kind, value, n_effective, method, correction, ci_low, ci_high, baseline, source_ref)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer st.Close() //nolint:errcheck
	for i, it := range c.Items {
		if _, err := st.ExecContext(ctx, c.ID, i, it.Kind, it.Value, it.NEffective,
			it.Method, it.Correction, it.CILow, it.CIHigh, it.Baseline, it.SourceRef); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// EvidenceClaimExists reports whether a claim id is already stored (used by
// idempotent seeding — a seed never overwrites a live, possibly-swept row).
func (s *Store) EvidenceClaimExists(ctx context.Context, id string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM evidence_claims WHERE id=?`, id).Scan(&n)
	return n > 0, err
}

// EvidenceClaim returns one claim with items, or sql.ErrNoRows.
func (s *Store) EvidenceClaim(ctx context.Context, id string) (EvidenceClaimRow, error) {
	rows, err := s.evidenceClaims(ctx, `WHERE id=?`, id)
	if err != nil {
		return EvidenceClaimRow{}, err
	}
	if len(rows) == 0 {
		return EvidenceClaimRow{}, sql.ErrNoRows
	}
	return rows[0], nil
}

// EvidenceClaims lists claims, optionally filtered by status and/or a lineage
// feature key (substring match inside lineage_json — feature keys are plain
// identifiers, so a LIKE with quotes is exact enough without a join table).
func (s *Store) EvidenceClaims(ctx context.Context, status, feature string) ([]EvidenceClaimRow, error) {
	where, args := "", []any{}
	switch {
	case status != "" && feature != "":
		where, args = `WHERE status=? AND lineage_json LIKE ?`, []any{status, `%"` + feature + `"%`}
	case status != "":
		where, args = `WHERE status=?`, []any{status}
	case feature != "":
		where, args = `WHERE lineage_json LIKE ?`, []any{`%"` + feature + `"%`}
	}
	return s.evidenceClaims(ctx, where, args...)
}

func (s *Store) evidenceClaims(ctx context.Context, where string, args ...any) ([]EvidenceClaimRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, text, scope_json, tier, status, last_validated, revalidate_by,
		       lineage_json, seeded, updated_at
		FROM evidence_claims `+where+` ORDER BY id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []EvidenceClaimRow
	byID := map[string]int{}
	for rows.Next() {
		var c EvidenceClaimRow
		var seeded int
		if err := rows.Scan(&c.ID, &c.Text, &c.ScopeJSON, &c.Tier, &c.Status,
			&c.LastValidated, &c.RevalidateBy, &c.LineageJSON, &seeded, &c.UpdatedAt); err != nil {
			return nil, err
		}
		c.Seeded = seeded != 0
		byID[c.ID] = len(out)
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return out, nil
	}
	irows, err := s.db.QueryContext(ctx, `
		SELECT claim_id, kind, value, n_effective, method, correction, ci_low, ci_high, baseline, source_ref
		FROM evidence_items ORDER BY claim_id, idx`)
	if err != nil {
		return nil, err
	}
	defer irows.Close() //nolint:errcheck
	for irows.Next() {
		var claimID string
		var it EvidenceItemRow
		if err := irows.Scan(&claimID, &it.Kind, &it.Value, &it.NEffective,
			&it.Method, &it.Correction, &it.CILow, &it.CIHigh, &it.Baseline, &it.SourceRef); err != nil {
			return nil, err
		}
		if i, ok := byID[claimID]; ok {
			out[i].Items = append(out[i].Items, it)
		}
	}
	return out, irows.Err()
}

// SetEvidenceClaimState flips a claim's status and tier — the sweep's write
// path. last_validated is NOT touched: a downgrade is not a validation.
func (s *Store) SetEvidenceClaimState(ctx context.Context, id, status, tier string) error {
	_, err := s.w.ExecContext(ctx, `
		UPDATE evidence_claims SET status=?, tier=?, updated_at=? WHERE id=?`,
		status, tier, time.Now().Unix(), id)
	return err
}
