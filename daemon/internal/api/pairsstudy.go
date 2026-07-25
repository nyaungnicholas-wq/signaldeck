// GET /api/pairs-study — the cointegration pairs-trading test that resolved
// research hypothesis H018 (CORR63) DO-NOT-SHIP.
//
// This route exists to publish a NEGATIVE result, which is the opposite of what
// a product surface usually does. It is here because the failure mode it
// documents is the one this platform is most exposed to: H018 was a statistic
// with a good point estimate, a tight out-of-sample interval, and no tradable
// content whatsoever. Nothing about "collect more quarters" would have exposed
// that. Building the position that would have to earn the money did, in one run.
//
// The payload is frozen — an offline walk-forward backtest, not a worker — so it
// ships the tool's own numbers verbatim alongside the method choices that decide
// whether a pairs backtest is honest at all, and the limitations that survive
// the verdict.
package api

import (
	"net/http"

	"github.com/nyaungnicholas-wq/signaldeck/internal/pairsstudy"
)

func (d Deps) pairsStudy(w http.ResponseWriter, r *http.Request) {
	study, err := pairsstudy.Load()
	if err != nil {
		httpErr(w, http.StatusInternalServerError, "pairs-study: "+err.Error())
		return
	}

	writeJSON(w, map[string]any{
		"study":  study,
		"frozen": true,
		"whyFrozen": "This is a completed offline study (tools/pairs_trading.py, roughly 45 minutes over " +
			"918 symbols), not a worker on a cadence. The numbers are the ones the run emitted on " +
			pairsstudy.RanOn + " and they do not move until someone re-runs it.",
		"headlineMetric": "selectionEdge",
		"readThisFirst": "Read the three arms side by side before reading any single mean return. The " +
			"cointegration-selected arm, random same-sector pairs, and the LEAST cointegrated pairs are " +
			"traded on identical rules, and they are indistinguishable — the least cointegrated arm has " +
			"the highest point estimate of the three. Whatever return is present is generic sector mean " +
			"reversion available by picking pairs at random, and its interval contains zero at ZERO cost, " +
			"before a cent of friction.",
		"reproduce":    "python3 tools/pairs_trading.py --json scratchpad/pairs_results.json",
		"writeup":      "PAIRS_TRADING.md",
		"ledgerTag":    "2026-07-25 pairs-test",
		"survivorship": survivorshipBlock(),
	})
}

func (d Deps) registerPairsStudy(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/pairs-study", d.pairsStudy)
}
