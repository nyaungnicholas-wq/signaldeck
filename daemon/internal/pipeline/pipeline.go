// Package pipeline holds the intelligence workers that turn stored bars into
// scores, tendencies, and readable insights — the compute half of the in-app
// agent fleet (ingest lives in internal/ingest, housekeeping in
// internal/maintain).
package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/expectancy"
	"github.com/nyaungnicholas-wq/signaldeck/internal/insights"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/signals"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// dailyLookback / minuteLookback bound the bar windows the engines see.
const (
	dailyLookback  = 500
	minuteLookback = 25_000 // ~17 trading days of stock minutes; caps memory
)

// loadBars fetches the standard windows for one symbol.
func loadBars(ctx context.Context, st *store.Store, id int64) (daily, minute []md.Bar, err error) {
	daily, err = st.LastBars(ctx, id, md.TF1d, dailyLookback)
	if err != nil {
		return nil, nil, err
	}
	minute, err = st.LastBars(ctx, id, md.TF1m, minuteLookback)
	if err != nil {
		return nil, nil, err
	}
	return daily, minute, nil
}

// CurrentState returns the live expectancy state keys for a symbol — wired
// into the API so the symbol page can highlight "you are here".
func CurrentState(ctx context.Context, st *store.Store, symbolID int64) (map[md.Horizon]string, error) {
	daily, minute, err := loadBars(ctx, st, symbolID)
	if err != nil {
		return nil, err
	}
	return expectancy.CurrentStateKeys(daily, minute), nil
}

// ── SignalRunner ────────────────────────────────────────────────────────

// SignalRunner recomputes the Pressure Scores for every active symbol each
// minute. Every insert also seeds the score's outcome row, so the honesty
// backtest is fed at write time.
type SignalRunner struct {
	St *store.Store
}

// Name implements workers.Worker.
func (w *SignalRunner) Name() string { return "signal-runner" }

// Interval implements workers.Worker.
func (w *SignalRunner) Interval() time.Duration { return time.Minute }

// Run scores the streamed hot set (+ crypto) every minute, and the broad
// daily-only universe at most once per UTC day.
//
// CADENCE SPLIT (free-scale): hot-set symbols have live minute bars, so they
// are scored every run. The broad daily-only universe (~500 stock names,
// stream=0) only gets fresh DAILY bars once a day — scoring it every minute
// would write ~1440 identical rows per symbol per day and explode the derived
// tables (scores/score_outcomes), defeating the tiered-storage goal. So
// daily-only symbols are scored once per UTC day, gated by a meta cursor.
func (w *SignalRunner) Run(ctx context.Context) (string, error) {
	syms, err := w.St.ListSymbols(ctx, true)
	if err != nil {
		return "", err
	}
	// Live cadence: every 10m while the market is open, once/day closed (see
	// universecadence.go — universe bars are now minute-live, so more passes
	// mean fresh scores, not duplicate rows).
	doUniverse, universeCursor := universeDue(ctx, w.St, "signal_universe_day", time.Now())
	ts := time.Now().Truncate(time.Minute).Unix()
	scored, hotCount := 0, 0
	for _, s := range syms {
		hot := s.Market == md.Crypto || s.Stream
		if !hot && !doUniverse {
			continue // daily-only universe symbol already scored today
		}
		if hot {
			hotCount++
		}
		daily, minute, err := loadBars(ctx, w.St, s.ID)
		if err != nil {
			return "", fmt.Errorf("%s: %w", s.Symbol, err)
		}
		micro := signals.MicroInputs{}
		if s.Market == md.Crypto {
			now := time.Now().Unix()
			snaps, err := w.St.Snaps(ctx, s.ID, now-120, now+1, 121)
			if err != nil {
				return "", err
			}
			if n := len(snaps); n > 0 {
				last := snaps[n-1]
				micro.Ok = true
				micro.ImbSigned = last.ImbSigned
				if last.Mid > 0 {
					micro.WmidMinusMidBps = (last.WMid - last.Mid) / last.Mid * 10_000
				}
			}
		}
		for h, sc := range signals.ComputeScores(daily, minute, micro) {
			sc.SymbolID, sc.Ts, sc.Horizon = s.ID, ts, h
			if err := w.St.InsertScore(ctx, sc); err != nil {
				return "", fmt.Errorf("%s %s: %w", s.Symbol, h, err)
			}
			scored++
		}
	}
	if doUniverse {
		// Mark the daily universe pass done for today only after it succeeded,
		// so a mid-run error simply retries next minute.
		_ = w.St.SetMeta(ctx, "signal_universe_day", universeCursor)
	}
	return fmt.Sprintf("scored %d symbol-horizons (%d hot symbols%s)", scored, hotCount,
		map[bool]string{true: " + daily universe", false: ""}[doUniverse]), nil
}

// ── ExpectancyRunner ────────────────────────────────────────────────────

// ExpectancyRunner rebuilds the conditional forward-return tables hourly —
// the app's honest "prediction" layer, refreshed as history grows.
type ExpectancyRunner struct {
	St *store.Store
}

// Name implements workers.Worker.
func (w *ExpectancyRunner) Name() string { return "expectancy-runner" }

// Interval implements workers.Worker.
func (w *ExpectancyRunner) Interval() time.Duration { return time.Hour }

