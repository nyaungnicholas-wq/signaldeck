// CompositeScorer (SIGNALS-hub overhaul): the worker behind the per-symbol
// SignalScore. It consumes the LATEST STORED calibrated predictions (it never
// recomputes the ensemble), ranks their edges on a forced cross-sectional
// curve (internal/composite), builds the per-symbol evidence payload (factor
// tiles + additive ledger), and upserts composite_scores rows.
//
// CADENCE (the same free-scale split as the PredictionRunner): the streamed
// hot set + crypto is re-scored every run; the broad daily-only universe is
// scored once per UTC day via the composite_universe_day meta cursor. The
// forced curve itself is ALWAYS computed over the FULL cross-section of
// usable predictions — a rank against six hot names would be meaningless —
// only the row-persist cadence is split.
//
// HONESTY GATE: when fewer than composite.MinCurveN symbols have fresh,
// ungated predictions the WHOLE pass is gated: nothing is stored and the
// worker detail says why. A forced curve over a thin cross-section would
// manufacture 10s and 1s out of noise.
package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/adaptive"
	"github.com/nyaungnicholas-wq/signaldeck/internal/composite"
	"github.com/nyaungnicholas-wq/signaldeck/internal/ensemble"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// compositeMaxPredAge is how old a symbol's latest prediction may be (in
// seconds) and still enter the curve. The predictor covers the broad universe
// once per UTC day, so 3 days of slack tolerates weekends/outages while a
// symbol whose predictions have stopped drops out instead of being ranked on
// stale evidence.
const compositeMaxPredAge = 3 * 86400

// compositeInsiderWindow is the Form 4 lookback for the insiders factor.
const compositeInsiderWindow = 90 * 86400

// compositeBreakoutWindow is the recency window for the breakout factor.
const compositeBreakoutWindow = 7 * 86400

// CompositeScorer computes and persists the composite SignalScore rows for ONE
// horizon. Horizon="" defaults to 1d (back-compat with the original single-
// horizon worker); register a second instance with Horizon:md.H1w to also
// score the weekly cross-section — momentum/estimate-revision edges are more
// plausible at 1w than the near-efficient 1d coin flip (see EDGE_PLAN.md).
type CompositeScorer struct {
	St      *store.Store
	Horizon md.Horizon // "" -> md.H1d
}

func (w *CompositeScorer) horizon() md.Horizon {
	if w.Horizon == "" {
		return md.H1d
	}
	return w.Horizon
}

func (w *CompositeScorer) Name() string {
	if w.horizon() == md.H1w {
		return "composite-scorer-1w"
	}
	return "composite-scorer"
}
func (w *CompositeScorer) Interval() time.Duration { return 10 * time.Minute }

