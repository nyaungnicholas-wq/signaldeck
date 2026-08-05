// Publication-verdict and grader-heartbeat store methods.
//
// publication_verdicts is append-only: one row per (predictor, horizon,
// variant) per grading pass, and the newest row by evaluated_at is the current
// public state. Nothing updates, which is why the sticky-retirement guard in
// schema.sql is a BEFORE INSERT trigger rather than the BEFORE UPDATE one the
// obvious design reaches for — an un-retire arrives here as a fresh retired=0
// row, not as an edit to an old one.
//
// LatestVerdicts is the read the API serves and the read publication.
// BuildVerdict consumes as its PriorVerdict: the history is what keeps a model
// retired after the window that condemned it has rolled out of range.
//
// Reads use the pooled s.db handle; writes go through s.w.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// PublicationVerdictRow is one persisted verdict.
type PublicationVerdictRow struct {
	Predictor         string   `json:"predictor"`
	Horizon           string   `json:"horizon"`
	Variant           string   `json:"variant"`
	PublicationStatus string   `json:"publication_status"`
	Retired           bool     `json:"retired"`
	RetirementSticky  bool     `json:"retirement_sticky"`
	RetireReason      string   `json:"retire_reason,omitempty"`
	RetirementSource  string   `json:"retirement_source,omitempty"`
	EvidenceClaimID   string   `json:"evidence_claim_id,omitempty"`
	CurrentNEff       *float64 `json:"current_n_eff"`
	CurrentBlocks     *int     `json:"current_distinct_blocks"`
	CIMethod          string   `json:"ci_method,omitempty"`
	CILower           *float64 `json:"ci_lower"`
	CIUpper           *float64 `json:"ci_upper"`
	NullRate          *float64 `json:"null_rate"`
	SkillPP           *float64 `json:"skill_pp"`
	GraderSHA256      string   `json:"grader_sha256,omitempty"`
	Reasons           []string `json:"reasons"`
	EvidenceRefs      []string `json:"evidence_refs"`
	EvaluatedAt       string   `json:"evaluated_at"`
}

// ErrUnretireRefused is returned when the sticky-retirement trigger rejects an
// insert. It is a typed error because the caller must treat it as a doctrine
// violation to surface, never as a transient write failure to retry: a retry
// would either fail identically or, worse, succeed against a database whose
// history had been cleared in between.
var ErrUnretireRefused = fmt.Errorf("retirement is sticky and cannot be cleared")

// PutPublicationVerdict appends one verdict.
func (s *Store) PutPublicationVerdict(ctx context.Context, r PublicationVerdictRow) error {
	reasons, err := json.Marshal(nonNil(r.Reasons))
	if err != nil {
		return err
	}
	refs, err := json.Marshal(nonNil(r.EvidenceRefs))
	if err != nil {
		return err
	}
	evaluatedAt := r.EvaluatedAt
	if evaluatedAt == "" {
		evaluatedAt = time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	}
	_, err = s.w.ExecContext(ctx, `
		INSERT INTO publication_verdicts
		  (predictor, horizon, variant, publication_status, retired, retirement_sticky,
		   retire_reason, retirement_source, evidence_claim_id,
		   current_n_eff, current_distinct_blocks, ci_method, ci_lower, ci_upper,
		   null_rate, skill_pp, grader_sha256, reasons_json, evidence_refs_json, evaluated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		r.Predictor, r.Horizon, r.Variant, r.PublicationStatus,
		boolToInt(r.Retired), boolToInt(r.RetirementSticky),
		nullString(r.RetireReason), nullString(r.RetirementSource), nullString(r.EvidenceClaimID),
		r.CurrentNEff, r.CurrentBlocks, nullString(r.CIMethod), r.CILower, r.CIUpper,
		r.NullRate, r.SkillPP, nullString(r.GraderSHA256),
		string(reasons), string(refs), evaluatedAt)
	if err != nil && isStickyRetirementAbort(err) {
		return fmt.Errorf("%w: %s %s %s", ErrUnretireRefused, r.Predictor, r.Horizon, r.Variant)
	}
	return err
}

// LatestVerdicts returns the newest verdict for every (predictor, horizon,
// variant), newest evaluation first.
func (s *Store) LatestVerdicts(ctx context.Context) ([]PublicationVerdictRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT predictor, horizon, variant, publication_status, retired, retirement_sticky,
		       COALESCE(retire_reason,''), COALESCE(retirement_source,''), COALESCE(evidence_claim_id,''),
		       current_n_eff, current_distinct_blocks, COALESCE(ci_method,''), ci_lower, ci_upper,
		       null_rate, skill_pp, COALESCE(grader_sha256,''), reasons_json, evidence_refs_json, evaluated_at
		  FROM publication_verdicts v
		 WHERE v.evaluated_at = (
		       SELECT MAX(p.evaluated_at) FROM publication_verdicts p
		        WHERE p.predictor = v.predictor AND p.horizon = v.horizon AND p.variant = v.variant)
		 ORDER BY v.predictor, v.horizon, v.variant`)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []PublicationVerdictRow
	for rows.Next() {
		var r PublicationVerdictRow
		var retired, sticky int
		var reasons, refs string
		if err := rows.Scan(&r.Predictor, &r.Horizon, &r.Variant, &r.PublicationStatus,
			&retired, &sticky, &r.RetireReason, &r.RetirementSource, &r.EvidenceClaimID,
			&r.CurrentNEff, &r.CurrentBlocks, &r.CIMethod, &r.CILower, &r.CIUpper,
			&r.NullRate, &r.SkillPP, &r.GraderSHA256, &reasons, &refs, &r.EvaluatedAt); err != nil {
			return nil, err
		}
		r.Retired, r.RetirementSticky = retired == 1, sticky == 1
		_ = json.Unmarshal([]byte(reasons), &r.Reasons)
		_ = json.Unmarshal([]byte(refs), &r.EvidenceRefs)
		r.Reasons, r.EvidenceRefs = nonNil(r.Reasons), nonNil(r.EvidenceRefs)
		out = append(out, r)
	}
	return out, rows.Err()
}

