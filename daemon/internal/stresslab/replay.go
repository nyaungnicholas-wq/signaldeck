package stresslab

import (
	"fmt"
	"math"

	"github.com/nyaungnicholas-wq/signaldeck/internal/papertrade"
	"github.com/nyaungnicholas-wq/signaldeck/internal/riskgate"
)

// Action is what a decider wants done with the committed signal.
type Action int

const (
	Hold Action = iota
	Long
	Flat
)

// Decider maps one committed calibrated P(up) to an action. It is an
// interface so the EV-gate decision engine (internal/ev, when it lands) can
// slot in without touching this harness; ThresholdDecider is the current
// production decision — the same 0.60/0.40 deadband signalbt and the paper
// book use.
type Decider interface {
	Decide(p float64) Action
}

// ThresholdDecider is the probability-threshold decision currently in
// production: at/above LongAt target long, at/below FlatAt target flat, in
// between hold.
type ThresholdDecider struct {
	LongAt float64
	FlatAt float64
}

// DefaultDecider mirrors signalbt/papertrade's deadband.
func DefaultDecider() ThresholdDecider { return ThresholdDecider{LongAt: 0.60, FlatAt: 0.40} }

func (t ThresholdDecider) Decide(p float64) Action {
	switch {
	case p >= t.LongAt:
		return Long
	case p <= t.FlatAt:
		return Flat
	default:
		return Hold
	}
}

// TradeEvent is one entry/exit (or refusal) in the replay, kept so the worst
// positions and the refusal ledger are inspectable, not just counted.
type TradeEvent struct {
	Ts       int64    `json:"ts"`
	Side     string   `json:"side"` // "buy" | "sell" | "refused"
	Notional float64  `json:"notional"`
	CostBps  float64  `json:"costBps"`
	Capped   bool     `json:"capped"`
	Reasons  []string `json:"reasons,omitempty"`
	Breaches []string `json:"breaches,omitempty"`
	// PnL is filled on the closing sell: realized round-trip PnL in dollars.
	PnL float64 `json:"pnl"`
}

// SystemBehavior is the replay's answer: not "would we have made money" but
// "what did the SYSTEM do" — which trades the gate allowed and refused, which
// breakers tripped, how the drawdown path evolved and how badly the fills
// degraded under the scenario.
type SystemBehavior struct {
	Scenario    string `json:"scenario"`
	Bars        int    `json:"bars"`
	SignalsSeen int    `json:"signalsSeen"`

	TradesTaken    int            `json:"tradesTaken"`
	TradesRefused  int            `json:"tradesRefused"`
	RefusalReasons map[string]int `json:"refusalReasons,omitempty"` // breach name -> count
	BreakerTrips   int            `json:"breakerTrips"`             // max-drawdown + max-daily-loss refusals

	StartEquity  float64   `json:"startEquity"`
	FinalEquity  float64   `json:"finalEquity"`
	MaxDrawdown  float64   `json:"maxDrawdown"`
	DrawdownPath []float64 `json:"drawdownPath"` // per bar, fraction below running peak

	// Fill degradation under the scenario's spread/ADV/vol effects.
	MeanFillCostBps float64 `json:"meanFillCostBps"`
	WorstFillCostBps float64 `json:"worstFillCostBps"`
	CappedFills      int    `json:"cappedFills"`

	// WorstTrades are the most damaging closed round trips (worst first,
	// capped at 5); OpenPositionPnL is the unrealized PnL still on the book
	// at the end of the window.
	WorstTrades     []TradeEvent `json:"worstTrades,omitempty"`
	OpenPositionPnL float64      `json:"openPositionPnl"`
}

// replayStartEquity is the synthetic book the replay runs on. The number is
// arbitrary (everything reported is fractional); it exists so the riskgate
// minimum-ticket floor behaves like it does on the real paper book.
const replayStartEquity = 100_000

