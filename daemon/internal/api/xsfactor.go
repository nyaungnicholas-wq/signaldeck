// CROSS-SECTIONAL FACTOR READ (2026-07-24) — a pure READ-TIME endpoint:
//
//	GET /api/xs-factor?horizon=21d&limit=50[&market=stocks|crypto|all]
//
// WHY THIS EXISTS: absolute direction fails at every horizon, while the
// CROSS-SECTIONAL question — "will this symbol beat the same-day universe
// MEDIAN forward return?" — carries a small but measurable edge. The app had no
// surface for it. This is that surface.
//
// 2026-07-26: the per-leg edges are now re-derived by tools/xsfactor_edge.py,
// committed, and asserted against its output in the tests. That re-derivation
// RETRACTED the size/liquidity leg at every horizon (it measures negative, not
// the published +1.46/+2.50/+3.04pp) and low-volatility at 63d. The edge does
// NOT grow with horizon, as the old copy here claimed; 63d has no surviving leg
// at all and the endpoint gates it with a stated reason.
//
// DELIBERATELY NO NEW STATE: no table, no worker, no migration. Every number is
// recomputed at read time from the daily bars already stored (one batched
// window read, not N+1), then served through the shared stale-while-revalidate
// body cache — the payload is identical for every user, so it is a textbook fit
// for that cache and a visitor never waits behind a rebuild.
//
// HONESTY (shipped verbatim in every payload): xsfactor.Caveat — relative rank
// only; what survives is the PUBLIC low-volatility anomaly (5d/21d) and
// momentum-12-1 (5d), capacity-constrained, at a measured 51.1-52.0% accuracy
// against a 50% base rate. The edge block ships as data, exactly as re-derived,
// with no invented rows — and the legs that FAILED ship beside it in "withheld"
// with their measured values and their reasons, so a reader can audit the
// retraction rather than take it on trust.
package api

import (
	"fmt"
	"net/http"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
	"github.com/nyaungnicholas-wq/signaldeck/internal/xsfactor"
)

const (
	// xsFactorDefaultLimit / xsFactorMaxLimit bound the rendered rows. Ranking
	// always runs over the FULL universe first — a limited read must never
	// change anyone's rank or anyone's percentile.
	xsFactorDefaultLimit = 50
	xsFactorMaxLimit     = 500

	// xsFactorMinUniverse is the cross-section floor. A percentile is a
	// statement about a peer group; over a handful of symbols it is noise, not
	// a rank, so below this the endpoint reports its gate instead of rows.
	xsFactorMinUniverse = 20

	// xsFactorMaxSkipped caps the reported skip list so one bad ingest cannot
	// balloon the payload; the count is always complete.
	xsFactorMaxSkipped = 100

	// xsFactorGateThinUniverse is the stated gate reason.
	xsFactorGateThinUniverse = "cross-section too thin — a percentile over this few symbols is noise, not a rank; the re-derived edge was estimated across a ~1,000-name daily cross-section"
	// xsFactorSplitNote explains the artifact rejections.
	xsFactorSplitNote = "symbols whose trailing series contains a |daily return| above 0.65 are REFUSED, not smoothed: this repo's bars carry known uncorrected-split artifacts that would corrupt both the volatility and the momentum leg"
	// xsFactorUniverseNote states what the cross-section actually is, and names
	// the gap between it and the universe the edge was measured on — the live
	// rank is over SURVIVORS, the measurement deliberately was not.
	xsFactorUniverseNote = "percentiles are relative to the ACTIVE tracked symbols in this request only (default market=stocks, since the edge was measured on US stocks) — not to the whole market. The edge itself was re-derived on the SURVIVORSHIP-CLEAN universe of 1,059 stocks including 739 delisted names, which this live cross-section does not contain; on the active-only universe the same script measures every leg as indistinguishable from zero, and that gap is a real limit on what a live rank here can be expected to deliver"
)

// sharedXSFactorSWR fronts GET /api/xs-factor. The read costs one batched
// window query over ~300 trailing daily bars per active symbol plus a pure
// ranking pass; the payload is user-independent and changes only when the daily
// bars change, so a 5m TTL is comfortably fresher than the daily-bar cadence.
var sharedXSFactorSWR = newSWRBodyCache(5 * time.Minute)

// xsFactorHorizon resolves ?horizon= to a MEASURED horizon, defaulting to 21d.
// An unmeasured value falls back to the default rather than inventing an edge
// block for a horizon nobody tested.
func xsFactorHorizon(r *http.Request) xsfactor.Horizon {
	if h, ok := xsfactor.ParseHorizon(r.URL.Query().Get("horizon")); ok {
		return h
	}
	return xsfactor.H21d
}

// xsFactorMarket resolves ?market= to the cross-section to rank. Default
// stocks: the edge was measured on US stocks, and pooling crypto's volatility
// into the same low-vol percentile would make the rank incomparable to the
// measurement. "all" is available for the curious, labeled as such.
func xsFactorMarket(r *http.Request) string {
	switch m := r.URL.Query().Get("market"); m {
	case string(md.Crypto), "all":
		return m
	default:
		return string(md.Stocks)
	}
}

// xsFactorCacheKey scopes the cache entry to the resolved query AND to the
// store instance. The store pointer matters: this cache is package-level, so
// without it a test with its own isolated store would be served another test's
// body (the predictions cache keys the same way).
func xsFactorCacheKey(st *store.Store, r *http.Request) string {
	return fmt.Sprintf("%s|%s|%s|%d", st.CacheKey(), xsFactorHorizon(r), xsFactorMarket(r),
		limitParam(r, xsFactorDefaultLimit, xsFactorMaxLimit))
}

