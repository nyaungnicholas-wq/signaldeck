package api

import (
	"net/http"

	"github.com/nyaungnicholas-wq/signaldeck/internal/ensemble"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// predictions returns the latest calibrated ensemble prediction per horizon
// for a symbol.
func (d Deps) predictions(w http.ResponseWriter, r *http.Request) {
	s, err := d.symbolFromQuery(r)
	if err != nil {
		httpErr(w, 404, err.Error())
		return
	}
	out := map[md.Horizon]any{}
	for _, h := range md.Horizons {
		if p, ok, err := d.St.LatestPrediction(r.Context(), s.ID, h); err == nil && ok {
			out[h] = p
		}
	}
	writeJSON(w, out)
}

// calibration returns the reliability curve for one horizon across all
// symbols — the "are our 70% calls actually 70%?" evidence.
func (d Deps) calibration(w http.ResponseWriter, r *http.Request) {
	h := md.Horizon(r.URL.Query().Get("horizon"))
	if h != md.H1d && h != md.H1w {
		h = md.H1d
	}
	probs, ups, err := d.St.ResolvedPredictionPairs(r.Context(), h, 10000)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	pairs := make([]ensemble.Pair, len(probs))
	for i := range probs {
		pairs[i] = ensemble.Pair{Pred: probs[i], Actual: ups[i]}
	}
	writeJSON(w, map[string]any{
		"horizon":     h,
		"n":           len(pairs),
		"bins":        ensemble.CalibrationCurve(pairs, 10),
		"brier":       ensemble.BrierScore(pairs),
		"reliability": ensemble.ReliabilityScore(pairs),
		// Phase 0 labeling: calibration is measured over BACKTESTED / in-sample
		// resolutions until the system accrues a live track record. The frontend
		// badges off `live`.
		"live":       false,
		"trackLabel": "backtested / in-sample — not a live track record",
	})
}

// regimes returns the current regime per symbol + recent regime changes.
func (d Deps) regimes(w http.ResponseWriter, r *http.Request) {
	states, err := d.St.Regimes(r.Context())
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	changes, err := d.St.RecentRegimeChanges(r.Context(), 50)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	writeJSON(w, map[string]any{"states": states, "changes": changes})
}

// ranking returns the latest cross-sectional relative-strength ranking.
func (d Deps) ranking(w http.ResponseWriter, r *http.Request) {
	rows, err := d.St.LatestRanking(r.Context())
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	writeJSON(w, rows)
}

// breakouts returns recent detected breakout/squeeze/correlation-break events.
func (d Deps) breakouts(w http.ResponseWriter, r *http.Request) {
	rows, err := d.St.RecentBreakouts(r.Context(), 80)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	writeJSON(w, rows)
}

// registerPredict wires the prediction + trends routes.
func (d Deps) registerPredict(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/predictions", d.predictions)
	mux.HandleFunc("GET /api/calibration", d.calibration)
	mux.HandleFunc("GET /api/regime", d.regimes)
	mux.HandleFunc("GET /api/ranking", d.ranking)
	mux.HandleFunc("GET /api/breakouts", d.breakouts)
}
