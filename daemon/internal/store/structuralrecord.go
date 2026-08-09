// Live record accessors for the STRUCTURAL predictors (2026-07-25).
//
// The model-health gate was pointed only at the directional ensemble — the
// model that is now retired. trend21, vol21 and liquidity21, the three that
// actually survived validation, had no live-record accessor at all, so the
// machinery that graded a failing model against its own claim could not be
// aimed at the models worth keeping.
//
// This supplies that: per-kind and per-conviction-band live accuracy, measured
// against the accuracy each forecast CLAIMED at the time it was made. That
// comparison is the whole point — a structural forecast ships a banded number
// ("97.2% of very-high-conviction calls are right"), and until something checks
// that number against what happened, it is a backtest assertion wearing a live
// label.
package store

import (
	"context"

	"github.com/nyaungnicholas-wq/signaldeck/internal/structregime"
)

// StructuralRecordRow is one predictor's live scoreboard.
type StructuralRecordRow struct {
	Kind string `json:"kind"`
	// N counts INDEPENDENT resolutions: one per (symbol, kind, UTC-day). The
	// forecast writer already dedups, but recomputing here means a future
	// schema change cannot silently reintroduce pseudo-replication.
	N int `json:"n"`
	// Correct and Accuracy are the realized live record.
	Correct  int     `json:"correct"`
	Accuracy float64 `json:"accuracy"`
	// ClaimedAccuracy is the mean accuracy these same forecasts ADVERTISED when
	// they were made — the number the live record is being held to.
	ClaimedAccuracy float64 `json:"claimedAccuracy"`
	// PersistenceBase is the honest null: the share of calls whose regime simply
	// continued. A structural predictor that merely reports "it persists" scores
	// this by default, so accuracy only means something above it.
	PersistenceBase float64 `json:"persistenceBase"`
	FirstTs         int64   `json:"firstTs"`
	LastTs          int64   `json:"lastTs"`
	DistinctDays    int     `json:"distinctDays"`
}

// StructuralPendingRow is one kind's UNRESOLVED forecast backlog: how many
// calls are outstanding and when the earliest of them comes due.
//
// It exists so "not graded yet" can say WHICH kind of not-yet it is. A
// predictor with 4,000 outstanding calls whose first horizon elapses in a
// fortnight is in a completely different state from one that has never emitted,
// and reporting both as "the health worker runs hourly" blames a scheduler for
// the passage of time.
type StructuralPendingRow struct {
	Kind string `json:"kind"`
	// Pending counts unresolved calls; FirstDueTs is when the earliest of them
	// completes its horizon (0 when there are none).
	Pending   int   `json:"pending"`
	FirstDue  int64 `json:"firstDueTs"`
	FirstCall int64 `json:"firstCallTs"`
}

