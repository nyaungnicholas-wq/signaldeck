// Package ev is the DECISION ENGINE (ARCHITECTURE_EV.md Layer 1): the one
// place where everything the platform knows about a candidate trade is JOINED
// before the book acts.
//
// WHY THIS EXISTS. The entry decision used to be a bare probability threshold
// (papertrade.DecideTarget on cal_prob) while every input that actually decides
// whether a trade is WORTH TAKING — the cost-adjusted expected value and
// no-trade zone from internal/distribution, the state-conditional expectancy,
// the measured adverse excursion, the execution cost and capacity model, the
// book's correlation structure — was computed, persisted, and never consulted.
// A probability is not an edge: a 70% call on a move smaller than its own cost
// is a losing trade with good aim. This package makes the join explicit.
//
// THREE RULES, in the platform's standing doctrine:
//
//   - MISSING INPUTS ARE EXPLICIT, NEVER SILENTLY ZERO. Every input carries a
//     has-flag. A required input that is absent produces a REFUSAL with a named
//     reason, not a decision computed over a flattering default.
//   - EXITS ARE NEVER BLOCKED. Same rule as riskgate: a gate that can refuse a
//     de-risking trade is a trap, not a control. Decide answers SELL for an
//     exit intent unconditionally.
//   - EVERY DO_NOTHING IS A DECISION, with an enumerated reason, so refusals
//     are auditable ("what did not trading cost") instead of being entries that
//     silently never happened.
//
// Like riskgate, this package is PURE: no I/O, no clock, no store. The caller
// (pipeline/paper.go) measures the inputs; the engine only judges them — a gate
// that computed its own inputs could quietly compute flattering ones.
package ev

import (
	"math"
	"os"
	"sort"
	"strconv"
)

// Action is the engine's verdict on one candidate.
type Action string

const (
	// BUY — open the long. Positive net EV, every gate passed. Sizing is still
	// riskgate's job; this is go/no-go only.
	BUY Action = "BUY"
	// SELL — close the position. Exits are never blocked.
	SELL Action = "SELL"
	// DO_NOTHING — the refusal. Always carries an enumerated Reason.
	DO_NOTHING Action = "DO_NOTHING"
)

// Intent is what the caller wants to do — mirrors riskgate.Action so the two
// gates share a vocabulary.
type Intent int

const (
	// EnterLong opens a position — everything here applies.
	EnterLong Intent = iota
	// ExitLong closes one — never gated (riskgate doctrine, restated here).
	ExitLong
)

// Reason enumerates WHY the engine decided what it decided, in the style of
// riskgate's Sizing labels: a reader of the ledger sees a label, not prose.
type Reason string

const (
	// ReasonPositiveNetEV — the BUY: net EV cleared the floor with every gate passed.
	ReasonPositiveNetEV Reason = "positive-net-ev"
	// ReasonExitNeverBlocked — the SELL: de-risking is unconditional.
	ReasonExitNeverBlocked Reason = "exit-never-blocked"
	// ReasonMissingInput — a REQUIRED input (probability, distribution, cost)
	// was not measurable. Refusing is the fail-closed doctrine: deciding over a
	// silently-defaulted input is how a gate flatters itself.
	ReasonMissingInput Reason = "missing-required-input"
	// ReasonNoTradeZone — the forecast distribution says the move most likely
	// stays INSIDE the cost band (p_inside past the ceiling): the majority
	// outcome is "not worth its cost either way".
	ReasonNoTradeZone Reason = "inside-no-trade-zone"
	// ReasonTailTooFat — the measured adverse excursion of comparable holds is
	// past the ceiling: even a correct call typically hurts more on the way
	// than the book should sit through.
	ReasonTailTooFat Reason = "tail-too-fat"
	// ReasonNetEVBelowFloor — the cost-adjusted expected value, net of tau and
	// the modelled execution cost of THIS fill, does not clear the floor. This
	// is the case the bare probability threshold could never express: high
	// probability, negative economics.
	ReasonNetEVBelowFloor Reason = "net-ev-below-floor"
	// ReasonOutranked — the candidate's net EV ranks below better candidates in
	// the same pass. Capital spent here is capital not spent on a higher-EV
	// name — the opportunity-cost refusal.
	ReasonOutranked Reason = "outranked-by-better-ev"
)

