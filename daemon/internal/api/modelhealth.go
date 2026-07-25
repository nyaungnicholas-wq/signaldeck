// GET /api/model-health — every tracked model's live record against its own
// shipped claim.
//
// This is the scoreboard the platform is ultimately judged on, and it is
// deliberately blunt: for each model, what it CLAIMED, what it actually did,
// what the naive alternative would have scored, and whether it is still
// permitted to emit.
//
// The directional ensemble is already retired here on live evidence. The three
// structural predictors are the ones that survived validation, and until their
// horizons elapse their claims are backtest numbers — which the payload says
// rather than implies.
package api

import (
	"encoding/json"
	"net/http"

	"github.com/nyaungnicholas-wq/signaldeck/internal/pipeline"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

func (d Deps) modelHealth(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	models := []map[string]any{}

	// Directional + structural verdicts are stored by the hourly worker under a
	// shared meta prefix; read whatever it has written rather than recomputing,
	// so this endpoint and the gate can never disagree.
	keys := []string{"directional-ensemble-1d", "directional-ensemble-1w"}
	for _, k := range store.StructuralKinds() {
		keys = append(keys, "structural-"+k)
	}
	for _, k := range keys {
		raw, err := d.St.GetMeta(ctx, pipeline.MetaKeyPrefix+k)
		if err != nil || raw == "" {
			models = append(models, map[string]any{
				"model": k, "graded": false,
				"note": "not yet graded — the health worker runs hourly",
			})
			continue
		}
		var v map[string]any
		if json.Unmarshal([]byte(raw), &v) != nil {
			continue
		}
		v["graded"] = true
		models = append(models, v)
	}

	// Live structural records, including the ungraded ones, so the page can show
	// how far each is from having enough evidence to judge.
	all, _ := d.St.StructuralRecords(ctx, 0)
	hi, _ := d.St.StructuralRecords(ctx, 0.9)
	// Serialise an empty result as [] rather than null: a JS consumer can map
	// over the first and crashes on the second, and "no resolved outcomes yet"
	// is the NORMAL state until the first horizons elapse.
	if all == nil {
		all = []store.StructuralRecordRow{}
	}
	if hi == nil {
		hi = []store.StructuralRecordRow{}
	}

	writeJSON(w, map[string]any{
		"models":              models,
		"structuralAll":       all,
		"structuralHighConv":  hi,
		"minObservations":     30,
		"howToRead": "liveAccuracy is what happened. claimedAccuracy is what the " +
			"forecast advertised when it was made. persistenceBase is what doing " +
			"nothing would have scored — for a structural call that is the honest " +
			"null, so edgeVsPersistence, not accuracy, is the number that says " +
			"whether the model contributes anything.",
		"retirementRule": "A model whose accuracy sits below its own naive baseline " +
			"is retired automatically and stops emitting, regardless of how it scores " +
			"on calibration, drift or freshness. Below 30 independent observations no " +
			"verdict is claimed in either direction.",
		"survivorship": survivorshipBlock(),
	})
}

func (d Deps) registerModelHealth(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/model-health", d.modelHealth)
}
