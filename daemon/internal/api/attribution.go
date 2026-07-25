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
//
// The ledger walk is cached fleet-wide per horizon (see below) — the endpoint
// used to cost ~29s per call and is now a warm-cache read.
package api

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/attribution"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// ── the live-evidence cache (perf wave 2026-07-24) ───────────────────────────
//
// MEASURED 28.7s per call, which forced the symbol page to hide this behind an
// opt-in "≈1 min scan" button. The dominant cost was one line: the handler
// pulled the entire resolved ledger (120k rows) for the horizon and then threw
// away every row that wasn't this symbol.
//
// Two changes here:
//  1. What is cached is the FLEET-WIDE grade, not a per-symbol response: two
//     entries (1d, 1w) carry every symbol at once. That is why there is no
//     per-symbol map here to bound — a per-symbol response cache would have
//     needed an eviction policy AND would still have missed on every symbol
//     nobody had visited yet. WarmCaches builds both entries FIRST in its pass
//     for the same reason: this is now a load-on-mount endpoint.
//  2. The grade itself moved into one windowed SQL aggregate
//     (DirectionalAccuracyBySymbol) — 0.8-1.1s where the Go loop over the raw
//     ledger took 2.4s. Worth doing because at a 2m TTL every background rebuild
//     is a ledger walk contending with the workers.
//
// Counter-intuitive things that were NOT the problem, recorded so nobody
// re-derives them: the driver's per-row Scan (120k rows scan in ~1.2s — the
// aggregate wins by doing one partitioned pass, not by returning fewer rows),
// and the small per-request queries (Expectancy 0-2ms, RegimeLabels 1-3ms).
// The one that WAS still expensive after all of the above is CurrentState —
// see sharedCurrentStateCache below.
//
// TTL 2m matches sharedTrackCache: the outcome resolver writes on a 10m
// cadence, so staleness is bounded well inside one resolver pass.
var sharedAttributionLiveCache = newSWRCache(2 * time.Minute)

// attributionLive is the per-symbol live evidence for ONE horizon, keyed by
// symbol id. It rides inside the swrCache's map[string]any payload under
// attributionLiveField — that payload is an internal carrier here, not a JSON
// response body.
type attributionLive map[int64]attribution.Evidence

const attributionLiveField = "byID"

// attributionCacheKey scopes the entry to the horizon AND to the store
// instance. The store pointer matters: this cache is package-level, so without
// it a test with its own isolated store would be served another test's
// aggregation (the predictions and xs-factor caches key the same way).
func attributionCacheKey(st *store.Store, h md.Horizon) string {
	return fmt.Sprintf("%p|%s", st, h)
}

// buildAttributionLive grades the resolved ledger for one horizon and returns
// every symbol's live evidence.
//
// The grade itself is done in SQL (see DirectionalAccuracyBySymbol): directional
// accuracy — predUp == actualUp, NOT the up-rate (the base-rate bug we fixed
// fleet-wide) — over at most one observation per UTC day, so a symbol predicted
// many times in one session can't inflate its own N.
func (d Deps) buildAttributionLive(ctx context.Context, h md.Horizon) (map[string]any, error) {
	grades, err := d.St.DirectionalAccuracyBySymbol(ctx, h)
	if err != nil {
		return nil, err
	}
	byID := attributionLive{}
	for id, g := range grades {
		if g.N <= 0 {
			continue
		}
		byID[id] = attribution.Evidence{HitRate: float64(g.Correct) / float64(g.N), N: g.N}
	}
	return map[string]any{attributionLiveField: byID}, nil
}

// ── the current-state cache ──────────────────────────────────────────────────
//
// The OTHER slow thing in this handler, found only after the ledger walk was
// cached and attribution was STILL 1.4-8.7s where /api/explain was 0.06-0.19s
// under identical interleaved load: d.CurrentState recomputes the expectancy
// state keys from scratch, and that means loading up to 25,000 minute bars plus
// 500 daily bars for the symbol (pipeline.loadBars) on every request. The keys
// are not persisted anywhere — all three call sites recompute them — so there is
// no cheap lookup to swap in, only a cache.
//
// Per-symbol here, unlike the ledger grade, because the work genuinely is
// per-symbol. The map is bounded by construction rather than by an eviction
// policy: symbolFromQuery resolves against the symbols table and 404s before we
// ever reach this, so the key space is the ~1k tracked symbols, and each entry
// holds a handful of short strings.
//
// This does NOT fix /api/symbol, which pays the same cost through the same
// helper and is the reason a symbol page's mount was already seconds-slow. That
// is a wider change than this endpoint's, and is left alone deliberately.
var sharedCurrentStateCache = newSWRCache(2 * time.Minute)

const currentStateField = "states"

// currentStateFor returns the live expectancy state keys for one symbol, cached.
// A miss or a build failure yields "" — the same unmatched-state fallback the
// handler already took when CurrentState errored, which downgrades the prior to
// the N-weighted base rate rather than inventing a match.
func (d Deps) currentStateFor(ctx context.Context, symbolID int64) map[md.Horizon]string {
	if d.CurrentState == nil {
		return nil
	}
	payload, err := sharedCurrentStateCache.get(ctx, fmt.Sprintf("%p|%d", d.St, symbolID),
		func(c context.Context) (map[string]any, error) {
			states, serr := d.CurrentState(c, symbolID)
			if serr != nil {
				return nil, serr
			}
			return map[string]any{currentStateField: states}, nil
		})
	if err != nil {
		return nil
	}
	states, _ := payload[currentStateField].(map[md.Horizon]string)
	return states
}

// attributionLiveFor returns one symbol's live evidence from the shared cache.
// A build failure yields zero evidence — exactly what the uncached handler did
// when the ledger read failed, and attribution.Assess already reports N=0 as
// underpowered rather than as a measured absence of edge.
func (d Deps) attributionLiveFor(ctx context.Context, h md.Horizon, symbolID int64) attribution.Evidence {
	payload, err := sharedAttributionLiveCache.get(ctx, attributionCacheKey(d.St, h),
		func(c context.Context) (map[string]any, error) {
			return d.buildAttributionLive(c, h)
		})
	if err != nil {
		return attribution.Evidence{}
	}
	byID, _ := payload[attributionLiveField].(attributionLive)
	return byID[symbolID]
}

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
	currentState := d.currentStateFor(ctx, s.ID)[h]
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
	// Read out of the fleet-wide cache above — per-symbol live N is almost always
	// thin, which is exactly why the prior must carry the confidence until it
	// grows.
	live := d.attributionLiveFor(ctx, h, s.ID)

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
