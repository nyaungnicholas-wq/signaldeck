// Package riskgate is the PRETRADE risk check: the one place that can refuse or
// shrink a trade before the book takes it.
//
// WHY THIS EXISTS. The platform already measured risk in several places and
// enforced none of it. internal/risklens computes VaR and stress scenarios,
// internal/portopt solves a shrunk mean-variance allocation, papertrade.Summary
// reports a max drawdown — and every one of them is read AFTER the fact, by a
// human, from a dashboard. Nothing sat between "the signal fired" and "the book
// bought it". A measurement that cannot stop anything is a report, not a risk
// control, and the difference only becomes visible on the day it mattered.
//
// So this package is deliberately an ENFORCEMENT point and nothing else. It
// holds no state, performs no I/O, and reads no clock: given the book's measured
// condition, the candidate trade, and the realized edge, it returns a decision.
// That keeps every rule here unit-testable without a database, and it keeps the
// rules honest — a gate that computed its own inputs could quietly compute
// flattering ones.
//
// FOUR RULES WORTH STATING UP FRONT, because each is a decision and not an
// implementation detail:
//
//   - EXITS ARE NEVER GATED. Every check below applies to opening or increasing
//     risk. A gate that can block a de-risking trade is not a safety mechanism,
//     it is a trap: the exact moment the drawdown breaker trips is the moment
//     you most need to be able to sell.
//   - NO EDGE MEANS NO TRADE. When the realized round-trip record shows
//     non-positive Kelly, the gate REFUSES rather than sizing small. A negative
//     expectancy scaled down is still a negative expectancy, just slower.
//   - AN UNMEASURABLE EDGE IS NOT A LICENCE TO GUESS. Below the evidence floor
//     the gate does not invent a Kelly fraction from a thin sample; it falls back
//     to the book's existing equal-slice budget and says so in the decision, so
//     "we sized this by rule of thumb" is never mistaken for "we sized this on
//     measured odds".
//   - LIQUIDITY IS SOMEONE ELSE'S JOB, ON PURPOSE. papertrade's execution model
//     already caps a fill at a participation share of the name's average daily
//     dollar volume and refuses a fill it cannot price at all. Re-deriving that
//     here would mean two places to keep in sync and one of them silently wrong,
//     so this gate sizes the INTENT and the execution model bounds the FILL.
package riskgate

import (
	"fmt"
	"math"
	"os"
	"strconv"

	"github.com/nyaungnicholas-wq/signaldeck/internal/envcfg"
)

// Action is what the caller wants to do.
type Action int

const (
	// Enter opens or adds to a position — everything here applies.
	Enter Action = iota
	// Exit closes or reduces one — never gated, never resized.
	Exit
)

// Sizing records HOW the returned notional was arrived at, so a reader can tell
// a measured bet from a fallback.
type Sizing string

const (
	// SizingNone — no size was computed (a refusal, or an exit).
	SizingNone Sizing = "none"
	// SizingFractionalKelly — sized from the realized round-trip edge.
	SizingFractionalKelly Sizing = "fractional-kelly"
	// SizingEqualSlice — the edge was unmeasurable, so the book's equal-slice
	// budget was used instead. Not a claim about odds.
	SizingEqualSlice Sizing = "equal-slice"
)

// Limits is the risk envelope. Every field is env-overridable so the assumption
// is explicit and tunable rather than compiled in and forgotten; Defaults
// documents where each number comes from.
type Limits struct {
	// MaxDrawdown halts NEW entries once the book's current peak-to-trough
	// drawdown reaches it. Exits stay open.
	MaxDrawdown float64
	// MaxDailyLoss halts new entries once today's loss reaches this fraction of
	// the book's start-of-day equity.
	MaxDailyLoss float64
	// MaxPositionWeight caps any single position's notional as a share of equity.
	MaxPositionWeight float64
	// MaxSectorWeight caps one sector's total exposure as a share of equity.
	MaxSectorWeight float64
	// MaxPositions bounds how many names the book may hold at once.
	MaxPositions int
	// KellyFraction is the share of full Kelly actually bet.
	KellyFraction float64
	// MinEdgeTrips is the closed-round-trip floor below which Kelly is not used.
	MinEdgeTrips int
	// MinTicketFrac is the smallest position, as a share of equity, worth
	// opening at all. Below it the gate REFUSES rather than filling a remnant.
	MinTicketFrac float64
	// MaxCorrToBook refuses a candidate whose measured correlation to the book
	// it would join is at or above it. The position, sector and count caps all
	// measure concentration by LABEL; this one measures it by BEHAVIOUR, which
	// is the only version that survives a regime where the labels stop meaning
	// anything.
	MaxCorrToBook float64
	// MaxGrossExposure caps total deployed notional as a share of equity.
	MaxGrossExposure float64
}

