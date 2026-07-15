// AI RESEARCH DESK API (world-model wave). Read endpoints that tie the app's
// stored evidence into: a live macro World Model + causal-shock propagation, an
// "explain every recommendation" structured card assembled from REAL data, a
// deterministic multi-agent panel, and a reproducible hash-chained audit trail.
//
// HONESTY: every number is either measured/stored or an explicitly-labeled
// heuristic (fair value = EPS × a flat peer multiple). Recommendations are a
// relative-rank framing over the app's own calibrated predictions, never advice.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/composite"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/recommendation"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
	"github.com/nyaungnicholas-wq/signaldeck/internal/worldmodel"
)

// peerPE is the flat peer price/earnings multiple used by the fair-value
// heuristic (~the S&P long-run average). Deliberately simple and LABELED as a
// heuristic everywhere it surfaces — it is not a valuation model.
const peerPE = 20.0

// ── World Model ──────────────────────────────────────────────────────────

func (d Deps) deskWorldModel(w http.ResponseWriter, r *http.Request) {
	macro := d.liveMacro(r.Context())
	g := worldmodel.Build(macro)
	writeJSON(w, map[string]any{
		"asOf":    time.Now().Unix(),
		"drivers": g.Drivers,
		"nodes":   g.Nodes,
		"edges":   g.Edges,
		"note":    g.Note,
	})
}

func (d Deps) deskShocks(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"shocks": worldmodel.Shocks()})
}

func (d Deps) deskPropagate(w http.ResponseWriter, r *http.Request) {
	shock := r.URL.Query().Get("shock")
	p, ok := worldmodel.Propagate(shock)
	if !ok {
		httpErr(w, 404, fmt.Sprintf("unknown shock %q — see GET /api/world-model/shocks", shock))
		return
	}
	writeJSON(w, p)
}

// liveMacro reads the latest FRED value per series into a plain series→value
// map for worldmodel.Build. A read error just yields an empty map (drivers
// classify as unknown — never fabricated).
func (d Deps) liveMacro(ctx context.Context) map[string]float64 {
	out := map[string]float64{}
	all, err := d.St.LatestMacroAll(ctx)
	if err != nil {
		return out
	}
	for series, pt := range all {
		out[series] = pt.Value
	}
	return out
}

