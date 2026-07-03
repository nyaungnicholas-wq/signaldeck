package api

import "net/http"

// datastats serves the dataset-accounting snapshot (storage-permanence wave):
// per-table row counts + time spans and DB/WAL file sizes, so data growth is
// visible and provable on /quality. Read-only; gated like every other read
// via requiresAuth (public when SIGNALDECK_PUBLIC_READS=true).
func (d Deps) datastats(w http.ResponseWriter, r *http.Request) {
	stats, err := d.St.DataStats(r.Context())
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	writeJSON(w, stats)
}