func (w *CompositeScorer) Run(ctx context.Context) (string, error) {
	h := w.horizon()
	preds, err := w.St.LatestPredictionsForScoring(ctx, h)
	if err != nil {
		return "", err
	}
	now := time.Now().Unix()

	// Usable = fresh prediction that actually blended something (nUsed >= 1)
	// and whose components parse. Everything else is excluded up front so the
	// curve ranks only real evidence.
	type scored struct {
		pred store.PredForScoring
		comp ensemble.Components
	}
	eligible := make([]scored, 0, len(preds))
	parseErrs := 0
	for _, p := range preds {
		if p.NUsed < 1 || now-p.Ts > compositeMaxPredAge {
			continue
		}
		var c ensemble.Components
		if err := json.Unmarshal([]byte(p.Components), &c); err != nil {
			parseErrs++
			continue
		}
		eligible = append(eligible, scored{pred: p, comp: c})
	}

	// WHOLE-PASS HONESTY GATE: no forced curve below MinCurveN symbols.
	if len(eligible) < composite.MinCurveN {
		detail := fmt.Sprintf(
			"gated: only %d symbol(s) with fresh ungated predictions (<%d) — a forced 1-10 curve over a thin cross-section would fabricate extremes; no scores stored",
			len(eligible), composite.MinCurveN)
		if parseErrs > 0 {
			detail += fmt.Sprintf(" (%d component blob(s) unparsable)", parseErrs)
		}
		return detail, nil
	}

	edges := make([]composite.Edge, 0, len(eligible))
	for _, e := range eligible {
		edges = append(edges, composite.Edge{
			SymbolID: e.pred.SymbolID,
			Symbol:   e.pred.Symbol,
			Edge:     e.pred.CalProb - 0.5,
		})
	}
	curve, ok := composite.ForcedCurve(edges)
	if !ok { // unreachable given the gate above, but never score without a curve
		return "gated: forced curve refused the cross-section", nil
	}
	curveByID := make(map[int64]composite.CurveScore, len(curve))
	for _, c := range curve {
		curveByID[c.SymbolID] = c
	}

	// CADENCE SPLIT (live-everything wave): hot set + crypto every run; the
	// broad universe every 10m market-open / once per UTC day closed (see
	// universecadence.go). Same meta-cursor pattern as the PredictionRunner.
	universeCadenceKey := "composite_universe_day"
	if h == md.H1w {
		universeCadenceKey = "composite_universe_day_1w"
	}
	doUniverse, universeCursor := universeDue(ctx, w.St, universeCadenceKey, time.Now())

	// One-query-per-fleet context (best-effort, same as the PredictionRunner:
	// an error only means those factors gate as absent this pass).
	regimeLbls, err := w.St.RegimeLabels(ctx)
	if err != nil {
		regimeLbls = map[int64]string{}
	}
	hmmLbls, err := w.St.HMMRegimeLabels(ctx)
	if err != nil {
		hmmLbls = map[int64]string{}
	}
	rankPcts, err := w.St.RankingPercentiles(ctx)
	if err != nil {
		rankPcts = map[int64]float64{}
	}
	// Adaptive attribution, loaded ONCE per run — the factor tiles' skill
	// chips (hit-rate/IC/n per leg). Absent/invalid meta means no chips,
	// never invented ones.
	var learned adaptive.Weights
	if raw, err := w.St.GetMeta(ctx, adaptive.MetaKey); err == nil && raw != "" {
		if err := json.Unmarshal([]byte(raw), &learned); err != nil {
			learned = adaptive.Weights{}
		}
	}

	// External TradingView-rating skill, MEASURED once per run (pooled across
	// symbols): the tvrating factor stays context-only until this IC/N clears
	// the gate. Best-effort — a read error just leaves the factor gated as
	// unmeasured, never fabricates skill.
	tvSkill, err := w.St.TVRatingSkill(ctx)
	if err != nil {
		tvSkill = store.TVSkill{}
	}

	ts := time.Now().Truncate(time.Minute).Unix()
	n := 0
	for _, e := range eligible {
		p := e.pred
		hot := p.Market == md.Crypto || p.Stream
		if !hot && !doUniverse {
			continue // daily-only universe symbol already scored today
		}
		cs, ok := curveByID[p.SymbolID]
		if !ok {
			continue // defensive: every eligible symbol is on the curve
		}

		in := composite.Inputs{
			Components:     e.comp,
			RegimeLabel:    regimeLbls[p.SymbolID],
			HMMRegimeLabel: hmmLbls[p.SymbolID],
		}
		if pct, ok := rankPcts[p.SymbolID]; ok {
			in.RankPct = &pct
		}
		// Skill chips from the symbol's regime cell, falling back to the
		// pooled "all" cell — the same tiering adaptive.Pick applies to
		// weights, here applied to the EVIDENCE shown on the tiles. The cell
		// key has to match the one the PredictionRunner learned under, or the
		// chips would describe a different cell than the weights came from.
		cell, ok := learned.Cells[cellKey(regimeLbls[p.SymbolID], hmmLbls[p.SymbolID])]
		if !ok || len(cell.LegN) == 0 {
			cell = learned.Cells[adaptive.AllCell]
		}
		in.SkillHitRates, in.SkillICs, in.SkillLegN = cell.HitRates, cell.IC, cell.LegN

		// Insiders: open-market Form 4 net dollars over the last 90d.
		buys, sells, nBuys, nSells, err := w.St.InsiderNetActivity(ctx, p.SymbolID, now-compositeInsiderWindow)
		if err != nil {
			return "", err
		}
		in.InsiderBuys, in.InsiderSells = buys, sells
		in.InsiderNBuys, in.InsiderNSells = nBuys, nSells

		// Short-volume ratios (stocks only — Reg SHO is an equity dataset).
		if p.Market == md.Stocks {
			series, err := w.St.ShortVolumeSeries(ctx, p.SymbolID, 30)
			if err != nil {
				return "", err
			}
			for _, r := range series {
				in.ShortRatios = append(in.ShortRatios, r.ShortPct)
			}
		}

		// Most recent breakout, only within the 7d window.
		if b, ok, err := w.St.LatestBreakoutFor(ctx, p.SymbolID); err != nil {
			return "", err
		} else if ok && now-b.Ts <= compositeBreakoutWindow {
			in.Breakout = &composite.BreakoutInfo{
				Kind:    b.Kind,
				Detail:  b.Detail,
				AgeDays: float64(now-b.Ts) / 86400,
			}
		}

		// External TradingView rating (the 12th factor). The pooled measured
		// skill (tvSkill) is the SAME for every symbol; BuildFactors applies the
		// per-tile gate. Absent rating ⇒ the tile gates as absent.
		if tv, ok := w.St.LatestTVRating(ctx, p.SymbolID); ok {
			in.TVRating = &composite.TVRatingInfo{RecoAll: tv.RecoAll, Label: tv.Label}
			in.TVSkillHitRate, in.TVSkillIC, in.TVSkillN = tvSkill.HitRate, tvSkill.IC, tvSkill.N
		}

		payload := composite.Payload{
			Horizon: string(h),
			Edge:    p.CalProb - 0.5,
			RawProb: p.RawProb,
			CalProb: p.CalProb,
			NUsed:   p.NUsed,
			PredTs:  p.Ts,
			Factors: composite.BuildFactors(in),
			Ledger:  composite.BuildLedger(e.comp, p.RawProb, p.CalProb),
		}
		blob, err := json.Marshal(payload)
		if err != nil {
			return "", err
		}
		if err := w.St.UpsertCompositeScore(ctx, store.CompositeScore{
			SymbolID: p.SymbolID,
			Ts:       ts,
			Horizon:  string(h),
			Score:    cs.Score,
			CurvePct: cs.Pct,
			Edge:     p.CalProb - 0.5,
			Payload:  string(blob),
		}); err != nil {
			return "", err
		}
		n++
	}
	if doUniverse {
		// Only after a clean full pass, so a mid-run error retries next tick.
		_ = w.St.SetMeta(ctx, universeCadenceKey, universeCursor)
	}
	detail := fmt.Sprintf("scored %d symbol(s) on a %d-symbol forced curve (universe pass: %v)", n, len(eligible), doUniverse)
	if parseErrs > 0 {
		detail += fmt.Sprintf("; %d component blob(s) unparsable — excluded", parseErrs)
	}
	return detail, nil
}
