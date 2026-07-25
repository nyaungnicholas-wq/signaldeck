// GET /api/explain?symbol=&market= — the auditable regime forecast.
//
// Answers "why does the model say this?" with measured contributors, their
// signed weights, and a real historical analog with its realized outcome.
//
// Built ONLY over the structural regime forecasts. The directional model was
// auto-retired by the model-health gate (48.0% against a 54.4% baseline), and
// giving a rejected model an explanation UI would make it look more credible,
// not less — so this endpoint refuses to decorate it and says why.
package api

import (
	"net/http"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/structregime"
)

const explainMinBars = 260 // 200-day average plus enough warmup to rank it

func (d Deps) explain(w http.ResponseWriter, r *http.Request) {
	s, err := d.symbolFromQuery(r)
	if err != nil {
		httpErr(w, 404, err.Error())
		return
	}
	bars, err := d.St.Bars(r.Context(), s.ID, md.TF1d, 0, 1<<62, 0)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	if len(bars) < explainMinBars {
		writeJSON(w, map[string]any{
			"symbol": s.Symbol, "market": string(s.Market),
			"available": false,
			"reason": "not enough daily history to compute a 200-day average and rank it — " +
				"an explanation built on a short series would be a guess",
			"barsHave": len(bars), "barsNeed": explainMinBars,
		})
		return
	}
	closes := make([]float64, len(bars))
	for i, b := range bars {
		closes[i] = b.Close
	}

	ex, ok := structregime.AuditTrend(closes)
	if !ok {
		// The predictor refuses contaminated or degenerate windows. Absence is
		// the honest answer; a fabricated explanation is not.
		writeJSON(w, map[string]any{
			"symbol": s.Symbol, "market": string(s.Market),
			"available": false,
			"reason": "the trend predictor refused this series — usually an unadjusted-split " +
				"discontinuity or too little clean history (see /api/split-repairs)",
		})
		return
	}

	writeJSON(w, map[string]any{
		"symbol":      s.Symbol,
		"market":      string(s.Market),
		"available":   true,
		"asOf":        bars[len(bars)-1].Ts,
		"close":       bars[len(bars)-1].Close,
		"prediction":  ex.Summary,
		"kind":        ex.Kind,
		"regime":      ex.Regime,
		"horizonDays": ex.Horizon,
		// The accuracy of THIS conviction band, never the population average —
		// quoting the headline number on a low-conviction call is how an 83%
		// label ends up attached to a coin flip.
		"bandedAccuracy": ex.Accuracy,
		"tier":           ex.Tier,
		"conviction":     ex.Conviction,
		"supports":       ex.Supports,
		"opposes":        ex.Opposes,
		"analog":         ex.Analog,
		"caveat":         ex.Caveat,
		"whyNotDirection": "No BUY/SELL or expected-return field is offered. The directional " +
			"model was automatically retired after scoring 48.0% against a 54.4% naive " +
			"baseline over 12,696 independent observations; presenting it with contributors " +
			"and a confidence interval would make a rejected model look more credible.",
		"survivorship": survivorshipBlock(),
	})
}

func (d Deps) registerExplain(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/explain", d.explain)
}
