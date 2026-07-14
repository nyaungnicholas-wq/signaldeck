// CROSS-SECTIONAL ALPHA wave — API (read-only, public read like the other
// model-status endpoints):
//
//	GET /api/alphax — per horizon: the pooled cross-sectional model's latest
//	purged-walk-forward OOS grade (lift/AUC/accuracy/base rate/n), its gate
//	state with a stated reason, and — ONLY while ungated (measured OOS lift
//	> 0) — the top-20 current symbol scores, explicitly framed as RELATIVE
//	to the same-day universe, never absolute direction.
//
// HONESTY (shipped verbatim in every payload): see alphaxAPINote below. An
// ungraded or edgeless model shows its grade and its gate — it never shows a
// score.
package api

import (
	"net/http"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

const (
	// alphaxAPINote ships VERBATIM with every /api/alphax payload.
	alphaxAPINote = "pooled cross-sectional model — predicts RELATIVE outperformance vs the same-day universe median, graded on purged walk-forward out-of-sample splits; gated off (never blended, never displayed as signal) until measured OOS lift > 0; backtested, not a live track record"
	// alphaxScoresNote frames every score list: relative rank, not direction.
	alphaxScoresNote = "scores rank expected performance relative to the universe, not absolute direction — a high score in a falling market means \"expected to fall less\""
	// alphaxGateNoEdge / alphaxGateNoModel are the stated gate reasons.
	alphaxGateNoEdge  = "measured OOS lift <= 0 — the model is stored for the record but never blended and never displayed as a signal"
	alphaxGateNoModel = "no graded model yet — the alpha-trainer refuses to grade below 1000 pooled train rows / 200 out-of-sample test rows across 10-deep days"
)

// alphaXHorizons mirrors the trainer's horizons (1d primary, 1w when data).
var alphaXHorizons = []md.Horizon{md.H1d, md.H1w}

// alphaX serves the pooled cross-sectional model status per horizon.
// GET /api/alphax
func (d Deps) alphaX(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	horizons := map[string]any{}
	for _, h := range alphaXHorizons {
		m, found, err := d.St.AlphaXModel(ctx, h)
		if err != nil {
			httpErr(w, 500, err.Error())
			return
		}
		if !found {
			horizons[string(h)] = map[string]any{
				"available":  false,
				"gated":      true,
				"gateReason": alphaxGateNoModel,
			}
			continue
		}
		hOut := map[string]any{
			"available": true,
			"ts":        m.Ts,
			"grade": map[string]any{
				"lift":     m.OOSLift,
				"auc":      m.OOSAUC,
				"accuracy": m.OOSAcc,
				"baseRate": m.BaseRate,
				"nTrain":   m.NTrain,
				"nTest":    m.NTest,
			},
			"gated": m.Gated,
		}
		if m.Gated {
			hOut["gateReason"] = alphaxGateNoEdge
		} else {
			scores, err := d.St.TopModelForecastsByModel(ctx, store.ModelAlphaX, h, 20)
			if err != nil {
				httpErr(w, 500, err.Error())
				return
			}
			hOut["topScores"] = scores // highest P(top half) first, ≤20
			hOut["scoresNote"] = alphaxScoresNote
		}
		horizons[string(h)] = hOut
	}
	writeJSON(w, map[string]any{
		"note":     alphaxAPINote,
		"horizons": horizons,
	})
}

func (d Deps) registerAlphaX(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/alphax", d.alphaX)
}
