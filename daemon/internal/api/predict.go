package api

import (
	"net/http"
	"strconv"

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
	// INDEPENDENT (symbol, UTC-day) resolutions — the same unit /honesty and
	// /track-record report. On raw rows this endpoint reported n=10,000 for
	// what was 1,022 independent observations drawn from a SINGLE UTC day
	// (measured 2026-07-16), i.e. it presented one session as a 10,000-point
	// sample. Brier and reliability are means and so are unchanged by uniform
	// replication — it was never the curve that lied here, only `n`, and `n`
	// is what a reader judges the curve BY.
	probs, ups, distinctDays, err := d.St.ResolvedPredictionPairsIndependent(r.Context(), h, 10000)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	pairs := make([]ensemble.Pair, len(probs))
	for i := range probs {
		pairs[i] = ensemble.Pair{Pred: probs[i], Actual: ups[i]}
	}
	// Same two-axis gate as /track-record: enough independent observations AND
	// enough distinct market days. Below either floor the curve and its scores
	// are WITHHELD (null) rather than rendered — a reliability number computed
	// over one session is a statement about that session, not about the model.
	gated := len(pairs) < trackMinIndependentN || distinctDays < trackMinDistinctDays
	out := map[string]any{
		"horizon":         h,
		"n":               len(pairs),
		"distinctDays":    distinctDays,
		"minIndependentN": trackMinIndependentN,
		"minDistinctDays": trackMinDistinctDays,
		"gated":           gated,
		"bins":            nil,
		"brier":           nil,
		"reliability":     nil,
		// Phase 0 labeling: calibration is measured over BACKTESTED / in-sample
		// resolutions until the system accrues a live track record. The frontend
		// badges off `live`.
		"live":       false,
		"trackLabel": "backtested / in-sample — not a live track record",
	}
	if gated {
		out["note"] = notSignificant(len(pairs), trackMinIndependentN)
		if len(pairs) >= trackMinIndependentN && distinctDays < trackMinDistinctDays {
			out["note"] = "reliability withheld: " + strconv.Itoa(len(pairs)) + " independent resolutions span only " +
				strconv.Itoa(distinctDays) + " distinct market day(s), need " + strconv.Itoa(trackMinDistinctDays) +
				" (observations on one day share one market move)"
		}
		writeJSON(w, out)
		return
	}
	out["bins"] = ensemble.CalibrationCurve(pairs, 10)
	out["brier"] = ensemble.BrierScore(pairs)
	out["reliability"] = ensemble.ReliabilityScore(pairs)
	writeJSON(w, out)
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
