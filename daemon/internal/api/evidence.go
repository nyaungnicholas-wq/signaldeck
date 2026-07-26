// GET /api/evidence — the Evidence Engine, read side.
//
// Every claim carries its machine-checkable evidence items (cluster-robust
// n_effective, method, correction, CI vs its own null) plus a rule-justified
// confidence tier and an expiry. The nightly sweep marks past-due claims
// stale and downgrades them one tier; refuting evidence retires a claim.
// This surface renders exactly what is stored — no re-derivation — and
// carries the semantics in-payload so the numbers cannot be read as more
// than they are.
package api

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/nyaungnicholas-wq/signaldeck/internal/evidence"
)

func (d Deps) registerEvidence(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/evidence", d.evidenceList)
	mux.HandleFunc("GET /api/evidence/{id}", d.evidenceOne)
}

// evidenceNote is shipped with every list payload so the tier/status rules
// travel with the data.
const evidenceNote = "tiers are rule-justified from the items (strong = corrected CI excluding the null " +
	"with n_effective >= 300, cluster-robust); claims past revalidate_by are swept stale and lose one tier; " +
	"refuting evidence retires the claim; seeded=true rows are programmatic examples built from the " +
	"published registry/audit record, not live measurements"

func (d Deps) evidenceList(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	feature := r.URL.Query().Get("feature")
	claims, err := evidence.List(r.Context(), d.St, status, feature)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, "evidence: "+err.Error())
		return
	}
	writeJSON(w, map[string]any{
		"claims":  claims,
		"count":   len(claims),
		"status":  status,
		"feature": feature,
		"note":    evidenceNote,
	})
}

func (d Deps) evidenceOne(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	c, err := evidence.Get(r.Context(), d.St, id)
	if errors.Is(err, sql.ErrNoRows) {
		httpErr(w, http.StatusNotFound, "evidence: no claim "+id)
		return
	}
	if err != nil {
		httpErr(w, http.StatusInternalServerError, "evidence: "+err.Error())
		return
	}
	writeJSON(w, map[string]any{"claim": c, "note": evidenceNote})
}
