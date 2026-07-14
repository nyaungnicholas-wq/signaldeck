// Package rebalance is a small, dependency-free, tax-aware rebalance planner
// for a simulated paper book.
//
// Given a book's current positions (each with its purchase LOTS) and a set of
// TARGET portfolio weights — for example the output of a portfolio optimizer —
// BuildPlan computes the trades that move the book toward the target, the
// realized capital gains those sells create (FIFO lot matching, split into
// short- and long-term by holding period), and a rough tax estimate.
//
// It is a PLAN ONLY. Nothing here executes: there is no broker, no order
// router, no I/O, no persistence, no clock, and no randomness. Prices are the
// marks the caller passes in, and the caller passes nowTs so the computation is
// fully deterministic. It depends only on the standard library.
//
// The price of every symbol comes from its Position.Price. A symbol is
// therefore tradable only if it appears in positions with a positive price — to
// buy into a symbol the book does not yet hold, pass a zero-lot Position that
// carries its current price. Targets supply weights only.
//
// HONESTY NOTES (this is the brand):
//   - PLAN, NOT ADVICE OR EXECUTION. This sizes hypothetical trades and
//     estimates their tax on a paper book. It is not investment or tax advice
//     and it does not place, or promise to place, any order.
//   - FIFO, NOT OPTIMIZED LOT SELECTION. Sells match the oldest lots first.
//     The planner does not hunt for the most tax-efficient lots, harvest
//     losses, or model specific-identification sales.
//   - TAX IS A ROUGH ESTIMATE. EstTax applies flat marginal rates to the net
//     gain within each holding-period class (short and long net internally but
//     never against each other) and taxes only a positive class total. It
//     ignores brackets, the wash-sale rule, NIIT, state tax, and loss
//     carryforwards. RealizedGain is proceeds minus matched cost basis and is
//     NOT reduced by transaction costs, which are reported separately.
//   - DEGENERATE INPUT IS HANDLED, NOT FAKED. Zero or negative equity, empty
//     targets, target weights that sum to zero, and target symbols with no
//     priced position all yield an empty plan (or a skipped symbol) with a
//     plain-English Note. Divisions are guarded; the plan never contains NaN or
//     Inf.
package rebalance

import (
	"math"
	"strings"
)

// Trade sides reported in Trade.Side.
const (
	// SideBuy marks a purchase (delta above target).
	SideBuy = "buy"
	// SideSell marks a sale (delta below target).
	SideSell = "sell"
)

// Lot is one purchase lot of a position, used for FIFO cost-basis matching.
type Lot struct {
	Shares     float64 // number of shares in this lot
	CostBasis  float64 // per-share cost
	AcquiredTs int64   // acquisition time, unix seconds
}

// Position is the current holding in one symbol. Lots are oldest first so FIFO
// matching can simply walk them in order.
type Position struct {
	Symbol string
	Price  float64 // current mark, used for every trade in this symbol
	Lots   []Lot   // oldest first (FIFO)
}

// Target is a desired portfolio weight for one symbol. Weights should be
// non-negative and sum to about 1; BuildPlan normalizes them if they do not.
type Target struct {
	Symbol string
	Weight float64
}

// Trade is one rebalancing action. RealizedGain and ShortTerm are meaningful
// only for sells. Shares and Notional are magnitudes (always non-negative); the
// direction is carried by Side.
type Trade struct {
	Symbol       string
	Side         string  // "buy" | "sell"
	Shares       float64 // share magnitude traded
	Notional     float64 // Shares * Price
	Cost         float64 // transaction cost (costBps of Notional)
	RealizedGain float64 // sells only: proceeds - matched cost basis
	ShortTerm    bool    // sells only: true if any matched lot was held < holdingSecs
}

// Plan is the full rebalancing plan with tax accounting. On degenerate input
// Trades is empty and Note explains why; every numeric field is then zero.
type Plan struct {
	Trades            []Trade
	ShortTermGain     float64 // net realized gain from lots held < holdingSecs
	LongTermGain      float64 // net realized gain from lots held >= holdingSecs
	TotalRealizedGain float64 // ShortTermGain + LongTermGain
	EstTax            float64 // max(0,ShortTermGain)*shortRate + max(0,LongTermGain)*longRate
	TotalCost         float64 // sum of per-trade transaction costs
	TurnoverPct       float64 // total traded notional / equity, in percent
	Note              string
}

