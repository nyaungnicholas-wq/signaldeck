// Regime-outcome runner — the LIVE grading loop for the validated regime
// forecasts (credibility wave).
//
// The regime_forecasts table is overwritten in place each VolRegimeRunner pass,
// so on its own the platform's flagship claims ("97.2% at very-high
// conviction") could never be checked against what actually happened. This
// worker closes that loop:
//
//  1. SNAPSHOT — every current forecast is frozen into regime_outcomes at most
//     once per (symbol, kind, UTC-day of call ts): the call, its conviction and
//     its CLAIMED accuracy, captured before anyone knows the answer (INSERT OR
//     IGNORE on the dedup unique index — idempotent across re-runs).
//  2. RESOLVE — once ts + horizon_days trading days have elapsed (approximated
//     as horizon_days*1.45 calendar days for stocks) AND at least horizon_days
//     newer daily bars exist, the REALIZED regime label is recomputed with the
//     exact engine arithmetic (internal/structregime Resolve*At — exported from
//     the engine so this worker can never drift into subtly-different math).
//     correct = (call == realized). Grading reads only bars up to the horizon
//     bar — no lookahead by construction.
//  3. POSTMORTEM — a HIGH-conviction call (>=0.8) that resolved WRONG gets a
//     deterministic plain-English narrative (what was called, what realized +
//     the key measured number, and the base rate the CLAIMED accuracy itself
//     implies — computed, never invented). Idempotent per outcome.
//
// Never fails the fleet: per-symbol grading errors record a dq event and move
// on. Cadence 6h — outcomes only become due as daily bars arrive.
package pipeline

import (
	"context"
	"fmt"
	"math"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
	"github.com/nyaungnicholas-wq/signaldeck/internal/structregime"
)

// regimePMConvictionFloor is the conviction at or above which a wrong resolved
// call earns a postmortem — the "high conviction" band boundary.
const regimePMConvictionFloor = 0.8

// RegimeOutcomeWorker freezes, resolves, and postmortems regime forecasts.
type RegimeOutcomeWorker struct {
	St    *store.Store
	Now   func() time.Time // injectable clock; nil ⇒ time.Now
	Limit int              // max due outcomes graded per tick; 0 ⇒ 5000
}

func (w *RegimeOutcomeWorker) Name() string            { return "regime-outcome-runner" }
func (w *RegimeOutcomeWorker) Interval() time.Duration { return 6 * time.Hour }

func (w *RegimeOutcomeWorker) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now()
}

func (w *RegimeOutcomeWorker) Run(ctx context.Context) (string, error) {
	now := w.now()
	nowUnix := now.Unix()

	// 1. SNAPSHOT: freeze every current forecast (≤1 row per symbol/kind/day).
	calls, err := w.St.RegimeForecastCalls(ctx)
	if err != nil {
		return "", err
	}
	frozen := 0
	for _, c := range calls {
		isNew, err := w.St.InsertRegimeOutcome(ctx, c)
		if err != nil {
			return "", fmt.Errorf("freeze %d/%s: %w", c.SymbolID, c.Kind, err)
		}
		if isNew {
			frozen++
		}
	}

	// 2. RESOLVE due outcomes, loading each symbol's bars once.
	due, err := w.St.DueRegimeOutcomes(ctx, nowUnix, w.Limit)
	if err != nil {
		return "", err
	}
	type series struct {
		ts     []int64
		closes []float64
		vols   []float64
	}
	cache := map[int64]*series{}
	resolved, misses, pms := 0, 0, 0
	for _, o := range due {
		sr, ok := cache[o.SymbolID]
		if !ok {
			from := o.Ts - int64(volLookbackDays)*86400
			bars, err := w.St.Bars(ctx, o.SymbolID, md.TF1d, from, nowUnix+1, 5000)
			if err != nil {
				_ = w.St.InsertDQ(ctx, md.DQEvent{Ts: nowUnix, Kind: "regime_outcome_error",
					Detail: fmt.Sprintf("bars sym %d: %v", o.SymbolID, err)})
				continue
			}
			sr = &series{}
			for _, b := range bars {
				if b.Close > 0 {
					sr.ts = append(sr.ts, b.Ts)
					sr.closes = append(sr.closes, b.Close)
					sr.vols = append(sr.vols, b.Volume)
				}
			}
			cache[o.SymbolID] = sr
		}
		// call bar = last bar at or before the frozen call ts
		t := -1
		for i := len(sr.ts) - 1; i >= 0; i-- {
			if sr.ts[i] <= o.Ts {
				t = i
				break
			}
		}
		// require enough NEWER daily bars: the calendar-day approximation alone
		// must never grade against a window that hasn't printed yet.
		if t < 0 || len(sr.closes)-1-t < o.HorizonDays {
			continue // not enough forward bars yet — stays unresolved, retried later
		}
		var res structregime.Resolution
		var rok bool
		switch o.Kind {
		// The crypto kinds share the stock resolvers by design — the predictor
		// arithmetic is identical, only the accuracy tables differ.
		case structregime.KindTrend21, structregime.KindTrend63, structregime.KindTrendCrypto21:
			res, rok = structregime.ResolveTrendAt(sr.closes, t, o.HorizonDays)
		case structregime.KindLiquidity21, structregime.KindLiquidityCrypto21:
			res, rok = structregime.ResolveLiquidityAt(sr.closes, sr.vols, t)
		case structregime.KindVol21:
			// return index t-1 aligns to bar index t (ret i resolves at bar i+1)
			res, rok = structregime.ResolveVol21At(barReturns2(sr.closes), t-1)
		default:
			continue // unknown kind (e.g. retired gapfill rows) — never guessed
		}
		if !rok {
			continue // degenerate window (tie / NaN median) — honest non-grade
		}
		correct := res.Actual == o.Regime
		if err := w.St.ResolveRegimeOutcome(ctx, o.ID, res.Actual, correct, nowUnix); err != nil {
			_ = w.St.InsertDQ(ctx, md.DQEvent{Ts: nowUnix, Kind: "regime_outcome_error",
				Detail: fmt.Sprintf("resolve outcome %d: %v", o.ID, err)})
			continue
		}
		resolved++
		if !correct {
			misses++
			if o.Conviction >= regimePMConvictionFloor {
				o.Actual = res.Actual
				narrative := regimePostmortemNarrative(o, res)
				if err := w.St.InsertRegimePostmortem(ctx, o.ID, o.SymbolID, o,
					res.KeyName, res.KeyValue, narrative, nowUnix); err != nil {
					_ = w.St.InsertDQ(ctx, md.DQEvent{Ts: nowUnix, Kind: "regime_outcome_error",
						Detail: fmt.Sprintf("postmortem outcome %d: %v", o.ID, err)})
				} else {
					pms++
				}
			}
		}
	}
	return fmt.Sprintf("froze %d regime calls, resolved %d (%d wrong, %d high-conviction postmortems)",
		frozen, resolved, misses, pms), nil
}

