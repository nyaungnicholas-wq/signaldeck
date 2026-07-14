// Composite SignalScore API (SIGNALS-hub overhaul). Read-only, gated like
// every other read endpoint.
//
// HONESTY, carried in every payload: the 1-10 score is a FORCED cross-
// sectional curve (top 5% = 10 … bottom 5% = 1) over edge = calibrated
// P(up,1d) − 0.5 from the LATEST STORED prediction — a rank, not a
// probability, and never recomputed here. Factor tiles render their gates as
// explicit reasons; the additive ledger labels its method (exact vs
// proportional attribution). Calibration remains backtested / in-sample until
// the live track record clears its gate — trackLabel says so verbatim.
package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/composite"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

const (
	compositeCurveNote = "score 1-10 is a FORCED cross-sectional curve over all symbols scored in the pass (top 5% = 10 … bottom 5% = 1) — a rank on today's cross-section, not a probability; below 30 usable predictions no scores are emitted at all"
	compositeEdgeNote  = "edge = calibrated P(up,1d) − 0.5 from the latest stored ensemble prediction — never recomputed here"
)

// compositeDetail serves one symbol's latest SignalScore with its full
// evidence payload (factor tiles + additive ledger).
// GET /api/composite?symbol=&market=
func (d Deps) compositeDetail(w http.ResponseWriter, r *http.Request) {
	s, err := d.symbolFromQuery(r)
	if err != nil {
		httpErr(w, 404, err.Error())
		return
	}
	row, ok, err := d.St.LatestCompositeScore(r.Context(), s.ID)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	if !ok {
		// Honest absence: the scorer either hasn't run or gated its passes.
		writeJSON(w, map[string]any{
			"available": false,
			"symbol":    s.Symbol,
			"market":    s.Market,
			"reason":    "no composite score stored yet — the composite-scorer runs every 10m and stores nothing while fewer than 30 symbols have fresh predictions (forced-curve gate)",
			"curveNote": compositeCurveNote,
		})
		return
	}
	var p composite.Payload
	if err := json.Unmarshal([]byte(row.Payload), &p); err != nil {
		httpErr(w, 500, "stored payload does not parse: "+err.Error())
		return
	}
	writeJSON(w, map[string]any{
		"available": true,
		"symbol":    s.Symbol,
		"market":    s.Market,
		"horizon":   row.Horizon,
		"ts":        row.Ts,
		"score":     row.Score,
		"curvePct":  row.CurvePct,
		"edge":      row.Edge,
		"edgeLine": fmt.Sprintf("P(up 1d) %.1f%% vs 50%% coin = %+.1fpp edge",
			p.CalProb*100, row.Edge*100),
		"rawProb":    p.RawProb,
		"calProb":    p.CalProb,
		"nUsed":      p.NUsed,
		"predTs":     p.PredTs,
		"factors":    p.Factors,
		"ledger":     p.Ledger,
		"curveNote":  compositeCurveNote,
		"edgeNote":   compositeEdgeNote,
		"trackLabel": "backtested / in-sample — not a live track record",
	})
}

// compositeTopRow is one ranked row of the composite leaderboard. Rank is
// 1-based over the FULL latest cross-section (before the limit is applied);
// PrevRank/RankChange compare against the previous day's pass and are nil for
// symbols that were not scored then (honest absence, never a fabricated 0).
type compositeTopRow struct {
	Symbol     string  `json:"symbol"`
	Market     string  `json:"market"`
	Horizon    string  `json:"horizon"`
	Ts         int64   `json:"ts"`
	Score      int     `json:"score"`
	CurvePct   float64 `json:"curvePct"`
	Edge       float64 `json:"edge"`
	Rank       int     `json:"rank"`
	PrevRank   *int    `json:"prevRank"`
	RankChange *int    `json:"rankChange"` // prevRank − rank; positive = moved up
}

// compositeTop serves the ranked SignalScore leaderboard with rank-change
// movers vs the previous day's pass.
// GET /api/composite/top?limit=&market=
func (d Deps) compositeTop(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	market := ""
	if m := md.Market(r.URL.Query().Get("market")); m == md.Crypto || m == md.Stocks {
		market = string(m)
	}
	limit := limitParam(r, 50, 500)

	// Rank over the FULL latest set, then truncate — a limited read must not
	// change anyone's rank.
	rows, err := d.St.TopCompositeScores(ctx, 0, market)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	// Previous day's pass: each symbol's newest row before today (UTC).
	startOfToday := time.Now().UTC().Truncate(24 * time.Hour).Unix()
	prev, err := d.St.CompositeScoresBefore(ctx, startOfToday, market)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	prevRank := make(map[int64]int, len(prev))
	for i, p := range prev {
		prevRank[p.SymbolID] = i + 1 // store order = score DESC, pct DESC
	}

	out := make([]compositeTopRow, 0, min(limit, len(rows)))
	for i, c := range rows {
		if i >= limit {
			break
		}
		row := compositeTopRow{
			Symbol: c.Symbol, Market: c.Market, Horizon: c.Horizon, Ts: c.Ts,
			Score: c.Score, CurvePct: c.CurvePct, Edge: c.Edge, Rank: i + 1,
		}
		if pr, ok := prevRank[c.SymbolID]; ok {
			delta := pr - row.Rank
			row.PrevRank, row.RankChange = &pr, &delta
		}
		out = append(out, row)
	}
	writeJSON(w, map[string]any{
		"horizon":    string(md.H1d),
		"rows":       out,
		"n":          len(out),
		"total":      len(rows),
		"curveNote":  compositeCurveNote,
		"edgeNote":   compositeEdgeNote,
		"rankNote":   "rankChange compares against each symbol's newest row before today (UTC); symbols absent from the previous pass carry null, never a fabricated change",
		"trackLabel": "backtested / in-sample — not a live track record",
		"minCurveN":  composite.MinCurveN,
	})
}

// registerComposite wires the composite SignalScore read routes.
func (d Deps) registerComposite(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/composite", d.compositeDetail)
	mux.HandleFunc("GET /api/composite/top", d.compositeTop)
}