// Defaults returns the standard envelope.
//
// The numbers, and why each is what it is:
//
//   - MaxDrawdown 0.20. A fifth of the book is the conventional institutional
//     soft stop: far enough out that ordinary variance does not trip it, close
//     enough in that the book can still recover from it arithmetically.
//   - MaxDailyLoss 0.05. A single session losing a twentieth of the book is
//     evidence that something is wrong with the day, not with one name.
//   - MaxPositionWeight 0.10 and MaxPositions 10. Deliberately the same envelope
//     the existing engine already implied with equity/MaxPositions sizing, so
//     turning this gate on does not silently re-risk the book; it enforces what
//     was previously a convention.
//   - MaxSectorWeight 0.30. Ten equally-weighted names drawn from one sector are
//     one bet with extra commission. A third of the book is the point past which
//     "diversified" stops being true.
//   - KellyFraction 0.25. Full Kelly is growth-optimal only when the edge is
//     known EXACTLY; with an estimated p and b it overbets badly, and its
//     drawdowns are famously unlivable. Quarter Kelly is the standard practical
//     haircut for parameter uncertainty (Thorp's own recommendation for
//     real books), giving up a modest amount of growth for a large reduction in
//     the variance of the growth rate.
//   - MinEdgeTrips 20 matches papertrade.MinPayoffTrips: the same sample that is
//     too thin to describe a payoff shape is too thin to size on.
//   - MinTicketFrac 0.005. Every cap above TRIMS rather than refuses, and trims
//     compose: a name arriving last against a nearly-full sector gets whatever
//     dust is left. Filling that is worse than skipping it — the position pays
//     two spreads, occupies a slot, and cannot move the book. Half a percent of
//     equity is a twentieth of the largest allowed position, which is the point
//     below which a fill is bookkeeping rather than a bet.
//   - MaxCorrToBook 0.80. Ten names at 0.8 correlation are not ten bets; they
//     are one bet with ten commissions and a diversification story. 0.80 rather
//     than something tighter because equities are correlated by construction —
//     a market factor runs through all of them — and a cap that fires on the
//     ordinary market beta would refuse the whole universe. It is set to catch
//     the pathological case (a second share class, a sector twin, an ETF and
//     its largest holding), not to enforce statistical independence, which is
//     not available in this asset class at any price.
//   - MaxGrossExposure 1.00. No leverage. The book may deploy every dollar it
//     holds and not one more. This is a floor on honesty as much as on risk:
//     a simulated book that quietly runs at 1.3x gross has been reporting the
//     returns of a different, riskier strategy than the one described.
func Defaults() Limits {
	return Limits{
		MaxDrawdown:       envFrac("SIGNALDECK_RISK_MAX_DRAWDOWN", 0.20),
		MaxDailyLoss:      envFrac("SIGNALDECK_RISK_MAX_DAILY_LOSS", 0.05),
		MaxPositionWeight: envFrac("SIGNALDECK_RISK_MAX_POSITION_WEIGHT", 0.10),
		MaxSectorWeight:   envFrac("SIGNALDECK_RISK_MAX_SECTOR_WEIGHT", 0.30),
		MaxPositions:      envInt("SIGNALDECK_RISK_MAX_POSITIONS", 10),
		KellyFraction:     envFrac("SIGNALDECK_RISK_KELLY_FRACTION", 0.25),
		MinEdgeTrips:      envInt("SIGNALDECK_RISK_MIN_EDGE_TRIPS", 20),
		MinTicketFrac:     envFrac("SIGNALDECK_RISK_MIN_TICKET_FRAC", 0.005),
		MaxCorrToBook:     envFrac("SIGNALDECK_RISK_MAX_CORR_TO_BOOK", 0.80),
		MaxGrossExposure:  envFrac("SIGNALDECK_RISK_MAX_GROSS_EXPOSURE", 1.00),
	}
}

