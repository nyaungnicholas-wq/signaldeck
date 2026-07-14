package api

import (
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/nyaungnicholas-wq/signaldeck/internal/graph"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/marketmem"
	"github.com/nyaungnicholas-wq/signaldeck/internal/portopt"
	"github.com/nyaungnicholas-wq/signaldeck/internal/scenario"
)

// registerCapstones wires the "capstone" analytics surfaces built on the pure
// engines: macro scenario simulation, the mean-variance portfolio optimizer,
// the knowledge-graph ripple, and the market-memory historical analog. All GET,
// read-only.
func (d Deps) registerCapstones(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/scenario", d.scenario)
	mux.HandleFunc("GET /api/portfolio/optimize", d.portfolioOptimize)
	mux.HandleFunc("GET /api/graph", d.knowledgeGraph)
	mux.HandleFunc("GET /api/market-memory", d.marketMemory)
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

// knowledgeGraph returns the "ripple" neighborhood around one symbol built from
// FREE data only: the tracked names it co-moves with (return correlation) and
// those held by overlapping institutional managers (13F co-ownership). Query:
// symbol (required), minCorr (default 0.5). Computed live over the streamed hot
// set to stay fast.
//
//	GET /api/graph?symbol=NVDA&minCorr=0.5
func (d Deps) knowledgeGraph(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	center := strings.ToUpper(strings.TrimSpace(q.Get("symbol")))
	if center == "" {
		httpErr(w, 400, "symbol is required")
		return
	}
	minCorr := 0.5
	if s := q.Get("minCorr"); s != "" {
		if v, err := strconv.ParseFloat(s, 64); err == nil && v > 0 {
			minCorr = v
		}
	}
	ctx := r.Context()
	syms, err := d.St.ListSymbols(ctx, true)
	if err != nil {
		httpErr(w, 500, "list symbols")
		return
	}
	type row struct {
		id   int64
		name string
	}
	var set []row
	var centerName string
	seen := map[string]bool{}
	for _, s := range syms {
		base := s.Symbol
		if i := strings.IndexByte(base, '/'); i >= 0 {
			base = base[:i]
		}
		isCenter := equalFoldASCII(s.Symbol, center) || equalFoldASCII(base, center)
		if isCenter {
			centerName = s.Symbol
		}
		if (s.Stream || isCenter) && !seen[s.Symbol] {
			seen[s.Symbol] = true
			set = append(set, row{s.ID, s.Symbol})
		}
		if len(set) >= 30 {
			break
		}
	}
	if centerName == "" {
		httpErr(w, 404, "symbol not tracked: "+center)
		return
	}

	// Aligned daily returns over common days → correlation edges.
	retByDay := make([]map[int64]float64, len(set))
	dayCount := map[int64]int{}
	for i, rrow := range set {
		bars, _ := d.St.LastBars(ctx, rrow.id, md.TF1d, 260)
		m := map[int64]float64{}
		for j := 1; j < len(bars); j++ {
			if p := bars[j-1].Close; p != 0 {
				m[bars[j].Ts/86400] = bars[j].Close/p - 1
			}
		}
		retByDay[i] = m
		for day := range m {
			dayCount[day]++
		}
	}
	var days []int64
	for day, n := range dayCount {
		if n == len(set) {
			days = append(days, day)
		}
	}
	sort.Slice(days, func(i, j int) bool { return days[i] < days[j] })
	names := make([]string, len(set))
	returns := make([][]float64, len(set))
	for i := range set {
		names[i] = set[i].name
		s := make([]float64, len(days))
		for k, day := range days {
			s[k] = retByDay[i][day]
		}
		returns[i] = s
	}
	var corrEdges []graph.Edge
	if len(days) >= 30 {
		corrEdges = graph.CorrelationEdges(names, returns, minCorr)
	}

	// Co-ownership edges from 13F holders (Jaccard of manager CIK sets).
	holders := map[string][]string{}
	for _, rrow := range set {
		rows, _ := d.St.InstHoldingsBySymbol(ctx, rrow.id, 200)
		var ciks []string
		for _, h := range rows {
			if h.CIK != "" {
				ciks = append(ciks, h.CIK)
			}
		}
		if len(ciks) > 0 {
			holders[rrow.name] = ciks
		}
	}
	coEdges := graph.CoOwnershipEdges(holders, 0.15)

	all := append(append([]graph.Edge{}, corrEdges...), coEdges...)
	writeJSON(w, map[string]any{
		"center":       centerName,
		"neighborhood": graph.NeighborhoodOf(centerName, all),
		"comparedWith": names,
		"commonDays":   len(days),
		"edgeKinds":    map[string]int{"correlation": len(corrEdges), "coowned": len(coEdges)},
		"note":         "Ripple graph from FREE data only: return correlation (co-movement, not causation) over the streamed hot set + 13F institutional co-ownership (Jaccard of shared managers). Supplier/customer/peer edges need data not held here.",
	})
}

// marketMemory finds the historical days whose market state most resembles today
// and reports what FOLLOWED. Market state = SPY's own trend/vol features; the
// forward outcome = SPY's realized next-20-trading-day return. Honesty-gated
// (needs >=60 past days with a known forward outcome).
//
//	GET /api/market-memory
func (d Deps) marketMemory(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	syms, err := d.St.ListSymbols(ctx, true)
	if err != nil {
		httpErr(w, 500, "list symbols")
		return
	}
	var spyID int64
	for _, s := range syms {
		if equalFoldASCII(s.Symbol, "SPY") {
			spyID = s.ID
			break
		}
	}
	if spyID == 0 {
		httpErr(w, 404, "SPY not tracked (needed as the market proxy)")
		return
	}
	bars, err := d.St.LastBars(ctx, spyID, md.TF1d, 800)
	if err != nil || len(bars) < 90 {
		writeJSON(w, map[string]any{"gated": true, "note": "insufficient SPY history for analog matching"})
		return
	}
	const fwd, look = 20, 20
	var hist []marketmem.Snapshot
	var curFeat []float64
	for i := look; i < len(bars); i++ {
		ret1 := 0.0
		if bars[i-1].Close != 0 {
			ret1 = bars[i].Close/bars[i-1].Close - 1
		}
		vol5 := stddevReturns(bars[i-5 : i+1])
		mom20 := 0.0
		if bars[i-look].Close != 0 {
			mom20 = bars[i].Close/bars[i-look].Close - 1
		}
		feat := []float64{ret1, vol5, mom20}
		if i+fwd < len(bars) && bars[i].Close != 0 {
			hist = append(hist, marketmem.Snapshot{
				Ts: bars[i].Ts, Features: feat,
				FwdReturn: (bars[i+fwd].Close/bars[i].Close - 1) * 100,
			})
		}
		if i == len(bars)-1 {
			curFeat = feat
		}
	}
	if curFeat == nil || len(hist) < 60 {
		writeJSON(w, map[string]any{"gated": true, "history": len(hist),
			"note": "insufficient history (need >=60 past days with a known forward outcome)"})
		return
	}
	res := marketmem.Find(curFeat, hist, 8, 60, bars[len(bars)-1].Ts, int64(fwd)*86400)
	writeJSON(w, map[string]any{
		"proxy":              "SPY",
		"features":           []string{"1d return", "5d realized vol", "20d momentum"},
		"forwardHorizonDays": fwd,
		"today":              map[string]float64{"ret1": curFeat[0], "vol5": curFeat[1], "mom20": curFeat[2]},
		"result":             res,
		"note":               "Descriptive analogs, NOT a forecast: the K most similar past SPY days (by normalized trend/vol features) and the return that FOLLOWED them. The future need not rhyme.",
	})
}

// stddevReturns is the sample stddev of the day-over-day returns within bars.
func stddevReturns(bars []md.Bar) float64 {
	var rets []float64
	for i := 1; i < len(bars); i++ {
		if bars[i-1].Close != 0 {
			rets = append(rets, bars[i].Close/bars[i-1].Close-1)
		}
	}
	if len(rets) < 2 {
		return 0
	}
	m := meanF(rets)
	var ss float64
	for _, x := range rets {
		ss += (x - m) * (x - m)
	}
	return math.Sqrt(ss / float64(len(rets)-1))
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
