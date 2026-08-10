package api

import (
	"net/http"
	"strconv"
	"time"
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

	writeJSON(w, map[string]any{
		"windowDays":   days,
		"totalMisses":  total,
		"clusters":     clusters, // biggest recurring failure mode first
		"recent":       recent,
		"taxonomyNote": "primary reason per resolved WRONG prediction; 'unexplained' means no recorded signal saw it coming (a missing-feature flag, not an error).",
	})
}
