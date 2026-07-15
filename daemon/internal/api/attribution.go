// Blended-evidence attribution + calibration report (attribution-engine wave).
// GET /api/attribution?symbol=&market=&horizon=1d|1w — read-only, gated like
// every other read.
//
// It fuses two sources the doctrine forbids confusing: the HISTORICAL PRIOR
// (regime/state-conditioned expectancy over ~2y of bars) and LIVE resolved
// outcomes (the deployed engine's own predictions for THIS symbol+horizon,
// deduped to one independent obs per UTC day). internal/attribution does the
// sample-size-weighted blend and writes the honest explanation; this handler
// only loads the evidence. It never lets thin live volume read as "no edge".
package api

import (
	"net/http"

	"github.com/nyaungnicholas-wq/signaldeck/internal/attribution"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

func (d Deps) attribution(w http.ResponseWriter, r *http.Request) {
	s, err := d.symbolFromQuery(r)
	if err != nil {
		httpErr(w, 404, err.Error())
		return
	}
	ctx := r.Context()
	h := md.Horizon(r.URL.Query().Get("horizon"))
	if h != md.H1d && h != md.H1w {
		h = md.H1d
	}

	// ── HISTORICAL PRIOR: the expectancy table, matched to TODAY's state ──
	// The matched-state row is the best analog ("when this symbol looked like it
	// does now, forward returns were up X% of N times"). Absent a match, an
	// N-weighted symbol-level base rate is the weaker fallback prior.
	var prior attribution.Evidence
	matchedState := false
	expRows, _ := d.St.Expectancy(ctx, s.ID, h)
	currentState := ""
	if d.CurrentState != nil {
		if states, cerr := d.CurrentState(ctx, s.ID); cerr == nil {
			currentState = states[h]
		}
	}
	for _, e := range expRows {
		if currentState != "" && e.StateKey == currentState {
			prior = attribution.Evidence{HitRate: e.HitRate, N: e.N}
			matchedState = true
			break
		}
	}
	if !matchedState {
		var wSum, nSum float64
		for _, e := range expRows {
			wSum += e.HitRate * float64(e.N)
			nSum += float64(e.N)
		}
		if nSum > 0 {
			prior = attribution.Evidence{HitRate: wSum / nSum, N: int(nSum)}
		}
	}

	// ── LIVE EVIDENCE: this symbol's resolved outcomes, one obs per UTC day ──
	// Directional accuracy = (predUp == actualUp), NOT the up-rate (the base-rate
	// bug we fixed fleet-wide). Per-symbol live N is almost always thin — which
	// is exactly why the prior must carry the confidence until it grows.
	outcomes, _ := d.St.ResolvedPredictionOutcomes(ctx, h, 120000)
	seenDay := map[int64]bool{}
	correct, liveN := 0, 0
	for _, o := range outcomes {
		if o.SymbolID != s.ID {
			continue
		}
		day := o.Ts / 86400
		if seenDay[day] {
			continue
		}
		seenDay[day] = true
		liveN++
		if (o.Prob >= 0.5) == (o.Up == 1) {
			correct++
		}
	}
	live := attribution.Evidence{N: liveN}
	if liveN > 0 {
		live.HitRate = float64(correct) / float64(liveN)
	}

	// ── regime context ──
	regime := ""
	if labels, rerr := d.St.RegimeLabels(ctx); rerr == nil {
		regime = labels[s.ID]
	}

	rep := attribution.Assess(s.Symbol, string(h), regime, prior, live, matchedState)
	writeJSON(w, map[string]any{
		"available":    true,
		"market":       s.Market,
		"currentState": currentState,
		"report":       rep,
		"doctrine":     "Blended evidence: a regime/state-conditioned HISTORICAL prior (~2y expectancy) plus LIVE resolved outcomes, kept strictly separate and weighted by sample size. Live weight = liveN/(priorEff+liveN). Underpowered live attribution is reported as such — it is NOT a measured lack of edge.",
	})
}

func (d Deps) registerAttribution(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/attribution", d.attribution)
}
