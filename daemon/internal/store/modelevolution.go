// Model-evolution store methods (self-audit / drift-watchdog wave). The
// adaptive-weights worker persists only its LATEST weights in meta, overwriting
// each run — so there was no history to chart HOW the learned blend evolves.
// weight_history is the append-only per-run snapshot the worker now writes;
// ModelEvolution stitches it (plus the self_audit factor-IC trend) into compact
// per-series time lines the web can graph directly. Writes via s.w; reads s.db.
//
// HONESTY: no interpolation and no synthetic points — a series contains only the
// runs where that (regime, leg) actually had a learned weight, or where the
// factor IC was actually measured (n>=30). Gaps are gaps.
package store

import "context"

// WeightHistoryRow is one appended learned-weight snapshot point.
type WeightHistoryRow struct {
	Ts     int64
	Regime string
	Leg    string
	Weight float64
}

// InsertWeightHistory appends learned-weight snapshot rows in one transaction
// (idempotency is not needed — the worker runs on a cadence and each run is a
// new timestamped snapshot; an empty batch is a no-op).
func (s *Store) InsertWeightHistory(ctx context.Context, rows []WeightHistoryRow) error {
	if len(rows) == 0 {
		return nil
	}
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	for _, r := range rows {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO weight_history (ts, regime, leg, weight) VALUES (?,?,?,?)`,
			r.Ts, r.Regime, r.Leg, r.Weight); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ── /api/model-evolution payload shapes ──────────────────────────────────

// WeightPoint is one (ts, weight) sample in a learned-weight series.
type WeightPoint struct {
	Ts     int64   `json:"ts"`
	Weight float64 `json:"weight"`
}

// WeightSeries is one learned-weight line: the weight of one leg within one
// regime cell over time.
type WeightSeries struct {
	Regime string        `json:"regime"`
	Leg    string        `json:"leg"`
	Points []WeightPoint `json:"points"`
}

// ICPoint is one (ts, ic) sample in a factor-skill series, carrying the audit
// status that produced it (e.g. "ok" | "sign_flip").
type ICPoint struct {
	Ts     int64   `json:"ts"`
	IC     float64 `json:"ic"`
	Status string  `json:"status"`
}

// FactorICSeries is one ensemble leg's measured IC trend over time (from the
// self_audit table). Insufficient-sample audits are omitted — honest gaps.
type FactorICSeries struct {
	Leg    string    `json:"leg"`
	Points []ICPoint `json:"points"`
}

// ModelEvolutionData is the trailing-window model-evolution series bundle.
type ModelEvolutionData struct {
	Weights     []WeightSeries   `json:"weights"`
	FactorSkill []FactorICSeries `json:"factorSkill"`
}

// ModelEvolution returns the trailing-sinceTs adaptive-weight snapshots (grouped
// per regime+leg) and per-leg factor-IC trend (from self_audit, measured points
// only). Series are ordered deterministically (regime, leg) and each series'
// points ascend by ts. Empty slices (never nil) when nothing is stored yet.
func (s *Store) ModelEvolution(ctx context.Context, sinceTs int64) (ModelEvolutionData, error) {
	out := ModelEvolutionData{Weights: []WeightSeries{}, FactorSkill: []FactorICSeries{}}

	// Learned-weight series, grouped by (regime, leg).
	rows, err := s.db.QueryContext(ctx, `
		SELECT regime, leg, ts, weight FROM weight_history
		WHERE ts >= ? ORDER BY regime, leg, ts`, sinceTs)
	if err != nil {
		return out, err
	}
	defer rows.Close() //nolint:errcheck
	var curW *WeightSeries
	for rows.Next() {
		var regime, leg string
		var ts int64
		var weight float64
		if err := rows.Scan(&regime, &leg, &ts, &weight); err != nil {
			return out, err
		}
		if curW == nil || curW.Regime != regime || curW.Leg != leg {
			out.Weights = append(out.Weights, WeightSeries{Regime: regime, Leg: leg})
			curW = &out.Weights[len(out.Weights)-1]
		}
		curW.Points = append(curW.Points, WeightPoint{Ts: ts, Weight: weight})
	}
	if err := rows.Err(); err != nil {
		return out, err
	}

	// Factor-IC trend from self_audit (metric 'factor_ic:<leg>'), measured points
	// only — insufficient audits are honest gaps, never plotted as IC 0.
	sr, err := s.db.QueryContext(ctx, `
		SELECT metric, ts, value, status FROM self_audit
		WHERE metric LIKE 'factor_ic:%' AND status != 'insufficient' AND ts >= ?
		ORDER BY metric, ts`, sinceTs)
	if err != nil {
		return out, err
	}
	defer sr.Close() //nolint:errcheck
	var curF *FactorICSeries
	for sr.Next() {
		var metric, status string
		var ts int64
		var ic float64
		if err := sr.Scan(&metric, &ts, &ic, &status); err != nil {
			return out, err
		}
		leg := metric[len("factor_ic:"):]
		if curF == nil || curF.Leg != leg {
			out.FactorSkill = append(out.FactorSkill, FactorICSeries{Leg: leg})
			curF = &out.FactorSkill[len(out.FactorSkill)-1]
		}
		curF.Points = append(curF.Points, ICPoint{Ts: ts, IC: ic, Status: status})
	}
	return out, sr.Err()
}
