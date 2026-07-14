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
	"context"
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
	// CONVICTION — the honest second axis. Anchored to the model's MEASURED live
	// accuracy (not the overconfident per-symbol calProb), so a top rank on an
	// extreme-but-unbacked, stale, thin, or contradictory read is LOW conviction.
	// Computed at READ time so it reflects the current live-edge status.
	proven, winRate, skillNote := d.fleetEdgeSkill(r.Context())
	bull, bear := composite.FactorAgreement(p.Factors)
	conv := composite.Assess(composite.ConvictionInputs{
		Edge:           row.Edge,
		PredAgeSec:     time.Now().Unix() - p.PredTs,
		NUsed:          p.NUsed,
		Bull:           bull,
		Bear:           bear,
		EdgeProvenLive: proven,
		WinRate:        winRate,
		SkillNote:      skillNote,
	})
	writeJSON(w, map[string]any{
		"available":  true,
		"symbol":     s.Symbol,
		"market":     s.Market,
		"horizon":    row.Horizon,
		"ts":         row.Ts,
		"score":      row.Score,
		"curvePct":   row.CurvePct,
		"edge":       row.Edge,
		"edgeLine":   honestEdgeLine(p.CalProb, proven, winRate),
		"rawProb":    p.RawProb,
		"calProb":    p.CalProb,
		"nUsed":      p.NUsed,
		"predTs":     p.PredTs,
		"factors":    p.Factors,
		"ledger":     p.Ledger,
		"conviction": conv,
		"curveNote":  compositeCurveNote,
		"edgeNote":   compositeEdgeNote,
		"trackLabel": "backtested / in-sample — not a live track record",
	})
}

// honestEdgeLine frames the stored calibrated probability WITHOUT overstating
// it. The per-symbol calibration is overconfident (measured raw→cal inflation),
// so the line pairs the number with the model's realized accuracy and says,
// verbatim, to read it as a RANK signal rather than a literal probability.
func honestEdgeLine(calProb float64, proven bool, winRate float64) string {
	dir := "up"
	if calProb < 0.5 {
		dir = "down"
	}
	base := fmt.Sprintf("Model leans %s — calibrated P(up 1d) %.0f%%", dir, calProb*100)
	extreme := calProb >= 0.7 || calProb <= 0.3
	switch {
	case proven && extreme:
		return base + fmt.Sprintf(", but realized live accuracy is only %.0f%% — read this as a RANK, not a %.0f%% chance", winRate*100, calProb*100)
	case proven:
		return base + fmt.Sprintf(" (realized live accuracy %.0f%% — a ranking signal, not a literal probability)", winRate*100)
	default:
		return base + " — realized accuracy not yet proven live; treat as a RANK, not a probability"
	}
}

// fleetEdgeSkill answers the one fleet-level question conviction needs: has the
// platform proven a LIVE out-of-sample edge? It grades the platform's OWN
// calibrated 1d predictions (prediction_outcomes — prob frozen at prediction
// time, outcome filled by the resolver, no lookahead), deduped to ONE
// independent observation per (symbol, UTC-day), and calls the edge "proven"
// only when it clears the SAME gate the /track-record page uses AND the win
// rate's 95% Wilson lower bound sits above a coin flip. Best-effort: any error
// returns "not proven" with a stated reason — never a fabricated pass.
func (d Deps) fleetEdgeSkill(ctx context.Context) (proven bool, winRate float64, note string) {
	rows, err := d.St.ResolvedPredictionOutcomes(ctx, md.H1d, 20000)
	if err != nil {
		return false, 0, "live edge status unavailable (" + err.Error() + ")"
	}
	// One independent obs per (symbol, UTC-day), keeping the latest (rows ts DESC).
	seen := map[[2]int64]bool{}
	dayset := map[int64]bool{}
	wins, indepN := 0, 0
	for _, o := range rows {
		key := [2]int64{o.SymbolID, o.Ts / 86400}
		if seen[key] {
			continue
		}
		seen[key] = true
		indepN++
		dayset[o.Ts/86400] = true
		if o.Up == 1 {
			wins++
		}
	}
	distinctDays := len(dayset)
	if indepN < trackMinIndependentN || distinctDays < trackMinDistinctDays {
		return false, 0, fmt.Sprintf("live track record still thin — %d independent resolutions across %d day(s) (need %d / %d)",
			indepN, distinctDays, trackMinIndependentN, trackMinDistinctDays)
	}
	winRate = float64(wins) / float64(indepN)
	lo, _ := wilson(wins, indepN)
	if lo > 0.5 {
		return true, winRate, fmt.Sprintf("edge proven live: %.1f%% directional accuracy over %d independent resolutions across %d days (95%% floor %.1f%% > 50%%)",
			winRate*100, indepN, distinctDays, lo*100)
	}
	return false, winRate, fmt.Sprintf("no proven live edge yet: %.1f%% accuracy over %d independent resolutions is within noise of a coin flip (95%% floor %.1f%% ≤ 50%%)",
		winRate*100, indepN, lo*100)
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
	// Conviction: the coarse (edge-magnitude + live-edge-cap) conviction band for
	// this row — so the leaderboard never reads as a ladder of certainty. The
	// per-symbol detail view adds the freshness/blend/agreement nuance.
	Conviction      string `json:"conviction"`
	ConvictionLabel string `json:"convictionLabel"`
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

	// Live-edge status once for the whole leaderboard (the conviction ceiling).
	proven, winRate, skillNote := d.fleetEdgeSkill(ctx)

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
		// Coarse conviction for the list: measured-skill ceiling + the
		// overconfidence penalty on extreme edges. NUsed=2 / PredAgeSec=0 skip the
		// freshness/blend/agreement discounts (the detail view applies those).
		conv := composite.Assess(composite.ConvictionInputs{
			Edge:           c.Edge,
			NUsed:          2,
			EdgeProvenLive: proven,
			WinRate:        winRate,
			SkillNote:      skillNote,
		})
		row.Conviction, row.ConvictionLabel = string(conv.Band), conv.Label
		out = append(out, row)
	}
	writeJSON(w, map[string]any{
		"horizon":       string(md.H1d),
		"rows":          out,
		"n":             len(out),
		"total":         len(rows),
		"curveNote":     compositeCurveNote,
		"edgeNote":      compositeEdgeNote,
		"rankNote":      "rankChange compares against each symbol's newest row before today (UTC); symbols absent from the previous pass carry null, never a fabricated change",
		"trackLabel":    "backtested / in-sample — not a live track record",
		"convictionNote": "conviction is a SEPARATE axis from the rank: a top rank on a coin-flip-sized or unproven edge is low conviction. " + composite.Assess(composite.ConvictionInputs{}).RiskNote,
		"skillNote":     skillNote,
		"minCurveN":     composite.MinCurveN,
	})
}

// registerComposite wires the composite SignalScore read routes.
func (d Deps) registerComposite(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/composite", d.compositeDetail)
	mux.HandleFunc("GET /api/composite/top", d.compositeTop)
}
