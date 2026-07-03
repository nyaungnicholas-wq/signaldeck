// Alerts endpoints (alerts wave). Both are session-scoped and auth-required
// (enforced centrally in security.go's requiresAuth), so userID(r) is always
// non-zero here.
package api

import (
	"net/http"
	"strconv"
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
		httpErr(w, 500, err.Error())
		return
	}
	writeJSON(w, rows)
}

// alertsSeen marks ALL of the session user's alerts read (CSRF applies via
// the middleware's custom-header requirement on non-GET methods).
func (d Deps) alertsSeen(w http.ResponseWriter, r *http.Request) {
	n, err := d.St.MarkAlertsSeen(r.Context(), userID(r))
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true, "marked": n})
}

func (d Deps) registerAlerts(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/alerts", d.alertsList)
	mux.HandleFunc("POST /api/alerts/seen", d.alertsSeen)
}
