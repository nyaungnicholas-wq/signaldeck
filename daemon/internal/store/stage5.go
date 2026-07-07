// Stage 5 (visual hubs) — batched store read for the SIGNALS hub predictions
// table. ONE window-function query over the read pool (s.db): the latest
// calibrated prediction per ACTIVE symbol for one horizon, joined with the
// symbol row so the API never walks symbols issuing LatestPrediction per row
// (no N+1). Kept in its own file so the hub wave never touches write paths.
package store

import (
	"context"
	"database/sql"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// LatestPredRow is one symbol's newest calibrated prediction for a horizon.
// Tier/NSamples (Stage 2, verdict cards) carry the symbol-agent evidence tier
// behind the number so every verdict badge can say WHICH model produced it —
// "" tier = no symbol_models row stored yet (the UI reads that as honest
// still-learning/static, never a bespoke-model claim).
type LatestPredRow struct {
	Symbol   string    `json:"symbol"`
	Market   md.Market `json:"market"`
	Name     string    `json:"name"`
	Ts       int64     `json:"ts"`
	RawProb  float64   `json:"rawProb"`
	CalProb  float64   `json:"calProb"`
	NUsed    int       `json:"nUsed"`
	Tier     string    `json:"tier"`
	NSamples int       `json:"nSamples"`
}

// LatestPredictionsAll returns, for EVERY active symbol with at least one
// stored prediction on horizon h, its newest prediction — in ONE query,
// strongest conviction first (|calProb − 0.5| descending) so the UI's default
// order needs no client-side sort. Symbols never predicted are simply absent
// (honest absence; the frontend says why, it never fabricates a row). The
// symbol_models LEFT JOIN rides along so each row carries its evidence tier
// without a second pass — NULLs (no model row) surface as ""/0.
func (s *Store) LatestPredictionsAll(ctx context.Context, h md.Horizon) ([]LatestPredRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT sy.symbol, sy.market, sy.name, p.ts, p.raw_prob, p.cal_prob, p.n_used,
		       m.tier, m.n_samples
		FROM predictions p
		JOIN (SELECT symbol_id, MAX(ts) AS mx FROM predictions
		      WHERE horizon=? GROUP BY symbol_id) t
		  ON t.symbol_id = p.symbol_id AND t.mx = p.ts
		JOIN symbols sy ON sy.id = p.symbol_id AND sy.active = 1
		LEFT JOIN symbol_models m ON m.symbol_id = p.symbol_id AND m.horizon = p.horizon
		WHERE p.horizon=?
		ORDER BY ABS(p.cal_prob - 0.5) DESC, sy.symbol`, string(h), string(h))
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	out := []LatestPredRow{}
	for rows.Next() {
		var r LatestPredRow
		var market string
		var tier sql.NullString
		var nSamples sql.NullInt64
		if err := rows.Scan(&r.Symbol, &market, &r.Name, &r.Ts, &r.RawProb, &r.CalProb, &r.NUsed, &tier, &nSamples); err != nil {
			return nil, err
		}
		r.Market = md.Market(market)
		r.Tier = tier.String
		r.NSamples = int(nSamples.Int64)
		out = append(out, r)
	}
	return out, rows.Err()
}
