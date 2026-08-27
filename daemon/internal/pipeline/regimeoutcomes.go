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
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
	"github.com/nyaungnicholas-wq/signaldeck/internal/structregime"
)

// regimeAbandonMultiple is how many times its own horizon a due call may go
// unresolvable before it is retired as UNGRADABLE. The due queue admits a row
// at 1.45x its horizon, so 3x leaves a wide grace window: a symbol that is
// merely slow, or that the sweep prunes and later re-admits, has ample time to
// print the bars. Only rows that no plausible future bar can rescue are retired.
const regimeAbandonMultiple = 3

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

	// HARD REFUSAL 1 — no baseline column, no freezing. A binary predating the
	// naive_label migration would write rows that can never be benchmarked, and
	// the registry would be structurally unable to publish anything but
	// "HOLDING". Failing the worker run is the visible state; writing
	// null-less rows is the invisible one.
	hasNull, err := w.St.HasNaiveLabelColumn(ctx)
	if err != nil {
		return "", fmt.Errorf("naive_label column check: %w", err)
	}
	if !hasNull {
		return "", fmt.Errorf("regime_outcomes has no naive_label column: refusing to freeze " +
			"calls with no matched persistence null (rebuild/restart the daemon so the migration applies)")
	}

	// HARD REFUSAL 1b — STARTUP INVARIANT. The store now rejects a baseline-less
	// structural row at the write path, so a post-amendment row with a NULL
	// naive_label can only mean the deployed binary is not this source. Read the
	// table before doing any work: a divergence surfaces within one tick and
	// lands a dq event, instead of accumulating silently for weeks.
	//
	// One historical exception exists and is bounded by construction. Before the
	// write-path guard, a silent INSERT-OR-IGNORE no-op in the freeze path left
	// post-epoch rows with no baseline. They cannot be repaired (a persistence
	// label computed after the outcome is hindsight, not a null), so they are
	// frozen ONCE into a quarantine manifest whose digest rides the
	// pre-registration chain: countable, immutable, and still ungraded. The set
	// is not growable — the freeze is a no-op once the manifest exists, so any
	// row that goes unmatched from here on still fails this check.
	//
	// The manifest is VERIFIED before its exclusion is trusted: if the exempt set
	// were extended or edited after freezing, the recomputed digest differs and
	// the worker fails here rather than quietly exempting more rows.
	qm, didFreeze, err := w.St.FreezeNullQuarantine(ctx, nowUnix)
	if err != nil {
		return "", fmt.Errorf("freeze null quarantine: %w", err)
	}
	if didFreeze {
		_ = w.St.InsertDQ(ctx, md.DQEvent{Ts: nowUnix, Kind: "regime_outcome_quarantine",
			Detail: fmt.Sprintf("froze %d pre-guard regime_outcomes rows with no naive baseline "+
				"under manifest digest %s; they stay ungraded (NO BASELINE) and out of every "+
				"structural denominator, and the set cannot grow", qm.NRows, qm.Digest)})
	}
	if _, _, err := w.St.VerifyNullQuarantine(ctx); err != nil {
		detail := fmt.Sprintf("null-quarantine manifest verification failed: %v", err)
		_ = w.St.InsertDQ(ctx, md.DQEvent{Ts: nowUnix, Kind: "regime_outcome_error", Detail: detail})
		return "", errors.New(detail)
	}

	unmatched, err := w.St.UnmatchedNullCount(ctx)
	if err != nil {
		return "", fmt.Errorf("unmatched-null invariant check: %w", err)
	}
	if unmatched > 0 {
		detail := fmt.Sprintf("%d regime_outcomes rows at/after the null amendment carry no "+
			"naive_label; the write-path guard cannot have produced them (deployed binary "+
			"differs from source)", unmatched)
		_ = w.St.InsertDQ(ctx, md.DQEvent{Ts: nowUnix, Kind: "regime_outcome_error", Detail: detail})
		return "", fmt.Errorf("unmatched persistence null: %s", detail)
	}

	// Per-symbol daily series, loaded at most once per tick and shared by the
	// freeze (naive baseline) and resolve (grading) phases.
	cache := map[int64]*series{}
	load := func(symbolID, from int64) *series {
		if sr, ok := cache[symbolID]; ok {
			return sr
		}
		bars, err := w.St.Bars(ctx, symbolID, md.TF1d, from, nowUnix+1, 5000)
		if err != nil {
			_ = w.St.InsertDQ(ctx, md.DQEvent{Ts: nowUnix, Kind: "regime_outcome_error",
				Detail: fmt.Sprintf("bars sym %d: %v", symbolID, err)})
			return nil
		}
		sr := &series{}
		for _, b := range bars {
			if b.Close > 0 {
				sr.ts = append(sr.ts, b.Ts)
				sr.closes = append(sr.closes, b.Close)
				sr.vols = append(sr.vols, b.Volume)
			}
		}
		cache[symbolID] = sr
		return sr
	}

	// 1. SNAPSHOT: freeze every current forecast (≤1 row per symbol/kind/day),
	// each with its NAIVE-PERSISTENCE baseline — the "nothing changes" guess
	// computed from the call bar at freeze time, so the null is committed before
	// the outcome exists and can never be recomputed once the answer is known.
	calls, err := w.St.RegimeForecastCalls(ctx)
	if err != nil {
		return "", err
	}
	frozen, noNull, dropped, abstained := 0, 0, 0, 0
	// WHICH calls lack a baseline, not just how many. The refusal below is a
	// permanent red until the gap is closed, and "3/1151" gives nobody a handle
	// on which three — so the gate fired every pass for days with no way to act
	// on it. A count is an alarm; the identities are the fix.
	var noNullWho []string
	// Loaded once, and only if a gap actually appears — the happy path pays
	// nothing for diagnostics it will not print.
	var names map[int64]string
	symName := func(id int64) string {
		if names == nil {
			names, _ = w.St.SymbolNameMap(ctx)
			if names == nil {
				names = map[int64]string{}
			}
		}
		if s, ok := names[id]; ok && s != "" {
			return s
		}
		return fmt.Sprintf("sym%d", id)
	}
	for _, c := range calls {
		var degenerate bool
		c.NaiveLabel, degenerate = w.naiveLabel(load(c.SymbolID, c.Ts-int64(volLookbackDays)*86400), c)
		if c.NaiveLabel == "" {
			// The store would refuse this write anyway; skip it here so the pass
			// still resolves due outcomes and reports the gap at the end rather
			// than aborting on the first uncomputable baseline.
			//
			// A DEGENERATE null is not a gap. A null with no direction cannot
			// grade anything, so declining to freeze the row is the honest
			// outcome, not a coverage failure — counting it as one made the
			// refusal permanent and therefore meaningless.
			if degenerate {
				abstained++
				continue
			}
			noNull++
			if len(noNullWho) < 20 {
				noNullWho = append(noNullWho, fmt.Sprintf("%s/%s@%s",
					symName(c.SymbolID), c.Kind,
					time.Unix(c.Ts, 0).UTC().Format("2006-01-02")))
			}
			continue
		}
		isNew, err := w.St.InsertRegimeOutcome(ctx, c)
		if errors.Is(err, store.ErrNaiveLabelDropped) {
			// The stored row for this (symbol, kind, day) has no baseline and the
			// dedup would have discarded the one we just computed. That silent
			// no-op is how the quarantined rows were manufactured, so it is
			// recorded loudly and counted rather than folded into "already
			// frozen". The row is NOT repaired — backfilling a null after the
			// fact is hindsight — it simply stays NO BASELINE, visibly.
			dropped++
			_ = w.St.InsertDQ(ctx, md.DQEvent{Ts: nowUnix, Kind: "regime_outcome_error",
				Detail: fmt.Sprintf("baseline dropped by dedup: %v", err)})
			continue
		}
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
	resolved, misses, pms := 0, 0, 0
	// abandoned counts rows retired as UNGRADABLE this pass — see
	// regimeAbandonMultiple below. Reported, never silent.
	abandoned := 0
	// stuck counts due rows that could not be graded THIS pass for want of
	// forward bars, whether or not they are old enough to retire.
	//
	// Retirement alone does not fix the defect E22 recorded. The complaint was
	// SILENCE: this worker reported a bare `resolved 0` for 31 days while 2,288
	// rows sat unresolvable. A 3x grace window is deliberately wide, so the
	// oldest of those rows does not retire until 2026-09-18 -- three more weeks
	// of the same silence if the count is not surfaced until then. Reporting the
	// stuck total every pass makes the hole visible NOW, without retiring a
	// single row early.
	stuck := 0
	// abandon marks a row that is so far past its horizon that no future bar can
	// rescue it, and counts it. Returns true when the row was retired.
	abandon := func(o store.RegimeOutcomeRow, reason string) bool {
		if o.Ts+int64(o.HorizonDays)*regimeAbandonMultiple*86400 >= nowUnix {
			return false // still inside the grace window — genuinely retry later
		}
		if err := w.St.MarkRegimeOutcomeUngradable(ctx, o.ID, reason); err != nil {
			_ = w.St.InsertDQ(ctx, md.DQEvent{Ts: nowUnix, Kind: "regime_outcome_error",
				Detail: fmt.Sprintf("mark ungradable %d: %v", o.ID, err)})
			return false
		}
		abandoned++
		return true
	}
	for _, o := range due {
		sr := load(o.SymbolID, o.Ts-int64(volLookbackDays)*86400)
		if sr == nil {
			stuck++
			abandon(o, "no bars available for the symbol")
			continue
		}
		// call bar = last bar at or before the frozen call ts
		t := barIndexAt(sr.ts, o.Ts)
		// require enough NEWER daily bars: the calendar-day approximation alone
		// must never grade against a window that hasn't printed yet.
		if t < 0 || len(sr.closes)-1-t < o.HorizonDays {
			// "retried later" is TRUE only while later can still arrive. For a
			// symbol the universe sweep pruned it cannot: an inactive symbol
			// stops receiving daily bars, so the row is short of its horizon
			// forever. Measured 2026-08-27: 2,267 of 2,288 due rows sat on
			// inactive symbols, none had enough bars, and the worker had
			// reported ok/resolved 0 for 31 days. Past the grace window we
			// retire the row WITH A REASON instead of retrying it forever —
			// otherwise the structural record silently grades only survivors.
			stuck++
			abandon(o, "insufficient forward bars past the grace window (symbol likely left the universe)")
			continue // still unresolved this pass either way
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
			// An unknown kind is a kind the ENGINE no longer has (retired gapfill
			// rows). No future bar teaches it one, so this row is as permanently
			// unresolvable as one whose symbol left the universe. Same treatment:
			// retire it with a reason past the grace window rather than retry it
			// forever. Still never GUESSED at.
			abandon(o, "unknown regime kind (retired from the engine)")
			continue
		}
		if !rok {
			// A degenerate window (tie / NaN median) is computed from a FIXED
			// historical bar index, so re-running never changes the answer: this
			// row is un-gradable permanently, not yet. Retiring it past the grace
			// window keeps the honest non-grade AND stops the endless retry.
			abandon(o, "degenerate resolution window (tie or NaN median)")
			continue
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
	summary := fmt.Sprintf("froze %d regime calls (%d without a naive baseline, %d abstained on a tied null), resolved %d (%d wrong, %d high-conviction postmortems), retired %d ungradable, %d due but stuck short of forward bars",
		frozen, noNull, abstained, resolved, misses, pms, abandoned, stuck)
	// HARD REFUSAL 2 — partial coverage is not a matched null. If any call in
	// this pass was frozen without a baseline, the benchmark denominator is a
	// self-selected subset of the rows the model is scored on. Resolution work
	// above is kept (it is already committed and is never wrong), but the pass
	// lands as a FAILED worker_runs row so the gap is on the record instead of
	// being tolerated forever.
	if noNull > 0 {
		who := strings.Join(noNullWho, ", ")
		if noNull > len(noNullWho) {
			who += fmt.Sprintf(", … (%d more)", noNull-len(noNullWho))
		}
		// Also on the DQ feed, where a human looking at data quality will find it
		// without reading worker_runs detail.
		_ = w.St.InsertDQ(ctx, md.DQEvent{Ts: nowUnix, Kind: "regime_outcome_error",
			Detail: fmt.Sprintf("no naive baseline for %d/%d calls: %s", noNull, len(calls), who)})
		return summary, fmt.Errorf("%d/%d frozen calls carry no naive-persistence baseline "+
			"(%s): the null is unmatched for this pass", noNull, len(calls), who)
	}
	return summary, nil
}

// series is one symbol's positive-close daily history for this tick.
type series struct {
	ts     []int64
	closes []float64
	vols   []float64
}

// barIndexAt is the last bar index at or before ts (-1 when none).
func barIndexAt(ts []int64, at int64) int {
	for i := len(ts) - 1; i >= 0; i-- {
		if ts[i] <= at {
			return i
		}
	}
	return -1
}

// naiveLabel computes the frozen naive-persistence null for one call: the label
// the CURRENT state already carries at the call bar, in the same vocabulary the
// resolver will produce. "" is an honest absence (no bars, thin history, or a
// degenerate/tied state) and is stored as NULL — the benchmark drops those rows
// rather than scoring them as misses.
//
// It is MEASURED from bars, never copied from the call: for trend and liquidity
// the two are expected to coincide (this package's own caveats say the skill IS
// persistence), and the grader must be able to observe that rather than assume
// it.
// The second return distinguishes the two reasons for "": a genuine DATA GAP
// (no series, no bar at or before the call) from a DEGENERATE statistic — the
// current value sitting exactly on its own trailing median, which has no
// direction. Both leave the row unfrozen, but only the first is a coverage
// failure. Measured 2026-08-03: GOOGL/liquidity21 had cur and med equal to the
// bit (23.012578864499954), because `window = 200` makes the inclusive window
// 201 elements, so the median IS an element of it and exact ties are
// structural, not floating-point luck. Counting an undefined null as a missing
// one kept the worker permanently red for something nobody can fix.
func (w *RegimeOutcomeWorker) naiveLabel(sr *series, c store.RegimeCall) (string, bool) {
	if sr == nil {
		return "", false
	}
	t := barIndexAt(sr.ts, c.Ts)
	if t < 0 {
		return "", false
	}
	var lbl string
	var ok bool
	switch c.Kind {
	case structregime.KindTrend21, structregime.KindTrend63, structregime.KindTrendCrypto21:
		lbl, ok = structregime.NaiveTrendAt(sr.closes, t)
	case structregime.KindLiquidity21, structregime.KindLiquidityCrypto21:
		lbl, ok = structregime.NaiveLiquidityAt(sr.closes, sr.vols, t)
	case structregime.KindVol21:
		// return index t-1 aligns to bar index t, exactly as the resolver does
		lbl, ok = structregime.NaiveVol21At(barReturns2(sr.closes), t-1)
	default:
		return "", false // an unknown kind is not a data gap we can close
	}
	if !ok {
		// The series and the call bar were both present, so the inputs were
		// adequate; the statistic itself is undefined (a tie, or a NaN window).
		// That is an abstention, not missing data.
		return "", true
	}
	return lbl, false
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
