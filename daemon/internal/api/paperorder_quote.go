package api

import (
	"context"
	"fmt"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/papertrade"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

const manualQuoteMaxAgeSecs = int64(15 * 60)

// manualPaperStrategy returns the strategy key for a user's manual paper book.
// One simulated market-order book per user, keyed by user id, in the same paper_* tables as the flagship books.
func manualPaperStrategy(uid int64) string {
	return fmt.Sprintf("manual:u%d", uid)
}

// manualQuote builds ExecInputs for a market order using the most recent 1m bar.
//
// Liquidity (ADVUSD) uses SETTLED prior daily bars only. Volatility uses the newest
// settled daily bar when there is one and is otherwise left nil, which makes the
// execution model fall back to a 1-minute Parkinson sigma. That fallback is the
// honest description: in-session this does NOT match the flagship books, which
// always price impact off a completed daily bar.
func manualQuote(ctx context.Context, st *store.Store, sym md.Symbol, now int64) (papertrade.ExecInputs, string, error) {
	bars, err := st.LastBars(ctx, sym.ID, md.TF1m, 1)
	if err != nil {
		return papertrade.ExecInputs{}, "", err
	}
	if len(bars) == 0 {
		return papertrade.ExecInputs{}, fmt.Sprintf("no 1m bars stored for %s - market orders fill only against a live bar", sym.Symbol), nil
	}
	quote := bars[0]
	age := now - quote.Ts
	if age > manualQuoteMaxAgeSecs {
		return papertrade.ExecInputs{}, fmt.Sprintf("no fresh quote for %s: newest 1m bar is %ds old (market closed?) - market orders fill only against a live bar", sym.Symbol, age), nil
	}
	daily, err := st.BarsBefore(ctx, sym.ID, md.TF1d, quote.Ts, 21)
	if err != nil {
		return papertrade.ExecInputs{}, "", err
	}
	// Two skips the flagship advUSD already makes and this loop did not. BarsBefore
	// is bounded by the 1-MINUTE quote's ts, so today's still-forming daily bar sorts
	// before it and was averaged in at partial volume, dragging ADV down and
	// understating impact. A halted or untraded day contributed a zero and divided
	// the mean down again. Dividing by the bars actually summed, rather than by
	// len(daily), is the other half: skipping a bar must not also shrink the average.
	adv := 0.0
	var sum float64
	var n int
	for _, d := range daily {
		if d.Close <= 0 || d.Volume <= 0 {
			continue
		}
		if !md.DailyBarSettled(sym.Market, d.Ts, now) {
			continue
		}
		sum += d.Close * d.Volume
		n++
	}
	if n > 0 {
		adv = sum / float64(n)
	}
	var volBar *md.Bar
	if len(daily) > 0 {
		last := daily[len(daily)-1]
		if md.DailyBarSettled(sym.Market, last.Ts, now) {
			volBar = &last
		}
	}
	return papertrade.ExecInputs{
		Bar:           quote,
		Market:        sym.Market,
		ADVUSD:        adv,
		VolatilityBar: volBar,
	}, "", nil
}

// manualPositionsValue marks the OTHER open positions of the manual book at their newest stored price;
// the traded symbol is valued by the caller from the fill.
func manualPositionsValue(ctx context.Context, st *store.Store, strategy string, excludeSymbolID int64, now int64) (float64, error) {
	positions, err := st.PaperPositions(ctx, strategy)
	if err != nil {
		return 0, err
	}
	total := 0.0
	for _, p := range positions {
		if p.SymbolID == excludeSymbolID || p.Qty <= 0 {
			continue
		}
		price := p.AvgPx
		bars, err := st.LastBars(ctx, p.SymbolID, md.TF1m, 1)
		if err != nil {
			return 0, err
		}
		if len(bars) > 0 && bars[0].Close > 0 {
			price = bars[0].Close
		} else {
			db, ok, err := st.BarAtOrBefore(ctx, p.SymbolID, md.TF1d, now)
			if err != nil {
				return 0, err
			}
			if ok && db.Close > 0 {
				price = db.Close
			}
		}
		total += p.Qty * price
	}
	return total, nil
}
