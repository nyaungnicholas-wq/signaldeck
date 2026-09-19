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
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/canary"
	"github.com/nyaungnicholas-wq/signaldeck/internal/clusterstat"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
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
	pendingKnown := true
	if rows, err := d.St.StructuralPending(ctx); err == nil {
		for _, p := range rows {
			pending[p.Kind] = p
		}
	} else {
		// An empty pending map steers ungradedModel into its most CONFIDENT
		// branch -- "no unresolved forecasts on record, so this model has not
		// emitted a call the grader could score" -- for every model at once,
		// from a single failed read.
		pendingKnown = false
	}

	keys := []string{"directional-ensemble-1d", "directional-ensemble-1w"}
	for _, k := range store.StructuralKinds() {
		keys = append(keys, "structural-"+k)
	}
	for _, k := range keys {
		raw, err := d.St.GetMeta(ctx, pipeline.MetaKeyPrefix+k)
		if err != nil || raw == "" {
			m := ungradedModel(k, pending)
			if !pendingKnown {
				m["pendingUnknown"] = true
				m["note"] = "the pending-forecast table could not be read, so why this model is ungraded is UNKNOWN — not established as 'nothing to score'"
			}
			models = append(models, m)
			continue
		}
		var v map[string]any
		if uerr := json.Unmarshal([]byte(raw), &v); uerr != nil {
			// A corrupt grade used to VANISH from the array entirely, so a model
			// with an unreadable verdict was indistinguishable from one that was
			// never registered. Surface it instead.
			models = append(models, map[string]any{
				"model": k, "gradeUnreadable": true,
				"note": "this model's stored grade could not be parsed: " + uerr.Error(),
			})
			continue
		}
		v["graded"] = true
		v = guardDerivedVariant(v)
		// The retired flagship's one door back: its shadow rows keep accruing
		// in prediction_outcomes, and the coded threshold — never a judgment
		// call — decides whether they have earned emission back.
		if h, isDirectional := strings.CutPrefix(k, "directional-ensemble-"); isDirectional {
			if shadow, ok := d.directionalShadow(ctx, md.Horizon(h)); ok {
				v = applyReadmission(v, shadow)
			}
		}
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
		"inversionRule": "Inverting or relabeling a retired model is not a rescue. The " +
			"competing model is the constant majority guess, not a coin flip, and that " +
			"null sits above 50%: flipping an accuracy of a yields 1-a, which clears the " +
			"majority-class rate only by the margin the original trailed 50% by, not by " +
			"the margin it trailed the null by. Inversion relabels an edge, it does not " +
			"create one. A derived variant is a new model and may emit only after passing " +
			"the full canary re-admission gate on its own shadow record.",
		"readmissionRule": fmt.Sprintf("Re-admission is a coded threshold, not a judgment "+
			"call: a retired model's emitting flips back to true only when its shadow "+
			"record's day-clustered Wilson lower bound (design effect measured from the "+
			"between-day variance, interval evaluated at effective N) clears the "+
			"prequential null on at least %d distinct UTC days — 2x the platform's "+
			"%d-day interval floor. The same rule is the canary promotion bar for any "+
			"successor model, so the two gates can never disagree about the same record.",
			canary.ReadmitMinDistinctDays, clusterstat.MinDistinctDays),
		"survivorship": survivorshipBlock(),
	})
}

func (d Deps) registerModelHealth(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/model-health", d.modelHealth)
}

// derivedVariantMarkers flag a model key as a re-signed or re-badged rescue of
// a retired model — the same below-null signal wearing a new name. Inversion is
// refuted by policy AND by arithmetic: the competing model is the constant
// majority guess, whose rate is above 50%, so flipping an accuracy of a yields
// 1-a and clears that null only by the margin the original trailed 50% by. The
// specific figures are not typed here — they move with every grade, and an
// argument pinned to a stale pair can quietly stop being true. See
// PREDICTION_PROCESS.md, "Why inversion is not a rescue".
var derivedVariantMarkers = []string{"-inverted", "-relabeled", "-flipped"}