// StructuralPending reports the unresolved forecast backlog per kind.
func (s *Store) StructuralPending(ctx context.Context) ([]StructuralPendingRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT kind, COUNT(*), MIN(ts + horizon_days * 86400), MIN(ts)
		FROM regime_outcomes
		WHERE resolved_at IS NULL
		GROUP BY kind ORDER BY kind`)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []StructuralPendingRow
	for rows.Next() {
		var r StructuralPendingRow
		var due, first *int64
		if err := rows.Scan(&r.Kind, &r.Pending, &due, &first); err != nil {
			return nil, err
		}
		if due != nil {
			r.FirstDue = *due
		}
		if first != nil {
			r.FirstCall = *first
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// StructuralRecords grades every resolved structural forecast by kind.
// `minConviction` restricts to a conviction band; 0 includes everything.
func (s *Store) StructuralRecords(ctx context.Context, minConviction float64) ([]StructuralRecordRow, error) {
	// `day` is the stored settled-move key the dedup index is built on, and it is
	// used for BOTH the row dedup and the distinct-day count. Those were two
	// different folds: the partition used trading_day(ts) while the effective-N
	// count used a raw ts/86400 UTC day, so the reported day count did not
	// describe the rows it was counting.
	//
	// superseded_by IS NULL is the row filter rather than a ROW_NUMBER pick. A
	// superseded row is not an independent observation, and ordering by ts DESC
	// inside the partition actively preferred one: the stored dedup keeps the
	// EARLIEST call of a settled move, so the newest-wins pick could return a row
	// the table had already retired.
	rows, err := s.db.QueryContext(ctx, `
		SELECT kind,
		       COUNT(*),
		       SUM(CASE WHEN correct = 1 THEN 1 ELSE 0 END),
		       AVG(historical_accuracy),
		       MIN(ts), MAX(ts),
		       COUNT(DISTINCT day)
		FROM regime_outcomes
		WHERE resolved_at IS NOT NULL AND correct IN (0,1)
		  AND conviction >= ?
		  AND superseded_by IS NULL
		GROUP BY kind ORDER BY kind`, minConviction)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var out []StructuralRecordRow
	for rows.Next() {
		var r StructuralRecordRow
		var claimed *float64
		if err := rows.Scan(&r.Kind, &r.N, &r.Correct, &claimed,
			&r.FirstTs, &r.LastTs, &r.DistinctDays); err != nil {
			return nil, err
		}
		if r.N > 0 {
			r.Accuracy = float64(r.Correct) / float64(r.N)
		}
		if claimed != nil {
			r.ClaimedAccuracy = *claimed
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Persistence base rate, computed per kind over the same population: how
	// often the regime simply continued. For trend/liquidity/vol this IS the
	// naive strategy, so it is the number accuracy must beat to mean anything.
	for i := range out {
		var n, persisted int
		err := s.db.QueryRowContext(ctx, `
			SELECT COUNT(*), SUM(CASE WHEN actual = regime THEN 1 ELSE 0 END)
			FROM regime_outcomes
			WHERE kind = ? AND resolved_at IS NOT NULL AND correct IN (0,1)
			  AND conviction >= ?
			  AND superseded_by IS NULL`, out[i].Kind, minConviction).Scan(&n, &persisted)
		if err == nil && n > 0 {
			out[i].PersistenceBase = float64(persisted) / float64(n)
		}
	}
	return out, nil
}

// StructuralKinds is the set the health gate tracks as first-class models.
func StructuralKinds() []string {
	return []string{
		string(structregime.KindTrend21),
		string(structregime.KindVol21),
		string(structregime.KindLiquidity21),
	}
}

// SymbolStructuralRow is one (symbol, kind) live record — Phase 3.
//
// A predictor can be right in aggregate and reliably wrong on a subset. trend21
// scoring 83% fleet-wide says nothing about whether it works on a volatile
// micro-cap, and averaging hides exactly the cases a user would most want
// warned about. Grading per symbol is what turns "the model works" into "the
// model works HERE".
type SymbolStructuralRow struct {
	SymbolID        int64   `json:"symbolId"`
	Symbol          string  `json:"symbol"`
	Kind            string  `json:"kind"`
	N               int     `json:"n"`
	Correct         int     `json:"correct"`
	Accuracy        float64 `json:"accuracy"`
	ClaimedAccuracy float64 `json:"claimedAccuracy"`
	PersistenceBase float64 `json:"persistenceBase"`
	Edge            float64 `json:"edgeVsPersistence"`
}

// SymbolStructuralRecords grades each (symbol, kind) pair that has at least
// minN independent resolutions. Symbols below the floor are omitted rather than
// reported with a noisy number — a per-symbol accuracy from four observations
// is not evidence, and showing it invites acting on it.
func (s *Store) SymbolStructuralRecords(ctx context.Context, kind string, minN int) ([]SymbolStructuralRow, error) {
	if minN <= 0 {
		minN = 20
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT o.symbol_id, sy.symbol, o.kind,
		       COUNT(*),
		       SUM(CASE WHEN o.correct = 1 THEN 1 ELSE 0 END),
		       AVG(o.historical_accuracy),
		       AVG(CASE WHEN o.actual = o.regime THEN 1.0 ELSE 0.0 END)
		FROM regime_outcomes o
		JOIN symbols sy ON sy.id = o.symbol_id
		WHERE o.resolved_at IS NOT NULL AND o.correct IN (0,1)
		  AND (? = '' OR o.kind = ?)
		GROUP BY o.symbol_id, o.kind
		HAVING COUNT(*) >= ?
		ORDER BY o.kind, sy.symbol`, kind, kind, minN)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	out := []SymbolStructuralRow{}
	for rows.Next() {
		var r SymbolStructuralRow
		var claimed, persist *float64
		if err := rows.Scan(&r.SymbolID, &r.Symbol, &r.Kind, &r.N, &r.Correct,
			&claimed, &persist); err != nil {
			return nil, err
		}
		if r.N > 0 {
			r.Accuracy = float64(r.Correct) / float64(r.N)
		}
		if claimed != nil {
			r.ClaimedAccuracy = *claimed
		}
		if persist != nil {
			r.PersistenceBase = *persist
		}
		r.Edge = r.Accuracy - r.PersistenceBase
		return_ := r
		out = append(out, return_)
	}
	return out, rows.Err()
}