// Book is the measured condition of the book at decision time. The caller
// measures these; the gate never derives them, so it cannot flatter itself.
//
// DrawdownKnown / DailyKnown exist because "the breaker could not measure this"
// and "the breaker measured zero" are different states and must not collapse
// into the same allow.
type Book struct {
	Equity float64 // total book value (cash + marked positions)
	Cash   float64 // uninvested cash available to deploy now

	// CurrentDrawdown is the drawdown the book is carrying RIGHT NOW as a
	// positive fraction of its running peak — not the worst it ever carried. A
	// book that fell 30% and recovered is not in a 30% drawdown, and halting it
	// for a healed wound would be a bug.
	CurrentDrawdown float64
	DrawdownKnown   bool

	// DailyPnLFrac is today's equity change over start-of-day equity, SIGNED
	// (negative is a loss).
	DailyPnLFrac float64
	DailyKnown   bool

	OpenPositions int
	// ExposureBySector is current dollar exposure keyed by sector label. A
	// missing sector reads as zero exposure, which is correct: the gate caps
	// concentration it can see, and an unclassified name concentrates nothing.
	ExposureBySector map[string]float64

	// GrossExposure is total deployed notional right now, unsigned. GrossKnown
	// follows the DrawdownKnown doctrine: a caller that did not measure it
	// leaves the cap UNARMED and is told so, rather than passing a zero that
	// reads as an empty book with a full cap of headroom.
	GrossExposure float64
	GrossKnown    bool
}

// Edge is the realized round-trip record the Kelly fraction is derived from —
// papertrade.Payoff's WinRate, PayoffRatio, N and Valid, passed as plain numbers
// so this package imports nothing of the engine it guards.
type Edge struct {
	WinRate     float64 // p
	PayoffRatio float64 // b, in b:1 odds
	Trips       int     // closed round trips behind p and b
	Valid       bool    // the payoff shape was measurable at all
}

// Request is the candidate trade.
type Request struct {
	Symbol string
	Sector string // "" is fine — an unclassified name is capped by position, not sector
	Action Action

	// CorrToBook is this candidate's measured return correlation to the book it
	// would join. HasCorr distinguishes "measured as zero" from "not measured",
	// because only the first is a reason to allow.
	CorrToBook float64
	HasCorr    bool
}

// Decision is the gate's answer. Notional is the dollar size the caller may
// deploy (0 on a refusal, and meaningless for an Exit, which the caller sizes
// from the position it is closing).
//
// Reasons always explains the outcome; Breaches names the limits that bound or
// blocked it, so an operator can see WHICH rule fired without re-deriving it.
type Decision struct {
	Allow    bool     `json:"allow"`
	Notional float64  `json:"notional"`
	Sizing   Sizing   `json:"sizing"`
	Reasons  []string `json:"reasons"`
	Breaches []string `json:"breaches,omitempty"`
}

