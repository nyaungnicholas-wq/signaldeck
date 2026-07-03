package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/breakout"
	"github.com/nyaungnicholas-wq/signaldeck/internal/ensemble"
	"github.com/nyaungnicholas-wq/signaldeck/internal/expectancy"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/ranking"
	"github.com/nyaungnicholas-wq/signaldeck/internal/regime"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// predHorizons are the horizons the ensemble predicts (forecast + expectancy
// both cover these).
var predHorizons = []md.Horizon{md.H1d, md.H1w}

func horizonSecs(h md.Horizon) int64 {
	if h == md.H1w {
		return 7 * 86400
	}
	return 86400
}

// ── PredictionRunner: the calibrated ensemble ───────────────────────────

// PredictionRunner fuses the pressure score, expectancy tendency, and the
// backtested forecast into ONE probability, then CALIBRATES it against the
// symbol's own realized history — the flagship honest prediction.
type PredictionRunner struct {
	St *store.Store
}

func (w *PredictionRunner) Name() string            { return "prediction-runner" }
func (w *PredictionRunner) Interval() time.Duration { return 10 * time.Minute }

func (w *PredictionRunner) Run(ctx context.Context) (string, error) {
	syms, err := w.St.ListSymbols(ctx, true)
	if err != nil {
		return "", err
	}
	ts := time.Now().Truncate(time.Minute).Unix()
	n := 0
	for _, s := range syms {
		daily, minute, err := loadBars(ctx, w.St, s.ID)
		if err != nil {
			return "", err
		}
		states := expectancy.CurrentStateKeys(daily, minute)
		forecasts, err := w.St.Forecasts(ctx, s.ID)
		if err != nil {
			return "", err
		}
		for _, h := range predHorizons {
			sc, ok, err := w.St.LatestScore(ctx, s.ID, h)
			if err != nil {
				return "", err
			}
			if !ok {
				continue
			}
			c := ensemble.Components{PressureScore: sc.Score}
			// Expectancy hit rate for the current state.
			if rows, err := w.St.Expectancy(ctx, s.ID, h); err == nil {
				if row, ok := expectancy.Lookup(rows, states[h]); ok {
					hr := row.HitRate
					c.ExpectancyHitRate = &hr
				}
			}
			// Forecast prob + lift (lift gates whether it is trusted).
			for _, f := range forecasts {
				if f.Horizon == h {
					p, l := f.Prob, f.Lift
					c.ForecastProb, c.ForecastLift = &p, &l
				}
			}
			raw, nUsed := ensemble.RawProbability(c)
			cal := raw
			if probs, ups, err := w.St.ResolvedPredictionPairs(ctx, h, 3000); err == nil && len(probs) > 0 {
				pairs := make([]ensemble.Pair, len(probs))
				for i := range probs {
					pairs[i] = ensemble.Pair{Pred: probs[i], Actual: ups[i]}
				}
				if mapFn, calibrated := ensemble.Calibrate(pairs); calibrated {
					cal = mapFn(raw)
				}
			}
			comps, _ := json.Marshal(c)
			if err := w.St.UpsertPrediction(ctx, store.Prediction{
				SymbolID: s.ID, Horizon: h, Ts: ts,
				RawProb: raw, CalProb: cal, NUsed: nUsed, Components: string(comps),
			}); err != nil {
				return "", err
			}
			n++
		}
	}
	return fmt.Sprintf("wrote %d predictions over %d symbols", n, len(syms)), nil
}

// PredictionResolver grades past predictions (feeds the calibration curve).
type PredictionResolver struct {
	St *store.Store
}

func (w *PredictionResolver) Name() string            { return "prediction-resolver" }
func (w *PredictionResolver) Interval() time.Duration { return 10 * time.Minute }

func (w *PredictionResolver) Run(ctx context.Context) (string, error) {
	now := time.Now().Unix()
	resolved := 0
	for _, h := range predHorizons {
		pending, err := w.St.UnresolvedPredictions(ctx, h, now-horizonSecs(h), 1500)
		if err != nil {
			return "", err
		}
		for _, p := range pending {
			base, okB, err := w.St.BarAtOrBefore(ctx, p.SymbolID, md.TF1d, p.Ts)
			if err != nil {
				return "", err
			}
			if !okB {
				continue
			}
			target := base.Ts + horizonSecs(h)
			if now < target {
				continue
			}
			fwd, okF, err := w.St.BarAtOrAfter(ctx, p.SymbolID, md.TF1d, target)
			if err != nil {
				return "", err
			}
			if !okF || base.Close <= 0 || fwd.Ts-target > 3*horizonSecs(h) {
				continue
			}
			if err := w.St.ResolvePrediction(ctx, p.SymbolID, h, p.Ts, fwd.Close/base.Close-1); err != nil {
				return "", err
			}
			resolved++
		}
	}
	return fmt.Sprintf("resolved %d predictions", resolved), nil
}

// ── RegimeRunner: label + change detection ──────────────────────────────

type RegimeRunner struct {
	St *store.Store
}

func (w *RegimeRunner) Name() string            { return "regime-runner" }
func (w *RegimeRunner) Interval() time.Duration { return 30 * time.Minute }

