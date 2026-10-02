package api

import (
	"fmt"
	"net/http"

	"github.com/nyaungnicholas-wq/signaldeck/internal/ensemble"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/publication"
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
	// One pair per (symbol, settled trading day) over the grader's population,
	// uncapped (the graded window is the bound), so n is the independent N the
	// gate below and liveRecord.independentN both count.
	probs, ups, days, err := d.St.ResolvedPredictionPairs(r.Context(), h, -1)
	if err != nil {
		httpInternal(w, err)
		return
	}
	pairs := make([]ensemble.Pair, len(probs))
	dayset := make(map[int64]struct{}, len(days))
	for i := range probs {
		pairs[i] = ensemble.Pair{Pred: probs[i], Actual: ups[i]}
		dayset[days[i]] = struct{}{}
	}
	// The same two floors as /api/track-record: observations on one day share
	// one market move, so many symbols over a few days cannot ungate this.
	distinctDays := len(dayset)
	gated := len(pairs) < trackMinIndependentN || distinctDays < trackMinDistinctDays
	// 2026-07-17 inspection: the system HAS accrued a live prequential record
	// (every point's prob was frozen at prediction time and graded forward).
	// Once the independent sample clears the gate, "backtested" stops being
	// the honest label — the live record is, whatever it says.
	liveN, liveWin, err := d.St.LiveDirectionalRecord(r.Context(), h)
	if err != nil {
		httpInternal(w, err)
		return
	}
	live := liveN >= minIndependentN
	trackLabel := "backtested / in-sample — not a live track record"
	var liveWinRate any = liveWin
	if gated {
		// liveN is len(pairs) (one population), so a gated record publishes no
		// win rate anywhere in this payload, label included.
		liveWinRate = nil
	}
	if live && gated {
		trackLabel = "LIVE prequential record, figures withheld: " + independenceGateNote(len(pairs), distinctDays)
	} else if live {
		trackLabel = fmt.Sprintf("LIVE prequential record: win rate %.1f%% over %d independent symbol-days — probabilities were frozen at prediction time and graded forward; a bad number here is the honest product, not a display bug", liveWin*100, liveN)
	}
	// A bare Brier score is not interpretable and must never ship alone. The
	// live 1d record scores 0.302, which reads as "small error" until the 56.0%
	// base rate puts the constant forecast at 0.246 — the model is 23% WORSE
	// than always forecasting the base rate (skill -0.226). /api/trackrecord
	// already computed skill correctly, so publishing only the raw Brier here
	// read as selective (2026-07-26 review, C3). Both numbers, same payload.
	//
	// The graded variable is the PUBLISHED probability (prediction_outcomes.prob
	// = cal_prob frozen at prediction time), which is what this endpoint is for:
	// it answers "are our 70% calls actually 70%?" about the number users see.
	skill, baseRate, gradable := ensemble.BrierSkill(pairs)
	out := map[string]any{
		"horizon": h,
		// Independent (symbol, settled trading day) observations, not rows.
		"n":               len(pairs),
		"distinctDays":    distinctDays,
		"minIndependentN": trackMinIndependentN,
		"minDistinctDays": trackMinDistinctDays,
		"gated":           gated,
		"bins":            ensemble.CalibrationCurve(pairs, 10),
		"brier":           ensemble.BrierScore(pairs),
		"reliability":     ensemble.ReliabilityScore(pairs),
		"live":            live,
		"liveRecord":      map[string]any{"independentN": liveN, "winRate": liveWinRate},
		"trackLabel":      trackLabel,
		// C-2 (2026-08-02 re-audit): /api/track-record publishes the same
		// record at a different scope and therefore different numbers. Naming
		// that here is what stops the pair reading as a contradiction.
		"scopeNote": calibrationScopeNote,
		// C-1 (same re-audit): a bin of N=2 shipped in the same array, same
		// shape, as a bin of N=3,469 — "MeanActual 0.5 on two samples" reads as
		// evidence. This is NOT a MinCalibrationPairs breach: that gate governs
		// whether the map is fit at all and is holding. It is a reporting gap —
		// every bin carries its N, but nothing said what N is too thin to read,
		// so each consumer had to invent a threshold or ignore the problem.
		"binMinN": calibrationBinMinN,
		// The ~9pp figure is a property of the constant, derived once in
		// calibrationBinMinN's doc comment — not recomputed here. Computing it
		// per request would hand-roll a binomial kernel, which
		// TestExactlyOneWilsonImplementationInTree correctly refuses: this tree
		// keeps exactly one Wilson implementation, in clusterstat.
		"binNote": fmt.Sprintf(
			"each bin reports its own N: bins below %d resolved pairs are NOT comparable evidence and must not be "+
				"read as calibration deviations. At n=%d the binomial standard error on a proportion is already "+
				"~9 percentage points, which is wider than the miscalibration these bins exist to show.",
			calibrationBinMinN, calibrationBinMinN),
	}
	switch {
	case gated:
		// Below either floor every headline figure is withheld, as on
		// /api/track-record; the bins stay, each carrying its own N.
		out["brier"], out["reliability"] = nil, nil
		out["brierSkill"], out["baseRate"], out["brierRef"] = nil, nil, nil
		out["brierNote"] = independenceGateNote(len(pairs), distinctDays)
	case gradable:
		out["brierSkill"] = skill
		out["baseRate"] = baseRate
		out["brierRef"] = baseRate * (1 - baseRate)
		out["brierNote"] = fmt.Sprintf(
			"brier %.4f against the constant base-rate forecast's %.4f (base rate %.1f%%) → skill %+.3f: %s",
			ensemble.BrierScore(pairs), baseRate*(1-baseRate), baseRate*100, skill,
			map[bool]string{
				true:  "better than forecasting the base rate every day",
				false: "WORSE than forecasting the base rate every day — no measured probabilistic skill",
			}[skill > 0])
	default:
		// Ungradable, with the reason. null, never 0 — a zero skill score is a real
		// verdict ("exactly as good as the base rate") and must not be faked.
		out["brierSkill"] = nil
		out["baseRate"] = nil
		out["brierRef"] = nil
		out["brierNote"] = "brier skill not gradable: no resolved history, or every outcome resolved the same way (the base-rate reference has zero variance)"
	}
	// SD-30: every number above is scored against a label mostly realised at
	// issue. Sample sizes stay; the win rate, Brier, skill and bins do not.
	if why, ok := publication.DirectionalWithheld(string(h)); ok {
		out["liveRecord"] = map[string]any{"independentN": liveN, "winRate": nil}
		out["trackLabel"] = why
		out["bins"] = []any{}
		out["brier"], out["reliability"] = nil, nil
		out["brierSkill"], out["baseRate"], out["brierRef"] = nil, nil, nil
		out["brierNote"] = why
		out["withheld"] = why
	}
	writeJSON(w, out)
}

