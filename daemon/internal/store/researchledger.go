// Bayesian Research Ledger — persistence for internal/researchledger.
//
// The store stays policy-free: it moves Hypothesis + Evidence rows in and out;
// all Bayesian math (posterior recompute, status derivation, counters) lives in
// the pure package and is applied by the pipeline worker, which writes the
// derived fields back via UpdateLedgerDerived. Writes go through s.w, reads
// through the pooled s.db, matching the rest of the store.
package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	rl "github.com/nyaungnicholas-wq/signaldeck/internal/researchledger"
)

// UpsertLedgerHypothesis inserts a hypothesis or refreshes its mutable text
// fields. Prior and max_edge are IMMUTABLE after creation (changing a prior
// after seeing data is forbidden): on conflict only statement/open_questions/
// spec are updated. Peak/last-grade decay fields are written by the dedicated
// UpdateLedgerDecay, never here.
func (s *Store) UpsertLedgerHypothesis(ctx context.Context, h rl.Hypothesis, now int64) error {
	if h.OpenQuestions == nil {
		h.OpenQuestions = []string{} // marshal as [], matching the column default
	}
	oq, err := json.Marshal(h.OpenQuestions)
	if err != nil {
		return err
	}
	_, err = s.w.ExecContext(ctx, `
		INSERT INTO research_ledger_hypotheses
		  (id, family, statement, horizon, prior, max_edge, posterior, status,
		   replications, contradictions, regimes, open_questions,
		   peak_posterior, peak_ts, last_grade_ts, spec, tradable_form,
		   economic_test, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
		  statement=excluded.statement,
		  open_questions=excluded.open_questions,
		  spec=excluded.spec,
		  tradable_form=excluded.tradable_form,
		  economic_test=excluded.economic_test,
		  updated_at=excluded.updated_at`,
		h.ID, h.Family, h.Statement, h.Horizon, h.Prior, h.MaxEdge,
		h.Posterior, h.Status, h.Replications, h.Contradictions, h.Regimes,
		string(oq), h.PeakPosterior, h.PeakTs, h.LastGradeTs, h.Spec,
		h.TradableForm, h.EconomicTest, now, now)
	return err
}

// InsertLedgerEvidence appends one evidence row to a hypothesis's chain.
func (s *Store) InsertLedgerEvidence(ctx context.Context, e rl.Evidence) error {
	_, err := s.w.ExecContext(ctx, `
		INSERT INTO research_ledger_evidence
		  (hyp_id, ts, kind, k, n, p0, bf, note, window_from, window_to)
		VALUES (?,?,?,?,?,?,?,?,?,?)`,
		e.HypID, e.Ts, e.Kind, e.K, e.N, e.P0, e.BF, e.Note, e.WindowFrom, e.WindowTo)
	return err
}

// UpdateLedgerDerived writes the recomputed posterior/status/counters back
// after an evidence insert.
func (s *Store) UpdateLedgerDerived(ctx context.Context, id string, posterior float64, status string, replications, contradictions, regimes int, now int64) error {
	_, err := s.w.ExecContext(ctx, `
		UPDATE research_ledger_hypotheses
		SET posterior=?, status=?, replications=?, contradictions=?, regimes=?, updated_at=?
		WHERE id=?`,
		posterior, status, replications, contradictions, regimes, now, id)
	return err
}

// UpdateLedgerDecay writes the decay-tracker fields back after the research
// engine's decay sweep (peak posterior the chain ever reached, when it peaked,
// and the newest grade timestamp).
func (s *Store) UpdateLedgerDecay(ctx context.Context, id string, peakPosterior float64, peakTs, lastGradeTs int64) error {
	_, err := s.w.ExecContext(ctx, `
		UPDATE research_ledger_hypotheses
		SET peak_posterior=?, peak_ts=?, last_grade_ts=?
		WHERE id=?`,
		peakPosterior, peakTs, lastGradeTs, id)
	return err
}