func (w *RegimeRunner) Run(ctx context.Context) (string, error) {
	syms, err := w.St.ListSymbols(ctx, true)
	if err != nil {
		return "", err
	}
	n, changes := 0, 0
	for _, s := range syms {
		daily, err := w.St.LastBars(ctx, s.ID, md.TF1d, dailyLookback)
		if err != nil {
			return "", err
		}
		st, ok := regime.Classify(daily)
		if !ok {
			continue
		}
		ts := daily[len(daily)-1].Ts
		if err := w.St.UpsertRegime(ctx, s.ID, ts, string(st.Label), st.Strength, st.Note); err != nil {
			return "", err
		}
		n++
	}
	// Count recent changes surfaced this pass (informational).
	if cs, err := w.St.RecentRegimeChanges(ctx, 5); err == nil {
		changes = len(cs)
	}
	return fmt.Sprintf("classified %d symbols (%d recent changes on record)", n, changes), nil
}

// ── RankingRunner: cross-sectional relative strength ────────────────────

type RankingRunner struct {
	St *store.Store
}

func (w *RankingRunner) Name() string            { return "ranking-runner" }
func (w *RankingRunner) Interval() time.Duration { return time.Hour }

func (w *RankingRunner) Run(ctx context.Context) (string, error) {
	syms, err := w.St.ListSymbols(ctx, true)
	if err != nil {
		return "", err
	}
	var metrics []ranking.Metric
	idBySym := map[string]int64{}
	for _, s := range syms {
		daily, err := w.St.LastBars(ctx, s.ID, md.TF1d, dailyLookback)
		if err != nil {
			return "", err
		}
		m, ok := ranking.FromBars(s.Symbol, daily)
		if !ok {
			continue
		}
		metrics = append(metrics, m)
		idBySym[s.Symbol] = s.ID
	}
	if len(metrics) < 2 {
		return "not enough symbols to rank", nil
	}
	ranked := ranking.RelativeStrength(metrics)
	ts := time.Now().Truncate(time.Minute).Unix()
	rows := make([]struct {
		SymbolID     int64
		Score        float64
		Rank         int
		Ret1M, Ret3M float64
	}, 0, len(ranked))
	for _, r := range ranked {
		rows = append(rows, struct {
			SymbolID     int64
			Score        float64
			Rank         int
			Ret1M, Ret3M float64
		}{idBySym[r.Symbol], r.Score, r.Rank, r.Return1M, r.Return3M})
	}
	if err := w.St.ReplaceRanking(ctx, ts, rows); err != nil {
		return "", err
	}
	return fmt.Sprintf("ranked %d symbols", len(ranked)), nil
}

// ── BreakoutRunner: trend-creation events + correlation breaks ──────────

type BreakoutRunner struct {
	St *store.Store
}

func (w *BreakoutRunner) Name() string            { return "breakout-runner" }
func (w *BreakoutRunner) Interval() time.Duration { return 15 * time.Minute }

func (w *BreakoutRunner) Run(ctx context.Context) (string, error) {
	syms, err := w.St.ListSymbols(ctx, true)
	if err != nil {
		return "", err
	}
	inserted := 0
	var series []breakout.Series
	for _, s := range syms {
		daily, err := w.St.LastBars(ctx, s.ID, md.TF1d, dailyLookback)
		if err != nil {
			return "", err
		}
		closes := make([]float64, len(daily))
		tss := make([]int64, len(daily))
		for i, b := range daily {
			closes[i] = b.Close
			tss[i] = b.Ts
		}
		series = append(series, breakout.Series{Symbol: s.Symbol, Closes: closes, Ts: tss})
		for _, sig := range breakout.Detect(daily) {
			// Dedup: skip if we already logged this kind at/after this ts.
			last, err := w.St.LastBreakoutTs(ctx, s.ID, sig.Kind)
			if err != nil {
				return "", err
			}
			if last >= sig.Ts {
				continue
			}
			sid := s.ID
			if err := w.St.InsertBreakout(ctx, &sid, sig.Ts, sig.Kind, sig.Detail, sig.Strength); err != nil {
				return "", err
			}
			inserted++
		}
	}
	// Correlation breaks across the whole watchlist (recent 20d vs base 90d).
	// Throttle to every ~6h so the feed doesn't repeat the same pair each pass.
	now := time.Now()
	last, _ := w.St.GetMeta(ctx, "corr_break_last")
	lastTs := int64(0)
	fmt.Sscanf(last, "%d", &lastTs)
	if now.Unix()-lastTs > 6*3600 {
		for _, b := range breakout.CorrelationBreaks(series, 20, 90) {
			detail := fmt.Sprintf("%s vs %s: recent r=%.2f, base r=%.2f (change %.2f)", b.A, b.B, b.RecentR, b.BaseR, b.Delta)
			if err := w.St.InsertBreakout(ctx, nil, now.Unix(), "correlation_break", detail, b.Delta); err != nil {
				return "", err
			}
			inserted++
		}
		_ = w.St.SetMeta(ctx, "corr_break_last", fmt.Sprintf("%d", now.Unix()))
	}
	return fmt.Sprintf("%d event(s) logged", inserted), nil
}
