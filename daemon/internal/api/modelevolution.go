// Model-evolution API: GET /api/model-evolution (read-only, public like the
// other market-data reads). Returns the trailing-N-day series the web charts to
// show HOW the model has evolved: per-(regime cell, leg) learned-weight
// snapshots from weight_history (appended each adaptive-weights run) and the
// per-leg factor-IC trend from the self_audit table.
//
// HONESTY: no interpolation. A weight series has a point only for runs where
// that (regime, leg) actually had a learned weight; a factor-IC series has a
// point only where the IC was actually measured (n>=30). Gaps are gaps.
package api

import (
	"net/http"
	"strconv"
	"time"
)

const modelEvolutionNote = "How the learned model has evolved over the window: per-(regime cell, leg) adaptive blend-weight snapshots and per-leg factor-IC trend, measured from the platform's own graded history. Honest gaps where no learned weight existed or the factor was below the n>=30 skill gate — no interpolation."

// modelEvolution serves the trailing-window model-evolution series.
// GET /api/model-evolution?days=  (default 30, cap 365)
func (d Deps) modelEvolution(w http.ResponseWriter, r *http.Request) {
	days := 30
	if v := r.URL.Query().Get("days"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 365 {
			days = n
		}
	}
	since := time.Now().Add(-time.Duration(days) * 24 * time.Hour).Unix()
	data, err := d.St.ModelEvolution(r.Context(), since)
	if err != nil {
		httpInternal(w, err)
		return
	}
	writeJSON(w, map[string]any{
		"note":        modelEvolutionNote,
		"days":        days,
		"weights":     data.Weights,
		"factorSkill": data.FactorSkill,
	})
}

// registerModelEvolution wires the model-evolution read route.
func (d Deps) registerModelEvolution(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/model-evolution", d.modelEvolution)
}
