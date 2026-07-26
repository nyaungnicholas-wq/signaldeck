package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/adaptive"
	"github.com/nyaungnicholas-wq/signaldeck/internal/ensemble"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
	"github.com/nyaungnicholas-wq/signaldeck/internal/symbolagent"
)

// symbolAgentMaxRows caps how many of a symbol's own labeled examples one pass
// consumes per horizon (newest first — the labeled set only grows).
const symbolAgentMaxRows = 5000

// PerSymbolLearner is the per-symbol specialization pass: for every active
// symbol it re-derives that symbol's OWN model (weights + calibration + skill
// + personality + honest tier) from the symbol's own resolved outcomes and
// upserts one cheap row per horizon. ONE worker, all symbols — a symbol's
// agent is a stored row, not a process/goroutine.
//
// Honesty: a symbol only graduates to the "personal" tier once it has
// >= symbolagent.MinPersonal of its own resolved rows for a horizon spanning
// >= symbolagent.MinPersonalDays DISTINCT UTC DAYS, AND that history yields
// weights that survive the adaptive panel's shrinkage and multiplicity
// correction; until then the row records the measured skill but stays on the
// global fallback tier ("still learning").
// The FIRST time a symbol+horizon graduates to personal, one insight is
// written.
type PerSymbolLearner struct {
	St *store.Store
}

func (w *PerSymbolLearner) Name() string            { return "per-symbol-learner" }
func (w *PerSymbolLearner) Interval() time.Duration { return time.Hour }

func (w *PerSymbolLearner) Run(ctx context.Context) (string, error) {
	syms, err := w.St.ListSymbols(ctx, true)
	if err != nil {
		return "", err
	}

	// Global context, loaded ONCE per pass (not per symbol): the learned
	// per-regime weights and each symbol's current regime label decide the
	// FALLBACK tier a still-learning symbol reports. Best-effort — absence just
	// means the fallback is the static prior.
	var learned adaptive.Weights
	if raw, err := w.St.GetMeta(ctx, adaptive.MetaKey); err == nil && raw != "" {
		if err := json.Unmarshal([]byte(raw), &learned); err != nil {
			learned = adaptive.Weights{}
		}
	}
	globalLearned := len(learned.Cells[adaptive.AllCell].Weights) > 0
	regimeLbls, err := w.St.RegimeLabels(ctx)
	if err != nil {
		regimeLbls = map[int64]string{}
	}

	now := time.Now().Unix()
	upserts, personalCount, graduated := 0, 0, 0
	for _, s := range syms {
		// Does the symbol's CURRENT regime cell yield global weights? (Used only
		// to label the fallback tier for a still-learning symbol.)
		_, gate := adaptive.Pick(learned, regimeLbls[s.ID])
		regimeLearned := gate == adaptive.GateLearnedRegime

		for _, h := range predHorizons {
			rows, err := w.St.LabeledFeaturesBySymbol(ctx, s.ID, h, symbolAgentMaxRows)
			if err != nil {
				return "", fmt.Errorf("labeled features %s %s: %w", s.Symbol, h, err)
			}

			examples := make([]adaptive.Example, 0, len(rows))
			rawPairs := make([]ensemble.Pair, 0, len(rows))
			for _, r := range rows {
				legs, regime := adaptive.FromVector(r.Vec)
				// Ts carries the UTC day both floors count. Without it a symbol
				// with 300 rows drawn from three market moves looks like 300
				// observations, which is how 1,045 of 1,050 symbols held a
				// personal model on ~12 days of evidence.
				examples = append(examples, adaptive.Example{
					Legs: legs, Regime: regime, Ts: r.Ts, Up: r.Up, FwdReturn: r.FwdReturn,
				})
				// Prequential calibration pairs: the raw blend prob this symbol
				// produced at prediction time vs the realized outcome. Each pair
				// was resolved AFTER its prob was computed — no point trains on
				// its own outcome (that outcome didn't exist when the prob was
				// made, and a live prediction is never in this set).
				if raw, ok := r.Vec["pred_raw"]; ok {
					rawPairs = append(rawPairs, ensemble.Pair{Pred: raw, Actual: float64(r.Up), Ts: r.Ts})
				}
			}

			m := symbolagent.Learn(examples, rawPairs, regimeLearned, globalLearned)

			// Was this symbol+horizon already personal? (To fire the graduation
			// insight exactly once.) Best-effort read.
			wasPersonal := false
			if prev, ok, err := w.St.SymbolModel(ctx, s.ID, h); err == nil && ok {
				wasPersonal = prev.Tier == symbolagent.TierPersonal
			}

			row, err := marshalSymbolModel(s.ID, h, m, now)
			if err != nil {
				return "", err
			}
			if err := w.St.UpsertSymbolModel(ctx, row); err != nil {
				return "", fmt.Errorf("upsert %s %s: %w", s.Symbol, h, err)
			}
			upserts++
			if m.Tier == symbolagent.TierPersonal {
				personalCount++
				if !wasPersonal {
					graduated++
					if err := w.St.InsertInsight(ctx, graduationInsight(s.ID, s.Symbol, string(h), m)); err != nil {
						return "", fmt.Errorf("insight: %w", err)
					}
				}
			}
		}
	}
	// The day floors are reported beside the count so a drop in `personal` is
	// legible as a change of UNIT, not a loss of data.
	return fmt.Sprintf("modeled %d symbol×horizon(s) over %d symbols; %d personal, %d newly graduated (personal needs %d rows over %d distinct days)",
		upserts, len(syms), personalCount, graduated,
		symbolagent.MinPersonal, symbolagent.MinPersonalDays), nil
}

// marshalSymbolModel serializes a learned model into a storable row. The JSON
// blob columns are owned by the symbolagent types, so store stays type-free.
func marshalSymbolModel(symbolID int64, h md.Horizon, m symbolagent.Model, ts int64) (store.SymbolModelRow, error) {
	weights := m.Weights
	if weights == nil {
		weights = map[string]float64{}
	}
	wb, err := json.Marshal(weights)
	if err != nil {
		return store.SymbolModelRow{}, err
	}
	cb, err := json.Marshal(m.Calibration)
	if err != nil {
		return store.SymbolModelRow{}, err
	}
	sb, err := json.Marshal(m.Skill)
	if err != nil {
		return store.SymbolModelRow{}, err
	}
	return store.SymbolModelRow{
		SymbolID:    symbolID,
		Horizon:     string(h),
		Weights:     string(wb),
		Calibration: string(cb),
		Skill:       string(sb),
		Personality: m.Personality,
		NSamples:    m.NSamples,
		Tier:        m.Tier,
		UpdatedTs:   ts,
	}, nil
}

// graduationInsight announces a symbol earning its own personal model.
func graduationInsight(symbolID int64, symbol, horizon string, m symbolagent.Model) md.Insight {
	data, _ := json.Marshal(map[string]any{
		"kind": "symbol_agent_graduated", "symbol": symbol, "horizon": horizon,
		"nSamples": m.NSamples, "nDays": m.NDays, "weights": m.Weights,
	})
	id := symbolID
	return md.Insight{
		Scope:    "symbol",
		SymbolID: &id,
		Symbol:   symbol,
		Ts:       time.Now().Unix(),
		Headline: fmt.Sprintf("%s (%s) now has its own agent", symbol, horizon),
		Body: fmt.Sprintf("%s reached %d distinct days of its own resolved %s outcomes (%d graded rows) — enough to trust a model learned from THIS symbol's behavior instead of the global one. %s",
			symbol, m.NDays, horizon, m.NSamples, m.Personality),
		Data: string(data),
	}
}
