package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/backtest"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/portfolio"
	"github.com/nyaungnicholas-wq/signaldeck/internal/risklens"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// dailyCloses returns up to n most-recent daily closes (oldest→newest).
func (d Deps) dailyCloses(r *http.Request, symbolID int64, n int) ([]float64, error) {
	bars, err := d.St.LastBars(r.Context(), symbolID, md.TF1d, n)
	if err != nil {
		return nil, err
	}
	out := make([]float64, len(bars))
	for i, b := range bars {
		out[i] = b.Close
	}
	return out, nil
}

// lastClose returns the most recent daily close for a symbol.
func (d Deps) lastClose(r *http.Request, symbolID int64) (float64, error) {
	bars, err := d.St.LastBars(r.Context(), symbolID, md.TF1d, 1)
	if err != nil || len(bars) == 0 {
		return 0, err
	}
	return bars[len(bars)-1].Close, nil
}

// ── forecast (cached, read-only) ────────────────────────────────────────

func (d Deps) forecast(w http.ResponseWriter, r *http.Request) {
	s, err := d.symbolFromQuery(r)
	if err != nil {
		httpErr(w, 404, err.Error())
		return
	}
	fs, err := d.St.Forecasts(r.Context(), s.ID)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	writeJSON(w, fs)
}

// ── backtest (CopilotQuant) ─────────────────────────────────────────────

func (d Deps) backtestRun(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Symbol string    `json:"symbol"`
		Market md.Market `json:"market"`
		Text   string    `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpErr(w, 400, "bad json: "+err.Error())
		return
	}
	s, err := d.St.GetSymbol(r.Context(), strings.ToUpper(strings.TrimSpace(body.Symbol)), body.Market)
	if err != nil {
		httpErr(w, 404, "unknown symbol")
		return
	}
	strat, err := backtest.Parse(body.Text)
	if err != nil {
		httpErr(w, 422, err.Error()) // helpful "supported forms" message
		return
	}
	bars, err := d.St.LastBars(r.Context(), s.ID, md.TF1d, 1000)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	if len(bars) < 60 {
		httpErr(w, 422, "not enough history for this symbol yet")
		return
	}
	res, err := backtest.Backtest(bars, strat)
	if err != nil {
		httpErr(w, 422, err.Error())
		return
	}
	// Attach the bar timestamps so the frontend can plot the equity curve.
	ts := make([]int64, len(bars))
	for i, b := range bars {
		ts[i] = b.Ts
	}
	writeJSON(w, map[string]any{
		"strategy": strat,
		"result":   res,
		"explain":  backtest.Explain(strat, res),
		"ts":       ts,
	})
}

// ── risk (RiskLens) ─────────────────────────────────────────────────────

func (d Deps) risk(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Holdings []struct {
			Symbol string    `json:"symbol"`
			Market md.Market `json:"market"`
			Weight float64   `json:"weight"`
		} `json:"holdings"`
		NotionalUSD float64 `json:"notionalUSD"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpErr(w, 400, "bad json: "+err.Error())
		return
	}
	if len(body.Holdings) == 0 {
		httpErr(w, 400, "need at least one holding")
		return
	}
	if body.NotionalUSD <= 0 {
		body.NotionalUSD = 100_000
	}
	var holdings []risklens.Holding
	var series []risklens.Series
	minLen := 1 << 30
	raw := make([][]float64, 0, len(body.Holdings))
	for _, h := range body.Holdings {
		s, err := d.St.GetSymbol(r.Context(), strings.ToUpper(strings.TrimSpace(h.Symbol)), h.Market)
		if err != nil {
			httpErr(w, 404, "unknown symbol: "+h.Symbol)
			return
		}
		closes, err := d.dailyCloses(r, s.ID, 400)
		if err != nil {
			httpErr(w, 500, err.Error())
			return
		}
		if len(closes) < 60 {
			httpErr(w, 422, "not enough history for "+h.Symbol)
			return
		}
		if len(closes) < minLen {
			minLen = len(closes)
		}
		raw = append(raw, closes)
		holdings = append(holdings, risklens.Holding{Symbol: s.Symbol, Weight: h.Weight})
	}
	// Align every series to the most-recent minLen window (approximate
	// date-alignment over recent liquid daily bars).
	for i, h := range holdings {
		c := raw[i]
		series = append(series, risklens.Series{Symbol: h.Symbol, Closes: c[len(c)-minLen:]})
	}
	report, err := risklens.Analyze(holdings, series, 0.95, body.NotionalUSD)
	if err != nil {
		httpErr(w, 422, err.Error())
		return
	}
	writeJSON(w, map[string]any{"report": report, "summary": risklens.Summary(report)})
}

// ── correlation ─────────────────────────────────────────────────────────

