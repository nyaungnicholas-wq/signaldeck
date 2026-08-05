package api

// PER-EPOCH SEGMENTATION for the published paper-book statistics.
//
// The rule this file exists to enforce: no summary statistic may span a
// strategy change. A return, a Sharpe, a drawdown or a win rate computed across
// a boundary describes a strategy that was never run — it is an average of two
// different things presented as one.
//
// The book itself is continuous (see store/paper_epochs). Only the measurement
// splits, so `equity` and `trades` in the payload stay whole and the SEGMENTS
// carry the per-epoch numbers beside them.

import (
	"context"

	"github.com/nyaungnicholas-wq/signaldeck/internal/moneymetrics"
	"github.com/nyaungnicholas-wq/signaldeck/internal/papertrade"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// paperEpochCaption sits beside the segments so a reader cannot mistake the
// book-wide headline for a statement about the strategy running today.
const paperEpochCaption = "The book is continuous; the record is not. " +
	"`summary` and `money` span every strategy this book has run — they describe the CAPITAL. " +
	"Each entry in `epochs` describes ONE strategy, and only those numbers may be quoted as a strategy's record. " +
	"A round trip that straddles a boundary is counted by neither epoch and appears as `straddlingRoundTrips`."

// EpochSegment is one epoch's own record: its boundary, and every published
// statistic recomputed over that window alone.
type EpochSegment struct {
	Epoch  int    `json:"epoch"`
	Label  string `json:"label"`
	Reason string `json:"reason"`
	FromTs int64  `json:"fromTs"`
	ToTs   int64  `json:"toTs"` // exclusive; 0 = still open

	Marks      int `json:"marks"`                // equity marks inside the window
	Fills      int `json:"fills"`                // fills inside the window
	Straddling int `json:"straddlingRoundTrips"` // opened before it, closed inside it

	Summary papertrade.Summary `json:"summary"`
	Money   moneymetrics.Money `json:"money"`
}

// paperEpochSegments recomputes the published statistics once per epoch.
//
// Round trips are matched WITHIN the window, so a trip that straddles the
// boundary — bought under one rule set, sold under the next — belongs to
// neither epoch and is counted by neither. It is reported as `straddling` so
// the omission is visible rather than silent: a reader who sees 3 fills and 1
// round trip should be able to find out where the other fill went.
func paperEpochSegments(
	ctx context.Context,
	st *store.Store,
	strategy string,
	curve []papertrade.EquityPoint,
	all []store.PaperTrade,
) ([]EpochSegment, error) {
	epochs, err := st.PaperEpochs(ctx, strategy)
	if err != nil {
		return nil, err
	}
	if len(epochs) == 0 {
		return nil, nil
	}

	out := make([]EpochSegment, 0, len(epochs))
	for i, e := range epochs {
		from, to := store.EpochBounds(epochs, i)

		seg := EpochSegment{
			Epoch: e.Epoch, Label: e.Label, Reason: e.Reason, FromTs: from, ToTs: to,
		}

		var winCurve []papertrade.EquityPoint
		for _, p := range curve {
			if store.InEpoch(p.Ts, from, to) {
				winCurve = append(winCurve, p)
			}
		}
		var winTrades []store.PaperTrade
		for _, t := range all {
			if store.InEpoch(t.Ts, from, to) {
				winTrades = append(winTrades, t)
			}
		}
		seg.Marks, seg.Fills = len(winCurve), len(winTrades)

		closed, numFills, tradedNotional := reconstructRoundTrips(winTrades)
		seg.Summary = papertrade.Summarize(winCurve, closed, numFills, tradedNotional)
		seg.Money = moneymetrics.FromReturns(roundTripReturns(winTrades))

		// Sells inside the window whose buy is outside it: the straddlers the
		// matcher dropped. Counting them here is what keeps the omission honest.
		seg.Straddling = countStraddlingExits(winTrades)

		out = append(out, seg)
	}
	return out, nil
}

// countStraddlingExits counts sells in a window that have no matching buy in
// the same window — round trips whose entry belonged to an earlier epoch.
func countStraddlingExits(win []store.PaperTrade) int {
	openBySymbol := map[int64]int{}
	for _, t := range win {
		if t.Side == "buy" {
			openBySymbol[t.SymbolID]++
		}
	}
	straddling := 0
	for _, t := range win {
		if t.Side != "sell" {
			continue
		}
		if openBySymbol[t.SymbolID] > 0 {
			openBySymbol[t.SymbolID]--
			continue
		}
		straddling++
	}
	return straddling
}