// regimes returns the current regime per symbol + recent regime changes.
func (d Deps) regimes(w http.ResponseWriter, r *http.Request) {
	states, err := d.St.Regimes(r.Context())
	if err != nil {
		httpInternal(w, err)
		return
	}
	changes, err := d.St.RecentRegimeChanges(r.Context(), 50)
	if err != nil {
		httpInternal(w, err)
		return
	}
	writeJSON(w, map[string]any{"states": states, "changes": changes})
}

// ranking returns the latest cross-sectional relative-strength ranking.
func (d Deps) ranking(w http.ResponseWriter, r *http.Request) {
	rows, err := d.St.LatestRanking(r.Context())
	if err != nil {
		httpInternal(w, err)
		return
	}
	writeJSON(w, rows)
}

// breakouts returns recent detected breakout/squeeze/correlation-break events.
func (d Deps) breakouts(w http.ResponseWriter, r *http.Request) {
	rows, err := d.St.RecentBreakouts(r.Context(), 80)
	if err != nil {
		httpInternal(w, err)
		return
	}
	writeJSON(w, rows)
}

// registerPredict wires the prediction + trends routes.
func (d Deps) registerPredict(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/predictions", d.predictions)
	mux.HandleFunc("GET /api/calibration", func(w http.ResponseWriter, r *http.Request) {
		// Perf wave 2026-07-24: measured 22.6s per request; SWR-cached.
		// C6: keyed on the WHITELISTED horizon, never on r.URL.RawQuery — a raw
		// query string is attacker-controlled, and every novel one was a cold
		// build holding one of the store's four read connections for ~22s.
		sharedCalibrationSWR.serve(d.St.CacheKey()+"|"+calibrationCacheKey(r), w, r, d.calibration)
	})
	mux.HandleFunc("GET /api/regime", d.regimes)
	mux.HandleFunc("GET /api/ranking", d.ranking)
	mux.HandleFunc("GET /api/breakouts", d.breakouts)
}