// Run rebuilds expectancy for all symbols (history is kept even for
// unsubscribed ones, so include inactive: their tables stay queryable).
func (w *ExpectancyRunner) Run(ctx context.Context) (string, error) {
	syms, err := w.St.ListSymbols(ctx, false)
	if err != nil {
		return "", err
	}
	tables := 0
	for _, s := range syms {
		daily, minute, err := loadBars(ctx, w.St, s.ID)
		if err != nil {
			return "", err
		}
		for h, rows := range expectancy.Build(daily, minute) {
			if err := w.St.ReplaceExpectancy(ctx, s.ID, h, rows); err != nil {
				return "", fmt.Errorf("%s %s: %w", s.Symbol, h, err)
			}
			tables++
		}
	}
	return fmt.Sprintf("rebuilt %d tendency tables over %d symbols", tables, len(syms)), nil
}

// ── InsightWriter ───────────────────────────────────────────────────────

// InsightWriter turns the latest scores + tendencies into plain-English
// insights every 15 minutes — deduplicated so the feed stays readable: a
// symbol only gets a new insight when its 1d verdict bucket changes, or
// every 6h regardless.
type InsightWriter struct {
	St *store.Store
}

// Name implements workers.Worker.
func (w *InsightWriter) Name() string { return "insight-writer" }

// Interval implements workers.Worker.
func (w *InsightWriter) Interval() time.Duration { return 15 * time.Minute }

func verdictBucket(score float64) string {
	switch {
	case score >= 0.5:
		return "strong-buy"
	case score >= 0.15:
		return "buy"
	case score > -0.15:
		return "balanced"
	case score > -0.5:
		return "sell"
	default:
		return "strong-sell"
	}
}

// Run composes per-symbol insights + one market brief.
func (w *InsightWriter) Run(ctx context.Context) (string, error) {
	syms, err := w.St.ListSymbols(ctx, true)
	if err != nil {
		return "", err
	}
	now := time.Now()
	written := 0
	var briefs []insights.MarketBrief

	for _, s := range syms {
		scores := map[md.Horizon]md.Score{}
		for _, h := range md.Horizons {
			if sc, ok, err := w.St.LatestScore(ctx, s.ID, h); err == nil && ok {
				scores[h] = sc
			}
		}
		sc1d, has1d := scores[md.H1d]
		if !has1d {
			continue // nothing to say without at least a daily read
		}

		daily, minute, err := loadBars(ctx, w.St, s.ID)
		if err != nil {
			return "", err
		}
		var lastClose, dayChange float64
		if n := len(daily); n > 0 {
			lastClose = daily[n-1].Close
			if n > 1 && daily[n-2].Close != 0 {
				dayChange = (daily[n-1].Close/daily[n-2].Close - 1) * 100
			}
		}
		briefs = append(briefs, insights.MarketBrief{
			Symbol: s.Symbol, Market: s.Market, Score1d: sc1d.Score, DayChangePct: dayChange,
		})

		// Dedup gate: new verdict bucket OR 6h since the last insight.
		bucket := verdictBucket(sc1d.Score)
		metaKey := "insight_last_" + strconv.FormatInt(s.ID, 10)
		if prev, _ := w.St.GetMeta(ctx, metaKey); prev != "" {
			var last struct {
				Bucket string `json:"bucket"`
				Ts     int64  `json:"ts"`
			}
			if json.Unmarshal([]byte(prev), &last) == nil &&
				last.Bucket == bucket && now.Unix()-last.Ts < 6*3600 {
				continue
			}
		}

		states := expectancy.CurrentStateKeys(daily, minute)
		expect := map[md.Horizon]*md.Expectancy{}
		for _, h := range md.Horizons {
			rows, err := w.St.Expectancy(ctx, s.ID, h)
			if err != nil {
				return "", err
			}
			if row, ok := expectancy.Lookup(rows, states[h]); ok {
				row.Horizon = h
				expect[h] = &row
			}
		}
		stale := sc1d.Ts < now.Add(-30*time.Minute).Unix()
		in := insights.ComposeSymbol(insights.SymbolContext{
			Sym: s, LastClose: lastClose, DayChangePct: dayChange,
			Scores: scores, Expect: expect, StateKeys: states,
			Stale: stale, StaleFor: time.Duration(now.Unix()-sc1d.Ts) * time.Second,
		}, now)
		if err := w.St.InsertInsight(ctx, in); err != nil {
			return "", err
		}
		state, _ := json.Marshal(map[string]any{"bucket": bucket, "ts": now.Unix()})
		if err := w.St.SetMeta(ctx, metaKey, string(state)); err != nil {
			return "", err
		}
		written++
	}

	// Market brief: same dedup discipline via breadth bucket.
	if len(briefs) > 0 {
		positive := 0
		for _, b := range briefs {
			if b.Score1d > 0 {
				positive++
			}
		}
		bucket := fmt.Sprintf("%d/%d", positive, len(briefs))
		if prev, _ := w.St.GetMeta(ctx, "insight_last_market"); prev != bucket {
			if err := w.St.InsertInsight(ctx, insights.ComposeMarket(briefs, now)); err != nil {
				return "", err
			}
			if err := w.St.SetMeta(ctx, "insight_last_market", bucket); err != nil {
				return "", err
			}
			written++
		}
	}
	return fmt.Sprintf("wrote %d insights (%d symbols considered)", written, len(briefs)), nil
}