// Inputs is everything the caller measured about one candidate. Every group
// carries a has-flag; a false flag means "not measurable", and nothing in this
// package treats it as zero.
type Inputs struct {
	Symbol  string `json:"symbol"`
	Horizon string `json:"horizon"`

	// Calibrated probability of the flagship prediction. REQUIRED.
	CalProb float64 `json:"calProb"`
	HasProb bool    `json:"hasProb"`

	// Forecast return distribution (internal/distribution, via return_forecasts).
	// REQUIRED — without it there is no expected value to gate on.
	DistMean float64 `json:"distMean"` // expected return
	Tau      float64 `json:"tau"`      // the cost band the distribution was built against
	Sigma    float64 `json:"sigma"`    // vol (distribution width)
	PInside  float64 `json:"pInside"`  // P(|return| <= tau) — the no-trade zone
	DistEV   float64 `json:"distEV"`   // E[return|side]·side − tau (already net of tau)
	DistN    int     `json:"distN"`
	HasDist  bool    `json:"hasDist"`

	// Volatility regime the distribution was conditioned on ("" = unknown).
	Regime    string `json:"regime"`
	HasRegime bool   `json:"hasRegime"`

	// State-conditional expectancy prior (internal/expectancy). Advisory.
	ExpMeanFwd    float64 `json:"expMeanFwd"`
	ExpN          int     `json:"expN"`
	HasExpectancy bool    `json:"hasExpectancy"`

	// Tail: measured adverse excursion of comparable historical holds
	// (confidence.AdverseExcursion). Positive fractions of entry.
	TailP90  float64 `json:"tailP90"`
	TailMean float64 `json:"tailMean"`
	TailN    int     `json:"tailN"`
	HasTail  bool    `json:"hasTail"`

	// Liquidity/capacity from the execution model's inputs: average daily
	// dollar volume and the largest fill the participation cap allows.
	ADVUSD       float64 `json:"advUSD"`
	CapacityUSD  float64 `json:"capacityUSD"`
	HasLiquidity bool    `json:"hasLiquidity"`

	// Execution cost of the intended fill, ROUND TRIP, as a fraction of
	// notional (spread + square-root impact, both sides). REQUIRED — an
	// unpriceable fill must refuse, not fill free (papertrade doctrine).
	RoundTripCostFrac float64 `json:"roundTripCostFrac"`
	HasCost           bool    `json:"hasCost"`

	// Correlation of the candidate's daily returns to the current book's.
	// Advisory: recorded for the audit trail (and Layer 7's allocator later).
	CorrToBook float64 `json:"corrToBook"`
	HasCorr    bool    `json:"hasCorr"`
}

// Assessment is Inputs plus the derived net EV and, after RankByNetEV, the
// candidate's rank within its pass — the opportunity-cost input.
type Assessment struct {
	Inputs

	// NetEV is the distribution's cost-adjusted expected value with the
	// EXECUTION cost of this specific fill layered on: DistEV is already net of
	// tau, so only the part of the modelled round-trip cost EXCEEDING tau is
	// charged again (charging all of it would double-count the band).
	NetEV    float64 `json:"netEV"`
	HasNetEV bool    `json:"hasNetEV"`

	// Rank is this candidate's 1-based position by net EV among the RankOf
	// candidates assessed in the same pass (0 = not ranked). Rank IS the
	// opportunity cost: capital taken by rank 8 is denied to rank 1.
	Rank   int `json:"rank"`
	RankOf int `json:"rankOf"`
}

// Assess derives the net EV from one candidate's measured inputs. It never
// invents a missing input: NetEV is only measurable when the distribution and
// the execution cost both are.
func Assess(in Inputs) Assessment {
	a := Assessment{Inputs: in}
	if !in.HasDist || !in.HasCost {
		return a // NetEV unmeasurable — Decide will refuse with the named reason
	}
	extra := in.RoundTripCostFrac - in.Tau
	if extra < 0 {
		extra = 0 // the band already covers the modelled cost — nothing extra to charge
	}
	a.NetEV = in.DistEV - extra
	a.HasNetEV = !math.IsNaN(a.NetEV) && !math.IsInf(a.NetEV, 0)
	return a
}

// RankByNetEV orders one pass's candidates by net EV, best first, and stamps
// Rank/RankOf into each. Candidates with no measurable net EV sort last (they
// will refuse on the missing input anyway); ties break on symbol so the order
// is deterministic. This REPLACES the arbitrary symbol-iteration order the
// paper worker used to allocate capital by.
func RankByNetEV(as []Assessment) []Assessment {
	out := append([]Assessment(nil), as...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].HasNetEV != out[j].HasNetEV {
			return out[i].HasNetEV
		}
		if out[i].NetEV != out[j].NetEV {
			return out[i].NetEV > out[j].NetEV
		}
		return out[i].Symbol < out[j].Symbol
	})
	for i := range out {
		out[i].Rank = i + 1
		out[i].RankOf = len(out)
	}
	return out
}

