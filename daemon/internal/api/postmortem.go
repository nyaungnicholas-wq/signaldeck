package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// postmortems serves the Research Lab's failure-attribution surface: the
// clustered failure modes (biggest recurring cause first) plus the most recent
// individually-attributed misses. This is the honest, self-critical view of
// where the model has been WRONG and why — the raw material the Research Lab
// mines for new hypotheses.
//
// ?days=N restricts clustering to postmortems created in the last N days
// (default 30, 0 ⇒ all time). The recent feed is always the newest ~100.
func (d Deps) postmortems(w http.ResponseWriter, r *http.Request) {
	days := 30
	if v := r.URL.Query().Get("days"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			days = n
		}
	}

	var sinceTs int64
	if days > 0 {
		sinceTs = time.Now().Add(-time.Duration(days) * 24 * time.Hour).Unix()
	}

	clusters, total, err := d.St.PostmortemClusters(r.Context(), sinceTs)
	if err != nil {
		httpInternal(w, err)
		return
	}
	recent, err := d.St.RecentPostmortems(r.Context(), 100)
	if err != nil {
		httpInternal(w, err)
		return
	}

	var recentOut any = recent
	if !d.isOperator(r) {
		// fwdReturn is close/close-1 on two licensed vendor closes, keyed to a
		// symbol and a time: RAW under the licence line in datalicense.go, the
		// same row shape /api/export/outcomes.csv is governed for. This route is
		// in publicRoutes, so everyone but the operator gets the row without it.
		// An allowlist, not a copy with one field blanked: a column added to the
		// store row later stays off this surface until someone lists it here.
		pub := make([]publicPostmortemRow, 0, len(recent))
		for _, p := range recent {
			pub = append(pub, publicPostmortemRow{
				Symbol: p.Symbol, Market: p.Market, Horizon: p.Horizon, Ts: p.Ts, Prob: p.Prob,
				Up: p.Up, Primary: p.Primary, Secondary: p.Secondary, Reasons: p.Reasons,
			})
		}
		recentOut = pub
	}

	writeJSON(w, map[string]any{
		"windowDays":   days,
		"totalMisses":  total,
		"clusters":     clusters, // biggest recurring failure mode first
		"recent":       recentOut,
		"taxonomyNote": "primary reason per resolved WRONG prediction; 'unexplained' means no recorded signal saw it coming (a missing-feature flag, not an error).",
	})
}

// publicPostmortemRow is store.RecentPostmortemRow without fwdReturn: the
// recent-misses row everyone but the operator receives.
type publicPostmortemRow struct {
	Symbol    string          `json:"symbol"`
	Market    md.Market       `json:"market"`
	Horizon   md.Horizon      `json:"horizon"`
	Ts        int64           `json:"ts"`
	Prob      float64         `json:"prob"`
	Up        int             `json:"up"`
	Primary   string          `json:"primary"`
	Secondary string          `json:"secondary,omitempty"`
	Reasons   json.RawMessage `json:"reasons"`
}