func (d Deps) correlation(w http.ResponseWriter, r *http.Request) {
	syms, err := d.St.ListSymbols(r.Context(), false)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	var series []portfolio.Series
	minLen := 1 << 30
	raw := map[string][]float64{}
	for _, s := range syms {
		closes, err := d.dailyCloses(r, s.ID, 250)
		if err != nil {
			httpErr(w, 500, err.Error())
			return
		}
		if len(closes) < 30 {
			continue
		}
		if len(closes) < minLen {
			minLen = len(closes)
		}
		raw[s.Symbol] = closes
	}
	if len(raw) < 2 {
		writeJSON(w, map[string]any{"symbols": []string{}, "matrix": [][]float64{}})
		return
	}
	// Deterministic order by symbol.
	for _, s := range syms {
		if c, ok := raw[s.Symbol]; ok {
			series = append(series, portfolio.Series{Symbol: s.Symbol, Closes: c[len(c)-minLen:]})
		}
	}
	symbols, matrix, err := portfolio.CorrelationMatrix(series)
	if err != nil {
		httpErr(w, 422, err.Error())
		return
	}
	mostPair, leastPair, mostR, leastR := portfolio.MostAndLeastCorrelated(symbols, matrix)
	writeJSON(w, map[string]any{
		"symbols":         symbols,
		"matrix":          matrix,
		"mostPair":        mostPair,
		"mostR":           mostR,
		"leastPair":       leastPair,
		"leastR":          leastR,
		"diversification": portfolio.DiversificationScore(matrix),
	})
}

// ── portfolio (paper positions) ─────────────────────────────────────────

func (d Deps) portfolioGet(w http.ResponseWriter, r *http.Request) {
	positions, err := d.St.Positions(r.Context(), false)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	last := map[string]float64{}
	type row struct {
		store.Position
		LastPrice float64 `json:"lastPrice"`
		PnLAbs    float64 `json:"pnlAbs"`
		PnLPct    float64 `json:"pnlPct"`
	}
	var rows []row
	var pfPositions []portfolio.Position
	for _, p := range positions {
		// Closed positions realize at their exit; open ones mark to the
		// latest close.
		lp := p.EntryPrice
		if p.ExitPrice != nil {
			lp = *p.ExitPrice
		} else if c, err := d.lastClose(r, p.SymbolID); err == nil && c > 0 {
			lp = c
		}
		last[p.Symbol] = lp
		pnlAbs, pnlPct := portfolio.PositionPnL(portfolio.Position{
			Symbol: p.Symbol, Qty: p.Qty, EntryPrice: p.EntryPrice,
			EntryTs: p.EntryTs, Note: p.Note, ScoreAtEntry: p.ScoreAtEntry,
		}, lp)
		rows = append(rows, row{Position: p, LastPrice: lp, PnLAbs: pnlAbs, PnLPct: pnlPct})
		if p.Open {
			pfPositions = append(pfPositions, portfolio.Position{
				Symbol: p.Symbol, Qty: p.Qty, EntryPrice: p.EntryPrice,
			})
		}
	}
	stat := portfolio.Evaluate(pfPositions, last)
	writeJSON(w, map[string]any{"positions": rows, "stat": stat})
}

func (d Deps) portfolioAdd(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Symbol string    `json:"symbol"`
		Market md.Market `json:"market"`
		Qty    float64   `json:"qty"`
		Note   string    `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpErr(w, 400, "bad json: "+err.Error())
		return
	}
	s, err := d.St.GetSymbol(r.Context(), strings.ToUpper(strings.TrimSpace(body.Symbol)), body.Market)
	if err != nil {
		httpErr(w, 404, "unknown symbol")
		return
	}
	if body.Qty == 0 {
		httpErr(w, 400, "qty must be non-zero")
		return
	}
	entry, err := d.lastClose(r, s.ID)
	if err != nil || entry == 0 {
		httpErr(w, 422, "no price for this symbol yet")
		return
	}
	var scoreAtEntry float64
	if sc, ok, _ := d.St.LatestScore(r.Context(), s.ID, md.H1d); ok {
		scoreAtEntry = sc.Score
	}
	id, err := d.St.InsertPosition(r.Context(), store.Position{
		SymbolID: s.ID, Qty: body.Qty, EntryPrice: entry,
		EntryTs: time.Now().Unix(), Note: body.Note, ScoreAtEntry: scoreAtEntry,
	})
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	writeJSON(w, map[string]any{"id": id, "entryPrice": entry, "scoreAtEntry": scoreAtEntry})
}

func (d Deps) portfolioClose(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID       int64     `json:"id"`
		Symbol   string    `json:"symbol"`
		Market   md.Market `json:"market"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpErr(w, 400, "bad json: "+err.Error())
		return
	}
	s, err := d.St.GetSymbol(r.Context(), strings.ToUpper(strings.TrimSpace(body.Symbol)), body.Market)
	if err != nil {
		httpErr(w, 404, "unknown symbol")
		return
	}
	price, err := d.lastClose(r, s.ID)
	if err != nil || price == 0 {
		httpErr(w, 422, "no price to close at")
		return
	}
	if err := d.St.ClosePosition(r.Context(), body.ID, price); err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	writeJSON(w, map[string]any{"closed": body.ID, "exitPrice": price})
}

// registerQuant wires the Wave-2 quant routes onto the mux.
func (d Deps) registerQuant(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/forecast", d.forecast)
	mux.HandleFunc("POST /api/backtest", d.backtestRun)
	mux.HandleFunc("POST /api/risk", d.risk)
	mux.HandleFunc("GET /api/correlation", d.correlation)
	mux.HandleFunc("GET /api/portfolio", d.portfolioGet)
	mux.HandleFunc("POST /api/portfolio/add", d.portfolioAdd)
	mux.HandleFunc("POST /api/portfolio/close", d.portfolioClose)
}
