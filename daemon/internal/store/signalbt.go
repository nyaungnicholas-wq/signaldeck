// STAGE 5 — own-signal backtester data assembly.
//
// SignalBacktestObs assembles the labeled feature store into the flat
// observation set the internal/signalbt engine grades OUT OF SAMPLE: each
// resolved prediction's CALIBRATED signal (the prob the pipeline actually
// emitted), joined to the realized forward returns at the primary horizon AND a
// set of additional lags for the IC-decay curve.
//
// No lookahead: the primary forward return is the already-RESOLVED fwd_return
// (only resolved, non-voided rows are joined, exactly like LabeledFeatures). The
// extra-lag forward returns are recomputed from realized DAILY bars that are
// strictly LATER than the prediction bar — the same close-to-forward-close
// convention the PredictionResolver uses, using trading-day (bar-index) offsets
// so a lag is "L bars forward", never a wall-clock window that could straddle a
// gap. A lag whose forward bar does not exist yet is simply absent (the engine
// tolerates missing lags), so a still-open longer horizon never fabricates a
// return.
package store

import (
	"context"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// SignalBTObs is one assembled observation for the own-signal backtester: the
// calibrated signal a prediction emitted at Ts for a symbol, plus realized
// forward returns keyed by trading-day lag. Mirrors signalbt.Observation but
// lives in store so the store stays free of a dependency on the engine package
// (the API handler maps this to signalbt.Observation).
type SignalBTObs struct {
	SymbolID int64
	Ts       int64
	Signal   float64         // calibrated P(up) emitted at Ts (prediction_outcomes.prob)
	FwdByLag map[int]float64 // lag (trading-day bars) -> realized forward return
}

// primaryLagForHorizon returns the primary forward window in trading-day BARS
// for a horizon, matching md.BarsPerHorizon's daily-bar count (1d→1, 1w→5).
// Only daily-graded horizons are supported here (the backtester replays the
// daily feature store), so an unknown/1h horizon maps to 1.
func primaryLagForHorizon(h md.Horizon) int {
	if h == md.H1w {
		return 5
	}
	return 1
}

// SignalBacktestObs builds the observation set for one horizon's own-signal
// backtest. It joins RESOLVED prediction outcomes to their realized forward
// returns and augments each with extra-lag forward returns computed from the
// symbol's realized daily bars.
//
// limit caps the number of resolved outcomes considered (newest first, like the
// other feature-store joins). extraLags are additional trading-day lags for the
// IC-decay curve (the primary lag is added automatically). The returned slice is
// NOT deduped or sorted — the engine's Backtest collapses to the independent
// (symbol, UTC-day) set and sorts. A symbol with no daily bars still yields its
// primary-lag observation (from the stored fwd_return); only the extra lags need
// the bar series.
func (s *Store) SignalBacktestObs(ctx context.Context, h md.Horizon, extraLags []int, limit int) ([]SignalBTObs, error) {
	primary := primaryLagForHorizon(h)

	// The resolved (signal, primary-fwd) pairs — the labeled set, resolved-only,
	// no lookahead (same join as LabeledFeatures but we read prob directly from
	// prediction_outcomes, which is the calibrated signal at prediction time).
	rows, err := s.db.QueryContext(ctx, `
		SELECT symbol_id, ts, prob, fwd_return
		FROM prediction_outcomes
		WHERE horizon=? AND resolved_at IS NOT NULL
		  AND up IS NOT NULL AND fwd_return IS NOT NULL
		ORDER BY ts DESC LIMIT ?`,
		string(h), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	type base struct {
		symbolID  int64
		ts        int64
		signal    float64
		primaryFR float64
	}
	var bases []base
	symSet := map[int64]struct{}{}
	for rows.Next() {
		var b base
		if err := rows.Scan(&b.symbolID, &b.ts, &b.signal, &b.primaryFR); err != nil {
			return nil, err
		}
		bases = append(bases, b)
		symSet[b.symbolID] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// De-dup + sort the extra lags, dropping the primary (already have it) and
	// any non-positive values.
	wantExtra := make([]int, 0, len(extraLags))
	seen := map[int]struct{}{primary: {}}
	for _, l := range extraLags {
		if l <= 0 {
			continue
		}
		if _, dup := seen[l]; dup {
			continue
		}
		seen[l] = struct{}{}
		wantExtra = append(wantExtra, l)
	}

	// Per-symbol daily bar series (ascending), loaded once, used to compute the
	// extra-lag forward returns by BAR INDEX (trading-day offset). We cache the
	// series + an ascending ts->index map per symbol so each observation is an
	// O(log n) lookup, not a re-query.
	type series struct {
		bars []md.Bar
		idx  map[int64]int // bar ts -> position
	}
	cache := map[int64]series{}
	loadSeries := func(symbolID int64) (series, error) {
		if sr, ok := cache[symbolID]; ok {
			return sr, nil
		}
		bars, err := s.LastBars(ctx, symbolID, md.TF1d, 5000)
		if err != nil {
			return series{}, err
		}
		idx := make(map[int64]int, len(bars))
		for i, b := range bars {
			idx[b.Ts] = i
		}
		sr := series{bars: bars, idx: idx}
		cache[symbolID] = sr
		return sr, nil
	}

	out := make([]SignalBTObs, 0, len(bases))
	for _, b := range bases {
		o := SignalBTObs{
			SymbolID: b.symbolID,
			Ts:       b.ts,
			Signal:   b.signal,
			FwdByLag: map[int]float64{primary: b.primaryFR},
		}
		if len(wantExtra) > 0 {
			sr, err := loadSeries(b.symbolID)
			if err != nil {
				return nil, err
			}
			// Anchor to the base bar AT OR BEFORE the prediction ts, exactly like
			// the PredictionResolver: the forward window starts from the last
			// daily close known at prediction time, so the lag return is
			// close[base+lag]/close[base]-1 over realized (strictly later) bars.
			baseIdx := indexAtOrBefore(sr.bars, b.ts)
			if baseIdx >= 0 && sr.bars[baseIdx].Close > 0 {
				baseClose := sr.bars[baseIdx].Close
				for _, lag := range wantExtra {
					fi := baseIdx + lag
					if fi < len(sr.bars) && sr.bars[fi].Close > 0 {
						o.FwdByLag[lag] = sr.bars[fi].Close/baseClose - 1
					}
					// else: forward bar not realized yet → lag absent (no lookahead)
				}
			}
		}
		out = append(out, o)
	}
	return out, nil
}

// SPYDailyCloses returns the SPY daily (ts, close) series ascending for the
// buy-and-hold benchmark. It resolves SPY by symbol name; when SPY is not a
// tracked symbol (or has no daily bars) it returns empty slices and the caller
// renders the strategy curve without a benchmark. Only Stocks SPY is used.
func (s *Store) SPYDailyCloses(ctx context.Context, limit int) (ts []int64, closes []float64, err error) {
	sym, err := s.GetSymbol(ctx, "SPY", md.Stocks)
	if err != nil {
		// Not tracked — an honest empty benchmark, not an error.
		return nil, nil, nil
	}
	bars, err := s.LastBars(ctx, sym.ID, md.TF1d, limit)
	if err != nil {
		return nil, nil, err
	}
	ts = make([]int64, 0, len(bars))
	closes = make([]float64, 0, len(bars))
	for _, b := range bars {
		ts = append(ts, b.Ts)
		closes = append(closes, b.Close)
	}
	return ts, closes, nil
}

// indexAtOrBefore returns the index of the last bar with ts <= t in an
// ascending-by-ts bar slice, or -1 when every bar is after t. Binary search.
func indexAtOrBefore(bars []md.Bar, t int64) int {
	lo, hi := 0, len(bars)-1
	res := -1
	for lo <= hi {
		mid := (lo + hi) / 2
		if bars[mid].Ts <= t {
			res = mid
			lo = mid + 1
		} else {
			hi = mid - 1
		}
	}
	return res
}
