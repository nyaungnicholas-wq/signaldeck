package api

import (
	"encoding/json"
	"net/http"
)

// research serves the Research Lab's hypothesis registry: the automated
// discovery loop's current shadow candidates, promoted (independently-verified)
// findings, and rejected ones, plus the advisory feedback signal. This is the
// audit trail of "what has the lab tried, and what actually survived out-of-
// sample re-testing on fresh data."
//
// ?status=shadow|promoted|rejected filters the list (default: all). The
// summary counts are always included.
func (d Deps) research(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	list, err := d.St.HypothesesByStatus(r.Context(), status, 500)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	counts, err := d.St.HypothesisStatusCounts(r.Context())
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}

	resp := map[string]any{
		"counts":     counts, // status → count
		"hypotheses": list,
		"discipline": "each survives strict walk-forward OOS + a Bonferroni-corrected Wilson floor over baseline; promotion requires a sustained streak of wins on fresh data; nothing here mutates live predictions.",
	}
	// Advisory feedback signal (recurring failures + promotions), if present.
	if raw, _ := d.St.GetMeta(r.Context(), "research_feedback:v1"); raw != "" {
		var fb any
		if json.Unmarshal([]byte(raw), &fb) == nil {
			resp["feedback"] = fb
		}
	}
	writeJSON(w, resp)
}