// Thresholds is the decision envelope. Every field is env-overridable so each
// assumption stays explicit and tunable, never compiled in and forgotten —
// same pattern as riskgate.Limits.
type Thresholds struct {
	// MinNetEV is the floor net EV must EXCEED for a BUY. Zero is the honest
	// conservative default: a trade whose cost-adjusted expected value is not
	// strictly positive is, at best, paying spread to flip a fair coin.
	MinNetEV float64
	// MaxPInside refuses when the distribution puts at least this much mass
	// INSIDE the cost band. 0.75 rather than 0.5 because p_inside is the honest
	// MAJORITY outcome on a 1-day horizon (see internal/distribution) — gating
	// at 0.5 would freeze the book on its own honesty; three quarters says the
	// forecast itself calls the move overwhelmingly not worth its cost.
	MaxPInside float64
	// MaxTailP90 refuses when the 90th-percentile adverse excursion of
	// comparable holds is at least this fraction of entry. 0.10: a setup that
	// typically trades 10% against the entry before resolving is a position the
	// drawdown breaker (riskgate MaxDrawdown 0.20) would meet half-way on two
	// names — too fat regardless of where it ends.
	MaxTailP90 float64
	// MaxRank refuses candidates ranked below this in their pass. 10 matches
	// riskgate MaxPositions: the book cannot hold more names than that, so a
	// candidate outside the top ten is competing for capital that better EV has
	// already claimed.
	MaxRank int
}

// DefaultThresholds returns the standard envelope (env-overridable per field).
func DefaultThresholds() Thresholds {
	return Thresholds{
		MinNetEV:   envFloat("SIGNALDECK_EV_MIN_NET_EV", 0.0),
		MaxPInside: envFloat("SIGNALDECK_EV_MAX_P_INSIDE", 0.75),
		MaxTailP90: envFloat("SIGNALDECK_EV_MAX_TAIL_P90", 0.10),
		MaxRank:    envInt("SIGNALDECK_EV_MAX_RANK", 10),
	}
}

// Decision is the engine's answer for one candidate.
type Decision struct {
	Action Action `json:"action"`
	Reason Reason `json:"reason"`
}

// Decide applies the envelope to one assessed candidate.
//
// The order of checks is a design choice: the structural refusals (missing
// input, no-trade zone, fat tail) run before the EV floor so the ledger names
// the most specific reason, and the opportunity-cost refusal runs LAST — a
// candidate is only "outranked" if it would otherwise have traded.
func Decide(a Assessment, intent Intent, th Thresholds) Decision {
	// EXITS ARE NEVER BLOCKED. Stated first so it cannot be reordered behind a
	// check that might refuse (riskgate.go doctrine, verbatim in spirit).
	if intent == ExitLong {
		return Decision{Action: SELL, Reason: ReasonExitNeverBlocked}
	}

	// Required inputs: the probability, the distribution (the EV itself), and
	// the execution cost of this fill. Absent any one, the engine refuses
	// rather than deciding over a default.
	if !a.HasProb || !a.HasDist || !a.HasCost || !a.HasNetEV {
		return Decision{Action: DO_NOTHING, Reason: ReasonMissingInput}
	}
	if a.PInside >= th.MaxPInside {
		return Decision{Action: DO_NOTHING, Reason: ReasonNoTradeZone}
	}
	// Tail is checked only when measured: a thin episode sample WITHHOLDS the
	// excursion (confidence.MinEpisodes) and withholding an advisory input is
	// not the same as it being fat. The required-input rule above covers what
	// must never be absent.
	if a.HasTail && a.TailP90 >= th.MaxTailP90 {
		return Decision{Action: DO_NOTHING, Reason: ReasonTailTooFat}
	}
	if a.NetEV <= th.MinNetEV {
		return Decision{Action: DO_NOTHING, Reason: ReasonNetEVBelowFloor}
	}
	if th.MaxRank > 0 && a.Rank > th.MaxRank {
		return Decision{Action: DO_NOTHING, Reason: ReasonOutranked}
	}
	return Decision{Action: BUY, Reason: ReasonPositiveNetEV}
}

// ── env helpers (riskgate's pattern: a bad value keeps the default) ────────

func envFloat(key string, def float64) float64 {
	s := os.Getenv(key)
	if s == "" {
		return def
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return def
	}
	return v
}

func envInt(key string, def int) int {
	s := os.Getenv(key)
	if s == "" {
		return def
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return v
}
