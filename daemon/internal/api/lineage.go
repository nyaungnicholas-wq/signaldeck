// GET /api/lineage — the research lineage spine (Layers 2+8), read side.
//
// Returns the connected subgraph around one node: ?kind=&id=&depth= (depth
// defaults to 3, hard-capped at lineage.MaxTraceDepth). Nodes carry their hop
// distance; edges carry meta_json (the git revision of the code that wrote
// them). Read-only store queries, gated like every other read.
package api

import (
	"net/http"
	"strconv"

	"github.com/nyaungnicholas-wq/signaldeck/internal/lineage"
)

func (d Deps) registerLineage(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/lineage", d.lineageTrace)
}

const lineageNote = "edges are written at produce time by the wired research producers (research loop, " +
	"research ledger, prediction runner); meta_json.rev is the VCS revision of the build that wrote the edge; " +
	"unwired producers are listed as TODOs in internal/lineage/doc.go — an absent edge means not-yet-wired, not not-related"

func (d Deps) lineageTrace(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	kind, id := q.Get("kind"), q.Get("id")
	if kind == "" || id == "" {
		httpErr(w, http.StatusBadRequest, "lineage: kind and id are required")
		return
	}
	if !lineage.ValidNodeKind(kind) {
		httpErr(w, http.StatusBadRequest, "lineage: unknown kind "+kind)
		return
	}
	depth := 3
	if s := q.Get("depth"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 {
			httpErr(w, http.StatusBadRequest, "lineage: depth must be a positive integer")
			return
		}
		depth = n
	}
	g, err := lineage.Trace(r.Context(), d.St, kind, id, depth)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, "lineage: "+err.Error())
		return
	}
	writeJSON(w, map[string]any{"graph": g, "note": lineageNote})
}