// The order of checks is itself a design choice: the book-wide circuit breakers
// (drawdown, daily loss) run BEFORE any sizing, because when they are tripped the
// answer is "not this trade, nor any other entry today", and computing a size
// first would invite a caller to use it.
// Admit answers a question that is logically PRIOR to any candidate: is this
// book open for new risk at all?
//
// WHY THIS IS A SEPARATE FUNCTION. The book-wide breakers do not depend on
// which candidate is asking, so evaluating them per-candidate — after some
// other component has already decided the candidate is worth trading — makes
// risk look like a property of the trade rather than a precondition for
// trading. A caller must be able to ask "may I trade at all today?" and get a
// refusal it cannot route around by finding a better candidate. Evaluate calls
// this first, so the two can never disagree about what a halted book is.
//
// A permitting Decision carries Notional 0 and SizingNone: admission is not a
// size, and nothing here may be mistaken for one.
func Admit(b Book, lim Limits) Decision {
	lim = lim.withDefaults()
	d := Decision{Sizing: SizingNone}

	if b.Equity <= 0 || math.IsNaN(b.Equity) || math.IsInf(b.Equity, 0) {
		d.Reasons = append(d.Reasons, "book equity is not a usable positive number — nothing can be sized against it")
		d.Breaches = append(d.Breaches, "equity")
		return d
	}

	// ── Book-wide circuit breakers ──────────────────────────────────────────
	if b.DrawdownKnown && b.CurrentDrawdown >= lim.MaxDrawdown {
		d.Reasons = append(d.Reasons, fmt.Sprintf(
			"drawdown circuit breaker: the book is %.1f%% below its peak, at or past the %.1f%% halt — no new entries until it recovers",
			b.CurrentDrawdown*100, lim.MaxDrawdown*100))
		d.Breaches = append(d.Breaches, "max-drawdown")
		return d
	}
	if !b.DrawdownKnown {
		// A book with no measurable equity history has no drawdown to break on —
		// this is a young book, not a hidden loss. Allowing is correct, but the
		// breaker is UNARMED and the decision says so rather than reading as a
		// clean pass.
		d.Reasons = append(d.Reasons, "drawdown breaker unarmed: the book has no measurable equity history yet")
	}
	if b.DailyKnown && b.DailyPnLFrac <= -lim.MaxDailyLoss {
		d.Reasons = append(d.Reasons, fmt.Sprintf(
			"daily loss limit: down %.2f%% today, at or past the %.2f%% stop — the session is closed to new entries",
			-b.DailyPnLFrac*100, lim.MaxDailyLoss*100))
		d.Breaches = append(d.Breaches, "max-daily-loss")
		return d
	}
	if !b.DailyKnown {
		d.Reasons = append(d.Reasons, "daily-loss limit unarmed: start-of-day equity was not measurable")
	}
	if lim.MaxPositions > 0 && b.OpenPositions >= lim.MaxPositions {
		d.Reasons = append(d.Reasons, fmt.Sprintf(
			"position count at the %d-name cap — a new name would have to replace one, which is a decision this gate does not make",
			lim.MaxPositions))
		d.Breaches = append(d.Breaches, "max-positions")
		return d
	}

	// Gross exposure EXHAUSTED is book-wide: a fully deployed book has no room
	// for any candidate, so the answer is "no entries", not "not this one".
	// The partial case is a trim and belongs with the other trims in Evaluate.
	if b.GrossKnown && lim.MaxGrossExposure > 0 {
		if allowed := lim.MaxGrossExposure * b.Equity; b.GrossExposure >= allowed {
			d.Reasons = append(d.Reasons, fmt.Sprintf(
				"gross exposure is %.1f%% of equity, at or past the %.1f%% cap — the book has no unlevered room left",
				b.GrossExposure/b.Equity*100, lim.MaxGrossExposure*100))
			d.Breaches = append(d.Breaches, "max-gross-exposure")
			return d
		}
	}
	if !b.GrossKnown {
		d.Reasons = append(d.Reasons, "gross-exposure cap unarmed: total deployed notional was not measurable")
	}

	d.Allow = true
	return d
}

