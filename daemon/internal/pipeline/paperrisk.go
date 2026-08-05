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
func (w *PaperTrader) tradedEdge(ctx context.Context, strategy string) (riskgate.Edge, error) {
	trades, err := w.St.AllPaperTradesAsc(ctx, strategy)
	if err != nil {
		return riskgate.Edge{}, err
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
