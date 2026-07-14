package api

import (
	"net/http"
	"sort"
	"strconv"
	"strings"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/portopt"
	"github.com/nyaungnicholas-wq/signaldeck/internal/scenario"
)

// registerCapstones wires the "capstone" analytics surfaces built on the pure
// engines: macro scenario simulation and the mean-variance portfolio optimizer.
// (market-memory / knowledge-graph join here as their handlers land.) All GET,
// read-only.
func (d Deps) registerCapstones(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/scenario", d.scenario)
	mux.HandleFunc("GET /api/portfolio/optimize", d.portfolioOptimize)
}

// scenario estimates how a hypothetical macro shock would move a symbol, from
// the historical sensitivity (OLS beta) of the symbol's daily returns to daily
// changes in a macro factor. Query: symbol (required), factor (FRED series id,
// default VIXCLS), shock (factor-unit move, default +10). Honesty: the engine
// gates thin history (n<30) and never projects a move without a fitted beta.
//
//	GET /api/scenario?symbol=NVDA&factor=VIXCLS&shock=10
func (d Deps) scenario(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	sym := q.Get("symbol")
	if sym == "" {
		httpErr(w, 400, "symbol is required")
		return
	}
	factor := q.Get("factor")
	if factor == "" {
		factor = "VIXCLS"
	}
	shock := 10.0
	if s := q.Get("shock"); s != "" {
		if v, err := strconv.ParseFloat(s, 64); err == nil {
			shock = v
		}
	}

	ctx := r.Context()
	// Resolve the ticker to a tracked symbol (match "NVDA" or "BTC" of "BTC/USD").
	syms, err := d.St.ListSymbols(ctx, true)
	if err != nil {
		httpErr(w, 500, "list symbols")
		return
	}
	var symID int64
	var canon string
	for _, s := range syms {
		base := s.Symbol
		for i := 0; i < len(base); i++ {
			if base[i] == '/' {
				base = base[:i]
				break
			}
		}
		if equalFoldASCII(s.Symbol, sym) || equalFoldASCII(base, sym) {
			symID, canon = s.ID, s.Symbol
			break
		}
	}
	if symID == 0 {
		httpErr(w, 404, "symbol not tracked: "+sym)
		return
	}

	// Daily closes and macro observations, aligned by UTC calendar day.
	bars, err := d.St.LastBars(ctx, symID, md.TF1d, 800)
	if err != nil {
		httpErr(w, 500, "bars")
		return
	}
	macro, err := d.St.MacroSeries(ctx, factor, 800)
	if err != nil {
		httpErr(w, 500, "macro")
		return
	}
	closeByDay := map[int64]float64{}
	for _, b := range bars {
		closeByDay[b.Ts/86400] = b.Close
	}
	macroByDay := map[int64]float64{}
	for _, m := range macro {
		macroByDay[m.Ts/86400] = m.Value
	}
	// Days present in BOTH series, sorted ascending.
	var days []int64
	for day := range closeByDay {
		if _, ok := macroByDay[day]; ok {
			days = append(days, day)
		}
	}
	sort.Slice(days, func(i, j int) bool { return days[i] < days[j] })

	// Paired daily return (%) vs daily factor change over consecutive common days.
	var assetRet, factorChg []float64
	for i := 1; i < len(days); i++ {
		p, c := closeByDay[days[i-1]], closeByDay[days[i]]
		if p == 0 {
			continue
		}
		assetRet = append(assetRet, (c/p-1)*100)
		factorChg = append(factorChg, macroByDay[days[i]]-macroByDay[days[i-1]])
	}

	label := factor + signedLabel(shock)
	impact := scenario.Estimate(canon, assetRet, factorChg, shock, label, 30)
	writeJSON(w, map[string]any{
		"symbol":      canon,
		"factor":      factor,
		"shock":       shock,
		"pairedDays":  len(assetRet),
		"impact":      impact,
		"disclaimer":  "Historical sensitivity (OLS beta), not a forecast. A shock's effect is estimated from how this symbol has co-moved with the factor; correlation is not causation and betas drift.",
	})
}

