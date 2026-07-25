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
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/pipeline"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

func (d Deps) modelHealth(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	models := []map[string]any{}

	// Directional + structural verdicts are stored by the hourly worker under a
	// shared meta prefix; read whatever it has written rather than recomputing,
	// so this endpoint and the gate can never disagree.
	// Unresolved backlog per structural kind, so an ungraded model can say
	// which kind of ungraded it is: never emitted, or emitting into horizons
	// that have not elapsed. Blaming the hourly worker for the latter — as this
	// endpoint used to — reads as a stuck scheduler when it is just time.
	pending := map[string]store.StructuralPendingRow{}
	if rows, err := d.St.StructuralPending(ctx); err == nil {
		for _, p := range rows {
			pending[p.Kind] = p
		}
	}

	keys := []string{"directional-ensemble-1d", "directional-ensemble-1w"}
	for _, k := range store.StructuralKinds() {
		keys = append(keys, "structural-"+k)
	}
	for _, k := range keys {
		raw, err := d.St.GetMeta(ctx, pipeline.MetaKeyPrefix+k)
		if err != nil || raw == "" {
			models = append(models, ungradedModel(k, pending))
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

	// Phase 3: per-symbol grading. A predictor can be right fleet-wide and
	// reliably wrong on a subset, and averaging hides exactly the cases a user
	// would most want warned about. Symbols below the evidence floor are omitted
	// rather than shown with a noisy number.
	perSymbol, _ := d.St.SymbolStructuralRecords(ctx, r.URL.Query().Get("kind"), 20)
	if perSymbol == nil {
		perSymbol = []store.SymbolStructuralRow{}
	}

	writeJSON(w, map[string]any{
		"models":             models,
		"perSymbol":          perSymbol,
		"perSymbolMinN":      20,
		"structuralAll":      all,
		"structuralHighConv": hi,
		"minObservations":    30,
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

// ungradedModel explains WHY a model has no verdict yet, using the only
// evidence that can distinguish the cases: its unresolved forecast backlog.
//
// Three genuinely different states hid behind one sentence before this:
//
//   - the model has never emitted a call, so there is nothing to grade;
//   - it has emitted and the horizons have not elapsed — the honest answer is a
//     DATE, not a scheduler cadence;
//   - horizons have elapsed and a verdict still has not been written, which is
//     the only case where the hourly worker is actually the explanation.
func ungradedModel(key string, pending map[string]store.StructuralPendingRow) map[string]any {
	m := map[string]any{"model": key, "graded": false}
	kind := strings.TrimPrefix(key, "structural-")
	p, ok := pending[kind]
	if !ok || p.Pending == 0 {
		m["note"] = "not yet graded — no unresolved forecasts on record, so this model " +
			"has not emitted a call the grader could score"
		return m
	}
	m["pending"] = p.Pending
	m["firstDueTs"] = p.FirstDue
	due := time.Unix(p.FirstDue, 0).UTC()
	if p.FirstDue > time.Now().Unix() {
		m["note"] = fmt.Sprintf("not yet graded — %d forecasts outstanding and the earliest "+
			"horizon does not elapse until %s. This is the passage of time, not a stalled worker: "+
			"no verdict is possible before the calls resolve.",
			p.Pending, due.Format("2006-01-02"))
		m["awaitingResolution"] = true
		return m
	}
	m["note"] = fmt.Sprintf("not yet graded — %d forecasts are outstanding and the earliest was "+
		"due %s, so resolution is overdue rather than pending. The health worker runs hourly; if "+
		"this persists, the resolver is not keeping up.",
		p.Pending, due.Format("2006-01-02"))
	m["awaitingResolution"] = false
	return m
}
