package api

import (
	"math"

	"github.com/nyaungnicholas-wq/signaldeck/internal/papertrade"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// paperFills converts []store.PaperTrade to []papertrade.FillRecord preserving order.
// This replaces the previous helper that kept only one open lot per symbol.
func paperFills(all []store.PaperTrade) []papertrade.FillRecord {
	fills := make([]papertrade.FillRecord, len(all))
	for i, pt := range all {
		fills[i] = papertrade.FillRecord{
			SymbolID: pt.SymbolID,
			Side:     pt.Side,
			Qty:      pt.Qty,
			Px:       pt.Px,
			Cost:     pt.Cost,
			Ts:       pt.Ts,
		}
	}
	return fills
}

// roundTripReturns computes the return (PnL/outlay) for each closed round trip
// derived from FIFO matching of paper trades. It replaces the single-open-lot
// version which incorrectly handled multiple buys before a sell.
func roundTripReturns(all []store.PaperTrade) []float64 {
	fills := paperFills(all)
	mr := papertrade.MatchRoundTrips(fills)
	var rets []float64
	for _, rt := range mr.Closed {
		outlay := rt.EntryPx * rt.Qty
		if outlay <= 0 {
			continue
		}
		rets = append(rets, rt.PnL/outlay)
	}
	return rets
}

// reconstructRoundTrips returns slice of papertrade.Trade representing each
// closed round trip, the number of fills processed, and the total traded
// notional (sum of |Px*Qty|). It replaces the earlier single-open-lot logic
// that lost partial fills and overwrote earlier buys.
func reconstructRoundTrips(all []store.PaperTrade) ([]papertrade.Trade, int, float64) {
	fills := paperFills(all)
	mr := papertrade.MatchRoundTrips(fills)

	var trades []papertrade.Trade
	var tradedNotional float64
	for _, pt := range all {
		tradedNotional += math.Abs(pt.Px * pt.Qty)
	}
	for _, rt := range mr.Closed {
		trades = append(trades, papertrade.Trade{
			Won:      rt.Won,
			Notional: rt.ExitPx * rt.Qty,
		})
	}
	return trades, len(all), tradedNotional
}
