// Stage 5 (visual hubs) — GET /api/predictions/latest: the SIGNALS hub
// predictions table in one call. Latest calibrated prediction per active
// symbol for one horizon (1d default, 1w allowed), strongest conviction
// first, WITH the same honesty gate the dashboard confidence gauge carries:
// below minIndependentN resolved outcomes the payload is explicitly gated and
// the caption says "n=X/30 resolved — not significant yet". Calibration is
// backtested / in-sample until a live track record exists — the trackLabel
// states that verbatim and the UI must render it next to every badge column.
package api

import (
	"net/http"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/symbolagent"
)

// predictionsLatest handles GET /api/predictions/latest?horizon=1d|1w.
func (d Deps) predictionsLatest(w http.ResponseWriter, r *http.Request) {
	h := md.Horizon(r.URL.Query().Get("horizon"))
	if h != md.H1d && h != md.H1w {
		h = md.H1d
	}
	rows, err := d.St.LatestPredictionsAll(r.Context(), h)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	// INDEPENDENT (symbol, UTC-day) resolutions — the unit minIndependentN is
	// DEFINED in (api.go: "the floor of distinct symbol-days"). A raw COUNT(*)
	// here overstated the evidence ~11x and published that inflated number to
	// the UI as `resolvedN`.
	resolvedN, distinctDays, err := d.St.ResolvedPredictionIndependentCount(r.Context(), h)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	gated := resolvedN < minIndependentN
	caption := confNote
	if gated {
		caption = fmtGateCaption(resolvedN)
	}
	writeJSON(w, map[string]any{
		"horizon":      h,
		"rows":         rows,
		"n":            len(rows),
		"resolvedN":    resolvedN,
		"distinctDays": distinctDays,
		"minResolvedN": minIndependentN,
		"gated":        gated,
		"caption":      caption,
		"trackLabel":   "backtested / in-sample — not a live track record",
		// Stage 2 (verdict cards): each row's tier/nSamples measures against
		// this personal-model graduation gate ("still learning 12/40 …").
		"tierThreshold": symbolagent.MinPersonal,
		"note": "Latest calibrated ensemble prediction per active symbol, strongest conviction first. " +
			"Symbols without a stored prediction are absent (the predictor fills them in on worker cadence) — never fabricated.",
	})
}

// registerStage5 wires the visual-hub batch routes.
func (d Deps) registerStage5(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/predictions/latest", d.predictionsLatest)
}