// xsFactorWarmKey is the key WarmCaches' default-query pre-build lands on — it
// must match what the route computes for a bare GET, or the warmer would fill an
// entry no visitor reads.
func xsFactorWarmKey(st *store.Store) string {
	req, err := http.NewRequest(http.MethodGet, "/api/xs-factor", nil)
	if err != nil {
		return fmt.Sprintf("%s|%s|%s|%d", st.CacheKey(), xsfactor.H21d, md.Stocks, xsFactorDefaultLimit)
	}
	return xsFactorCacheKey(st, req)
}

// xsFactor serves the cross-sectional factor ranking.
// GET /api/xs-factor?horizon=5d|21d|63d&limit=&market=
func (d Deps) xsFactor(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	horizon := xsFactorHorizon(r)
	market := xsFactorMarket(r)
	limit := limitParam(r, xsFactorDefaultLimit, xsFactorMaxLimit)

	syms, err := d.St.ListSymbols(ctx, true) // ACTIVE tracked universe
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	ids := make([]int64, 0, len(syms))
	byID := make(map[int64]md.Symbol, len(syms))
	for _, s := range syms {
		if market != "all" && string(s.Market) != market {
			continue
		}
		ids = append(ids, s.ID)
		byID[s.ID] = s
	}

	// ONE batched window read for the whole universe (LastBarsBatch), never a
	// per-symbol query — the screener learned that lesson at ~1,300 round-trips.
	bars, err := d.St.LastBarsBatch(ctx, ids, md.TF1d, xsfactor.TrailingBars)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}

	var asOfTs int64
	inputs := make([]xsfactor.Input, 0, len(ids))
	for _, id := range ids { // ids order = ListSymbols order ⇒ deterministic
		s := byID[id]
		bs := bars[id]
		in := xsfactor.Input{
			Symbol:     s.Symbol,
			Market:     string(s.Market),
			Closes:     make([]float64, len(bs)),
			DollarVols: make([]float64, len(bs)),
		}
		for i, b := range bs { // LastBarsBatch returns ascending ts per symbol
			in.Closes[i] = b.Close
			in.DollarVols[i] = b.Close * b.Volume // dollar volume proxy
			if b.Ts > asOfTs {
				asOfTs = b.Ts
			}
		}
		inputs = append(inputs, in)
	}

	res, err := xsfactor.Rank(horizon, inputs)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}

	out := map[string]any{
		"horizon":           string(res.Horizon),
		"horizonsAvailable": xsfactor.Horizons,
		"market":            market,
		"asOfTs":            asOfTs,
		"universeN":         res.UniverseN,
		"symbolsConsidered": len(ids),
		"trailingBars":      xsfactor.TrailingBars,
		"compositeLegs":     res.CompositeLegs,
		"edge":              res.Edge,
		"withheld":          res.Withheld,
		"edgeNote":          xsfactor.EvidenceNote,
		"retractionNote":    xsfactor.RetractionNote,
		"derivationScript":  xsfactor.DerivationScript,
		"derivationOutput":  "daemon/internal/xsfactor/" + xsfactor.DerivationJSON,
		"methodNote":        xsfactor.MethodNote,
		"universeNote":      xsFactorUniverseNote,
		"splitRejected":     res.SplitRejected,
		"splitNote":         xsFactorSplitNote,
		"skippedTotal":      len(res.Skipped),
		"skipped":           res.Skipped[:min(len(res.Skipped), xsFactorMaxSkipped)],
		"caveat":            xsfactor.Caveat,
	}

	// Gate: a horizon whose every leg was retracted or withheld has nothing to
	// rank BY. Composite would be an average over zero published legs, and the
	// house rule is that a gated metric is withheld with a reason, never
	// rendered as 0. This fires at 63d, where the re-derivation left no
	// surviving leg. The per-leg failures still ship in "withheld".
	if len(res.CompositeLegs) == 0 {
		out["gated"] = true
		out["gateReason"] = xsfactor.GateNoMeasuredLeg
		out["rows"] = []xsfactor.Row{}
		out["n"] = 0
		out["total"] = 0
		writeJSON(w, out)
		return
	}

	// Gate: below the cross-section floor a percentile means nothing, so the
	// endpoint shows its gate and its reason instead of a rank — same contract
	// as the other measured surfaces (/api/alphax, /api/composite).
	if res.UniverseN < xsFactorMinUniverse {
		out["gated"] = true
		out["gateReason"] = xsFactorGateThinUniverse
		out["minUniverse"] = xsFactorMinUniverse
		out["rows"] = []xsfactor.Row{}
		out["n"] = 0
		out["total"] = len(res.Rows)
		writeJSON(w, out)
		return
	}

	// Rank over the FULL cross-section, THEN truncate — the limit is a display
	// bound, never an input to anyone's rank or percentile.
	rows := res.Rows
	if len(rows) > limit {
		rows = rows[:limit]
	}
	out["gated"] = false
	out["rows"] = rows
	out["n"] = len(rows)
	out["total"] = len(res.Rows)
	writeJSON(w, out)
}

// registerXSFactor wires the cross-sectional factor read behind the shared SWR
// body cache (user-independent payload; a visitor gets the stale copy instantly
// while ONE background rebuild runs).
func (d Deps) registerXSFactor(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/xs-factor", func(w http.ResponseWriter, r *http.Request) {
		sharedXSFactorSWR.serve(xsFactorCacheKey(d.St, r), w, r, d.xsFactor)
	})
}
