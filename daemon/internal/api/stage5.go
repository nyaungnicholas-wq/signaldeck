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
	"context"
	"fmt"
	"net/http"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/pipeline"
	"github.com/nyaungnicholas-wq/signaldeck/internal/symbolagent"
)

// predictionsLatestCached serves GET /api/predictions/latest?horizon=1d|1w
// through the SWR cache — the only build path; tests exercise it too. Perf
// wave 2026-07-24: the latest-per-symbol self-join over the 240k-row
// predictions table costs ~45s per request in the pure-Go driver (measured);
// the payload is identical for every user and the prediction runner writes on
// a 10m cadence, so the 2m TTL bounds staleness by design.
func (d Deps) predictionsLatestCached(w http.ResponseWriter, r *http.Request) {
	h := md.Horizon(r.URL.Query().Get("horizon"))
	if h != md.H1d && h != md.H1w {
		h = md.H1d
	}
	// Cache key includes the store identity: production has exactly one store
	// so behavior is unchanged, while each test's isolated store gets its own
	// entry instead of inheriting another test's cached payload.
	resp, err := sharedPredictionsCache.get(r.Context(), fmt.Sprintf("%s|%s", d.St.CacheKey(), h),
		func(ctx context.Context) (map[string]any, error) {
			return d.buildPredictionsLatest(ctx, h)
		})
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	writeJSON(w, resp)
}

// buildPredictionsLatest computes the SIGNALS-hub predictions payload for one
// horizon. Pure build — no HTTP — so the cache can rebuild it off-request.
func (d Deps) buildPredictionsLatest(ctx context.Context, h md.Horizon) (map[string]any, error) {
	rows, err := d.St.LatestPredictionsAll(ctx, h)
	if err != nil {
		return nil, err
	}
	resolvedN, err := d.St.ResolvedPredictionCount(ctx, h)
	if err != nil {
		return nil, err
	}
	gated := resolvedN < minIndependentN
	caption := confNote
	if gated {
		caption = fmtGateCaption(resolvedN)
	}
	// The live forward verdict (2026-07-17 inspection): once enough
	// independent symbol-days have resolved, "backtested" is no longer the
	// honest label — the LIVE record is, whatever it says.
	liveN, liveWin, err := d.St.LiveDirectionalRecord(ctx, h)
	if err != nil {
		return nil, err
	}
	trackLabel := "backtested / in-sample — not a live track record"
	if liveN >= minIndependentN {
		verdict := "see /track-record before trusting any P(up)"
		if liveWin < 0.55 {
			verdict = "no demonstrated directional edge — treat P(up) as experimental; the validated surfaces are the regime forecasts"
		}
		trackLabel = fmt.Sprintf("LIVE forward record: win rate %.1f%% over %d independent symbol-days — %s", liveWin*100, liveN, verdict)
	}

	// Model-health gate (2026-07-24). A model the live record has condemned
	// must stop presenting itself as a forecast — the failure this closes is
	// exactly that the directional ensemble kept emitting through 18,762
	// observations of negative skill because nothing could switch it off.
	// Rows are still returned (retiring the model must not blank the page or
	// hide the evidence), but the payload declares it retired and non-emitting
	// so no consumer can read a P(up) as actionable.
	emitting, hVerdict := pipeline.ModelEmitting(ctx, d.St, "directional-ensemble-"+string(h))
	if !emitting {
		gated = true
		caption = "MODEL RETIRED — automatically disabled by the model-health gate; " +
			"these probabilities are shown for audit only and must not be traded"
		trackLabel = fmt.Sprintf(
			"RETIRED (%s): the live record does not support this model — %s", hVerdict, trackLabel)
	}
	return map[string]any{
		"horizon":      h,
		"rows":         rows,
		"n":            len(rows),
		"resolvedN":    resolvedN,
		"minResolvedN": minIndependentN,
		"gated":        gated,
		"caption":      caption,
		"live":         liveN >= minIndependentN,
		"modelEmitting": emitting,
		"modelVerdict":  hVerdict,
		"liveRecord":   map[string]any{"independentN": liveN, "winRate": liveWin},
		"trackLabel":   trackLabel,
		// Stage 2 (verdict cards): each row's tier/nSamples measures against
		// this personal-model graduation gate ("still learning 12/40 …").
		"tierThreshold": symbolagent.MinPersonal,
		"note": "Latest calibrated ensemble prediction per active symbol, strongest conviction first. " +
			"Symbols without a stored prediction are absent (the predictor fills them in on worker cadence) — never fabricated.",
	}, nil
}

// registerStage5 wires the visual-hub batch routes.
func (d Deps) registerStage5(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/predictions/latest", d.predictionsLatestCached)
}