// guardDerivedVariant forces an inverted/relabeled variant off unless the
// canary re-admission gate has written readmitted=true for it. A variant is a
// NEW model and earns emission the way one does; nothing it inherits from the
// retired original counts as evidence.
func guardDerivedVariant(v map[string]any) map[string]any {
	name, _ := v["model"].(string)
	derived := false
	for _, marker := range derivedVariantMarkers {
		if strings.Contains(name, marker) {
			derived = true
			break
		}
	}
	if !derived {
		return v
	}
	if readmitted, _ := v["readmitted"].(bool); readmitted {
		return v
	}
	v["emitting"] = false
	v["verdict"] = "retired"
	v["note"] = "inverted/relabeled variant of a retired model — blocked from emitting. " +
		"Inverting relabels a signal, it does not create an edge, and the competing " +
		"model is the constant majority guess rather than a coin flip. Emission requires " +
		"passing the canary re-admission gate, which records readmitted=true."
	return v
}

// applyReadmission is the other half of the retirement story guardDerivedVariant
// tells: the guard keeps a re-badged corpse from emitting, and this codifies the
// one door back in. Re-admission is a threshold, not a judgment call — emitting
// flips back to true ONLY when the shadow record's day-clustered CI lower bound
// (clusterstat.DesignEffect + WilsonEff, the same machinery as the canary gate)
// clears the prequential null on >= canary.ReadmitMinDistinctDays distinct days.
// The verdict block is published either way, designEffect and effectiveN
// included, so the payload always shows how far the record sits from the bar.
func applyReadmission(v map[string]any, shadow canary.Record) map[string]any {
	if verdict, _ := v["verdict"].(string); verdict != "retired" {
		return v
	}
	ra := canary.Readmit(shadow)
	v["readmission"] = ra
	if !ra.Eligible {
		return v
	}
	v["emitting"] = true
	v["readmitted"] = true
	v["verdict"] = "readmitted"
	v["note"] = "re-admitted by the coded threshold: " + ra.Reason
	return v
}

// directionalShadow rebuilds a retired directional model's LIVE shadow record
// from prediction_outcomes — the rows keep accruing after retirement precisely
// so re-admission can be a measurement. The aggregation mirrors the canary
// runner's: one observation per (symbol, UTC day), tallied per day, with the
// prequential null replayed in day order over the same tallies.
func (d Deps) directionalShadow(ctx context.Context, h md.Horizon) (canary.Record, bool) {
	// Bounded at the SURVIVORSHIP EPOCH, the same boundary the accuracy registry
	// grades on. Re-admission compares an ABSOLUTE record against an absolute
	// null, which is the comparison survivor-seeded rows distort; measured on the
	// live DB (2026-07-27) the unbounded window supplied 13,065 rows over 24 days
	// where only 21 rows over 3 days were post-epoch, so the 20-distinct-day
	// floor was being cleared entirely by rows the grader refuses to publish.
	rows, err := d.St.VersionedOutcomes(ctx, h, 200000, store.SurvivorshipEpoch)
	if err != nil || len(rows) == 0 {
		return canary.Record{}, false
	}
	rec := canary.Record{Version: "shadow-" + string(h), FirstTs: rows[0].Ts, LastTs: rows[0].Ts}
	byDay := map[int64]*canary.DayTally{}
	dayUps := map[int64]int{}
	for _, r := range rows {
		rec.N++
		if r.Correct {
			rec.Correct++
		}
		if r.Ts < rec.FirstTs {
			rec.FirstTs = r.Ts
		}
		if r.Ts > rec.LastTs {
			rec.LastTs = r.Ts
		}
		day := md.TradingDay(r.Ts)
		t := byDay[day]
		if t == nil {
			t = &canary.DayTally{Day: day}
			byDay[day] = t
		}
		t.N++
		if r.Correct {
			t.Hits++
		}
		if r.Up {
			dayUps[day]++
		}
	}
	tallies := make([]canary.DayTally, 0, len(byDay))
	for _, t := range byDay {
		tallies = append(tallies, *t)
	}
	sort.Slice(tallies, func(i, j int) bool { return tallies[i].Day < tallies[j].Day })
	rec.DayTallies = tallies
	rec.Days = len(tallies)
	rec.BaselineAccuracy = canary.PrequentialBaseline(tallies, dayUps)
	return rec, true
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
