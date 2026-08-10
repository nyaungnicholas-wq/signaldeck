// Feature health API: GET /api/feature-health — the per-INPUT scorecard and the
// retire set the GBM trainer actually honors.
//
// This is the read side of internal/featurehealth. The platform already retired
// MODELS automatically; this is the same discipline one level down, and the
// endpoint exists so the consequence is inspectable: a reader can see which
// inputs were dropped from the training vector, what score dropped them, and in
// as many words why.
//
// HONESTY: an ungraded horizon reports `graded: false` rather than an empty keep
// set, because "nothing was retired" and "nothing was examined" are different
// facts. When more than half the judged features grade out at once, the payload
// carries the report AND says retirement was withheld — a whole vector dying
// together points at the forward-return label, not at every feature dying
// independently.
package api

import (
	"net/http"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/pipeline"
)

const featureHealthNote = "Per-feature decay/stability/coverage grading of the model's INPUTS, with automatic retirement from the training vector. Surviving here is NOT evidence a feature predicts anything — it means the feature's measured relationship with forward returns has not decayed, flipped sign, or turned out to duplicate another input. Demonstrating predictive value remains the out-of-sample lift gate's job. A feature below the evidence floor is KEPT, never retired for being new."

func (d Deps) featureHealth(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	type horizonReport struct {
		Horizon string `json:"horizon"`
		Graded  bool   `json:"graded"`
		// Reason is set only when Graded is false.
		Reason string `json:"reason,omitempty"`

		Scores     any     `json:"scores,omitempty"`
		Keep       any     `json:"keep,omitempty"`
		Retire     any     `json:"retire,omitempty"`
		RetiredPct float64 `json:"retiredPct,omitempty"`
		Rows       int     `json:"rows,omitempty"`
		GradedAt   int64   `json:"gradedAt,omitempty"`
		Note       string  `json:"note,omitempty"`
	}

	out := make([]horizonReport, 0, 2)
	for _, h := range []md.Horizon{md.H1d, md.H1w} {
		rep, ok, err := pipeline.FeatureHealthFor(ctx, d.St, h)
		if err != nil {
			httpInternal(w, err)
			return
		}
		if !ok {
			out = append(out, horizonReport{
				Horizon: string(h),
				Graded:  false,
				Reason:  "the feature-health grader has not published a report for this horizon yet — no feature has been examined, which is not the same as no feature being retired",
			})
			continue
		}
		out = append(out, horizonReport{
			Horizon:    string(h),
			Graded:     true,
			Scores:     rep.Scores,
			Keep:       rep.Keep,
			Retire:     rep.Retire,
			RetiredPct: rep.RetiredPct,
			Rows:       rep.Rows,
			GradedAt:   rep.GradedAt,
			Note:       rep.Note,
		})
	}

	writeJSON(w, map[string]any{
		"note":        featureHealthNote,
		"generatedTs": time.Now().Unix(),
		"horizons":    out,
	})
}

// registerFeatureHealth wires the per-feature health read.
func (d Deps) registerFeatureHealth(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/feature-health", d.featureHealth)
}