// Evaluate applies the FULL envelope to one candidate trade: admission first,
// then the candidate-specific refusals, then sizing.
func Evaluate(b Book, req Request, edge Edge, lim Limits) Decision {
	lim = lim.withDefaults()

	// EXITS ARE NEVER GATED. Stated first so it cannot be reordered behind a
	// check that might refuse.
	if req.Action == Exit {
		return Decision{
			Allow:   true,
			Sizing:  SizingNone,
			Reasons: []string{"exit — reducing risk is never gated"},
		}
	}

	// Book-wide admission. A refusal here is final and carries its own reasons.
	d := Admit(b, lim)
	if !d.Allow {
		return d
	}
	// Admitted, but nothing is allowed YET — every check below can still refuse,
	// and a lingering Allow from admission would be read as one.
	d.Allow = false

	// Correlation to the book. This is an ADMISSION question, not a sizing one:
	// a name that moves with the book is not a smaller version of a good idea,
	// it is more of the idea already owned, so trimming it would just buy the
	// same exposure in a less honest package. Refuse or admit; never trim.
	if req.HasCorr && req.CorrToBook >= lim.MaxCorrToBook {
		d.Reasons = append(d.Reasons, fmt.Sprintf(
			"%s correlates %.2f to the book it would join, at or past the %.2f cap — this is not a new bet, it is more of the one already held",
			req.Symbol, req.CorrToBook, lim.MaxCorrToBook))
		d.Breaches = append(d.Breaches, "max-corr-to-book")
		return d
	}
	if !req.HasCorr {
		d.Reasons = append(d.Reasons, "correlation cap unarmed: this candidate's correlation to the book was not measurable")
	}

	// ── Sizing ──────────────────────────────────────────────────────────────
	frac, sizing, reason, ok := sizeFraction(edge, lim)
	if !ok {
		d.Reasons = append(d.Reasons, reason)
		d.Breaches = append(d.Breaches, "no-edge")
		return d
	}
	d.Sizing = sizing
	d.Reasons = append(d.Reasons, reason)

	notional := frac * b.Equity

	// Per-position cap.
	if cap := lim.MaxPositionWeight * b.Equity; notional > cap {
		notional = cap
		d.Reasons = append(d.Reasons, fmt.Sprintf("trimmed to the %.1f%% single-position cap", lim.MaxPositionWeight*100))
		d.Breaches = append(d.Breaches, "max-position-weight")
	}

	// Sector concentration: only the HEADROOM under the cap may be deployed.
	if req.Sector != "" && lim.MaxSectorWeight > 0 {
		allowed := lim.MaxSectorWeight * b.Equity
		used := b.ExposureBySector[req.Sector]
		headroom := allowed - used
		if headroom <= 0 {
			d.Allow = false
			d.Notional = 0
			d.Reasons = append(d.Reasons, fmt.Sprintf(
				"%s is already at %.1f%% of the book, at or past the %.1f%% sector cap — ten names from one sector are one bet",
				req.Sector, used/b.Equity*100, lim.MaxSectorWeight*100))
			d.Breaches = append(d.Breaches, "max-sector-weight")
			return d
		}
		if notional > headroom {
			notional = headroom
			d.Reasons = append(d.Reasons, fmt.Sprintf(
				"trimmed to the remaining %s sector headroom under the %.1f%% cap", req.Sector, lim.MaxSectorWeight*100))
			d.Breaches = append(d.Breaches, "max-sector-weight")
		}
	}

	// Gross exposure: the leverage cap, expressed as headroom the same way the
	// sector cap is. Admit already refused the exhausted case, so the only
	// question left here is whether THIS size fits in the room that remains.
	if b.GrossKnown && lim.MaxGrossExposure > 0 {
		if headroom := lim.MaxGrossExposure*b.Equity - b.GrossExposure; notional > headroom {
			notional = headroom
			d.Reasons = append(d.Reasons, fmt.Sprintf(
				"trimmed to the remaining headroom under the %.1f%% gross-exposure cap", lim.MaxGrossExposure*100))
			d.Breaches = append(d.Breaches, "max-gross-exposure")
		}
	}

	// Funded-book clamp: never deploy cash the book does not hold.
	if notional > b.Cash {
		notional = b.Cash
		d.Reasons = append(d.Reasons, "clamped to cash on hand")
	}

	// Minimum ticket. This check must come LAST, after every trim and the cash
	// clamp, because it is the composition of those trims that produces a
	// remnant: each cap individually looks reasonable while the survivor is dust.
	if floor := lim.MinTicketFrac * b.Equity; notional < floor {
		d.Allow = false
		d.Notional = 0
		d.Sizing = SizingNone
		d.Reasons = append(d.Reasons, fmt.Sprintf(
			"what survived the caps ($%.2f) is below the $%.2f minimum ticket (%.2f%% of equity) — a position this small pays two spreads to move nothing",
			notional, floor, lim.MinTicketFrac*100))
		d.Breaches = append(d.Breaches, "min-ticket")
		return d
	}

	if notional <= 0 || math.IsNaN(notional) || math.IsInf(notional, 0) {
		d.Allow = false
		d.Notional = 0
		d.Sizing = SizingNone
		d.Reasons = append(d.Reasons, "no deployable notional remains after the caps and the cash clamp")
		d.Breaches = append(d.Breaches, "no-notional")
		return d
	}

	d.Allow = true
	d.Notional = notional
	return d
}

