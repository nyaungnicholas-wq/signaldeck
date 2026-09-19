// TRADE-CAPITAL SEMANTICS for the confluence money scoreboard.
//
// WHY THIS EXISTS. A confluence outcome is a directional call on one symbol held
// for one day, and the scoreboard scored it as direction*rawReturn. That number
// is arithmetically exact and economically meaningless as account performance,
// because an unconstrained normalised short is unbounded below.
//
// CELUW is the case that forced this. It is a warrant that closed at $0.0003 on
// 2026-07-16 and $0.0019 on 2026-07-17 — the underlying rose 533%, which is a
// REAL move and stays visible everywhere in this system. A short of it therefore
// returns -533% per unit of notional, and that -5.33 was averaged in beside a
// +0.36 on TEAM and published as an expectancy. No account can lose 533% of
// itself on one position among thousands: a real book sizes the position, posts
// collateral against the short, and is liquidated long before the move finishes.
//
// The repair is NOT to cap the return, delete the symbol, or winsorise the tail.
// It is to state which QUESTION each number answers, and to compute the
// account-level one under constraints that a book actually operates under:
//
//	RawUnderlying  what the instrument did
//	RawTrade       what one unit of unconstrained notional did (unbounded)
//	CapitalAtRisk  what the capital committed to this position did
//	Constrained    what the ACCOUNT did, after sizing, collateral, stop and costs
//
// Only the last of those may be described as performance.
package confluence

import "math"

// Constraints is the paper/research trading model the constrained basis is
// computed under. Every field is explicit and configurable so the assumptions
// behind a published number can be read off rather than inferred.
type Constraints struct {
	// PositionWeight is the fraction of account equity committed per position.
	PositionWeight float64
	// MaxGrossExposure caps the sum of absolute position weights across the book.
	MaxGrossExposure float64
	// MaxNetExposure caps the absolute signed sum of weights (long minus short).
	MaxNetExposure float64
	// RiskBudget is the fraction of ACCOUNT equity one position may lose before
	// it is liquidated. This, not a return cap, is what bounds the tail.
	RiskBudget float64
	// ShortCollateral is collateral posted per unit of short notional: 1.5 means
	// 150%. A short ties up more capital than its notional, so the same slice of
	// equity supports a smaller short than long.
	ShortCollateral float64
	// CostPerSide is the proportional transaction cost charged on each side.
	CostPerSide float64
	// SlippagePerSide is proportional slippage charged on each side.
	SlippagePerSide float64
	// BorrowRateAnnual is the annualised cost of borrowing to short, prorated by
	// HoldingDays. A short book that ignores borrow is not reporting net returns.
	BorrowRateAnnual float64
	// HoldingDays is the holding period the borrow charge is prorated over.
	HoldingDays float64
	// MinPrice is the price below which an instrument is not tradable at size.
	// Sub-penny names do not have a book that absorbs a position; pretending they
	// do is how a 0.0003-to-0.0019 print became a portfolio statistic.
	MinPrice float64
}

// DefaultConstraints is the published research configuration.
//
// The values are deliberately ordinary rather than optimised: 2% per position,
// 1% of equity at risk on any one of them, 150% collateral on shorts (Reg T
// initial margin), 10bp cost and 5bp slippage a side, 5% annual borrow, and a
// 5-cent floor below which an instrument is treated as untradable. Tuning these
// to improve the headline would make the headline meaningless again.
func DefaultConstraints() Constraints {
	return Constraints{
		PositionWeight:   0.02,
		MaxGrossExposure: 1.0,
		MaxNetExposure:   0.5,
		RiskBudget:       0.01,
		ShortCollateral:  1.5,
		CostPerSide:      0.001,
		SlippagePerSide:  0.0005,
		BorrowRateAnnual: 0.05,
		HoldingDays:      1,
		MinPrice:         0.05,
	}
}

// Shortability records what is KNOWN about whether a name could be borrowed.
//
// This system holds no historical borrow file, so for most rows the honest value
// is ShortUnknown — not an assumption that the short was available. The count of
// unknowns is published beside the short book rather than being silently
// resolved in the optimistic direction.
type Shortability int

const (
	// ShortUnknown means no borrow evidence exists for this name and date.
	ShortUnknown Shortability = iota
	// ShortConfirmed means borrow was evidenced.
	ShortConfirmed
	// ShortIneligible means the name was evidenced as not borrowable.
	ShortIneligible
)

// String renders a Shortability for the API payload.
func (s Shortability) String() string {
	switch s {
	case ShortConfirmed:
		return "confirmed"
	case ShortIneligible:
		return "ineligible"
	default:
		return "unknown"
	}
}

// Trade is one graded confluence outcome expressed as a position.
//
// LowPx/HighPx are the intra-window extremes. They are what makes the stop
// realistic: without them the model can only see the close and would let a
// position that traded through its stop mid-window survive to the end.
type Trade struct {
	Direction int     // +1 long, -1 short
	EntryPx   float64 // price at entry
	ExitPx    float64 // price at exit
	LowPx     float64 // lowest price between entry and exit; 0 = unknown
	HighPx    float64 // highest price between entry and exit; 0 = unknown
	Short     Shortability
	Warrant   bool // warrant or unit: wider spreads, thinner book
}

