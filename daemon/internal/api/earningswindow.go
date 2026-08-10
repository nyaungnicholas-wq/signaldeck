// Credibility wave — EARNINGS AWARENESS (daemon side).
//
// GET /api/earnings-window?symbol&market — one symbol's estimated next
// earnings date from the SAME filing-cadence heuristic /api/earnings-est uses
// (last 10-Q/10-K + ~91 days; see companies.go). HONEST NULLS: a symbol with
// no stored periodic filing gets estimatedNext:null + daysUntil:null and the
// caveat says why — nothing is faked.
//
// The same math also ANNOTATES /api/regimes and /api/signal-report: symbols
// whose estimate falls within the next 7 days gain an earningsWindow
// {daysUntil, withinWindow} block plus the event-risk note. Forecasts are
// NEVER suppressed — the regime engines were validated over windows that
// include earnings, so the honest treatment is a label, not a filter.
package api

import (
	"context"
	"math"
	"net/http"
	"time"
)

// earningsWindowDays is the "inside an earnings window" horizon in days.
const earningsWindowDays = 7

// earningsRiskNote ships with every earnings-window annotation.
const earningsRiskNote = "high-conviction calls inside an earnings window carry event risk the model does not price"

// earningsWindowFrom is the PURE window math: given the last periodic filing
// ts and now, it returns the estimated next report ts, whole days until it
// (ceil, negative = the estimate already passed / cadence slipped), and
// whether that lands inside the forward window [0, earningsWindowDays].
func earningsWindowFrom(lastFiledTs, now int64) (estTs int64, daysUntil int, within bool) {
	estTs = NextPeriodicEstimate(lastFiledTs)
	daysUntil = int(math.Ceil(float64(estTs-now) / 86400))
	within = daysUntil >= 0 && daysUntil <= earningsWindowDays
	return estTs, daysUntil, within
}

// earningsWindow serves GET /api/earnings-window?symbol&market.
func (d Deps) earningsWindow(w http.ResponseWriter, r *http.Request) {
	s, err := d.symbolFromQuery(r)
	if err != nil {
		httpErr(w, 404, err.Error())
		return
	}
	resp := map[string]any{
		"symbol":        s.Symbol,
		"market":        string(s.Market),
		"estimatedNext": nil,
		"daysUntil":     nil,
		"withinWindow":  false,
		"method":        "filing-cadence heuristic",
		"caveat":        earningsEstNote,
	}
	p, ok, err := d.St.LatestPeriodicFiling(r.Context(), s.ID)
	if err != nil {
		httpInternal(w, err)
		return
	}
	if ok && p.FiledTs > 0 {
		est, days, within := earningsWindowFrom(p.FiledTs, time.Now().Unix())
		resp["estimatedNext"] = est
		resp["daysUntil"] = days
		resp["withinWindow"] = within
		resp["overdue"] = days < 0 // cadence slipped: date unknown, not "past"
		resp["lastForm"] = p.Form
		resp["lastFiledTs"] = p.FiledTs
	}
	writeJSON(w, resp)
}

// earningsWindowsForSymbols returns, for the given symbol names, the subset
// currently INSIDE the earnings window (0..7 est. days out) as
// symbol → {daysUntil, withinWindow}. Batched: one fleet-wide filing map +
// one symbol list. Best-effort — an error returns nil and the caller simply
// omits the annotation (a missing label, never a fabricated one).
func (d Deps) earningsWindowsForSymbols(ctx context.Context, want map[string]bool, now int64) map[string]map[string]any {
	if len(want) == 0 {
		return nil
	}
	periodic, err := d.St.LatestPeriodicFilingAll(ctx)
	if err != nil {
		return nil
	}
	syms, err := d.St.ActiveStockSymbols(ctx, nil)
	if err != nil {
		return nil
	}
	out := map[string]map[string]any{}
	for _, s := range syms {
		if !want[s.Symbol] {
			continue
		}
		p, ok := periodic[s.ID]
		if !ok || p.FiledTs <= 0 {
			continue // unknown date — honest absence, no annotation
		}
		if _, days, within := earningsWindowFrom(p.FiledTs, now); within {
			out[s.Symbol] = map[string]any{"daysUntil": days, "withinWindow": true}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// registerEarningsWindow wires the credibility-wave earnings-awareness read.
func (d Deps) registerEarningsWindow(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/earnings-window", d.earningsWindow)
}
