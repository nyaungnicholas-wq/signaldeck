// Alerts endpoints (alerts wave). The list + mark-seen pair are session-
// scoped and auth-required (enforced centrally in security.go's
// requiresAuth), so userID(r) is always non-zero in those handlers.
// /api/alert-outcomes (appended below) is aggregate fleet-wide stats — no
// user data — and reads like every other public read.
package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// alertsList returns the session user's alerts, newest first.
// Query: ?unseen=1 filters to unread; ?limit= caps rows (default/max 100).
func (d Deps) alertsList(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	unseen := r.URL.Query().Get("unseen") == "1"
	rows, err := d.St.Alerts(r.Context(), userID(r), unseen, limit)
	if err != nil {
		httpInternal(w, err)
		return
	}
	writeJSON(w, rows)
}

// alertsSeen marks ALL of the session user's alerts read (CSRF applies via
// the middleware's custom-header requirement on non-GET methods).
func (d Deps) alertsSeen(w http.ResponseWriter, r *http.Request) {
	n, err := d.St.MarkAlertsSeen(r.Context(), userID(r))
	if err != nil {
		httpInternal(w, err)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "marked": n})
}

func (d Deps) registerAlerts(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/alerts", d.alertsList)
	mux.HandleFunc("POST /api/alerts/seen", d.alertsSeen)
	mux.HandleFunc("GET /api/alert-outcomes", d.alertOutcomes) // SIGNALS-hub overhaul (appended): per-kind forward-outcome receipts
}

// ─────────────────────────────────────────────────────────────────────────
// SIGNALS-HUB OVERHAUL — ALERT FORWARD OUTCOMES (appended block).
// GET /api/alert-outcomes?days= measures, per alert kind, what the symbol's
// price actually did after the alert fired: n fired (distinct symbol+ts,
// deduped across users), mean/median forward return over the next 1 and 5
// daily bars, and the 1d hit rate. HONESTY: these are receipts, not advice —
// forward returns after alerts imply no causation, and kinds with fewer than
// store.MinAlertOutcomeN resolvable returns show NULL stats with n visible.

// alertOutcomesNote ships verbatim in every payload.
const alertOutcomesNote = "forward returns after alerts, not advice; n<20 withheld"

// alertOutcomes serves per-kind alert forward-outcome stats.
func (d Deps) alertOutcomes(w http.ResponseWriter, r *http.Request) {
	days := windowParam(r, "days", 90, 365)
	sinceTs := time.Now().Unix() - int64(days)*86400
	kinds, err := d.St.AlertKindOutcomes(r.Context(), sinceTs)
	if err != nil {
		httpInternal(w, err)
		return
	}
	if kinds == nil {
		kinds = []store.AlertKindOutcome{}
	}
	writeJSON(w, map[string]any{
		"kinds":   kinds,
		"days":    days,
		"sinceTs": sinceTs,
		"minN":    store.MinAlertOutcomeN,
		"note":    alertOutcomesNote,
		"method":  "per kind: distinct fired events (symbol+ts, deduped across users); base = last daily close at/before the alert; 1d/5d = the 1st/5th stored daily bars after it (trading days); hit rate = share of 1d forward returns > 0",
	})
}
