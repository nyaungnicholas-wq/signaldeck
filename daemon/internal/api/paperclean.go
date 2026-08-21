package api

// THE CONTAMINATED BOOK AND THE CLEAN RECORD.
//
// Before 2026-07-22, buildStep placed no lower bound on the fill bar, so a stale
// prediction filled at a bar the book had already marched past while every
// decision input was measured at the as-of clock. 46 of 123 fills were
// back-dated by up to 22 days, each booking the intervening move as one step's
// P&L. The defect is fixed and the fills are PRESERVED — quarantining by an
// epoch rather than deleting is what keeps the audit trail intact.
//
// But an epoch boundary only splits the MEASUREMENT. The book is continuous:
// cash and open positions carried across, so the equity LEVEL after the boundary
// still contains the fabricated P&L. Any statistic derived from a level that
// spans it — a total return, a Sharpe, a max drawdown, a win rate — is computed
// partly from moves that never happened.
//
// This file draws the line the payload needs:
//
//	ACCOUNTING EQUITY   the whole level series, contaminated, labelled, kept.
//	                    It is what the simulated account is worth. It is not
//	                    performance and must never be quoted as such.
//	CLEAN PERFORMANCE   the post-boundary window only, REBASED to a stated index
//	                    so it can be read as a return series in its own right.
//
// A derived statistic that would span the boundary is REFUSED, with the reason
// in the payload, rather than published with a footnote. A footnote on a number
// is read as a number.

import (
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/moneymetrics"
	"github.com/nyaungnicholas-wq/signaldeck/internal/papertrade"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// integrityEpochLabel marks the epoch whose START is a data-integrity boundary
// rather than a strategy change. Matching on the label keeps this in one place:
// a future integrity boundary is declared by reusing it (see
// pipeline/paperepoch.go), not by editing this file.
const integrityEpochLabel = "backdated-fills-fixed"

// CleanIndexBase is the value the clean series is rebased to at its first mark.
// An index, not a currency amount: the point is that the LEVEL carried across
// the boundary is contaminated, so re-using it as a starting balance would carry
// the contamination straight into the "clean" series.
const CleanIndexBase = 100.0

// cleanPerformanceNote ships with the block.
const cleanPerformanceNote = "CLEAN PERFORMANCE is the post-integrity-boundary window ONLY, rebased to an index of " +
	"100 at its first mark. It is rebased rather than started from the carried-over equity because that level still " +
	"contains the back-dated P&L this boundary exists to exclude. `equity` elsewhere in this payload is the ACCOUNTING " +
	"level of the simulated book across all time, including the contaminated period: it is what the account is worth, " +
	"not what the strategy earned."

// CleanPerformance is the post-boundary record, computed over that window alone.
type CleanPerformance struct {
	// Available is false when no integrity boundary is declared for this
	// strategy, or when no equity mark falls after it.
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`

	BoundaryTs  int64   `json:"boundaryTs"`
	BoundaryUTC string  `json:"boundaryUtc"`
	IndexBase   float64 `json:"indexBase"`

	Marks int `json:"marks"`
	Fills int `json:"fills"`

	// Index is the rebased equity series: the only chart that may be shown as
	// this strategy's performance.
	Index []papertrade.EquityPoint `json:"index"`

	Summary papertrade.Summary `json:"summary"`
	Money   moneymetrics.Money `json:"money"`
	Note    string             `json:"note"`
}

// RefusedStat replaces a statistic that cannot be computed without crossing the
// integrity boundary.
type RefusedStat struct {
	Refused     bool   `json:"refused"`
	Reason      string `json:"reason"`
	BoundaryTs  int64  `json:"boundaryTs"`
	BoundaryUTC string `json:"boundaryUtc"`
	UseInstead  string `json:"useInstead"`
}

// integrityBoundary returns the ts of the declared integrity boundary for a
// strategy, or 0 when none is declared.
func integrityBoundary(epochs []store.PaperEpoch) int64 {
	for _, e := range epochs {
		if e.Label == integrityEpochLabel {
			return e.FromTs
		}
	}
	return 0
}

// SpansIntegrityBoundary reports whether a window of equity marks straddles the
// boundary. A window entirely on one side of it is fine; one that crosses it is
// describing two different books as one.
func SpansIntegrityBoundary(curve []papertrade.EquityPoint, boundary int64) bool {
	if boundary <= 0 || len(curve) == 0 {
		return false
	}
	var before, after bool
	for _, p := range curve {
		if p.Ts < boundary {
			before = true
		} else {
			after = true
		}
		if before && after {
			return true
		}
	}
	return false
}

// refuseAcrossBoundary builds the refusal that replaces a spanning statistic.
func refuseAcrossBoundary(boundary int64) RefusedStat {
	return RefusedStat{
		Refused:     true,
		BoundaryTs:  boundary,
		BoundaryUTC: time.Unix(boundary, 0).UTC().Format(time.RFC3339),
		Reason: "this statistic would span the data-integrity boundary. Before it, 46 of 123 fills were " +
			"back-dated by up to 22 days and booked the intervening move as one step's P&L. A return, Sharpe, " +
			"drawdown or win rate computed across that instant is derived partly from moves that never happened, " +
			"so it is refused rather than published with a caveat.",
		UseInstead: "cleanPerformance (post-boundary, rebased) for the strategy's record, or epochs[] for any single epoch",
	}
}

// buildCleanPerformance computes the post-boundary record.
//
// The rebasing is the load-bearing step. Carrying the boundary-crossing equity
// LEVEL forward as a starting balance would put the fabricated P&L into the
// denominator of every clean return; indexing to a stated base removes it and
// says so in the payload.
func buildCleanPerformance(
	epochs []store.PaperEpoch,
	curve []papertrade.EquityPoint,
	all []store.PaperTrade,
) CleanPerformance {
	out := CleanPerformance{IndexBase: CleanIndexBase, Note: cleanPerformanceNote}
	boundary := integrityBoundary(epochs)
	if boundary <= 0 {
		out.Reason = "no data-integrity boundary is declared for this strategy, so the whole record is one basis"
		return out
	}
	out.BoundaryTs = boundary
	out.BoundaryUTC = time.Unix(boundary, 0).UTC().Format(time.RFC3339)

	var win []papertrade.EquityPoint
	for _, p := range curve {
		if p.Ts >= boundary {
			win = append(win, p)
		}
	}
	if len(win) == 0 {
		out.Reason = "no equity mark falls after the integrity boundary yet"
		return out
	}
	base := win[0].Equity
	if base <= 0 {
		out.Reason = "the first post-boundary equity mark is not positive, so the series cannot be rebased"
		return out
	}

	k := CleanIndexBase / base
	idx := make([]papertrade.EquityPoint, len(win))
	for i, p := range win {
		idx[i] = papertrade.EquityPoint{
			Ts: p.Ts, Cash: p.Cash * k, PositionsValue: p.PositionsValue * k, Equity: p.Equity * k,
		}
	}

	var winTrades []store.PaperTrade
	for _, t := range all {
		if t.Ts >= boundary {
			winTrades = append(winTrades, t)
		}
	}

	closed, numFills, tradedNotional := reconstructRoundTrips(winTrades)
	out.Available = true
	out.Marks, out.Fills = len(idx), len(winTrades)
	out.Index = idx
	// Summarize on the INDEX, so startEquity/lastEquity read as index points and
	// nobody can mistake them for dollars carried over from the contaminated book.
	out.Summary = papertrade.Summarize(idx, closed, numFills, tradedNotional)
	out.Money = moneymetrics.FromReturns(roundTripReturns(winTrades))
	return out
}