// BuildPlan computes the tax-aware rebalance that moves positions toward
// targets.
//
// equity is the book's total value (cash plus positions marked at Price) and
// sets the dollar size of each target weight. nowTs is the current time in unix
// seconds; holdingSecs (typically 365*24*3600) is the boundary below which a
// matched lot is short-term. shortRate and longRate are marginal tax rates
// (e.g. 0.35 and 0.15). costBps is the per-side transaction cost in basis
// points (10 bps = 0.10% of notional).
//
// For each symbol currentValue = heldShares*Price and targetValue =
// normalizedWeight*equity. A negative delta becomes a sell of |delta|/Price
// shares whose realized gain is matched FIFO against the lots and classified
// short or long term; a positive delta becomes a buy of delta/Price shares with
// no realized gain. Symbols held but absent from targets get target weight 0
// and are fully sold. Target symbols with no priced position are skipped and
// noted. Zero or negative equity, empty targets, or target weights summing to
// zero return an empty plan with a Note.
func BuildPlan(positions []Position, targets []Target, equity float64, nowTs int64, holdingSecs int64, shortRate, longRate, costBps float64) Plan {
	if equity <= 0 {
		return emptyPlan("equity is zero or negative; nothing to plan")
	}
	if len(targets) == 0 {
		return emptyPlan("no target weights supplied; nothing to rebalance toward")
	}

	// Normalize target weights over their (non-negative) sum. Duplicate target
	// symbols accumulate; negative weights are clamped to zero because this
	// planner is long-only.
	rawW := make(map[string]float64, len(targets))
	sumW := 0.0
	clampedNeg := false
	for _, tg := range targets {
		w := tg.Weight
		if w < 0 {
			w = 0
			clampedNeg = true
		}
		rawW[tg.Symbol] += w
		sumW += w
	}
	if sumW <= 0 {
		return emptyPlan("target weights sum to zero; nothing to rebalance toward")
	}

	// Deterministic symbol order: positions in input order, then any target-only
	// symbols in target order. No map is ever ranged over, so output is stable.
	seen := make(map[string]bool, len(positions)+len(targets))
	order := make([]string, 0, len(positions)+len(targets))
	posBySym := make(map[string]Position, len(positions))
	for _, p := range positions {
		if seen[p.Symbol] {
			continue // first occurrence wins; symbols are expected to be unique
		}
		seen[p.Symbol] = true
		posBySym[p.Symbol] = p
		order = append(order, p.Symbol)
	}
	for _, tg := range targets {
		if seen[tg.Symbol] {
			continue
		}
		seen[tg.Symbol] = true
		order = append(order, tg.Symbol)
	}

	var (
		trades         []Trade
		shortGain      float64
		longGain       float64
		totalCost      float64
		tradedNotional float64
		skipped        []string
	)

	for _, sym := range order {
		pos, hasPos := posBySym[sym]
		if !hasPos || pos.Price <= 0 {
			// No mark to size a trade against. A target-only or unpriced symbol
			// simply cannot be traded here; record it instead of guessing.
			skipped = append(skipped, sym)
			continue
		}
		price := pos.Price
		heldShares := sumLotShares(pos.Lots)
		currentValue := heldShares * price
		targetValue := rawW[sym] / sumW * equity // rawW[sym] is 0 when sym is untargeted
		delta := targetValue - currentValue
		if math.Abs(delta) < 1e-9 {
			continue // already at target for this symbol
		}

		if delta > 0 {
			shares := delta / price
			notional := shares * price
			cost := notional * costBps / 10000
			trades = append(trades, Trade{
				Symbol:   sym,
				Side:     SideBuy,
				Shares:   shares,
				Notional: notional,
				Cost:     cost,
			})
			totalCost += cost
			tradedNotional += notional
			continue
		}

		// Sell. |delta|/price shares, never more than we hold.
		shares := -delta / price
		if shares > heldShares {
			shares = heldShares
		}
		notional := shares * price
		cost := notional * costBps / 10000
		gain, st, lt, anyShort := matchFIFO(pos.Lots, shares, price, nowTs, holdingSecs)
		trades = append(trades, Trade{
			Symbol:       sym,
			Side:         SideSell,
			Shares:       shares,
			Notional:     notional,
			Cost:         cost,
			RealizedGain: gain,
			ShortTerm:    anyShort,
		})
		shortGain += st
		longGain += lt
		totalCost += cost
		tradedNotional += notional
	}

	plan := Plan{
		Trades:            trades,
		ShortTermGain:     shortGain,
		LongTermGain:      longGain,
		TotalRealizedGain: shortGain + longGain,
		EstTax:            taxOnGains(shortGain, longGain, shortRate, longRate),
		TotalCost:         totalCost,
		TurnoverPct:       tradedNotional / equity * 100,
	}
	plan.Note = buildNote(sumW, clampedNeg, skipped, len(trades))
	if plan.Trades == nil {
		plan.Trades = []Trade{}
	}
	return plan
}

