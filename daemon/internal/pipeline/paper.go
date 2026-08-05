package pipeline

import (
	"context"
	"fmt"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/ev"
	"github.com/nyaungnicholas-wq/signaldeck/internal/killswitch"
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
	refused := 0 // entries the EV engine or the pretrade risk gate refused, across all strategies
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
		return fmt.Sprintf("marked %d strateg(ies) at asof=%d — EV/risk gates refused %d entr(ies)", acted, asof, refused), nil
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
	// refused counts entries the EV decision engine or the pretrade risk gate
	// refused this step. It is
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
	// EPOCH BOUNDARIES. Declared from code every pass so they exist on any
	// database, including a fresh one, without a migration anybody has to
	// remember. See paperepoch.go for what an epoch is and why the record splits.
	if err := w.ensureEpochs(ctx, strategy); err != nil {
		return apply, refused, err
	}
	limits := riskgate.Defaults()
	book, err := w.riskBook(ctx, strategy, equity, cash, symByID, asof)
	if err != nil {
		return apply, refused, err
	}
	// The terminal rung, decided ONCE per pass. riskgate.Admit below stops the
	// book growing; this closes it. Computed here, before any position is
	// examined, so every name in the pass is judged against one book-wide
	// measurement rather than a drawdown that shifts as the loop fills.
	flatten := riskgate.ShouldFlatten(book, limits)

	// The sizing edge is measured over the CURRENT EPOCH only: a Kelly fraction
	// staked on a previous rule set's win rate is the cross-regime error that
	// costs money rather than merely misreporting.
	edge, err := w.tradedEdge(ctx, strategy, asof)
	if err != nil {
		return apply, refused, err
	}

	// KILL SWITCH (internal/killswitch). Fail-closed, file-based, read fresh
	// before EVERY order rather than cached for the pass, so tripping it part
	// way through a run stops the very next entry instead of the next restart.
	// Entries refuse; EXITS still proceed — a halt that traps the book inside
	// the position it was tripped by is a larger risk than the one it controls.
	halt := killswitch.Check()

	// DECISION ENGINE (internal/ev — ARCHITECTURE_EV.md Layer 1). The entry
	// go/no-go is no longer the bare cal_prob threshold: every candidate is
	// ASSESSED (net EV, no-trade zone, tail, cost, liquidity, correlation, all
	// with explicit has-flags), the pass's candidates are RANKED by net EV —
	// replacing the old arbitrary symbol-order capital allocation — and only a
	// BUY proceeds to riskgate for its final size. Every verdict, especially
	// every DO_NOTHING, is ledgered to ev_decisions so refusals stay auditable.
	// cal_prob still picks the INTENT (long/flat/hold deadband, unchanged);
	// the engine decides whether the long is WORTH ITS COST.
	//
	// ORDERING, and it is load-bearing: RISK RUNS BEFORE EV ADMISSION. The book
	// must be admitted for new risk at all (riskgate.Admit) before any candidate
	// is considered, and each candidate must clear the gate before it is ranked
	// or decided. Sizing a trade that a go/no-go already approved is not risk
	// control — it is a decorator on a decision already made, and it leaves the
	// gate arguing about how much of something the book should never have been
	// doing. A risk-rejected name must not even consume an EV rank slot, because
	// rank IS the opportunity cost: capital denied to rank 1 by an untradeable
	// rank 8 is capital misallocated by the accounting, not by the market.
	forecasts, err := w.returnForecastsByID(ctx, h)
	if err != nil {
		return apply, refused, err
	}
	thresholds := ev.DefaultThresholds()

	// Phase 1: execute EXITS and collect entry candidates. Exits run first —
	// they are never gated (see below) and the cash they free is real before
	// any entry is sized.
	type entryCand struct {
		s      md.Symbol
		pred   store.Prediction
		bar    md.Bar
		in     papertrade.ExecInputs
		assess ev.Assessment
	}
	var cands []entryCand
	for _, s := range syms {
		pos, hasPos, err := w.St.PaperPosition(ctx, strategy, s.ID)
		if err != nil {
			return apply, refused, err
		}
		pred, okP, err := w.St.LatestPrediction(ctx, s.ID, h)
		if err != nil {
			return apply, refused, err
		}

		// ── EXIT PATH ────────────────────────────────────────────────────────
		// An OPEN position is evaluated for exit on every pass, whether or not a
		// fresh prediction exists and whatever it says. Barriers are risk
		// controls; a stop that only fires when the model happens to have an
		// opinion is not a stop. planExit weighs the barrier against the
		// probability flip and returns whichever CLOSE came first in time.
		if hasPos {
			plan, wantExit, err := w.planExit(ctx, s, h, pos, pred, okP, asof, flatten.Reason)
			if err != nil {
				return apply, refused, err
			}
			if !wantExit {
				continue // still holding
			}
			adv, err := w.advUSD(ctx, s.ID, plan.fillBar.Ts)
			if err != nil {
				return apply, refused, err
			}
			in := papertrade.ExecInputs{Bar: plan.fillBar, Market: marketByID[s.ID], ADVUSD: adv}

			// Deliberately NOT gated — by the EV engine, by riskgate, or by the
			// kill switch. All three state the same doctrine: routing a de-risking
			// trade through a component that can refuse is how a book ends up
			// trapped in the position a breaker was tripped by. The engine still
			// LEDGERS the SELL so the decision log is the complete record of every
			// transition.
			f, ok := papertrade.ExitLong(pos.Qty, in)
			if !ok {
				continue
			}
			exitAssess := ev.Assessment{Inputs: ev.Inputs{
				Symbol: s.Symbol, Horizon: string(h), CalProb: pred.CalProb, HasProb: okP,
			}}
			exitDecision := ev.Decide(exitAssess, ev.ExitLong, thresholds)
			if plan.fromBarrier {
				// The barrier is the reason, so the ledger says so rather than
				// filing every exit under the signal's name.
				exitDecision.Reason = ev.BarrierReason(string(plan.barrier.Kind))
			}
			if err := w.ledgerBarrierExit(ctx, strategy, s.ID, exitAssess, exitDecision, plan, asof); err != nil {
				return apply, refused, err
			}
			cash += f.CashDelta
			apply.CloseSymbolIDs = append(apply.CloseSymbolIDs, s.ID)
			apply.Trades = append(apply.Trades, store.PaperTrade{
				Strategy: strategy, SymbolID: s.ID, Side: f.Side, Qty: f.Qty, Px: f.Px, Cost: f.Cost,
				Ts: plan.fillBar.Ts, Reason: plan.reason,
			})
			continue
		}

		// ── ENTRY PATH ───────────────────────────────────────────────────────
		if !okP {
			continue // no signal for this symbol/horizon yet
		}
		if papertrade.DecideTarget(pred.CalProb) != papertrade.GoLong {
			continue // HOLD or flat — nothing to open
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

		// KILL SWITCH, checked before this entry is even assessed. A halted
		// platform does no measuring it would then have to throw away, but the
		// refusal is still ledgered: "the switch was down" has to be a queryable
		// record, not an entry that silently never happened.
		if halt.Halted {
			refused++
			if err := w.ledgerGateRefusal(ctx, strategy, s.ID,
				minimalAssessment(s.Symbol, h, pred.CalProb),
				ev.ReasonHalted, nil, &halt, asof); err != nil {
				return apply, refused, err
			}
			continue
		}

		// Entry candidate: measure every Decision Engine input now; the verdict
		// waits until the whole pass can be ranked.
		var fc *store.ReturnForecast
		if f, okF := forecasts[s.ID]; okF {
			fc = &f
		}
		a, err := w.assessEntry(ctx, s, h, strategy, pred, in, equity, cash, fc, asof)
		if err != nil {
			return apply, refused, err
		}
		cands = append(cands, entryCand{s: s, pred: pred, bar: fillBar, in: in, assess: a})
	}

	// Phase 2: RISK ADMISSION — before any EV verdict is rendered.
	//
	// Step one asks the question that is logically prior to every candidate: is
	// this book open for new risk at all? A tripped breaker admits NOTHING, and
	// no candidate can route around it by being a better trade.
	book.Cash = cash
	if admit := riskgate.Admit(book, limits); !admit.Allow {
		for _, c := range cands {
			refused++
			if err := w.ledgerGateRefusal(ctx, strategy, c.s.ID, c.assess,
				ev.ReasonRiskRefused, &admit, nil, asof); err != nil {
				return apply, refused, err
			}
		}
		cands = nil
	}

	// Step two gates each candidate individually. Survivors — and only
	// survivors — go on to be ranked and decided, so a name the gate would
	// never fund cannot displace one it would.
	admitted := cands[:0]
	for _, c := range cands {
		gate := riskgate.Evaluate(book, riskgate.Request{
			Symbol: c.s.Symbol, Sector: riskSector(c.s.Symbol), Action: riskgate.Enter,
			CorrToBook: c.assess.CorrToBook, HasCorr: c.assess.HasCorr,
		}, edge, limits)
		if !gate.Allow {
			refused++
			if err := w.ledgerGateRefusal(ctx, strategy, c.s.ID, c.assess,
				ev.ReasonRiskRefused, &gate, nil, asof); err != nil {
				return apply, refused, err
			}
			continue
		}
		admitted = append(admitted, c)
	}
	cands = admitted

	// Phase 3: rank the RISK-ADMITTED candidates by net EV — the rank IS the
	// opportunity-cost input — then decide each in rank order, so capital goes
	// to the best measured EV first instead of the alphabetically luckiest.
	byName := make(map[string]entryCand, len(cands))
	var assessments []ev.Assessment
	for _, c := range cands {
		byName[c.s.Symbol] = c
		assessments = append(assessments, c.assess)
	}
	for _, a := range ev.RankByNetEV(assessments) {
		c := byName[a.Symbol]
		decision := ev.Decide(a, ev.EnterLong, thresholds)
		if err := w.ledgerEVDecision(ctx, strategy, c.s.ID, a, decision, asof); err != nil {
			return apply, refused, err
		}
		if decision.Action != ev.BUY {
			refused++ // the EV gate's DO_NOTHING — ledgered above, auditable
			continue
		}

		// KILL SWITCH, re-read immediately before the order. The pass-level read
		// above is not enough: a halt tripped while this pass was ranking must
		// stop the next fill, not the next run.
		if h := killswitch.Check(); h.Halted {
			refused++
			if err := w.ledgerGateRefusal(ctx, strategy, c.s.ID, a, ev.ReasonHalted, nil, &h, asof); err != nil {
				return apply, refused, err
			}
			continue
		}

		// SIZING. The candidate already cleared the gate in Phase 2; this second
		// call is not a re-litigation of admission but a measurement against the
		// book AS IT NOW STANDS — cash, slots and sector headroom all move
		// within a pass as earlier entries consume them. It can still refuse,
		// and that refusal is a different fact from the admission one: not "this
		// name is untradeable" but "there is no longer room for it today".
		//
		// riskgate owns the size: fractional Kelly on the realized round-trip
		// record when that record can support it, the old equal-slice budget
		// when it cannot, and a REFUSAL when the measured expectancy is
		// non-positive or a limit is breached.
		book.Cash = cash
		sector := riskSector(c.s.Symbol)
		gate := riskgate.Evaluate(book, riskgate.Request{
			Symbol: c.s.Symbol, Sector: sector, Action: riskgate.Enter,
			CorrToBook: a.CorrToBook, HasCorr: a.HasCorr,
		}, edge, limits)
		if !gate.Allow {
			refused++
			if err := w.ledgerGateRefusal(ctx, strategy, c.s.ID, a,
				ev.ReasonRiskRefused, &gate, nil, asof); err != nil {
				return apply, refused, err
			}
			continue
		}
		budget := gate.Notional
		f, qty, avgPx, ok := papertrade.EnterLong(budget, c.in)
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
		book.GrossExposure += f.Qty * f.Px
		if sector != "" {
			book.ExposureBySector[sector] += f.Qty * f.Px
		}
		apply.Opens = append(apply.Opens, store.PaperPosition{
			Strategy: strategy, SymbolID: c.s.ID, Qty: qty, AvgPx: avgPx, OpenedTs: c.bar.Ts,
		})
		apply.Trades = append(apply.Trades, store.PaperTrade{
			Strategy: strategy, SymbolID: c.s.ID, Side: f.Side, Qty: f.Qty, Px: f.Px, Cost: f.Cost, Ts: c.bar.Ts,
			// The sizing rationale is part of the audit trail: a reader of the
			// log should be able to see WHY this size, not just this price.
			Reason: fmt.Sprintf("net_ev %.4f (rank %d/%d) · cal_prob %.3f >= long %.2f · %s",
				a.NetEV, a.Rank, a.RankOf, c.pred.CalProb, papertrade.LongThreshold(), gate.Sizing),
		})
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
