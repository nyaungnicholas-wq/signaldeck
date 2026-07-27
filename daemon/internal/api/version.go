package api

import (
	"net/http"

	"github.com/nyaungnicholas-wq/signaldeck/internal/lineage"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// version answers the first question a reviewer asks about any published
// number: WHAT CODE PRODUCED IT. The binary's embedded vcs.revision was already
// read at startup but was visible nowhere — not in an endpoint, not on a row —
// so a reader had to take the deployment's word for it.
//
// `revision` is the raw commit; `modified` is vcs.modified, true when the
// binary was built from a dirty checkout. `rowStamp` is exactly the string this
// process writes onto every regime_outcomes / prediction_ledger / worker_runs
// row it creates, so a row's stamp can be matched against a live daemon without
// guessing at the encoding. An empty revision is reported as empty, never as a
// placeholder that could be mistaken for a real commit.
func (d Deps) version(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{
		"version":    d.Version,
		"revision":   lineage.BuildRevision(),
		"modified":   lineage.BuildModified(),
		"rowStamp":   store.CodeRevision(),
		"resolvable": lineage.BuildRevision() != "" && !lineage.BuildModified(),
	})
}