// regimePostmortemNarrative is the DETERMINISTIC plain-English miss report:
// what was called, what realized (with the key measured number), and the base
// rate the claimed accuracy itself implies. Every number is computed from the
// frozen row and the resolution — nothing invented.
func regimePostmortemNarrative(o store.RegimeOutcomeRow, res structregime.Resolution) string {
	what := fmt.Sprintf("%s called %q at %.2f conviction, claiming %.1f%% accuracy.",
		o.Kind, o.Regime, o.Conviction, o.HistoricalAccuracy*100)
	var happened string
	switch o.Kind {
	case structregime.KindTrend21, structregime.KindTrend63, structregime.KindTrendCrypto21:
		happened = fmt.Sprintf("At the %d-trading-day horizon the close sat %+.1f%% vs its SMA200 — realized %q.",
			o.HorizonDays, res.KeyValue, res.Actual)
	case structregime.KindLiquidity21, structregime.KindLiquidityCrypto21:
		happened = fmt.Sprintf("Forward 21-session mean log dollar volume came in %+.3f log points vs the call-time trailing median — realized %q.",
			res.KeyValue, res.Actual)
	case structregime.KindVol21:
		happened = fmt.Sprintf("Forward 21-session realized vol came in %+.4f daily vs the call-time trailing median — realized %q.",
			res.KeyValue, res.Actual)
	default:
		happened = fmt.Sprintf("Realized %q (%s %+.4f).", res.Actual, res.KeyName, res.KeyValue)
	}
	return what + " " + happened + " " + regimeBaseRateSentence(o.HistoricalAccuracy)
}

// regimeBaseRateSentence turns the CLAIMED accuracy into its own honest miss
// rate: an a-claim implies being wrong ~1 in 1/(1-a) times. Computed, never
// invented; degenerate claims get a plain fallback.
func regimeBaseRateSentence(acc float64) string {
	if acc <= 0 || acc >= 1 {
		return "No base-rate claim can be computed from this accuracy figure."
	}
	oneIn := int(math.Round(1 / (1 - acc)))
	if oneIn < 2 {
		oneIn = 2
	}
	return fmt.Sprintf("A %.1f%% tier call is still wrong ~1 in %d times; this is that case.",
		acc*100, oneIn)
}

// barReturns2 is barReturns over an already-filtered positive-close series:
// simple returns, ret[i] = closes[i+1]/closes[i]-1 (resolves at bar i+1).
func barReturns2(closes []float64) []float64 {
	if len(closes) < 2 {
		return nil
	}
	out := make([]float64, len(closes)-1)
	for i := 1; i < len(closes); i++ {
		out[i-1] = closes[i]/closes[i-1] - 1
	}
	return out
}