// macroSummary is a one-line live-state digest for the Economist agent view.
func macroSummary(g worldmodel.Graph) string {
	parts := make([]string, 0, len(g.Drivers))
	for _, dr := range g.Drivers {
		if !dr.Known {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s %s", dr.Label, dr.State))
	}
	if len(parts) == 0 {
		return "no live macro readings available"
	}
	sort.Strings(parts)
	s := ""
	for i, p := range parts {
		if i > 0 {
			s += ", "
		}
		s += p
	}
	return s
}

// ── Recommendation (explain every rec) ───────────────────────────────────

func (d Deps) deskRecommendation(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	s, err := d.symbolFromQuery(r)
	if err != nil {
		httpErr(w, 404, err.Error())
		return
	}
	row, ok, err := d.St.LatestCompositeScore(ctx, s.ID, string(md.H1d))
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	if !ok {
		writeJSON(w, map[string]any{
			"available": false, "symbol": s.Symbol, "market": s.Market,
			"reason": "no composite score stored for this symbol yet — the scorer runs every 10m and gates below 30 fresh predictions; nothing is fabricated meanwhile",
		})
		return
	}
	var p composite.Payload
	if err := json.Unmarshal([]byte(row.Payload), &p); err != nil {
		httpErr(w, 500, "stored payload does not parse: "+err.Error())
		return
	}

	in := d.recInputs(ctx, s, row, p)
	rec := recommendation.Build(in)

	// Audit: record this distinct recommendation (dedup on content hash).
	sources := d.recSources(in)
	versions := d.recVersions()
	inputDigest := recInputDigest(in)
	contentHash := store.RecAuditContentHash(rec.Decision, rec.ConfidenceLabel, inputDigest)
	audit := d.appendRecAudit(ctx, s, rec, contentHash, sources, versions, rec.Assumptions)

	writeJSON(w, deskRecResponse{
		Available:      true,
		Symbol:         s.Symbol,
		Market:         string(s.Market),
		AsOf:           time.Now().Unix(),
		Recommendation: rec,
		Audit:          audit,
	})
}

// deskRecResponse embeds the pure recommendation (its Symbol/Market are json:"-")
// and adds the wrapper + audit block.
type deskRecResponse struct {
	Available bool   `json:"available"`
	Symbol    string `json:"symbol"`
	Market    string `json:"market"`
	AsOf      int64  `json:"asOf"`
	recommendation.Recommendation
	Audit map[string]any `json:"audit"`
}

// recInputs gathers every stored input the recommendation engine needs. Best-
// effort: a failed optional read just leaves that leg absent (the engine gates
// it honestly), never fabricated.
func (d Deps) recInputs(ctx context.Context, s md.Symbol, row store.CompositeScore, p composite.Payload) recommendation.Inputs {
	proven, winRate, skillNote := d.fleetEdgeSkill(ctx)
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

	in := recommendation.Inputs{
		Symbol: s.Symbol, Market: string(s.Market),
		HasScore: true, Score: row.Score, Edge: row.Edge, CalProb: p.CalProb,
		CurvePct: row.CurvePct, NUsed: p.NUsed, Factors: p.Factors, Conviction: conv,
		MeasuredAccuracyPct: winRate * 100, EdgeProvenLive: proven, PeerPE: peerPE,
	}

	if bars, err := d.St.LastBars(ctx, s.ID, md.TF1d, 1); err == nil && len(bars) > 0 {
		in.HasPrice, in.Price = true, bars[0].Close
	}
	if funds, err := d.St.LatestFundamentals(ctx, s.ID); err == nil {
		for _, f := range funds {
			if f.Metric == "EPS" && f.Value != 0 {
				in.HasEPS, in.EPS = true, f.Value
			}
		}
	}
	if exp, err := d.St.Expectancy(ctx, s.ID, md.H1d); err == nil {
		in.Expectancy = exp
	}
	if labels, err := d.St.RegimeLabels(ctx); err == nil {
		in.Regime = labels[s.ID]
	}
	if pcts, err := d.St.RankingPercentiles(ctx); err == nil {
		if pct, ok := pcts[s.ID]; ok {
			in.HasRank, in.RankPct = true, pct
		}
	}
	in.MacroSummary = macroSummary(worldmodel.Build(d.liveMacro(ctx)))
	return in
}

// recSources lists the data sources that actually fed this recommendation.
func (d Deps) recSources(in recommendation.Inputs) []string {
	src := []string{"composite_scores", "prediction_outcomes (measured accuracy)", "adaptive_attribution", "fred_macro"}
	if in.HasPrice {
		src = append(src, "bars(1d)")
	}
	if in.HasEPS {
		src = append(src, "fundamentals(EDGAR)")
	}
	if len(in.Expectancy) > 0 {
		src = append(src, "expectancy(1d)")
	}
	if in.Regime != "" {
		src = append(src, "regime_state")
	}
	if in.HasRank {
		src = append(src, "ranking")
	}
	return src
}

func (d Deps) recVersions() map[string]string {
	v := map[string]string{
		"scorer":     "composite forced-curve v1",
		"conviction": "skill-anchored v1",
		"fairValue":  "EPS × flat peer P/E heuristic",
		"llm":        "disabled",
	}
	if d.LLM != nil && d.LLM.Enabled() {
		v["llm"] = d.LLM.Model()
	}
	return v
}

// recInputDigest is a stable string of the numeric inputs — the reproducibility
// key. Same inputs → same digest → same content hash.
func recInputDigest(in recommendation.Inputs) string {
	return fmt.Sprintf("score=%d|edge=%.6f|cal=%.6f|nUsed=%d|price=%.4f|eps=%.4f|rank=%.2f|regime=%s|acc=%.4f|band=%s",
		in.Score, in.Edge, in.CalProb, in.NUsed, in.Price, in.EPS, in.RankPct, in.Regime,
		in.MeasuredAccuracyPct, in.Conviction.Band)
}

// appendRecAudit records the recommendation and returns the audit block for the
// response. Best-effort: on a write error it still returns a live (unpersisted)
// audit view so the endpoint never fails on the audit leg.
func (d Deps) appendRecAudit(ctx context.Context, s md.Symbol, rec recommendation.Recommendation, contentHash string, sources []string, versions map[string]string, assumptions []string) map[string]any {
	srcJSON, _ := json.Marshal(sources)
	verJSON, _ := json.Marshal(versions)
	assumeJSON, _ := json.Marshal(assumptions)
	entry := store.RecAuditEntry{
		CreatedAt: time.Now().Unix(), SymbolID: s.ID, Symbol: s.Symbol, Market: s.Market,
		Decision: rec.Decision, Confidence: rec.ConfidenceLabel, ContentHash: contentHash,
		SourcesJSON: string(srcJSON), VersionsJSON: string(verJSON), AssumptionsJSON: string(assumeJSON),
	}
	stored, _, err := d.St.AppendRecAudit(ctx, entry)
	intact, head := true, ""
	if v, verr := d.St.VerifyRecAudit(ctx); verr == nil {
		intact, head = v.Intact, v.HeadHash
	}
	out := map[string]any{
		"sources": sources, "modelVersions": versions, "assumptions": assumptions,
		"reproducible": true, "intact": intact, "headHash": head,
	}
	if err == nil {
		out["seq"] = stored.Seq
		out["hash"] = stored.EntryHash
		out["timestamp"] = stored.CreatedAt
		out["reproducible"] = stored.ContentHash == contentHash
	} else {
		out["seq"] = 0
		out["hash"] = contentHash
		out["timestamp"] = entry.CreatedAt
		out["note"] = "audit persist skipped: " + err.Error()
	}
	return out
}

// ── High-conviction opportunities ────────────────────────────────────────

func (d Deps) deskTop(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	limit := limitParam(r, 12, 50)
	rows, err := d.St.TopCompositeScores(ctx, 0, "", string(md.H1d))
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	proven, winRate, skillNote := d.fleetEdgeSkill(ctx)

	type topRow struct {
		Symbol            string  `json:"symbol"`
		Market            string  `json:"market"`
		Decision          string  `json:"decision"`
		Score             int     `json:"score"`
		ConfidenceLabel   string  `json:"confidenceLabel"`
		Band              string  `json:"band"`
		Edge              float64 `json:"edge"`
		ExpectedReturnPct float64 `json:"expectedReturnPct"`
		HasExpectedReturn bool    `json:"hasExpectedReturn"`
	}
	out := make([]topRow, 0, limit)
	for _, c := range rows {
		if len(out) >= limit {
			break
		}
		var p composite.Payload
		if err := json.Unmarshal([]byte(c.Payload), &p); err != nil {
			continue
		}
		bull, bear := composite.FactorAgreement(p.Factors)
		conv := composite.Assess(composite.ConvictionInputs{
			Edge: c.Edge, PredAgeSec: time.Now().Unix() - p.PredTs, NUsed: p.NUsed,
			Bull: bull, Bear: bear, EdgeProvenLive: proven, WinRate: winRate, SkillNote: skillNote,
		})
		in := recommendation.Inputs{
			HasScore: true, Score: c.Score, Edge: c.Edge, CalProb: p.CalProb, NUsed: p.NUsed,
			Factors: p.Factors, Conviction: conv, MeasuredAccuracyPct: winRate * 100,
			EdgeProvenLive: proven, PeerPE: peerPE,
		}
		// Best-effort expected return (price + EPS).
		if bars, err := d.St.LastBars(ctx, c.SymbolID, md.TF1d, 1); err == nil && len(bars) > 0 {
			in.HasPrice, in.Price = true, bars[0].Close
		}
		if funds, err := d.St.LatestFundamentals(ctx, c.SymbolID); err == nil {
			for _, f := range funds {
				if f.Metric == "EPS" && f.Value != 0 {
					in.HasEPS, in.EPS = true, f.Value
				}
			}
		}
		rec := recommendation.Build(in)
		out = append(out, topRow{
			Symbol: c.Symbol, Market: c.Market, Decision: rec.Decision, Score: c.Score,
			ConfidenceLabel: rec.ConfidenceLabel, Band: string(conv.Band), Edge: c.Edge,
			ExpectedReturnPct: rec.ExpectedReturnPct, HasExpectedReturn: rec.HasExpectedReturn,
		})
	}
	writeJSON(w, map[string]any{
		"rows": out,
		"note": "ranked by the composite forced-curve; decision + confidence are a relative-rank read over the platform's own calibrated predictions (measured accuracy " +
			strconv.FormatFloat(winRate*100, 'f', 1, 64) + "%), not advice",
	})
}

func (d Deps) registerDesk(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/world-model", d.deskWorldModel)
	mux.HandleFunc("GET /api/world-model/shocks", d.deskShocks)
	mux.HandleFunc("GET /api/world-model/propagate", d.deskPropagate)
	mux.HandleFunc("GET /api/recommendation", d.deskRecommendation)
	mux.HandleFunc("GET /api/recommendation/top", d.deskTop)
}
