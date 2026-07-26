package papertrade

import (
	"math"
	"sort"
)

// ── REALIZED ROUND TRIPS ────────────────────────────────────────────────────
//
// Summary already reports a win RATE, and a win rate on its own cannot size a
// position. A 60%-hit-rate edge that loses three times what it wins is ruin; the
// same hit rate winning three times what it loses is being underbet. Everything
// that needs the OTHER half of the distribution — Kelly sizing, profit factor,
// the downside-only deviation Sortino asks for — needs realized P&L per
// completed round trip, and the only place that number existed was the fill log,
// unextracted.
//
// The log carries one row per FILL (side, quantity, price, execution cost) and
// says nothing about which buy a given sell closed, so round trips have to be
// reconstructed. Matching is FIFO — a sell closes the oldest open lot — which is
// both the convention tax lots default to and, more to the point, the only rule
// that needs no information the log does not already hold.
//
// WHAT THIS DELIBERATELY DOES NOT DO:
//
//   - It does not mark open lots. An open position has no realized P&L, and
//     counting its paper gain is exactly the unrealized-optimism this package
//     exists to avoid. Open quantity is reported separately so a caller can see
//     how much of the book is still unresolved.
//   - It does not infer a short book. This engine is long/flat by construction,
//     so a sell with no open lot is a DEFECT in the log (a dropped buy, a
//     double-applied step), and it is reported as UnmatchedSellQty rather than
//     silently booked as a profitable short.
//   - It does not net across strategies. Callers pass one strategy's log; two
//     books that happen to hold the same symbol are two books.

// qtyEpsilon is the quantity below which a lot is considered fully consumed.
// Fills are floats and repeated partial matches accumulate representation error,
// so an exact == 0 test would leave micro-lots alive forever and mis-order the
// FIFO queue behind them.
const qtyEpsilon = 1e-9

// FillRecord is one fill from the trade log in the minimal shape the matcher
// needs. The store's PaperTrade rows convert to this at the call site so this
// package stays free of the store — it performs no I/O, and that is worth
// keeping, because it is what makes every rule here unit-testable without a
// database.
type FillRecord struct {
	SymbolID int64
	Side     string // "buy" | "sell"
	Qty      float64
	Px       float64
	Cost     float64 // execution cost charged on THIS fill, in dollars
	Ts       int64
}

// RoundTrip is one completed buy→sell cycle over a matched quantity. Cost is
// both sides' execution charge attributed to that quantity, so PnL is what the
// book actually kept, not a gross price difference.
type RoundTrip struct {
	SymbolID int64   `json:"-"`
	Qty      float64 `json:"qty"`
	EntryPx  float64 `json:"entryPx"`
	ExitPx   float64 `json:"exitPx"`
	EntryTs  int64   `json:"entryTs"`
	ExitTs   int64   `json:"exitTs"`
	Cost     float64 `json:"cost"` // prorated entry cost + prorated exit cost
	PnL      float64 `json:"pnl"`  // (ExitPx-EntryPx)*Qty - Cost
	Won      bool    `json:"won"`  // PnL > 0 (a scratch is not a win)
}

// MatchResult is the reconstruction of a fill log.
type MatchResult struct {
	// Closed round trips in exit order (a sell that consumes three lots yields
	// three round trips, oldest lot first).
	Closed []RoundTrip
	// OpenQty is quantity per symbol still open at the end of the log. Present
	// so a caller can tell "no round trips because the book is young" from "no
	// round trips because nothing ever traded".
	OpenQty map[int64]float64
	// UnmatchedSellQty is sell quantity that closed nothing. Non-zero means the
	// log is missing buys it should have — a data defect worth surfacing, never
	// a short position.
	UnmatchedSellQty float64
	// Unusable counts fills rejected for a non-positive or non-finite quantity
	// or price. They are skipped, not guessed at.
	Unusable int
}

// lot is one open FIFO parcel. costPerUnit carries the entry's execution charge
// so a partial exit pays only its share of it.
type lot struct {
	qty         float64
	px          float64
	costPerUnit float64
	ts          int64
}

// MatchRoundTrips reconstructs completed round trips from a fill log.
//
// It never mutates its input: the log is copied and stably sorted by ts, so
// fills recorded at the same timestamp keep the order the caller supplied
// (which, for this engine, is the order the worker applied them in).
func MatchRoundTrips(fills []FillRecord) MatchResult {
	res := MatchResult{OpenQty: map[int64]float64{}}

	ordered := make([]FillRecord, len(fills))
	copy(ordered, fills)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Ts < ordered[j].Ts })

	open := map[int64][]lot{}
	for _, f := range ordered {
		if !usableFill(f) {
			res.Unusable++
			continue
		}
		switch f.Side {
		case "buy":
			open[f.SymbolID] = append(open[f.SymbolID], lot{
				qty:         f.Qty,
				px:          f.Px,
				costPerUnit: f.Cost / f.Qty,
				ts:          f.Ts,
			})
		case "sell":
			remaining := f.Qty
			exitCostPerUnit := f.Cost / f.Qty
			lots := open[f.SymbolID]
			for remaining > qtyEpsilon && len(lots) > 0 {
				l := &lots[0]
				q := math.Min(remaining, l.qty)
				cost := (l.costPerUnit + exitCostPerUnit) * q
				pnl := (f.Px-l.px)*q - cost
				res.Closed = append(res.Closed, RoundTrip{
					SymbolID: f.SymbolID,
					Qty:      q,
					EntryPx:  l.px,
					ExitPx:   f.Px,
					EntryTs:  l.ts,
					ExitTs:   f.Ts,
					Cost:     cost,
					PnL:      pnl,
					Won:      pnl > 0,
				})
				l.qty -= q
				remaining -= q
				if l.qty <= qtyEpsilon {
					lots = lots[1:]
				}
			}
			open[f.SymbolID] = lots
			if remaining > qtyEpsilon {
				res.UnmatchedSellQty += remaining
			}
		default:
			res.Unusable++
		}
	}

	for id, lots := range open {
		var q float64
		for _, l := range lots {
			q += l.qty
		}
		if q > qtyEpsilon {
			res.OpenQty[id] = q
		}
	}
	return res
}