// portfolioOptimize computes long-only mean-variance allocations (minimum-
// variance AND maximum-Sharpe) over a set of symbols from their aligned daily
// return history. Query: symbols (comma list; default = the streamed hot set,
// capped for conditioning), lookback (trading days, default 180), rf (per-day
// risk-free, default 0). Expected returns use the trailing mean daily return —
// a descriptive input, NOT a forecast, and the payload says so.
//
//	GET /api/portfolio/optimize?symbols=NVDA,AAPL,MSFT&lookback=180
func (d Deps) portfolioOptimize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	lookback := 180
	if s := q.Get("lookback"); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v > 20 {
			lookback = v
		}
	}
	rf := 0.0
	if s := q.Get("rf"); s != "" {
		if v, err := strconv.ParseFloat(s, 64); err == nil {
			rf = v
		}
	}

	ctx := r.Context()
	syms, err := d.St.ListSymbols(ctx, true)
	if err != nil {
		httpErr(w, 500, "list symbols")
		return
	}
	want := map[string]bool{}
	if raw := q.Get("symbols"); raw != "" {
		for _, s := range strings.Split(raw, ",") {
			if s = strings.TrimSpace(strings.ToUpper(s)); s != "" {
				want[s] = true
			}
		}
	}
	var ids []int64
	var symbols []string
	for _, s := range syms {
		base := s.Symbol
		if i := strings.IndexByte(base, '/'); i >= 0 {
			base = base[:i]
		}
		pick := false
		if len(want) > 0 {
			pick = want[strings.ToUpper(s.Symbol)] || want[strings.ToUpper(base)]
		} else {
			pick = s.Stream // default: the streamed hot set
		}
		if pick {
			ids = append(ids, s.ID)
			symbols = append(symbols, s.Symbol)
		}
		if len(ids) >= 25 {
			break
		}
	}
	if len(ids) < 2 {
		httpErr(w, 400, "need at least 2 tracked symbols (pass ?symbols=A,B,C)")
		return
	}

	// Per-symbol daily returns keyed by UTC day; count coverage per day.
	retByDay := make([]map[int64]float64, len(ids))
	dayCount := map[int64]int{}
	for i, id := range ids {
		bars, err := d.St.LastBars(ctx, id, md.TF1d, lookback+1)
		if err != nil {
			httpErr(w, 500, "bars")
			return
		}
		m := map[int64]float64{}
		for j := 1; j < len(bars); j++ {
			p := bars[j-1].Close
			if p == 0 {
				continue
			}
			m[bars[j].Ts/86400] = bars[j].Close/p - 1
		}
		retByDay[i] = m
		for day := range m {
			dayCount[day]++
		}
	}
	var days []int64
	for day, n := range dayCount {
		if n == len(ids) { // present for ALL symbols
			days = append(days, day)
		}
	}
	sort.Slice(days, func(i, j int) bool { return days[i] < days[j] })
	if len(days) < 30 {
		writeJSON(w, map[string]any{
			"gated": true, "commonDays": len(days), "symbols": symbols,
			"note": "insufficient overlapping history across the selected symbols (need >=30 common days)",
		})
		return
	}

	returns := make([][]float64, len(ids))
	expRet := make([]float64, len(ids))
	for i := range ids {
		s := make([]float64, len(days))
		for k, day := range days {
			s[k] = retByDay[i][day]
		}
		returns[i] = s
		expRet[i] = meanF(s)
	}
	cov := portopt.Covariance(returns)

	writeJSON(w, map[string]any{
		"symbols":     symbols,
		"commonDays":  len(days),
		"lookback":    lookback,
		"minVariance": portopt.MinVariance(symbols, cov),
		"maxSharpe":   portopt.MaxSharpe(symbols, expRet, cov, rf),
		"note":        "Long-only mean-variance (Markowitz). Expected returns = trailing mean daily return — DESCRIPTIVE, not a forecast. Covariance from aligned daily returns; past covariance is an imperfect guide to future risk.",
	})
}

// meanF is the arithmetic mean of xs (0 for empty).
func meanF(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	sum := 0.0
	for _, x := range xs {
		sum += x
	}
	return sum / float64(len(xs))
}

// signedLabel renders a shock as " +10" / " -0.25" for the human label.
func signedLabel(x float64) string {
	if x >= 0 {
		return " +" + strconv.FormatFloat(x, 'g', -1, 64)
	}
	return " " + strconv.FormatFloat(x, 'g', -1, 64)
}

// equalFoldASCII is a tiny case-insensitive ASCII compare (avoids importing
// strings just for this file's one use).
func equalFoldASCII(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if 'A' <= ca && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if 'A' <= cb && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}
