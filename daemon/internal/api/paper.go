package api

import (
	"context"
	"net/http"
	"slices"
	"strconv"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/moneymetrics"
	"github.com/nyaungnicholas-wq/signaldeck/internal/papertrade"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// paperMoneyCaption ships VERBATIM on the paper/track-record money scoreboard —
// the one honesty line that reframes the whole page: profit, not win rate.
const paperMoneyCaption = "Win rate alone does not equal profit — a high win rate with large losers still loses money. Expectancy (avg profit per trade after costs) is what matters."

// ── STAGE 4: INTERNAL SIMULATED paper-trading read route ─────────────────────
//
// GET /api/paper?strategy=flagship-1d returns the simulated book's equity curve,
// open positions, recent trades, and a costed summary (return, Sharpe when
// valid, maxDD, win-rate when enough closed trades, turnover). It is a READ:
// public under SIGNALDECK_PUBLIC_READS like the other market-data reads. The
// book is a SIMULATION driven by the platform's own predictions — no broker, no
// real money — and the payload carries live:false + a label so the UI cannot
// misrepresent it.

// sharedPaperSWR body-caches the flagship books (never the manual one).
var sharedPaperSWR = newSWRBodyCache(2 * time.Minute)

// defaultPaperStrategy is what the UI lands on when no ?strategy= is given.
const defaultPaperStrategy = "flagship-1d"

// paperStrategies mirrors the worker's simulated portfolios (kept here so the
// API can advertise the choices without importing the pipeline package).
var paperStrategies = []string{"flagship-1d", "flagship-1w"}

func (d Deps) paper(w http.ResponseWriter, r *http.Request) {
	strategy := r.URL.Query().Get("strategy")
	if strategy == "" {
		strategy = defaultPaperStrategy
	}
	manual := strategy == "manual" // the caller's own manual book (paperorder.go); never another user's
	if manual {
		if userID(r) == 0 {
			httpErr(w, 401, "sign in to view your manual paper book")
			return
		}
		strategy = manualPaperStrategy(userID(r))
	} else if !slices.Contains(paperStrategies, strategy) { // live books only; replay books are research artifacts
		httpErr(w, 404, "unknown paper strategy "+strconv.Quote(strategy)+"; choose flagship-1d or flagship-1w")
		return
	}
	tradeLimit := 100
	if q := r.URL.Query().Get("trades"); q != "" {
		if n, err := strconv.Atoi(q); err == nil && n > 0 && n <= 1000 {
			tradeLimit = n
		}
	}

	rawCurve, err := d.St.PaperEquityCurve(r.Context(), strategy, 5000)
	if err != nil {
		httpInternal(w, err)
		return
	}
	curve := make([]papertrade.EquityPoint, len(rawCurve))
	for i, p := range rawCurve {
		curve[i] = papertrade.EquityPoint{Ts: p.Ts, Cash: p.Cash, PositionsValue: p.PositionsValue, Equity: p.Equity}
	}

	positions, err := d.St.PaperPositions(r.Context(), strategy)
	if err != nil {
		httpInternal(w, err)
		return
	}
	recent, err := d.St.PaperTrades(r.Context(), strategy, tradeLimit)
	if err != nil {
		httpInternal(w, err)
		return
	}

	// A nil Go slice marshals to JSON `null`, NOT `[]`. A book with no open
	// positions and no closed trades — every fresh install, and any strategy
	// between round trips — therefore shipped {"positions":null,"trades":null},
	// while the web client's type declares them as arrays and calls
	// `data.positions.length`. That threw "Cannot read properties of null
	// (reading 'length')" and took the WHOLE /lab/paper page down to a blank
	// error boundary: measured 2026-08-14, the page rendered 70 characters.
	// Normalising at the JSON boundary fixes every consumer at once (web, MCP,
	// any API client) instead of asking each to guard a shape the API should
	// never have sent. "No rows" is an empty list, not the absence of a list.
	if positions == nil {
		positions = []store.PaperPosition{}
	}
	if recent == nil {
		recent = []store.PaperTrade{}
	}

	// Reconstruct closed round-trips + turnover from the FULL ordered trade log
	// (per symbol: a buy opens, the matching sell closes; won = sell net proceeds
	// exceed the buy net outlay). This is how win-rate + turnover stay honest.
	all, err := d.St.AllPaperTradesAsc(r.Context(), strategy)
	if err != nil {
		httpInternal(w, err)
		return
	}
	closed, numFills, tradedNotional := reconstructRoundTrips(all)
	summary := papertrade.Summarize(curve, closed, numFills, tradedNotional)

	// EPOCH SPLIT. `summary` and `money` above span the whole book, which is
	// correct as a description of the CAPITAL and wrong as a description of any
	// STRATEGY once the rules have changed. The segments recompute both inside
	// each epoch, and `epochCaption` tells a reader which number answers which
	// question rather than leaving them to assume the headline is the strategy's.
	segments, err := paperEpochSegments(r.Context(), d.St, strategy, curve, all)
	if err != nil {
		httpInternal(w, err)
		return
	}
	var currentEpoch *EpochSegment
	if n := len(segments); n > 0 {
		currentEpoch = &segments[n-1]
	}

	// THE INTEGRITY BOUNDARY IS NOT A CAPTION.
	//
	// `summary` and `money` were book-wide with a paragraph beside them saying so.
	// A paragraph beside a number does not stop the number being quoted: these
	// span 46 back-dated fills that booked up to 22 days of market move as one
	// step's P&L, so a total return, Sharpe, drawdown or win rate over them is
	// derived partly from moves that never happened. They are REFUSED when they
	// would cross, and the clean record is published in their place.
	//
	// The equity LEVEL series stays whole and is labelled as accounting, because
	// what the simulated account is worth is a real fact — it just is not
	// performance.
	epochRows, err := d.St.PaperEpochs(r.Context(), strategy)
	if err != nil {
		httpInternal(w, err)
		return
	}
	boundary := integrityBoundary(epochRows)
	boundaryLabel, boundaryReason := "", ""
	for _, epoch := range epochRows {
		if epoch.FromTs == boundary {
			boundaryLabel, boundaryReason = epoch.Label, epoch.Reason
		}
	}
	spans := SpansIntegrityBoundary(curve, boundary)
	// A wholly historical window is also ineligible for the corrected simulator.
	for _, mark := range curve {
		if boundary > 0 && mark.Ts < boundary {
			spans = true
			break
		}
	}
	clean := buildCleanPerformance(epochRows, curve, all)

	// FILL FIDELITY: re-derive every logged fill from the bar it names, on every
	// read. A verified-by-assumption trade log is how 21 of 44 fills sat in a
	// "track record" while differing from their own bars by up to 90.3 bps —
	// `bars` is written INSERT OR REPLACE, so a provider revision rewrites the
	// reference a past fill priced off and nothing notices. This is the check
	// that notices.
	fidelityTF := md.TF1d
	if manual {
		fidelityTF = md.TF1m // manual fills quote the newest 1m bar; auditable only inside 1m retention
	}
	fidelity := d.checkPaperFills(r.Context(), all, fidelityTF)

	// MONEY SCOREBOARD: score the closed round-trips by EXPECTED PROFIT
	// (expectancy / profit factor / payoff), the numbers that actually decide
	// whether the signal makes money. Returns are already NET of both-side costs.
	money := moneymetrics.FromReturns(roundTripReturns(all))

	// Basis audit for the stored entry prices this book compares against live
	// bars. Published as a measured zero rather than left as an assumption.
	staleBasis, err := d.St.StaleBasisPositions(r.Context())
	if err != nil {
		httpInternal(w, err)
		return
	}

	payload := map[string]any{
		"strategy":   strategy,
		"strategies": append(append([]string{}, paperStrategies...), "manual"),
		"manual":     manual, // the caller's own market-order book (POST /api/paper/order)
		// Honesty framing: this is a self-contained simulation, not a live account.
		"live":       false,
		"label":      "simulated paper trading — not live money, not advice",
		"startCash":  papertrade.StartingCash(),
		"longThresh": papertrade.LongThreshold(),
		"flatThresh": papertrade.FlatThreshold(),
		// ACCOUNTING level across all time, contaminated period included.
		"equity":             curve,
		"equityIsAccounting": true,
		"equityNote": "accounting equity of the simulated book across all time. It carries the pre-2026-07-22 " +
			"back-dated P&L and is NOT a performance series; use cleanPerformance.index for that.",
		"positions": positions,
		"trades":    recent,
		// Post-boundary record, rebased. This is the strategy's published number.
		"cleanPerformance": clean,
		"integrityBoundary": map[string]any{
			"ts":     boundary,
			"utc":    boundaryUTC(boundary),
			"spans":  spans,
			"label":  boundaryLabel,
			"reason": boundaryReason,
		},
		// Stored-vs-live price basis audit (paper_positions.avg_px against bars a
		// split repair may have rescaled).
		"priceBasisAudit": map[string]any{
			"staleBasisPositions": staleBasis,
			"clean":               len(staleBasis) == 0,
			"note": "an open position whose stored entry price predates a SUCCESSFUL re-backfill of its own " +
				"symbol sits on a dead basis. The barrier path re-reads the entry bar so both legs move together; " +
				"this is the audit that the stored values themselves are clean.",
		},
		// Per-epoch record. `summary`/`money` above are BOOK-WIDE and therefore
		// span every strategy this book has run; `epochs` is where the
		// single-strategy numbers live, and `epoch` is the one in force now.
		"epochs":       segments,
		"epoch":        currentEpoch,
		"epochCaption": paperEpochCaption,
		// The equity curve is only as good as the fills under it. Ship the
		// reconciliation beside the summary so a reader never has to assume it.
		"fillFidelity": fidelity,
		"verified":     fidelity.Verified,
		"moneyCaption": paperMoneyCaption,
		// PROCESS facts (paperprocess.go): is the simulator healthy and abstaining,
		// or stalled? A flat curve alone cannot say (audit 2026-09-07).
		"process": d.paperProcess(r.Context(), strategy, time.Now().Unix()),
	}

	// A statistic that would span the boundary is replaced by its refusal.
	if spans {
		payload["summary"] = refuseAcrossBoundary(boundary)
		payload["money"] = nil
		payload["moneyRefused"] = refuseAcrossBoundary(boundary)
	} else {
		payload["summary"] = summary
		payload["money"] = money
	}
	writeJSON(w, payload)
}

// boundaryUTC renders a boundary instant, or empty when none is declared.
func boundaryUTC(ts int64) string {
	if ts <= 0 {
		return ""
	}
	return time.Unix(ts, 0).UTC().Format(time.RFC3339)
}

// maxFidelityChecks bounds how many fills one read reconciles. The paper log is
// small (tens of fills) and each check is one indexed bar lookup, but the read
// pool has four connections and this route is public — an unbounded per-request
// fan-out over a growing log is a denial-of-service waiting to be written.
const maxFidelityChecks = 500

// checkPaperFills re-derives each logged fill from the stored bar at its
// timestamp and returns the verdict. Fills are written AT the bar open, so any
// deviation means the bar was revised after the fill (or the fill never came
// from that bar) — either way the equity curve built on it can no longer be
// re-derived from the database, and the payload has to say so.
//
// Only the most recent maxFidelityChecks fills are checked; the returned counts
// describe exactly that window, never the whole log by implication.
func (d Deps) checkPaperFills(ctx context.Context, all []store.PaperTrade, tf md.Timeframe) papertrade.Fidelity {
	from := 0
	if len(all) > maxFidelityChecks {
		from = len(all) - maxFidelityChecks
	}
	pairs := make([]papertrade.FillVsBar, 0, len(all)-from)
	for _, t := range all[from:] {
		p := papertrade.FillVsBar{Px: t.Px}
		// BarAtOrBefore is the exact bar only when its ts matches; an earlier bar
		// is NOT the one this fill named, so it counts as "no bar".
		//
		// A lookup ERROR is kept apart from that. This used to read
		// `err == nil && ok && bar.Ts == t.Ts`, which folded a store failure into
		// the same bucket as a fill whose bar is genuinely absent. The fidelity
		// report then blamed "no stored bar at all" for what was an outage, and
		// an operator reading it went hunting a data gap that did not exist.
		// "We could not look" is not "we looked and found nothing".
		bar, ok, err := d.St.BarAtOrBefore(ctx, t.SymbolID, tf, t.Ts)
		switch {
		case err != nil:
			p.Unchecked = true
		case ok && bar.Ts == t.Ts:
			p.BarOpen = bar.Open
			p.HasBar = true
		}
		pairs = append(pairs, p)
	}
	return papertrade.CheckFillFidelity(pairs)
}

// registerPaper wires the Stage-4 simulated paper-trading read route.
func (d Deps) registerPaper(mux *http.ServeMux) {
	// Flagship books are user-independent and slow to reconstruct (7s quiet,
	// 116s under load on 2026-09-07), so they are body-cached (2 min SWR) and
	// warmed. The manual book is per-user and bypasses the cache entirely.
	mux.HandleFunc("GET /api/paper", func(w http.ResponseWriter, r *http.Request) {
		if s := r.URL.Query().Get("strategy"); s == "manual" {
			d.paper(w, r)
			return
		}
		sharedPaperSWR.serve(d.St.CacheKey()+"|paper|"+r.URL.RawQuery, w, r, d.paper)
	})
}
