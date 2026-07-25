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
	"sync"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/composite"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/pipeline"
)

// fleetSkillWindow is how many recent resolved 1d outcomes fleetEdgeSkill scans.
// It MUST comfortably span the trackMinDistinctDays (10) distinct market days
// even as the monitored universe grows — at ~3k resolutions/day a 20k cap only
// reached ~7 days, which wrongly RE-GATED the live track record to "0% / not
// proven". 120k spans ~40 days at current volume (still >10 if the universe
// doubles). The dedup to one obs per (symbol, UTC-day) runs over this window.
const fleetSkillWindow = 120000

// fleetSkillCache memoizes the fleet-wide live-edge grade (it changes only as
// outcomes resolve, hours apart) so the 120k-row scan fires at most once per
// fleetSkillTTL instead of on every composite/recommendation read.
var fleetSkillCache struct {
	sync.Mutex
	at      time.Time
	proven  bool
	winRate float64
	note    string
	valid   bool
}

const fleetSkillTTL = 60 * time.Second

const (
	compositeCurveNote = "score 1-10 is a FORCED cross-sectional curve over all symbols scored in the pass (top 5% = 10 … bottom 5% = 1) — a rank on today's cross-section, not a probability; below 30 usable predictions no scores are emitted at all"
	compositeEdgeNote  = "edge = calibrated P(up,1d) − 0.5 from the latest stored ensemble prediction — never recomputed here"
)

// compositeDetail serves one symbol's latest SignalScore with its full
// evidence payload (factor tiles + additive ledger).
// GET /api/composite?symbol=&market=
// compositeHorizon reads ?horizon= (default 1d), honoring only the horizons
// the scorer actually produces (1d, 1w) — anything else falls back to 1d
// rather than silently returning an empty/mixed result.
func compositeHorizon(r *http.Request) md.Horizon {
	switch md.Horizon(r.URL.Query().Get("horizon")) {
	case md.H1w:
		return md.H1w
	default:
		return md.H1d
	}
}