// sizeFraction returns the share of equity to deploy, how it was derived, and
// why. ok is false when the trade should be REFUSED outright rather than sized.
func sizeFraction(edge Edge, lim Limits) (frac float64, sizing Sizing, reason string, ok bool) {
	if !edge.Valid || edge.Trips < lim.MinEdgeTrips {
		// Not enough resolved evidence to claim odds. Fall back to the book's
		// existing equal-slice convention and label it as exactly that.
		slice := 1.0 / float64(maxInt(lim.MaxPositions, 1))
		return slice, SizingEqualSlice, fmt.Sprintf(
			"edge not measurable yet (%d closed round trips, need %d) — sized by the %.1f%% equal slice, NOT by measured odds",
			edge.Trips, lim.MinEdgeTrips, slice*100), true
	}

	f, kok := FullKelly(edge.WinRate, edge.PayoffRatio)
	if !kok {
		return 0, SizingNone, "the realized win rate and payoff ratio do not form a usable Kelly fraction", false
	}
	if f <= 0 {
		// This is the check the whole package is for. A measured non-positive
		// edge is not a small opportunity, it is a negative one.
		return 0, SizingNone, fmt.Sprintf(
			"refused on measured expectancy: %.1f%% win rate at a %.2f payoff ratio is a full-Kelly fraction of %.3f — a negative edge sized small is still a negative edge",
			edge.WinRate*100, edge.PayoffRatio, f), false
	}
	frac = lim.KellyFraction * f
	return frac, SizingFractionalKelly, fmt.Sprintf(
		"%.0f%% of full Kelly on the realized record (%.1f%% win rate, %.2f payoff over %d round trips) — %.2f%% of equity",
		lim.KellyFraction*100, edge.WinRate*100, edge.PayoffRatio, edge.Trips, frac*100), true
}

// FullKelly is the growth-optimal fraction of capital to risk on a binary bet
// that pays b:1 with probability p:
//
//	f* = p - (1-p)/b
//
// It is exported because it is a claim worth testing directly, and because a
// reader checking the sizing should be able to see the formula rather than infer
// it. ok is false for inputs outside the domain (p not a probability, b not a
// positive finite payoff), where the formula has no meaning — the caller must
// refuse rather than substitute a number.
//
// f* can be NEGATIVE, and that is the useful part: it is the arithmetic
// statement that the bet loses money, which no amount of position sizing fixes.
func FullKelly(p, b float64) (float64, bool) {
	if math.IsNaN(p) || math.IsNaN(b) || math.IsInf(p, 0) || math.IsInf(b, 0) {
		return 0, false
	}
	if p < 0 || p > 1 || b <= 0 {
		return 0, false
	}
	f := p - (1-p)/b
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, false
	}
	if f > 1 {
		// Bounded at the whole book: a formula output above 1 means "bet
		// everything", and levering past it is a different product.
		f = 1
	}
	return f, true
}

// withDefaults fills any unset (non-positive) field from Defaults so a caller
// passing a partial envelope gets the documented limit rather than "no limit".
// A zero MaxDrawdown must not read as "halt immediately", and a zero
// MaxSectorWeight must not read as "no sector may hold anything".
func (l Limits) withDefaults() Limits {
	d := Defaults()
	if l.MaxDrawdown <= 0 {
		l.MaxDrawdown = d.MaxDrawdown
	}
	if l.MaxDailyLoss <= 0 {
		l.MaxDailyLoss = d.MaxDailyLoss
	}
	if l.MaxPositionWeight <= 0 {
		l.MaxPositionWeight = d.MaxPositionWeight
	}
	if l.MaxSectorWeight <= 0 {
		l.MaxSectorWeight = d.MaxSectorWeight
	}
	if l.MaxPositions <= 0 {
		l.MaxPositions = d.MaxPositions
	}
	if l.KellyFraction <= 0 {
		l.KellyFraction = d.KellyFraction
	}
	if l.MinEdgeTrips <= 0 {
		l.MinEdgeTrips = d.MinEdgeTrips
	}
	if l.MinTicketFrac <= 0 {
		l.MinTicketFrac = d.MinTicketFrac
	}
	if l.MaxCorrToBook <= 0 {
		l.MaxCorrToBook = d.MaxCorrToBook
	}
	if l.MaxGrossExposure <= 0 {
		l.MaxGrossExposure = d.MaxGrossExposure
	}
	return l
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// envFrac reads a fraction in (0,1] from env, falling back on anything else. A
// limit outside that range is a typo (a percent typed as 20 instead of 0.20),
// and honoring it would silently disable the check it configures.
func envFrac(key string, def float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 && f <= 1 {
			return f
		}
	}
	return def
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		n, err := strconv.Atoi(v)
		switch {
		case err != nil:
			envcfg.Reject(key, v, "not an integer", strconv.Itoa(def))
		case n <= 0:
			envcfg.Reject(key, v, "must be > 0", strconv.Itoa(def))
		default:
			return n
		}
	}
	return def
}
