package api

import (
	"net/http"

	"github.com/nyaungnicholas-wq/signaldeck/internal/researchx"
)

// researchGraph serves the research evidence graph: every ledger hypothesis
// linked to its signal family, the attacks run against it, the eras its
// evidence graded, and the kinds of evidence it carries — the ledger's
// structure as one navigable picture. Node and edge order is deterministic.
func (d Deps) researchGraph(w http.ResponseWriter, r *http.Request) {
	hyps, err := d.St.LedgerHypotheses(r.Context())
	if err != nil {
		httpInternal(w, err)
		return
	}
	evidence, err := d.St.LedgerEvidence(r.Context(), "")
	if err != nil {
		httpInternal(w, err)
		return
	}
	writeJSON(w, map[string]any{"graph": researchx.BuildGraph(hyps, evidence)})
}