func (d Deps) compositeDetail(w http.ResponseWriter, r *http.Request) {
	s, err := d.symbolFromQuery(r)
	if err != nil {
		httpErr(w, 404, err.Error())
		return
	}
	horizon := compositeHorizon(r)
	row, ok, err := d.St.LatestCompositeScore(r.Context(), s.ID, string(horizon))
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
	edgeLine := honestEdgeLine(p.CalProb, proven, winRate)
	trackLabel := "backtested / in-sample — not a live track record"
	// Model-health gate (mirrors /api/predictions/latest, 2026-07-24): the
	// composite score's edge leg is fed by this same directional ensemble, so
	// a symbol the model-health worker has RETIRED must say so here too — the
	// flagship SIGNALS page must not read as more trustworthy than the page
	// that already carries the retirement notice.
	emitting, hVerdict := pipeline.ModelEmitting(r.Context(), d.St, "directional-ensemble-"+string(horizon))
	if !emitting {
		edgeLine = fmt.Sprintf("MODEL RETIRED (%s) — the live record does not support this model; %s shown for audit only, not tradeable", hVerdict, edgeLine)
		trackLabel = fmt.Sprintf("RETIRED (%s): the live record does not support this model — %s", hVerdict, trackLabel)
	}
	writeJSON(w, map[string]any{
		"available":     true,
		"symbol":        s.Symbol,
		"market":        s.Market,
		"horizon":       row.Horizon,
		"ts":            row.Ts,
		"score":         row.Score,
		"curvePct":      row.CurvePct,
		"edge":          row.Edge,
		"edgeLine":      edgeLine,
		"rawProb":       p.RawProb,
		"calProb":       p.CalProb,
		"nUsed":         p.NUsed,
		"predTs":        p.PredTs,
		"factors":       p.Factors,
		"ledger":        p.Ledger,
		"conviction":    conv,
		"curveNote":     compositeCurveNote,
		"edgeNote":      compositeEdgeNote,
		"trackLabel":    trackLabel,
		"modelEmitting": emitting,
		"modelVerdict":  hVerdict,
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
	// Serve from the short-TTL cache when fresh (the 120k-row scan is heavy).
	fleetSkillCache.Lock()
	if fleetSkillCache.valid && time.Since(fleetSkillCache.at) < fleetSkillTTL {
		p, wr, n := fleetSkillCache.proven, fleetSkillCache.winRate, fleetSkillCache.note
		fleetSkillCache.Unlock()
		return p, wr, n
	}
	fleetSkillCache.Unlock()

	var ok bool
	proven, winRate, note, ok = d.computeFleetEdgeSkill(ctx)

	// Only pin a SUCCESSFUL measurement. A transient read failure (e.g. a
	// canceled context from a client that disconnected mid-scan) is not a
	// verdict about the model's skill — caching it would serve "status
	// unavailable" to every reader of the composite leaderboard for the
	// remainder of the TTL, which is exactly the kind of stale-error bug this
	// gate exists to prevent elsewhere. Leave the entry as it was (still
	// stale, so the very next call retries) instead of overwriting a good
	// cached verdict with a bad one, or pinning "unavailable" from cold.
	if ok {
		fleetSkillCache.Lock()
		fleetSkillCache.at, fleetSkillCache.proven, fleetSkillCache.winRate, fleetSkillCache.note = time.Now(), proven, winRate, note
		fleetSkillCache.valid = true
		fleetSkillCache.Unlock()
	}
	return proven, winRate, note
}

func (d Deps) computeFleetEdgeSkill(ctx context.Context) (proven bool, winRate float64, note string, ok bool) {
	rows, err := d.St.ResolvedPredictionOutcomes(ctx, md.H1d, fleetSkillWindow)
	if err != nil {
		return false, 0, "live edge status unavailable (" + err.Error() + ")", false
	}
	// One independent obs per (symbol, UTC-day), keeping the latest (rows ts DESC).
	// correct = the model got the DIRECTION right (predUp==actualUp); ups = how
	// often the market actually rose (the base rate). Grading accuracy against a
	// 50% coin flip is WRONG — equities close up >50% of days, so the honest
	// benchmark is the best NAIVE constant predictor max(upRate, 1-upRate).
	seen := map[[2]int64]bool{}
	dayset := map[int64]bool{}
	correct, ups, indepN := 0, 0, 0
	for _, o := range rows {
		key := [2]int64{o.SymbolID, o.Ts / 86400}
		if seen[key] {
			continue
		}
		seen[key] = true
		indepN++
		dayset[o.Ts/86400] = true
		actualUp := o.Up == 1
		if (o.Prob >= 0.5) == actualUp {
			correct++
		}
		if actualUp {
			ups++
		}
	}
	distinctDays := len(dayset)
	if indepN < trackMinIndependentN || distinctDays < trackMinDistinctDays {
		return false, 0, fmt.Sprintf("live track record still thin — %d independent resolutions across %d day(s) (need %d / %d)",
			indepN, distinctDays, trackMinIndependentN, trackMinDistinctDays), true
	}
	acc := float64(correct) / float64(indepN) // the model's REAL directional accuracy
	baseUp := float64(ups) / float64(indepN)  // market up-rate
	naive := baseUp                           // best constant predictor = max(up, 1-up)
	naiveDir := "up"
	if 1-baseUp > naive {
		naive, naiveDir = 1-baseUp, "down"
	}
	lo, _ := wilson(correct, indepN) // 95% Wilson floor of the ACCURACY
	// PROVEN only if the accuracy floor clears the naive baseline — beating a coin
	// flip is not enough when "always up" already wins >50% of days.
	if lo > naive {
		return true, acc, fmt.Sprintf("edge proven live: model directional accuracy %.1f%% beats the naive 'always-%s' baseline %.1f%% over %d resolutions / %d days (95%% floor %.1f%% > %.1f%%)",
			acc*100, naiveDir, naive*100, indepN, distinctDays, lo*100, naive*100), true
	}
	return false, acc, fmt.Sprintf("NO measured edge: model directional accuracy %.1f%% vs the naive 'always-%s' baseline %.1f%% = %+.1fpp edge over %d resolutions / %d days — the predictions are not skillful (right ~half the time, below the baseline)",
		acc*100, naiveDir, naive*100, (acc-naive)*100, indepN, distinctDays), true
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
// GET /api/composite/top?limit=&market=&horizon=1d|1w (default 1d)
func (d Deps) compositeTop(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	market := ""
	if m := md.Market(r.URL.Query().Get("market")); m == md.Crypto || m == md.Stocks {
		market = string(m)
	}
	horizon := compositeHorizon(r)
	limit := limitParam(r, 50, 500)

	// Rank over the FULL latest set, then truncate — a limited read must not
	// change anyone's rank.
	rows, err := d.St.TopCompositeScores(ctx, 0, market, string(horizon))
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	// Previous PASS: 1d compares vs the start of today (UTC); 1w compares vs 7
	// days ago, so "previous" means the last weekly pass, not yesterday's.
	startOfToday := time.Now().UTC().Truncate(24 * time.Hour).Unix()
	cutoff := startOfToday
	if horizon == md.H1w {
		cutoff = startOfToday - 7*86400
	}
	prev, err := d.St.CompositeScoresBefore(ctx, cutoff, market, string(horizon))
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
	trackLabel := "backtested / in-sample — not a live track record"
	emitting, hVerdict := pipeline.ModelEmitting(ctx, d.St, "directional-ensemble-"+string(horizon))
	if !emitting {
		trackLabel = fmt.Sprintf("RETIRED (%s): the live record does not support this model — %s", hVerdict, trackLabel)
	}
	writeJSON(w, map[string]any{
		"horizon":        string(horizon),
		"rows":           out,
		"n":              len(out),
		"total":          len(rows),
		"curveNote":      compositeCurveNote,
		"edgeNote":       compositeEdgeNote,
		"rankNote":       "rankChange compares against each symbol's newest row before today (UTC); symbols absent from the previous pass carry null, never a fabricated change",
		"trackLabel":     trackLabel,
		"convictionNote": "conviction is a SEPARATE axis from the rank: a top rank on a coin-flip-sized or unproven edge is low conviction. " + composite.Assess(composite.ConvictionInputs{}).RiskNote,
		"skillNote":      skillNote,
		"minCurveN":      composite.MinCurveN,
		"modelEmitting":  emitting,
		"modelVerdict":   hVerdict,
	})
}

// registerComposite wires the composite SignalScore read routes.
func (d Deps) registerComposite(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/composite", d.compositeDetail)
	mux.HandleFunc("GET /api/composite/top", func(w http.ResponseWriter, r *http.Request) {
		// Perf wave 2026-07-24: measured 40s per request; SWR-cached by query.
		sharedCompositeSWR.serve(r.URL.RawQuery, w, r, d.compositeTop)
	})
}