// usableFill rejects a fill the matcher cannot price. A zero or negative
// quantity would divide the cost by zero; a non-finite price would poison every
// statistic downstream of it.
func usableFill(f FillRecord) bool {
	if f.Qty <= 0 || math.IsNaN(f.Qty) || math.IsInf(f.Qty, 0) {
		return false
	}
	if f.Px <= 0 || math.IsNaN(f.Px) || math.IsInf(f.Px, 0) {
		return false
	}
	if math.IsNaN(f.Cost) || math.IsInf(f.Cost, 0) || f.Cost < 0 {
		return false
	}
	return true
}

// MinPayoffTrips is the number of closed round trips below which the payoff
// shape is WITHHELD rather than reported.
//
// It is deliberately stricter than minWinRateTrades (which gates a number the UI
// merely displays) because a payoff ratio is a ratio of two conditional means:
// it needs enough of BOTH tails to be a statistic, and it is the input to
// position sizing, where being confidently wrong costs money rather than
// credibility. Twenty is the floor at which each tail plausibly holds a handful
// of observations; it is not a claim that twenty is statistically comfortable.
const MinPayoffTrips = 20

// Payoff is the realized win/loss shape of a set of closed round trips — the
// half of the distribution a win rate omits.
//
// AvgLoss and GrossLoss are POSITIVE magnitudes so the ratios below read the way
// their names do. Valid is false whenever the sample cannot support the ratios,
// and Note says why; a caller that ignores Valid and uses PayoffRatio anyway is
// sizing on noise.
type Payoff struct {
	N      int `json:"n"`
	Wins   int `json:"wins"`
	Losses int `json:"losses"`

	WinRate float64 `json:"winRate"`
	AvgWin  float64 `json:"avgWin"`  // mean P&L of winners
	AvgLoss float64 `json:"avgLoss"` // mean |P&L| of losers, positive

	// PayoffRatio is AvgWin/AvgLoss — the b in Kelly's b:1 odds.
	PayoffRatio float64 `json:"payoffRatio"`
	// ProfitFactor is gross wins / gross losses. Above 1 the book made money.
	ProfitFactor float64 `json:"profitFactor"`

	GrossWin  float64 `json:"grossWin"`
	GrossLoss float64 `json:"grossLoss"` // positive
	NetPnL    float64 `json:"netPnl"`

	Valid bool   `json:"valid"`
	Note  string `json:"note"`
}

// Payoffs summarizes closed round trips into the win/loss shape sizing needs.
//
// A round trip with exactly zero P&L is neither a win nor a loss: it is counted
// in N and excluded from both tails, because rounding a scratch into the winners
// would inflate a hit rate that sizing then compounds.
func Payoffs(rts []RoundTrip) Payoff {
	p := Payoff{N: len(rts)}
	for _, r := range rts {
		switch {
		case r.PnL > 0:
			p.Wins++
			p.GrossWin += r.PnL
		case r.PnL < 0:
			p.Losses++
			p.GrossLoss += -r.PnL
		}
		p.NetPnL += r.PnL
	}
	if p.N > 0 {
		p.WinRate = float64(p.Wins) / float64(p.N)
	}
	if p.Wins > 0 {
		p.AvgWin = p.GrossWin / float64(p.Wins)
	}
	if p.Losses > 0 {
		p.AvgLoss = p.GrossLoss / float64(p.Losses)
	}
	if p.AvgLoss > 0 {
		p.PayoffRatio = p.AvgWin / p.AvgLoss
	}
	if p.GrossLoss > 0 {
		p.ProfitFactor = p.GrossWin / p.GrossLoss
	}

	switch {
	case p.N < MinPayoffTrips:
		p.Note = "too few closed round trips to describe a payoff shape — withheld until the book has resolved enough of both outcomes"
	case p.Wins == 0:
		p.Note = "no winning round trip yet: a payoff ratio needs both tails, and this sample has only losses"
	case p.Losses == 0:
		// An unbroken winning streak is the most dangerous input a sizing rule
		// can be handed: AvgLoss is 0, so every ratio built on it is infinite,
		// and full Kelly on infinite odds is the whole book.
		p.Note = "no losing round trip yet, so the payoff ratio is unbounded — withheld rather than sized on"
	default:
		p.Valid = true
		p.Note = "realized round-trip payoff, net of both sides' modelled execution cost"
	}
	return p
}
