package pipeline

import (
	"context"
	"fmt"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/papertrade"
	"github.com/nyaungnicholas-wq/signaldeck/internal/riskgate"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// ── PaperTrader: INTERNAL simulated paper-trading driven by live predictions ──
//
// SAFETY: this worker NEVER contacts a broker. Every fill is a pure arithmetic
// event computed from the daemon's OWN stored bar data (internal/papertrade). It
// exists to accumulate an honest, costed, out-of-sample track record of the
// flagship calibrated prediction — labeled "simulated — not live money" wherever
// it surfaces.
//
// One simulated portfolio ("strategy") per prediction horizon: flagship-1d and
// flagship-1w. Each starts flat with a notional book (papertrade.StartingCash).
//
// Rule, per symbol, per run:
//   - read the symbol's LATEST calibrated prediction for the strategy's horizon;
//   - map cal_prob to a target: >= LONG -> long, <= FLAT -> flat, else HOLD
//     (a deadband so we don't churn on noise near 0.5);
//   - if a transition is needed, fill at the OPEN of the FIRST daily bar STRICTLY
//     AFTER the prediction's ts (never same-bar — the no-lookahead guarantee),
//     provided that bar exists at/before the run's as-of clock;
//   - price the fill through papertrade's execution model: the recorded price is
//     the stored bar's OPEN exactly (so the trade log stays reconcilable against
//     the bars), and the charge is the market's half-spread plus square-root-law
//     market impact for that size against the name's average daily dollar
//     volume, capped at a participation limit (a partial fill, not a pretend
//     one). A symbol with no usable ADV estimate is SKIPPED, not filled at zero
//     impact.
// Then mark equity at the as-of clock and advance the strategy cursor.
//
// IDEMPOTENCY: the strategy's cursor stores the newest global bar ts it has
// acted on. The run's as-of clock is the max latest daily bar ts across active
// symbols; if that is not strictly newer than the cursor, the run is a no-op.
// The store's ApplyPaperStep re-checks this under the write lock, so re-running
// a bar (or a crash mid-run) can never double-trade.

// paperStrategies binds each simulated portfolio id to the horizon it trades.
var paperStrategies = []struct {
	Name    string
	Horizon md.Horizon
}{
	{"flagship-1d", md.H1d},
	{"flagship-1w", md.H1w},
}

// PaperTrader runs the internal simulated book(s). Registered like any worker.
type PaperTrader struct {
	St *store.Store
}

func (w *PaperTrader) Name() string { return "paper-trader" }

// Interval: hourly. It only ACTS when a genuinely new daily bar has arrived, so
// most runs are cheap no-ops; hourly keeps the book responsive without churn.
func (w *PaperTrader) Interval() time.Duration { return time.Hour }

func (w *PaperTrader) Run(ctx context.Context) (string, error) {
	syms, err := w.St.ListSymbols(ctx, true)
	if err != nil {
		return "", err
	}
	// As-of clock: newest daily bar open-time across the active universe. Nothing
	// to do until at least one symbol has a daily bar.
	var asof int64
	marketByID := map[int64]md.Market{}
	for _, s := range syms {
		marketByID[s.ID] = s.Market
		ts, err := w.St.LatestBarTs(ctx, s.ID, md.TF1d)
		if err != nil {
			return "", err
		}
		if ts > asof {
			asof = ts
		}
	}
	if asof == 0 {
		return "no daily bars yet — nothing to simulate", nil
	}

	startCash := papertrade.StartingCash()
	acted := 0
	refused := 0 // entries the pretrade risk gate vetoed, across all strategies
	for _, strat := range paperStrategies {
		if _, err := w.St.InitPaperBook(ctx, strat.Name, startCash, asof); err != nil {
			return "", err
		}
		cur, ok, err := w.St.PaperCursor(ctx, strat.Name)
		if err != nil {
			return "", err
		}
		if !ok {
			// Just initialized above; re-read defensively.
			cur, _, err = w.St.PaperCursor(ctx, strat.Name)
			if err != nil {
				return "", err
			}
		}
		if asof <= cur.LastBarTs {
			continue // no new global bar for this strategy — idempotent no-op
		}

		apply, vetoed, err := w.buildStep(ctx, strat.Name, strat.Horizon, syms, marketByID, cur, asof)
		if err != nil {
			return "", err
		}
		refused += vetoed
		applied, err := w.St.ApplyPaperStep(ctx, apply)
		if err != nil {
			return "", err
		}
		if applied {
			acted++
		}
	}
	if refused > 0 {
		return fmt.Sprintf("marked %d strateg(ies) at asof=%d — risk gate vetoed %d entr(ies)", acted, asof, refused), nil
	}
	return fmt.Sprintf("marked %d strateg(ies) at asof=%d", acted, asof), nil
}

// buildStep assembles the atomic PaperApply for one strategy at the as-of clock:
// it walks each active symbol, decides the target from its latest calibrated
// prediction, executes any transition at the correct next-bar open (no
// lookahead), and marks equity. The cash math threads through so the book stays
// fully funded and never negative. This is I/O-thin glue; the arithmetic lives
// in internal/papertrade so it is unit-tested without a DB.
func (w *PaperTrader) buildStep(
	ctx context.Context,
	strategy string,
	h md.Horizon,
	syms []md.Symbol,
	marketByID map[int64]md.Market,
	cur store.PaperCursor,
	asof int64,
) (store.PaperApply, int, error) {
	cash := cur.Cash
	apply := store.PaperApply{Strategy: strategy, BarTs: asof, EquityTs: asof}
	// refused counts entries the pretrade risk gate vetoed this step. It is
	// reported in the worker's status line so a veto is visible in the run log
	// rather than being an entry that silently never happened.
	refused := 0

	// Book equity for POSITION SIZING: cash + the marked value of positions that
	// are already open coming into this step (valued at the as-of clock). Each new
	// entry targets equity/MaxPositions dollars so multiple names can coexist —
	// without this the first all-in entry would starve every later signal. This is
	// a stable per-step budget basis (it does not change as we deploy cash within
	// the step; the per-entry clamp to the running `cash` keeps the book funded).
	priorPosValue, err := w.openPositionsValue(ctx, strategy, asof)
	if err != nil {
		return apply, refused, err
	}
	equity := cash + priorPosValue

	// PRETRADE RISK GATE (internal/riskgate). Every ENTRY below is sized and
	// vetted by it; exits are never gated, so a tripped breaker can never trap
	// the book in a position. The book's condition and its realized edge are
	// measured once per step, then updated in-step as entries consume cash,
	// sector headroom and position slots — without that, ten entries in one step
	// would each be judged against an empty book.
	symByID := make(map[int64]string, len(syms))
	for _, s := range syms {
		symByID[s.ID] = s.Symbol
	}
	limits := riskgate.Defaults()
	book, err := w.riskBook(ctx, strategy, equity, cash, symByID, asof)
	if err != nil {
		return apply, refused, err
	}
	edge, err := w.tradedEdge(ctx, strategy)
	if err != nil {
		return apply, refused, err
	}

	for _, s := range syms {
		pos, hasPos, err := w.St.PaperPosition(ctx, strategy, s.ID)
		if err != nil {
			return apply, refused, err
		}
		pred, okP, err := w.St.LatestPrediction(ctx, s.ID, h)
		if err != nil {
			return apply, refused, err
		}
		if !okP {
			continue // no signal for this symbol/horizon yet
		}
		target := papertrade.DecideTarget(pred.CalProb)

		// Transition? Enter when flat+GoLong; exit when long+GoFlat. HOLD (and a
		// target that matches the current state) changes nothing.
		wantEnter := !hasPos && target == papertrade.GoLong
		wantExit := hasPos && target == papertrade.GoFlat
		if !wantEnter && !wantExit {
			continue
		}

		// NO LOOKAHEAD: fill at the OPEN of the first DAILY bar STRICTLY AFTER the
		// prediction's ts. A prediction made at ts P cannot fill on the bar whose
		// data produced it.
		fillBar, okFill, err := w.St.BarAtOrAfter(ctx, s.ID, md.TF1d, pred.Ts+1)
		if err != nil {
			return apply, refused, err
		}
		if !okFill || fillBar.Open <= 0 {
			continue // no eligible next bar yet — wait
		}
		// Never fill in the future relative to the run's as-of clock.
		if fillBar.Ts > asof {
			continue
		}

		// Liquidity for the execution model: the name's trailing average daily
		// DOLLAR volume, measured on bars at or before the fill (never after —
		// the fill may not know how much traded on days it has not seen).
		adv, err := w.advUSD(ctx, s.ID, fillBar.Ts)
		if err != nil {
			return apply, refused, err
		}
		in := papertrade.ExecInputs{Bar: fillBar, Market: marketByID[s.ID], ADVUSD: adv}

		if wantEnter {
			// The gate owns sizing now: fractional Kelly on the realized
			// round-trip record when that record can support it, the old
			// equal-slice budget when it cannot, and a REFUSAL when the measured
			// expectancy is non-positive or a limit is breached.
			book.Cash = cash
			sector := riskSector(s.Symbol)
			decision := riskgate.Evaluate(book, riskgate.Request{
				Symbol: s.Symbol, Sector: sector, Action: riskgate.Enter,
			}, edge, limits)
			if !decision.Allow {
				refused++
				continue
			}
			budget := decision.Notional
			f, qty, avgPx, ok := papertrade.EnterLong(budget, in)
			if !ok {
				// No cash slice to deploy, or no liquidity estimate to price the
				// fill with. Skipping is the honest outcome: a fill we cannot
				// cost would enter the book at the most favourable price
				// available and never be questioned again.
				continue
			}
			cash += f.CashDelta
			// Consume the slot, the cash and the sector headroom this entry just
			// took, so the next candidate in this same step is judged against the
			// book as it now stands.
			book.OpenPositions++
			if sector != "" {
				book.ExposureBySector[sector] += f.Qty * f.Px
			}
			apply.Opens = append(apply.Opens, store.PaperPosition{
				Strategy: strategy, SymbolID: s.ID, Qty: qty, AvgPx: avgPx, OpenedTs: fillBar.Ts,
			})
			apply.Trades = append(apply.Trades, store.PaperTrade{
				Strategy: strategy, SymbolID: s.ID, Side: f.Side, Qty: f.Qty, Px: f.Px, Cost: f.Cost, Ts: fillBar.Ts,
				// The sizing rationale is part of the audit trail: a reader of the
				// log should be able to see WHY this size, not just this price.
				Reason: fmt.Sprintf("cal_prob %.3f >= long %.2f · %s", pred.CalProb, papertrade.LongThreshold(), decision.Sizing),
			})
		} else { // wantExit
			// Deliberately NOT gated. riskgate would allow every exit anyway, and
			// routing a de-risking trade through a component that can refuse is how
			// a book ends up trapped in the position a breaker was tripped by.
			f, ok := papertrade.ExitLong(pos.Qty, in)
			if !ok {
				continue
			}
			cash += f.CashDelta
			apply.CloseSymbolIDs = append(apply.CloseSymbolIDs, s.ID)
			apply.Trades = append(apply.Trades, store.PaperTrade{
				Strategy: strategy, SymbolID: s.ID, Side: f.Side, Qty: f.Qty, Px: f.Px, Cost: f.Cost, Ts: fillBar.Ts,
				Reason: fmt.Sprintf("cal_prob %.3f <= flat %.2f", pred.CalProb, papertrade.FlatThreshold()),
			})
		}
	}

	// Mark-to-market: value every still-open position at its latest close at/
	// before the as-of clock. Positions just opened above are reflected in
	// apply.Opens; positions just closed are in CloseSymbolIDs. Recompute the
	// open set = (stored open positions - closed) + newly opened.
	posValue, err := w.markPositions(ctx, strategy, apply, asof)
	if err != nil {
		return apply, refused, err
	}

	apply.NewCash = cash
	apply.EquityCash = cash
	apply.EquityPositionsValue = posValue
	apply.EquityValue = cash + posValue
	return apply, refused, nil
}

// advLookbackBars is how many daily bars the average-daily-dollar-volume
// estimate is drawn from. A month of sessions is long enough to smooth a single
// heavy print and short enough to track a name whose liquidity is changing.
const advLookbackBars = 21

// advUSD estimates a symbol's average daily DOLLAR volume from the bars at or
// before ts. It is the denominator of both the market-impact and the capacity
// calculation, so it must never look past the fill: using volume from days the
// fill has not lived through would price the trade with information it could
// not have had.
//
// It scans the most recent advLookbackBars*2 stored bars, which covers a fill
// up to about a month behind the latest bar — far more slack than the worker
// ever needs, since it fills on the first bar after a fresh prediction. Returns
// 0 when that window holds no priced, non-zero-volume bar at or before ts; the
// execution model treats that as "cannot price this fill" and the caller skips
// the symbol rather than filling at zero impact. Failing to a skip, rather than
// to a free fill, is the whole point.
func (w *PaperTrader) advUSD(ctx context.Context, symbolID, ts int64) (float64, error) {
	bars, err := w.St.LastBars(ctx, symbolID, md.TF1d, advLookbackBars*2)
	if err != nil {
		return 0, err
	}
	// LastBars returns ascending by ts; walk backwards so the window is the most
	// recent advLookbackBars bars at or before ts, not the oldest ones.
	var sum float64
	var n int
	for i := len(bars) - 1; i >= 0 && n < advLookbackBars; i-- {
		b := bars[i]
		if b.Ts > ts {
			continue // strictly no lookahead
		}
		if b.Close <= 0 || b.Volume <= 0 {
			continue
		}
		sum += b.Close * b.Volume
		n++
	}
	if n == 0 {
		return 0, nil
	}
	return sum / float64(n), nil
}

// markPositions returns the marked-to-market value of the strategy's open
// positions AS THEY WILL BE after this step is applied — i.e. the stored open
// positions, minus the ones being closed this step, plus the ones being opened
// this step. Each is valued at its latest daily close at/before asof.
func (w *PaperTrader) markPositions(ctx context.Context, strategy string, apply store.PaperApply, asof int64) (float64, error) {
	closed := map[int64]bool{}
	for _, id := range apply.CloseSymbolIDs {
		closed[id] = true
	}
	opened := map[int64]float64{} // symbol_id -> qty (opened this step)
	for _, p := range apply.Opens {
		opened[p.SymbolID] = p.Qty
	}

	stored, err := w.St.PaperPositions(ctx, strategy)
	if err != nil {
		return 0, err
	}
	// Effective open set: qty per symbol after this step.
	qtyBySym := map[int64]float64{}
	for _, p := range stored {
		if closed[p.SymbolID] {
			continue // being exited this step
		}
		qtyBySym[p.SymbolID] = p.Qty
	}
	for id, q := range opened {
		qtyBySym[id] = q // newly opened (overrides any stale stored qty)
	}

	var total float64
	for id, qty := range qtyBySym {
		if qty <= 0 {
			continue
		}
		bar, ok, err := w.St.BarAtOrBefore(ctx, id, md.TF1d, asof)
		if err != nil {
			return 0, err
		}
		if !ok || bar.Close <= 0 {
			continue // no mark available — skip (position value unknown, treated as 0)
		}
		total += qty * bar.Close
	}
	return total, nil
}

// openPositionsValue marks the strategy's CURRENTLY-stored open positions (i.e.
// the book coming into this step, before any of this step's writes) at their
// latest daily close at/before asof. Used to derive the book equity that sizes
// new entries. A position with no mark contributes 0.
func (w *PaperTrader) openPositionsValue(ctx context.Context, strategy string, asof int64) (float64, error) {
	stored, err := w.St.PaperPositions(ctx, strategy)
	if err != nil {
		return 0, err
	}
	var total float64
	for _, p := range stored {
		if p.Qty <= 0 {
			continue
		}
		bar, ok, err := w.St.BarAtOrBefore(ctx, p.SymbolID, md.TF1d, asof)
		if err != nil {
			return 0, err
		}
		if !ok || bar.Close <= 0 {
			continue
		}
		total += p.Qty * bar.Close
	}
	return total, nil
}
