// Credibility wave — REGIME-CALL POSTMORTEMS (read route).
//
// GET /api/regime-postmortems — the latest (≤50) plain-English postmortems for
// HIGH-conviction regime calls that resolved WRONG (written by the
// regime-outcome worker; see internal/pipeline/regimeoutcomes.go). A separate
// surface from /api/postmortems because the shapes differ: a directional miss
// gets a ranked reason taxonomy; a regime miss has one measured story — what
// was called, what realized (the key number), and the base rate the CLAIMED
// accuracy itself implies. Owning misses in public is the credibility play.
package api

import "net/http"

func (d Deps) regimePostmortems(w http.ResponseWriter, r *http.Request) {
	rows, err := d.St.RecentRegimePostmortems(r.Context(), 50)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	writeJSON(w, map[string]any{
		"postmortems": rows,
		"count":       len(rows),
		"note":        "every entry is a HIGH-conviction (>=0.8) regime call that resolved WRONG, graded with the exact engine math; the base-rate sentence is computed from the accuracy claimed at call time, never invented",
	})
}

// registerRegimePostmortems wires the credibility-wave miss feed.
func (d Deps) registerRegimePostmortems(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/regime-postmortems", d.regimePostmortems)
}