// Result carries every basis side by side, so no caller can quote one while
// meaning another.
type Result struct {
	Direction     int     `json:"direction"`
	RawUnderlying float64 `json:"rawUnderlying"` // what the instrument did
	RawTrade      float64 `json:"rawTrade"`      // unconstrained, uncosted, unbounded
	Constrained   float64 `json:"constrained"`   // contribution to ACCOUNT return
	CapitalAtRisk float64 `json:"capitalAtRisk"` // return on capital committed here
	Weight        float64 `json:"weight"`        // position weight actually used
	Liquidated    bool    `json:"liquidated"`    // the risk budget was breached
	GappedThrough bool    `json:"gappedThrough"` // the exit was worse than the stop
	Tradable      bool    `json:"tradable"`
	Reason        string  `json:"reason,omitempty"`
}

// Evaluate computes every basis for one trade under one set of constraints.
//
// An untradable trade still reports RawUnderlying and RawTrade. Suppressing them
// would hide the market fact; the point is to keep the fact and refuse the claim
// that an account experienced it.
func Evaluate(t Trade, c Constraints) Result {
	if t.EntryPx <= 0 || t.ExitPx <= 0 || t.Direction == 0 {
		return Result{Tradable: false, Reason: "invalid trade inputs"}
	}

	rawUnderlying := t.ExitPx/t.EntryPx - 1
	rawTrade := float64(sign(t.Direction)) * rawUnderlying
	base := Result{
		Direction:     sign(t.Direction),
		RawUnderlying: rawUnderlying,
		RawTrade:      rawTrade,
	}

	if t.EntryPx < c.MinPrice {
		base.Reason = "entry price below tradable minimum"
		return base
	}
	if t.Direction < 0 && t.Short == ShortIneligible {
		base.Reason = "short not borrowable"
		return base
	}

	// Sizing. A short consumes collateral, so a fixed slice of equity supports
	// proportionally less short notional than long notional.
	w := c.PositionWeight
	if t.Direction < 0 && c.ShortCollateral > 0 {
		w /= c.ShortCollateral
	}
	if c.MaxGrossExposure > 0 && w > c.MaxGrossExposure {
		w = c.MaxGrossExposure
	}
	base.Weight = w

	// The stop is derived from the risk budget, not chosen: a position weighted
	// w loses RiskBudget of the ACCOUNT when the adverse move reaches
	// RiskBudget/w. This is what makes the tail finite without capping anything.
	stopMove := math.Inf(1)
	if w > 0 {
		stopMove = c.RiskBudget / w
	}

	adverse := adverseExcursion(t, rawTrade)
	realised := rawTrade
	if adverse >= stopMove {
		base.Liquidated = true
		realised = -stopMove
		// GAP-THROUGH. The stop is an order, not a guarantee. When the close
		// itself is worse than the stop level, there was no price at which the
		// position could have been closed for -stopMove, so the account eats the
		// whole move. This is the clause that keeps the model from flattering
		// itself on exactly the trades that matter.
		if rawTrade < -stopMove {
			base.GappedThrough = true
			realised = rawTrade
		}
	}

	perSideCost := c.CostPerSide + c.SlippagePerSide
	if t.Warrant {
		perSideCost *= 2
	}
	costs := 2 * perSideCost
	if t.Direction < 0 {
		costs += c.BorrowRateAnnual * c.HoldingDays / 365
	}

	net := realised - costs
	base.CapitalAtRisk = net
	if t.Direction < 0 && c.ShortCollateral > 0 {
		// The capital actually tied up by a short is its collateral.
		base.CapitalAtRisk = net / c.ShortCollateral
	}
	// Account contribution. No collateral divisor here: it was already priced
	// into w above, and applying it twice would understate the short book.
	base.Constrained = w * net
	base.Tradable = true
	return base
}

// adverseExcursion is how far the position moved AGAINST itself, as a positive
// fraction. It prefers the intra-window extreme and falls back to the close when
// the extreme is unknown — a fallback that can only UNDERSTATE the excursion,
// which is the safe direction for a stop model to be wrong in.
func adverseExcursion(t Trade, rawTrade float64) float64 {
	var adverse float64
	switch {
	case t.Direction > 0 && t.LowPx > 0:
		adverse = 1 - t.LowPx/t.EntryPx
	case t.Direction < 0 && t.HighPx > 0:
		adverse = t.HighPx/t.EntryPx - 1
	case rawTrade < 0:
		adverse = -rawTrade
	}
	if adverse < 0 {
		return 0
	}
	return adverse
}

// Aggregate sums a book's account contributions and applies the exposure caps.
//
// The caps are applied to the REALISED book rather than assumed away: when a
// day flags 200 setups, an account cannot hold 200 full-weight positions, so the
// day's contribution is scaled to the gross cap and the fact is reported through
// `scaled`. Silently summing 200 unscaled weights is how a paper book reports
// leverage it never had.
func Aggregate(rs []Result, c Constraints) (accountReturn, gross, net float64, scaled bool) {
	for _, r := range rs {
		if !r.Tradable {
			continue
		}
		accountReturn += r.Constrained
		gross += math.Abs(r.Weight)
		net += float64(r.Direction) * r.Weight
	}
	if gross <= 0 {
		return accountReturn, gross, net, false
	}
	if c.MaxGrossExposure > 0 && gross > c.MaxGrossExposure {
		k := c.MaxGrossExposure / gross
		accountReturn, gross, net = accountReturn*k, gross*k, net*k
		scaled = true
	}
	if c.MaxNetExposure > 0 && math.Abs(net) > c.MaxNetExposure && net != 0 {
		k := c.MaxNetExposure / math.Abs(net)
		accountReturn, gross, net = accountReturn*k, gross*k, net*k
		scaled = true
	}
	return accountReturn, gross, net, scaled
}

// sign normalises a direction to -1, 0 or +1 so a caller passing ±2 cannot
// double a return.
func sign(d int) int {
	switch {
	case d > 0:
		return 1
	case d < 0:
		return -1
	}
	return 0
}