// --- internal helpers ------------------------------------------------------

// emptyPlan returns a zero-valued plan carrying only the given explanatory
// Note and an empty (non-nil) Trades slice.
func emptyPlan(note string) Plan {
	return Plan{Trades: []Trade{}, Note: note}
}

// sumLotShares returns the total share count across a position's lots.
func sumLotShares(lots []Lot) float64 {
	s := 0.0
	for _, l := range lots {
		s += l.Shares
	}
	return s
}

// matchFIFO matches shares against lots oldest-first and returns the total
// realized gain, its short- and long-term split, and whether any matched lot
// was short-term. Realized gain per matched share is (price - lot.CostBasis); a
// lot is short-term when nowTs-AcquiredTs is strictly less than holdingSecs.
// The lots are only read, never modified, so BuildPlan stays pure. shares is
// expected to be <= the total lot shares; any excess simply goes unmatched.
func matchFIFO(lots []Lot, shares, price float64, nowTs, holdingSecs int64) (gain, shortGain, longGain float64, anyShort bool) {
	remaining := shares
	for _, lot := range lots {
		if remaining <= 0 {
			break
		}
		take := lot.Shares
		if take > remaining {
			take = remaining
		}
		if take <= 0 {
			continue
		}
		g := (price - lot.CostBasis) * take
		gain += g
		if nowTs-lot.AcquiredTs < holdingSecs {
			shortGain += g
			anyShort = true
		} else {
			longGain += g
		}
		remaining -= take
	}
	return gain, shortGain, longGain, anyShort
}

// taxOnGains taxes only a positive class total: gains and losses net within a
// holding-period class, but a class that nets to a loss contributes zero tax
// (the loss is still reported on the Plan). Short and long classes never offset
// each other.
func taxOnGains(shortGain, longGain, shortRate, longRate float64) float64 {
	tax := 0.0
	if shortGain > 0 {
		tax += shortGain * shortRate
	}
	if longGain > 0 {
		tax += longGain * longRate
	}
	return tax
}

// buildNote assembles a plain-English Note from what happened while planning:
// whether weights were normalized or clamped, which symbols were skipped for
// lack of a price, and whether any trades resulted.
func buildNote(sumW float64, clampedNeg bool, skipped []string, numTrades int) string {
	var parts []string
	if math.Abs(sumW-1) > 1e-9 {
		parts = append(parts, "target weights did not sum to 1; normalized before planning")
	}
	if clampedNeg {
		parts = append(parts, "negative target weight(s) clamped to zero (long-only)")
	}
	if len(skipped) > 0 {
		parts = append(parts, "skipped symbol(s) without a priced position: "+strings.Join(skipped, ", "))
	}
	if numTrades == 0 {
		parts = append(parts, "already at target weights; no trades needed")
	}
	return strings.Join(parts, "; ")
}
