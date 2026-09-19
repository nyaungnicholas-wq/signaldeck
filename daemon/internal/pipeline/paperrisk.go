package pipeline

import (
	"context"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/papertrade"
	"github.com/nyaungnicholas-wq/signaldeck/internal/riskgate"
	"github.com/nyaungnicholas-wq/signaldeck/internal/sectors"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// ── RISK CONTEXT FOR THE PAPER BOOK ─────────────────────────────────────────
//
// internal/riskgate is a pure function and holds no state by design, so someone
// has to MEASURE the book before it can be judged. That is this file: it reads
// the strategy's own equity curve and fill log and assembles the two inputs the
// gate needs — the book's condition and its realized edge.
//
// Everything here is measured from what the book actually did. Nothing is
// estimated, and where a measurement is unavailable the corresponding Known flag
// stays false so the gate can report an UNARMED breaker rather than a clean pass.

// riskCurveLimit bounds how much equity history the drawdown breaker reads. The
// breaker needs the running peak, so it wants the whole curve; 5000 marks is
// roughly twenty years of daily marks, which is more history than this book can
// possibly have accrued and is the store's own default page size.
const riskCurveLimit = 5000

// riskBook measures the book's condition for the gate.
//
// equity and cash are passed in rather than re-read because buildStep already
// holds the authoritative values for THIS step (the stored cursor plus the
// positions it just marked), and re-deriving them here could disagree with the
// arithmetic the step is about to commit.
func (w *PaperTrader) riskBook(
	ctx context.Context,
	strategy string,
	equity, cash float64,
	symByID map[int64]string,
	asof int64,
) (riskgate.Book, error) {
	b := riskgate.Book{
		Equity:           equity,
		Cash:             cash,
		ExposureBySector: map[string]float64{},
	}

	rows, err := w.St.PaperEquityCurve(ctx, strategy, riskCurveLimit)
	if err != nil {
		return b, err
	}
	curve := make([]papertrade.EquityPoint, 0, len(rows))
	for _, r := range rows {
		curve = append(curve, papertrade.EquityPoint{
			Ts: r.Ts, Cash: r.Cash, PositionsValue: r.PositionsValue, Equity: r.Equity,
		})
	}
	// DRAWDOWN IS DELIBERATELY *NOT* EPOCH-SCOPED, unlike the sizing edge.
	//
	// The two measure different things. The edge is a property of the STRATEGY —
	// "what payoff shape do these rules produce?" — so it must be re-measured
	// when the rules change. Drawdown is a property of the CAPITAL — "how far is
	// this book below its high-water mark?" — and under an epoch boundary the
	// capital is explicitly carried across unchanged: same cash, same positions.
	//
	// Re-basing the peak at a boundary would let the book lose MaxDrawdown in
	// epoch 1 and MaxDrawdown again in epoch 2 without the breaker ever firing,
	// which is a larger hole than the attribution problem scoping it would fix.
	// A strategy change is not a reason to forgive the losses that preceded it.
	// The epoch-scoped drawdown is still reported, for attribution, beside this
	// one — see the paper API's per-epoch segments.
	if dd, ok := papertrade.CurrentDrawdown(curve); ok {
		b.CurrentDrawdown = dd
		b.DrawdownKnown = true
	}

	// The loss limit over the most recent COMPLETED marking period. This book
	// marks once per new daily bar and this step has not marked yet, so the
	// freshest completed period is the move between the last two stored marks —
	// for this strategy, the previous session. Calling it "today's" loss would
	// overstate what is measurable at decision time.
	if n := len(curve); n >= 2 {
		prev := curve[n-2].Equity
		if prev > 0 {
			b.DailyPnLFrac = curve[n-1].Equity/prev - 1
			b.DailyKnown = true
		}
	}

	// Open positions and their sector exposure, valued at the as-of clock.
	stored, err := w.St.PaperPositions(ctx, strategy)
	if err != nil {
		return b, err
	}
	// Gross exposure is only KNOWN if every open position could be marked. One
	// unmarked name makes the total an understatement, and an understated gross
	// hands out headroom the book may not have — so the cap goes unarmed rather
	// than arming on a number that is wrong in the permissive direction.
	allMarked := true
	for _, p := range stored {
		if p.Qty <= 0 {
			continue
		}
		b.OpenPositions++
		bar, ok, err := w.St.BarAtOrBefore(ctx, p.SymbolID, md.TF1d, asof)
		if err != nil {
			return b, err
		}
		if !ok || bar.Close <= 0 {
			allMarked = false
			continue // no mark: counted as a position, contributes no measurable exposure
		}
		notional := p.Qty * bar.Close
		b.GrossExposure += notional
		if sec := riskSector(symByID[p.SymbolID]); sec != "" {
			b.ExposureBySector[sec] += notional
		}
	}
	b.GrossKnown = allMarked
	return b, nil
}

// riskSector maps a symbol to the sector label the concentration cap uses, and
// returns "" for anything unclassified.
//
// This matters more than it looks. sectors.SectorOf returns OtherSector for every
// symbol outside its static map, which is most of the broad universe — so
// passing that label straight through would treat dozens of unrelated names as
// ONE sector and throttle the whole book at the sector cap. An unclassified name
// concentrates nothing that can be demonstrated, so it is bounded by the
// per-position cap alone, which is what the gate does with an empty sector.
func riskSector(symbol string) string {
	if symbol == "" {
		return ""
	}
	sec := sectors.SectorOf(symbol)
	if sec == sectors.OtherSector {
		return ""
	}
	return sec
}

// tradedEdge measures the strategy's realized edge from its own fill log: FIFO
// round trips, then the win rate and payoff ratio those round trips imply.
//
// This is the ONLY source of the sizing edge, deliberately. A calibrated
// probability is a forecast; a Kelly fraction has to be paid for out of realized
// P&L, and the fill log net of modelled execution cost is the only record of that
// in the system.
//
// EPOCH-SCOPED, and this is the whole reason epochs exist. The edge answers
// "what payoff shape does THIS strategy produce?", so it may only be measured
// over round trips this strategy actually made. Sizing today's trade from a
// previous rule set's win rate is the most expensive form of the cross-regime
// error: it does not merely misreport the past, it stakes real position size on
// a number the current strategy never earned.
//
// A round trip that STRADDLES a boundary — bought under the old rules, sold
// under the new — is counted by NEITHER epoch. Filtering to trades at or after
// the boundary leaves its sell with no matching buy, and MatchRoundTrips drops
// an unmatched sell. That is the honest outcome: a trip whose entry and exit
// were decided by different strategies is evidence about neither.
func (w *PaperTrader) tradedEdge(ctx context.Context, strategy string, asof int64) (riskgate.Edge, error) {
	trades, err := w.St.AllPaperTradesAsc(ctx, strategy)
	if err != nil {
		return riskgate.Edge{}, err
	}
	from, to, err := w.epochWindow(ctx, strategy, asof)
	if err != nil {
		return riskgate.Edge{}, err
	}
	if from > 0 || to > 0 {
		scoped := trades[:0]
		for _, t := range trades {
			if store.InEpoch(t.Ts, from, to) {
				scoped = append(scoped, t)
			}
		}
		trades = scoped
	}
	matched := papertrade.MatchRoundTrips(toFillRecords(trades))
	p := papertrade.Payoffs(matched.Closed)
	return riskgate.Edge{
		WinRate:     p.WinRate,
		PayoffRatio: p.PayoffRatio,
		Trips:       p.N,
		Valid:       p.Valid,
	}, nil
}

// toFillRecords converts stored trades into the matcher's input shape. It exists
// so internal/papertrade stays free of the store.
func toFillRecords(trades []store.PaperTrade) []papertrade.FillRecord {
	out := make([]papertrade.FillRecord, 0, len(trades))
	for _, t := range trades {
		out = append(out, papertrade.FillRecord{
			SymbolID: t.SymbolID,
			Side:     t.Side,
			Qty:      t.Qty,
			Px:       t.Px,
			Cost:     t.Cost,
			Ts:       t.Ts,
		})
	}
	return out
}

// releasePosition removes a name the step has just SOLD from the risk book,
// mirroring exactly how riskBook accumulated it.
//
// Without this, an exit frees CASH but not HEADROOM. riskBook is measured once,
// before Phase 1 runs, from the STORED positions; Phase 1 then sells into
// apply.CloseSymbolIDs and refreshes only book.Cash. The sold name keeps its
// slot in OpenPositions, its notional in GrossExposure and its bucket in
// ExposureBySector, so Phase 2 judges entries against exposure that no longer
// exists: at a binding cap every candidate is refused and ledgered
// risk-gate-refused while the book is in fact flat. buildStep runs once per new
// daily bar, so that costs a full trading day, not an hour.
//
// The arithmetic must MATCH riskBook or the book drifts: the same
// BarAtOrBefore(asof) mark, the same riskSector label, and the same treatment of
// an unmarkable name (counted as a position, contributing no exposure).
// GrossKnown is deliberately NOT recomputed — releasing one unmarked name tells
// us nothing about whether the others could be marked.
func (w *PaperTrader) releasePosition(
	ctx context.Context,
	b *riskgate.Book,
	symbolID int64,
	qty float64,
	symByID map[int64]string,
	asof int64,
) error {
	if qty <= 0 {
		return nil
	}
	if b.OpenPositions > 0 {
		b.OpenPositions--
	}
	bar, ok, err := w.St.BarAtOrBefore(ctx, symbolID, md.TF1d, asof)
	if err != nil {
		return err
	}
	if !ok || bar.Close <= 0 {
		return nil // never contributed exposure; riskBook already cleared GrossKnown
	}
	notional := qty * bar.Close
	b.GrossExposure -= notional
	if b.GrossExposure < 0 {
		b.GrossExposure = 0 // float drift only; a negative gross is meaningless
	}
	if sec := riskSector(symByID[symbolID]); sec != "" {
		b.ExposureBySector[sec] -= notional
		if b.ExposureBySector[sec] <= 0 {
			delete(b.ExposureBySector, sec)
		}
	}
	return nil
}