// Replay runs one window through the real decision path in memory:
// committed signal -> Decider -> riskgate.Evaluate -> papertrade fill model,
// marking the book bar by bar. Pure: no I/O, no clock, nothing written.
//
// The riskgate Book fields are MEASURED from the replay's own equity path
// (current drawdown from the running peak, daily PnL per UTC day), so the
// breakers trip exactly the way the live worker's would.
func Replay(w Window, lim riskgate.Limits, dec Decider, label string) SystemBehavior {
	if dec == nil {
		dec = DefaultDecider()
	}
	if w.SpreadMult <= 0 {
		w.SpreadMult = 1
	}

	sb := SystemBehavior{
		Scenario:       label,
		Bars:           len(w.Bars),
		StartEquity:    replayStartEquity,
		RefusalReasons: map[string]int{},
	}
	if len(w.Bars) == 0 {
		sb.FinalEquity = replayStartEquity
		return sb
	}

	// Assign each signal to the first bar at/after its ts, then delay.
	sigAt := map[int]float64{} // bar index -> latest signal P landing there
	si := 0
	for i := range w.Bars {
		for si < len(w.Signals) && w.Signals[si].Ts <= w.Bars[i].Ts {
			j := i + w.DelayBars
			if j < len(w.Bars) {
				sigAt[j] = w.Signals[si].P
				sb.SignalsSeen++
			}
			si++
		}
	}

	cash := float64(replayStartEquity)
	var qty, entryCash float64
	var entryEv *TradeEvent
	peak := cash
	var closed []TradeEvent
	var costBpsSum float64
	var fills int

	dayOf := func(ts int64) int64 { return ts / 86400 }
	dayStartEq := cash
	curDay := dayOf(w.Bars[0].Ts)

	baseSpreadFrac := papertrade.CostBpsFor(w.Market) / 1e4

	for i, bar := range w.Bars {
		if d := dayOf(bar.Ts); d != curDay {
			curDay = d
			dayStartEq = cash + qty*bar.Open
		}

		equity := cash + qty*bar.Close
		book := riskgate.Book{
			Equity:        equity,
			Cash:          cash,
			DrawdownKnown: true,
			DailyKnown:    dayStartEq > 0,
			OpenPositions: 0,
		}
		if equity < peak && peak > 0 {
			book.CurrentDrawdown = (peak - equity) / peak
		}
		if dayStartEq > 0 {
			book.DailyPnLFrac = equity/dayStartEq - 1
		}
		if qty > 0 {
			book.OpenPositions = 1
		}

		p, has := sigAt[i]
		if has {
			in := papertrade.ExecInputs{Bar: bar, Market: w.Market, ADVUSD: w.ADVUSD}
			switch dec.Decide(p) {
			case Long:
				if qty > 0 {
					break // already positioned; the harness does not pyramid
				}
				d := riskgate.Evaluate(book, riskgate.Request{Symbol: "STRESS", Action: riskgate.Enter}, riskgate.Edge{}, lim)
				if !d.Allow {
					sb.TradesRefused++
					for _, br := range d.Breaches {
						sb.RefusalReasons[br]++
						if br == "max-drawdown" || br == "max-daily-loss" {
							sb.BreakerTrips++
						}
					}
					closedRefusal := TradeEvent{Ts: bar.Ts, Side: "refused", Reasons: d.Reasons, Breaches: d.Breaches}
					closed = append(closed, closedRefusal)
					break
				}
				budget := math.Min(d.Notional, cash)
				fill, gotQty, _, ok := papertrade.EnterLong(budget, in)
				if !ok {
					sb.TradesRefused++
					sb.RefusalReasons["execution:"+fill.Reason]++
					break
				}
				// Scenario spread widening: the extra spread the papertrade
				// constant cannot express, charged on the filled notional.
				extra := fill.Qty * fill.Px * baseSpreadFrac * (w.SpreadMult - 1)
				cash += fill.CashDelta - extra
				qty = gotQty
				entryCash = fill.Qty*fill.Px + fill.Cost + extra
				cb := fill.SpreadBps*w.SpreadMult + fill.ImpactBps
				costBpsSum += cb
				fills++
				if cb > sb.WorstFillCostBps {
					sb.WorstFillCostBps = cb
				}
				if fill.Capped {
					sb.CappedFills++
				}
				sb.TradesTaken++
				entryEv = &TradeEvent{Ts: bar.Ts, Side: "buy", Notional: fill.Qty * fill.Px, CostBps: cb, Capped: fill.Capped}
			case Flat:
				if qty <= 0 {
					break
				}
				// Exits are never gated (riskgate's own first rule).
				fill, ok := papertrade.ExitLong(qty, in)
				if !ok {
					// Cannot even price the exit (e.g. ADV gone) — the position
					// is stuck; record the refusal and carry it.
					sb.RefusalReasons["exit-unpriceable:"+fill.Reason]++
					break
				}
				extra := fill.Qty * fill.Px * baseSpreadFrac * (w.SpreadMult - 1)
				cash += fill.CashDelta - extra
				cb := fill.SpreadBps*w.SpreadMult + fill.ImpactBps
				costBpsSum += cb
				fills++
				if cb > sb.WorstFillCostBps {
					sb.WorstFillCostBps = cb
				}
				if fill.Capped {
					sb.CappedFills++
				}
				sb.TradesTaken++
				ev := TradeEvent{Ts: bar.Ts, Side: "sell", Notional: fill.Qty * fill.Px, CostBps: cb, Capped: fill.Capped,
					PnL: (fill.CashDelta - extra) - entryCash}
				if entryEv != nil {
					ev.Reasons = []string{fmt.Sprintf("round trip entered at ts %d", entryEv.Ts)}
				}
				closed = append(closed, ev)
				qty, entryCash, entryEv = 0, 0, nil
			}
		}

		equity = cash + qty*bar.Close
		if equity > peak {
			peak = equity
		}
		dd := 0.0
		if peak > 0 && equity < peak {
			dd = (peak - equity) / peak
		}
		sb.DrawdownPath = append(sb.DrawdownPath, dd)
		if dd > sb.MaxDrawdown {
			sb.MaxDrawdown = dd
		}
	}

	last := w.Bars[len(w.Bars)-1]
	sb.FinalEquity = cash + qty*last.Close
	if qty > 0 {
		sb.OpenPositionPnL = qty*last.Close - entryCash
	}
	if fills > 0 {
		sb.MeanFillCostBps = costBpsSum / float64(fills)
	}

	// Worst closed round trips, worst first, capped at 5.
	sortWorst(closed)
	if len(closed) > 5 {
		closed = closed[:5]
	}
	sb.WorstTrades = closed
	return sb
}

func sortWorst(evs []TradeEvent) {
	for i := 1; i < len(evs); i++ {
		for j := i; j > 0 && evs[j].PnL < evs[j-1].PnL; j-- {
			evs[j], evs[j-1] = evs[j-1], evs[j]
		}
	}
}
