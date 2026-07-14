package api

import (
	"net/http"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/srchealth"
)

// sourceHealth serves GET /api/source-health — the one dashboard that answers
// "is any data source quietly dead?". For every EXTERNAL source it reports the
// newest row's timestamp, its age, the tolerated staleness budget, whether it is
// stale, whether it is market-gated, and a human note; plus an overall
// staleCount. Public read (gated exactly like the other read endpoints).
//
// HONESTY: market-gated stock sources are reported "market closed" — NOT stale —
// while the exchange is legitimately closed (weekend/holiday/overnight); crypto
// and other 24/7 sources are always checked. The tv_signals webhook source is
// only judged when a webhook secret is configured (else "webhook disabled"),
// which is what catches a silently-dead tunnel while trading is live. The same
// evaluation backs the source-audit worker's dq_events, so this view and the
// alerts can never disagree.
func (d Deps) sourceHealth(w http.ResponseWriter, r *http.Request) {
	reports, err := srchealth.Evaluate(r.Context(), d.St, time.Now(), d.Cfg.TVWebhookSecret != "")
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	writeJSON(w, map[string]any{
		"sources": reports,
		"overall": map[string]any{"staleCount": srchealth.StaleCount(reports)},
	})
}
