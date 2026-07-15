package store

import (
	"context"
	"encoding/json"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/postmortem"
)

// MissRow is a resolved, WRONG prediction that has NOT yet been postmortem'd,
// joined to the prediction's stored inputs (components + n_used) so the worker
// can attribute the failure. Newest-first.
type MissRow struct {
	SymbolID   int64
	Symbol     string
	Market     md.Market
	Horizon    md.Horizon
	Ts         int64
	Prob       float64
	Up         int
	FwdReturn  float64
	NUsed      int
	Components map[string]float64 // parsed predictions.components JSON
}

// UnPostmortemedMisses returns resolved, WRONG predictions for a horizon that
// have no postmortem row yet, joined to their stored components and n_used.
// "Wrong" is enforced in SQL: prob>0.5 with up=0, or prob<0.5 with up=1 (a
// prob of exactly 0.5 made no call and is excluded). Newest-first, capped.
func (s *Store) UnPostmortemedMisses(ctx context.Context, h md.Horizon, limit int) ([]MissRow, error) {
	if limit <= 0 {
		limit = 5000
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT po.symbol_id, sym.symbol, sym.market, po.ts, po.prob, po.up, po.fwd_return,
		       COALESCE(p.n_used, 0), COALESCE(p.components, '{}')
		FROM prediction_outcomes po
		JOIN symbols sym ON sym.id = po.symbol_id
		LEFT JOIN predictions p
		       ON p.symbol_id = po.symbol_id AND p.horizon = po.horizon AND p.ts = po.ts
		LEFT JOIN prediction_postmortems pm
		       ON pm.symbol_id = po.symbol_id AND pm.horizon = po.horizon AND pm.ts = po.ts
		WHERE po.horizon = ?
		  AND po.resolved_at IS NOT NULL AND po.up IS NOT NULL
		  AND pm.symbol_id IS NULL
		  AND ((po.prob > 0.5 AND po.up = 0) OR (po.prob < 0.5 AND po.up = 1))
		ORDER BY po.ts DESC
		LIMIT ?`, string(h), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []MissRow
	for rows.Next() {
		var m MissRow
		var mkt, comp string
		if err := rows.Scan(&m.SymbolID, &m.Symbol, &mkt, &m.Ts, &m.Prob, &m.Up,
			&m.FwdReturn, &m.NUsed, &comp); err != nil {
			return nil, err
		}
		m.Market = md.Market(mkt)
		m.Horizon = h
		m.Components = map[string]float64{}
		_ = json.Unmarshal([]byte(comp), &m.Components) // best-effort; bad JSON ⇒ empty
		out = append(out, m)
	}
	return out, rows.Err()
}

// InsertPostmortem stores one attributed miss. Idempotent via INSERT OR IGNORE
// on the (symbol_id, horizon, ts) PK — a miss is explained at most once.
func (s *Store) InsertPostmortem(ctx context.Context, m MissRow, rep postmortem.Report, now int64) error {
	reasonsJSON, err := json.Marshal(rep.Reasons)
	if err != nil {
		return err
	}
	_, err = s.w.ExecContext(ctx, `
		INSERT OR IGNORE INTO prediction_postmortems
		  (symbol_id, horizon, ts, prob, up, fwd_return, conviction, magnitude,
		   primary_reason, secondary_reason, reasons, created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		m.SymbolID, string(m.Horizon), m.Ts, m.Prob, m.Up, m.FwdReturn,
		rep.Conviction, rep.Magnitude, string(rep.Primary), string(rep.Secondary),
		string(reasonsJSON), now)
	return err
}

// PostmortemCluster is an aggregated failure mode straight from SQL, matching
// postmortem.Cluster's shape for the API.
type PostmortemCluster struct {
	Code       string  `json:"code"`
	Count      int     `json:"count"`
	Share      float64 `json:"share"`
	MeanMag    float64 `json:"meanMag"`
	MeanConv   float64 `json:"meanConviction"`
}

// PostmortemClusters aggregates stored postmortems by primary reason over the
// most recent `withinDays` days (0 ⇒ all time), biggest cluster first. This is
// the "cluster failures / discover patterns" surface the Research Lab reads.
func (s *Store) PostmortemClusters(ctx context.Context, sinceTs int64) ([]PostmortemCluster, int, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT primary_reason, COUNT(*), AVG(magnitude), AVG(conviction)
		FROM prediction_postmortems
		WHERE created_at >= ?
		GROUP BY primary_reason
		ORDER BY COUNT(*) DESC, primary_reason ASC`, sinceTs)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close() //nolint:errcheck
	var cs []PostmortemCluster
	var total int
	for rows.Next() {
		var c PostmortemCluster
		if err := rows.Scan(&c.Code, &c.Count, &c.MeanMag, &c.MeanConv); err != nil {
			return nil, 0, err
		}
		cs = append(cs, c)
		total += c.Count
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	if total > 0 {
		for i := range cs {
			cs[i].Share = float64(cs[i].Count) / float64(total)
		}
	}
	return cs, total, nil
}

// RecentPostmortemRow is one stored postmortem for the recent-misses feed.
type RecentPostmortemRow struct {
	Symbol    string     `json:"symbol"`
	Market    md.Market  `json:"market"`
	Horizon   md.Horizon `json:"horizon"`
	Ts        int64      `json:"ts"`
	Prob      float64    `json:"prob"`
	Up        int        `json:"up"`
	FwdReturn float64    `json:"fwdReturn"`
	Primary   string     `json:"primary"`
	Secondary string     `json:"secondary,omitempty"`
	Reasons   json.RawMessage `json:"reasons"`
}

// RecentPostmortems returns the newest stored postmortems (by prediction ts),
// capped at limit, for the API's recent-misses feed.
func (s *Store) RecentPostmortems(ctx context.Context, limit int) ([]RecentPostmortemRow, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT sym.symbol, sym.market, pm.horizon, pm.ts, pm.prob, pm.up, pm.fwd_return,
		       pm.primary_reason, pm.secondary_reason, pm.reasons
		FROM prediction_postmortems pm
		JOIN symbols sym ON sym.id = pm.symbol_id
		ORDER BY pm.ts DESC
		LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []RecentPostmortemRow
	for rows.Next() {
		var r RecentPostmortemRow
		var mkt, hz, reasons string
		if err := rows.Scan(&r.Symbol, &mkt, &hz, &r.Ts, &r.Prob, &r.Up,
			&r.FwdReturn, &r.Primary, &r.Secondary, &reasons); err != nil {
			return nil, err
		}
		r.Market = md.Market(mkt)
		r.Horizon = md.Horizon(hz)
		r.Reasons = json.RawMessage(reasons)
		out = append(out, r)
	}
	return out, rows.Err()
}