// LedgerHypotheses returns every hypothesis, highest posterior first.
func (s *Store) LedgerHypotheses(ctx context.Context) ([]rl.Hypothesis, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, family, statement, horizon, prior, max_edge, posterior, status,
		       replications, contradictions, regimes, open_questions,
		       peak_posterior, peak_ts, last_grade_ts, spec, tradable_form,
		       economic_test
		FROM research_ledger_hypotheses ORDER BY posterior DESC, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []rl.Hypothesis
	for rows.Next() {
		var h rl.Hypothesis
		var oq string
		if err := rows.Scan(&h.ID, &h.Family, &h.Statement, &h.Horizon, &h.Prior,
			&h.MaxEdge, &h.Posterior, &h.Status, &h.Replications, &h.Contradictions,
			&h.Regimes, &oq, &h.PeakPosterior, &h.PeakTs, &h.LastGradeTs, &h.Spec,
			&h.TradableForm, &h.EconomicTest); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(oq), &h.OpenQuestions); err != nil {
			h.OpenQuestions = nil // legacy/blank rows render empty, not broken
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// LedgerEvidence returns one hypothesis's full evidence chain, oldest first
// (” returns every chain — the meta-analysis input).
func (s *Store) LedgerEvidence(ctx context.Context, hypID string) ([]rl.Evidence, error) {
	q := `SELECT hyp_id, ts, kind, k, n, p0, bf, note, window_from, window_to
	      FROM research_ledger_evidence`
	args := []any{}
	if hypID != "" {
		q += ` WHERE hyp_id=?`
		args = append(args, hypID)
	}
	q += ` ORDER BY ts, id`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []rl.Evidence
	for rows.Next() {
		var e rl.Evidence
		if err := rows.Scan(&e.HypID, &e.Ts, &e.Kind, &e.K, &e.N, &e.P0, &e.BF,
			&e.Note, &e.WindowFrom, &e.WindowTo); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// LedgerEvidenceMaxWindow returns the newest graded window_to for a hypothesis
// (0 when no graded evidence exists) — the LIVE replication cursor that keeps
// successive grades on DISJOINT data windows. It counts backtest kinds
// alongside experiment/replication: the backfill engine writes historical era
// grades as backtest evidence, and if the live grader's cursor ignored them it
// could grade a window an era grade already consumed — the same data counted
// twice, once as backtest and once as live. Advancing past ALL graded windows
// makes that overlap impossible.
func (s *Store) LedgerEvidenceMaxWindow(ctx context.Context, hypID string) (int64, error) {
	return s.LedgerEvidenceMaxWindowKinds(ctx, hypID,
		[]string{rl.KindExperiment, rl.KindReplication, rl.KindBacktest})
}

// LedgerEvidenceMaxWindowKinds returns the newest graded window_to for a
// hypothesis over the given evidence kinds (0 when none) — the kind-scoped
// disjoint-window cursor.
func (s *Store) LedgerEvidenceMaxWindowKinds(ctx context.Context, hypID string, kinds []string) (int64, error) {
	if len(kinds) == 0 {
		return 0, nil
	}
	q, args := ledgerWindowQuery(`MAX(window_to)`, hypID, kinds)
	var maxTo int64
	err := s.db.QueryRowContext(ctx, q, args...).Scan(&maxTo)
	return maxTo, err
}

// LedgerEvidenceMinWindowKinds returns the oldest graded window_from for a
// hypothesis over the given evidence kinds (0 when none) — used to keep
// backtest windows clear of the live-graded ones.
func (s *Store) LedgerEvidenceMinWindowKinds(ctx context.Context, hypID string, kinds []string) (int64, error) {
	if len(kinds) == 0 {
		return 0, nil
	}
	q, args := ledgerWindowQuery(`MIN(window_from)`, hypID, kinds)
	var minFrom int64
	err := s.db.QueryRowContext(ctx, q, args...).Scan(&minFrom)
	return minFrom, err
}

// ledgerWindowQuery builds the kind-scoped window-cursor query. agg is a
// compile-time constant aggregate expression, never user input.
func ledgerWindowQuery(agg, hypID string, kinds []string) (string, []any) {
	q := `SELECT COALESCE(` + agg + `, 0) FROM research_ledger_evidence
	      WHERE hyp_id=? AND kind IN (?` + strings.Repeat(",?", len(kinds)-1) + `)`
	args := make([]any, 0, len(kinds)+1)
	args = append(args, hypID)
	for _, k := range kinds {
		args = append(args, k)
	}
	return q, args
}

// LabeledFeaturesSince is the replication graders' data source: labeled rows
// (feature vector + realized outcome) for one horizon STRICTLY AFTER sinceTs,
// oldest first, capped. Same join discipline as LabeledFeatures (resolved,
// non-void outcomes only).
func (s *Store) LabeledFeaturesSince(ctx context.Context, h md.Horizon, sinceTs int64, limit int) ([]LabeledFeature, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT f.symbol_id, f.ts, f.version, f.vec, o.up, o.fwd_return
		FROM features f
		JOIN prediction_outcomes o
		  ON o.symbol_id=f.symbol_id AND o.horizon=f.horizon AND o.ts=f.ts
		WHERE f.horizon=? AND f.ts>? AND o.resolved_at IS NOT NULL
		  AND o.up IS NOT NULL AND o.fwd_return IS NOT NULL
		ORDER BY f.ts ASC LIMIT ?`,
		string(h), sinceTs, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []LabeledFeature
	for rows.Next() {
		lf := LabeledFeature{Horizon: h}
		var vec string
		if err := rows.Scan(&lf.SymbolID, &lf.Ts, &lf.Version, &vec, &lf.Up, &lf.FwdReturn); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(vec), &lf.Vec); err != nil {
			return nil, err
		}
		out = append(out, lf)
	}
	return out, rows.Err()
}

// LedgerHypHealth is one hypothesis's testing verdict: what its posterior is
// made of, when it was last put at risk, and the word a surface may render.
type LedgerHypHealth struct {
	ID              string            `json:"id"`
	Posterior       float64           `json:"posterior"`
	Status          string            `json:"status"`
	Verdict         string            `json:"verdict"`
	ReplicationRows int               `json:"replicationRows"`
	ExperimentRows  int               `json:"experimentRows"`
	NewestEvidence  int64             `json:"newestEvidenceTs"`
	EvidenceAgeDays int               `json:"evidenceAgeDays"` // -1 when the chain is empty
	LastGradeTs     int64             `json:"lastGradeTs"`
	Liveness        rl.LedgerLiveness `json:"liveness"`
}

// LedgerEngineHealth is the liveness verdict on the Bayes LEDGER itself,
// modelled on LoopEngineHealth: the discovery loop already refuses to let a
// dead engine read like an honest null, and the ledger — which publishes the
// posteriors — had no equivalent. Corpus growth plays the role corpus size
// plays there: it is the precondition that makes silence diagnostic.
type LedgerEngineHealth struct {
	Hypotheses      []LedgerHypHealth `json:"hypotheses"`
	ReplicationRows int               `json:"replicationRows"`
	ExperimentRows  int               `json:"experimentRows"`
	EvidenceRows    int               `json:"evidenceRows"`
	NewestEvidence  int64             `json:"newestEvidenceTs"`
	EvidenceAgeDays int               `json:"evidenceAgeDays"` // -1 when the ledger is empty
	LastGradeTs     int64             `json:"lastGradeTs"`
	CorpusMaxTs     int64             `json:"corpusMaxTs"`
	CorpusGrew      bool              `json:"corpusGrew"`
	Unreplicated    int               `json:"unreplicated"`
	Stale           int               `json:"stale"`
	State           string            `json:"state"`
	Healthy         bool              `json:"healthy"`
	Detail          string            `json:"detail"`
}

// LedgerEngineHealth reports, per hypothesis and for the ledger as a whole, how
// many replication and experiment rows exist, how old the newest evidence row
// of any kind is, and the newest grade timestamp — and turns that into the two
// explicit states rl.LivenessOf defines.
//
// It changes no posterior, no null and no threshold; UNREPLICATED and STALE can
// only make a hypothesis read weaker than its band already does.
func (s *Store) LedgerEngineHealth(ctx context.Context, now time.Time) (LedgerEngineHealth, error) {
	h := LedgerEngineHealth{EvidenceAgeDays: -1, State: rl.LivenessLive, Healthy: true}

	hyps, err := s.LedgerHypotheses(ctx)
	if err != nil {
		return h, err
	}
	weeks, err := s.ResearchWeeksStats(ctx)
	if err != nil {
		return h, err
	}
	h.CorpusMaxTs = weeks.MaxTs

	type counts struct {
		rep, exp int
		newest   int64
	}
	perHyp := map[string]*counts{}
	rows, err := s.db.QueryContext(ctx, `
		SELECT hyp_id, kind, COUNT(*), MAX(ts)
		FROM research_ledger_evidence GROUP BY hyp_id, kind`)
	if err != nil {
		return h, err
	}
	defer rows.Close() //nolint:errcheck
	for rows.Next() {
		var id, kind string
		var n int
		var maxTs int64
		if err := rows.Scan(&id, &kind, &n, &maxTs); err != nil {
			return h, err
		}
		c, ok := perHyp[id]
		if !ok {
			c = &counts{}
			perHyp[id] = c
		}
		h.EvidenceRows += n
		switch kind {
		case rl.KindReplication:
			c.rep += n
			h.ReplicationRows += n
		case rl.KindExperiment:
			c.exp += n
			h.ExperimentRows += n
		}
		if maxTs > c.newest {
			c.newest = maxTs
		}
		if maxTs > h.NewestEvidence {
			h.NewestEvidence = maxTs
		}
	}
	if err := rows.Err(); err != nil {
		return h, err
	}
	h.EvidenceAgeDays = ageDays(h.NewestEvidence, now)
	// The corpus has moved past the ledger: there IS newer data to grade, so
	// silence is a stopped grader rather than an empty queue.
	h.CorpusGrew = weeks.MaxTs > h.NewestEvidence

	for _, hyp := range hyps {
		c := perHyp[hyp.ID]
		if c == nil {
			c = &counts{}
		}
		row := LedgerHypHealth{
			ID: hyp.ID, Posterior: hyp.Posterior, Status: hyp.Status,
			ReplicationRows: c.rep, ExperimentRows: c.exp,
			NewestEvidence: c.newest, EvidenceAgeDays: ageDays(c.newest, now),
			LastGradeTs: hyp.LastGradeTs,
		}
		row.Liveness = rl.LivenessOf(hyp.Posterior, c.rep, c.exp,
			row.EvidenceAgeDays, weeks.MaxTs > c.newest)
		row.Verdict = rl.Verdict(hyp.Status, row.Liveness)
		switch row.Liveness.State {
		case rl.LivenessUnreplicated:
			h.Unreplicated++
		case rl.LivenessStale:
			h.Stale++
		}
		if hyp.LastGradeTs > h.LastGradeTs {
			h.LastGradeTs = hyp.LastGradeTs
		}
		h.Hypotheses = append(h.Hypotheses, row)
	}

	switch {
	case h.Unreplicated > 0:
		h.State, h.Healthy = rl.LivenessUnreplicated, false
		h.Detail = fmt.Sprintf("%d of %d hypotheses publish a posterior ≥ %.2f with "+
			"ZERO replication rows; the ledger holds %d replication and %d experiment "+
			"rows over %d evidence rows in total — those numbers were never re-graded",
			h.Unreplicated, len(hyps), rl.LivenessUnreplicatedMin, h.ReplicationRows,
			h.ExperimentRows, h.EvidenceRows)
	case h.Stale > 0:
		h.State, h.Healthy = rl.LivenessStale, false
		h.Detail = fmt.Sprintf("%d of %d hypotheses have had no evidence row of any kind "+
			"for %d+ days while the research corpus holds newer data",
			h.Stale, len(hyps), rl.LivenessStaleDays)
	default:
		h.Detail = fmt.Sprintf("%d hypotheses; %d replication and %d experiment rows; "+
			"newest evidence %s", len(hyps), h.ReplicationRows, h.ExperimentRows,
			agePhraseDays(h.EvidenceAgeDays))
	}
	return h, nil
}

// ageDays is whole UTC days between an evidence timestamp and now, -1 when the
// timestamp is absent — never 0, which would read as "graded today".
func ageDays(ts int64, now time.Time) int {
	if ts <= 0 {
		return -1
	}
	d := int(now.UTC().Sub(time.Unix(ts, 0).UTC()).Hours() / 24)
	if d < 0 {
		d = 0
	}
	return d
}

func agePhraseDays(days int) string {
	if days < 0 {
		return "never"
	}
	return fmt.Sprintf("%d days old", days)
}
