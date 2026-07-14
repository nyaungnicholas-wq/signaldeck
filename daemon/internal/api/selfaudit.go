// Self-audit / drift-watchdog API: GET /api/self-audit (read-only, public like
// the other market-data reads). Returns the latest finding per metric from the
// self-audit worker — the platform grading its OWN honesty from resolved
// history (calibration drift, factor-IC sign flips, prediction bias).
//
// HONESTY: every finding carries a status; checks below the n>=30 gate come back
// with status "insufficient" and value 0 — measured too thin to judge, never a
// fabricated alarm. The note ships in the payload verbatim.
package api

import (
	"net/http"

	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

const selfAuditNote = "Deterministic self-audit of the platform's own honesty (no LLM): calibration drift, factor-IC sign flips, and prediction bias measured from resolved predictions, one observation per (symbol, UTC-day). Every check is gated at n>=30 independent resolutions — status 'insufficient' below that, never a false alarm."

// selfAudit serves the latest self-audit finding per metric.
func (d Deps) selfAudit(w http.ResponseWriter, r *http.Request) {
	findings, err := d.St.LatestSelfAudit(r.Context())
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	if findings == nil {
		findings = []store.SelfAuditRow{} // honest empty state → [], never null
	}
	var generatedTs int64
	for _, f := range findings {
		if f.Ts > generatedTs {
			generatedTs = f.Ts
		}
	}
	writeJSON(w, map[string]any{
		"note":        selfAuditNote,
		"generatedTs": generatedTs,
		"findings":    findings,
		"empty":       len(findings) == 0, // the worker hasn't run yet (once/UTC-day gate)
	})
}

// registerSelfAudit wires the self-audit read route.
func (d Deps) registerSelfAudit(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/self-audit", d.selfAudit)
}
