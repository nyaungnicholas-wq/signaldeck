// Stage 2 (verdict cards) — the ONE batched read behind every verdict card.
// For a set of symbol ids and one horizon, return each symbol's newest
// calibrated prediction (cal_prob + n_used) together with its symbol-agent
// evidence tier + own-sample count, in TWO fixed queries total (one over
// predictions, one over symbol_models) — never a per-symbol walk (no N+1).
//
// HONESTY: a symbol with no stored prediction has CalProb=nil (the UI renders
// "NO READ YET", never a fabricated lean); a symbol with no symbol_models row
// has Tier="" (the API layer maps that to the honest static/still-learning
// wording). Nothing here is interpolated or invented — ids are bound params.
package store

import (
	"context"
	"strings"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// VerdictStat is one symbol's verdict-card inputs for a horizon.
type VerdictStat struct {
	// CalProb is the newest calibrated P(up); nil = no prediction stored yet
	// (render "NO READ YET" — never a fake lean).
	CalProb *float64
	// NUsed is how many ensemble legs fed that prediction (0 when CalProb nil).
	NUsed int
	// PredTs is the prediction's timestamp (0 when CalProb nil).
	PredTs int64
	// Tier is the symbol-agent evidence tier (personal|regime|global|static);
	// "" = no symbol_models row stored yet (treat as static / still learning).
	Tier string
	// NSamples is the symbol's OWN resolved outcomes behind that tier.
	NSamples int
}

// VerdictStats returns, for each requested symbol id, its newest prediction
// (cal_prob, n_used, ts) and its symbol-agent tier + own-sample count for one
// horizon — batched into exactly two IN-list queries. Ids absent from both
// tables are still present in the result map with zero values (CalProb nil,
// Tier "") so callers can range the input list without existence checks.
func (s *Store) VerdictStats(ctx context.Context, symbolIDs []int64, h md.Horizon) (map[int64]VerdictStat, error) {
	out := make(map[int64]VerdictStat, len(symbolIDs))
	if len(symbolIDs) == 0 {
		return out, nil
	}
	for _, id := range symbolIDs {
		out[id] = VerdictStat{}
	}

	// Bound-param IN (?,...) list — ids are parameters, never interpolated.
	ph := strings.TrimSuffix(strings.Repeat("?,", len(symbolIDs)), ",")
	args := make([]any, 0, len(symbolIDs)+1)
	args = append(args, string(h))
	for _, id := range symbolIDs {
		args = append(args, id)
	}

	// (1) Newest prediction per requested symbol for the horizon (window
	// function — rn=1 is the newest row).
	rows, err := s.db.QueryContext(ctx, `
		SELECT symbol_id, ts, cal_prob, n_used FROM (
			SELECT symbol_id, ts, cal_prob, n_used,
			       ROW_NUMBER() OVER (PARTITION BY symbol_id ORDER BY ts DESC) AS rn
			FROM predictions WHERE horizon=? AND n_used > 0 AND symbol_id IN (`+ph+`)
		) WHERE rn = 1`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	for rows.Next() {
		var id, ts int64
		var p float64
		var nUsed int
		if err := rows.Scan(&id, &ts, &p, &nUsed); err != nil {
			return nil, err
		}
		v := out[id]
		prob := p
		v.CalProb, v.NUsed, v.PredTs = &prob, nUsed, ts
		out[id] = v
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// (2) Symbol-agent tier + own-sample count for the same set.
	mrows, err := s.db.QueryContext(ctx, `
		SELECT symbol_id, tier, n_samples FROM symbol_models
		WHERE horizon=? AND symbol_id IN (`+ph+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer mrows.Close() //nolint:errcheck
	for mrows.Next() {
		var id int64
		var tier string
		var n int
		if err := mrows.Scan(&id, &tier, &n); err != nil {
			return nil, err
		}
		v := out[id]
		v.Tier, v.NSamples = tier, n
		out[id] = v
	}
	return out, mrows.Err()
}