// RetirementHistory reports whether this row has EVER been retired, and why it
// first was. This is what makes retirement survive a shrinking window: the
// caller passes it into publication.BuildVerdict as the PriorVerdict.
func (s *Store) RetirementHistory(ctx context.Context, predictor, horizon, variant string) (retired bool, reason string, source string, err error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT COALESCE(retire_reason,''), COALESCE(retirement_source,'')
		  FROM publication_verdicts
		 WHERE predictor=? AND horizon=? AND variant=? AND retired=1
		 ORDER BY evaluated_at ASC LIMIT 1`, predictor, horizon, variant)
	switch err := row.Scan(&reason, &source); {
	case err == sql.ErrNoRows:
		return false, "", "", nil
	case err != nil:
		return false, "", "", err
	}
	return true, reason, source, nil
}

// GraderHeartbeat is one record of whether the GRADE ran — not whether the
// process started. A scheduled task exiting 0 proves only the latter, which is
// how a 33-hour grading outage reported success on 2026-08-03.
type GraderHeartbeat struct {
	Task          string `json:"task"`
	Success       bool   `json:"success"`
	FinishedAt    string `json:"finished_at"`
	GraderSHA256  string `json:"grader_sha256,omitempty"`
	RowsEvaluated *int   `json:"rows_evaluated"`
	Error         string `json:"error,omitempty"`
}

// PutGraderHeartbeat appends one heartbeat.
func (s *Store) PutGraderHeartbeat(ctx context.Context, h GraderHeartbeat) error {
	finished := h.FinishedAt
	if finished == "" {
		finished = time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	}
	_, err := s.w.ExecContext(ctx, `
		INSERT INTO grader_heartbeats (task, success, finished_at, grader_sha256, rows_evaluated, error)
		VALUES (?,?,?,?,?,?)`,
		h.Task, boolToInt(h.Success), finished,
		nullString(h.GraderSHA256), h.RowsEvaluated, nullString(h.Error))
	return err
}

// LatestGraderHeartbeat returns the newest heartbeat for a task. ok=false means
// none has ever been recorded, which is itself a failure state: a grader that
// has never reported is not a grader that is healthy.
func (s *Store) LatestGraderHeartbeat(ctx context.Context, task string) (h GraderHeartbeat, ok bool, err error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT task, success, finished_at, COALESCE(grader_sha256,''), rows_evaluated, COALESCE(error,'')
		  FROM grader_heartbeats WHERE task=? ORDER BY finished_at DESC LIMIT 1`, task)
	var success int
	switch err := row.Scan(&h.Task, &success, &h.FinishedAt, &h.GraderSHA256, &h.RowsEvaluated, &h.Error); {
	case err == sql.ErrNoRows:
		return GraderHeartbeat{}, false, nil
	case err != nil:
		return GraderHeartbeat{}, false, err
	}
	h.Success = success == 1
	return h, true, nil
}

// GraderStale reports whether this surface may publish. It judges the NEWEST
// heartbeat, not the newest successful one: the latest attempt is what
// describes current state, and a success this morning does not un-fail a
// refusal this afternoon. Reading an older success as current is precisely the
// "stale refusal with no alarm" defect (audit F-5, 2026-08-03).
//
// A missing heartbeat, a failed heartbeat, and an unparseable timestamp are all
// stale — every unknown resolves to "do not publish".
func (s *Store) GraderStale(ctx context.Context, task string, maxAge time.Duration, now time.Time) (bool, string, error) {
	h, ok, err := s.LatestGraderHeartbeat(ctx, task)
	if err != nil {
		return true, "heartbeat unreadable", err
	}
	if !ok {
		return true, "no grader heartbeat has ever been recorded", nil
	}
	if !h.Success {
		return true, "last grader run failed: " + h.Error, nil
	}
	t, err := time.Parse("2006-01-02T15:04:05.000Z", h.FinishedAt)
	if err != nil {
		if t, err = time.Parse(time.RFC3339, h.FinishedAt); err != nil {
			return true, "grader heartbeat timestamp unparseable: " + h.FinishedAt, nil
		}
	}
	if age := now.UTC().Sub(t); age > maxAge {
		return true, fmt.Sprintf("last successful grade was %s ago (max %s)",
			age.Round(time.Minute), maxAge), nil
	}
	return false, "", nil
}

func nonNil(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}

// isStickyRetirementAbort matches the RAISE(ABORT) from the schema trigger.
func isStickyRetirementAbort(err error) bool {
	return err != nil && strings.Contains(err.Error(), "retirement is sticky")
}
