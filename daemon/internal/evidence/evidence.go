// Package evidence is SignalDeck's Evidence Engine: every claim the system
// publishes gets an attached, machine-checkable evidence record that can go
// STALE and auto-downgrade.
//
// The design encodes the lessons of the 2026-07-26 re-audit directly as
// rules rather than as documentation:
//
//   - n_effective is CLUSTER-ROBUST (raw n / design effect), never a raw row
//     count. A1/A2 happened because decision surfaces read raw-row Wilson
//     intervals; here an item without a stated method and a positive
//     n_effective is not evidence at all and the claim is rejected.
//   - A confidence tier is JUSTIFIED, not asserted. "strong" requires a CI
//     that excludes the claim's own null, a multiple-testing correction on
//     record, and n_effective over a floor — the same shape of gate the
//     canary now enforces. A claim whose stated tier exceeds what its items
//     justify is rejected at validation, so the database cannot hold flattery.
//   - Claims EXPIRE. revalidate_by is a promise; the sweep (sweep.go) marks
//     past-due claims stale and removes one tier, so certainty decays unless
//     someone re-measures. Refuting evidence retires the claim outright.
package evidence

import (
	"errors"
	"fmt"
)

// Tier is a claim's confidence tier. Order matters: downgrade steps left.
type Tier string

const (
	TierStrong   Tier = "strong"
	TierModerate Tier = "moderate"
	TierWeak     Tier = "weak"
	TierRefuted  Tier = "refuted"
)

// tierRank orders tiers for "may not claim above justified" checks and for
// downgrades. refuted is outside the ladder (terminal), rank -1.
func tierRank(t Tier) int {
	switch t {
	case TierStrong:
		return 3
	case TierModerate:
		return 2
	case TierWeak:
		return 1
	default:
		return -1
	}
}

// Downgrade returns the tier one step weaker. weak stays weak (the status
// carries the stale mark); refuted is terminal.
func Downgrade(t Tier) Tier {
	switch t {
	case TierStrong:
		return TierModerate
	case TierModerate:
		return TierWeak
	default:
		return t
	}
}

// Status is a claim's lifecycle state.
type Status string

const (
	StatusActive     Status = "active"
	StatusStale      Status = "stale"
	StatusDowngraded Status = "downgraded"
	StatusRetired    Status = "retired"
)

// Scope bounds what a claim is about. A claim with no scope is a claim about
// everything, which is a claim about nothing — validation requires at least
// the date range.
type Scope struct {
	Assets   []string `json:"assets,omitempty"`   // e.g. ["stocks","crypto"] or symbols
	Regimes  []string `json:"regimes,omitempty"`  // e.g. ["all"], ["trend21:strong-up"]
	Horizons []string `json:"horizons,omitempty"` // e.g. ["1d","1w"]
	DateFrom string   `json:"dateFrom"`           // ISO date the evidence window starts
	DateTo   string   `json:"dateTo"`             // ISO date it ends
}

// Lineage links a claim to the code artifacts it is about, so a retired
// feature can find every claim it was carrying.
type Lineage struct {
	FeatureKeys []string `json:"featureKeys,omitempty"`
	Models      []string `json:"models,omitempty"`
}

// Item is one measured piece of support. Baseline is the null the interval is
// judged against (majority-class accuracy, zero effect, ...); when nil, zero
// is the null.
type Item struct {
	Kind       string   `json:"kind"`  // live-record | backtest | study | reproduction
	Value      float64  `json:"value"` // the measured statistic
	NEffective float64  `json:"nEffective"`
	Method     string   `json:"method"`               // walk-forward | purged-walk-forward | live-forward | ...
	Correction string   `json:"correction,omitempty"` // multiple-testing correction ("" = none)
	CILow      *float64 `json:"ciLow,omitempty"`
	CIHigh     *float64 `json:"ciHigh,omitempty"`
	Baseline   *float64 `json:"baseline,omitempty"`
	SourceRef  string   `json:"sourceRef"` // file/table the number came from
}

// baselineOrZero resolves the item's null.
func (it Item) baselineOrZero() float64 {
	if it.Baseline != nil {
		return *it.Baseline
	}
	return 0
}

// Supports reports whether the item's interval excludes its null in the
// claim's favor (CI entirely above baseline). No interval = no support.
func (it Item) Supports() bool {
	return it.CILow != nil && it.CIHigh != nil && *it.CILow > it.baselineOrZero()
}

// Refutes reports whether the item's interval sits entirely on the WRONG side
// of the null — the evidence contradicts the claim rather than merely failing
// to support it.
func (it Item) Refutes() bool {
	return it.CILow != nil && it.CIHigh != nil && *it.CIHigh < it.baselineOrZero()
}

// Claim is one evidence-backed statement.
type Claim struct {
	ID            string  `json:"id"`
	Text          string  `json:"text"`
	Scope         Scope   `json:"scope"`
	Items         []Item  `json:"items"`
	Tier          Tier    `json:"tier"`
	Status        Status  `json:"status"`
	LastValidated int64   `json:"lastValidated"` // unix seconds
	RevalidateBy  int64   `json:"revalidateBy"`  // unix seconds
	Lineage       Lineage `json:"lineage"`
	Seeded        bool    `json:"seeded"`
}

// StrongNEff is the cluster-robust effective-n floor for a "strong" tier.
// 300 is deliberately above the live 1d record's effective n (~887 would
// pass; the single-market-day ~408-row scenario from audit finding A2, once
// day-clustered, would not).
const StrongNEff = 300.0

// ModerateNEff is the floor for "moderate": enough effective observations
// that an interval means something, without demanding a corrected result.
const ModerateNEff = 100.0

// JustifiedTier computes the strongest tier the claim's own items can carry.
// Rules, in order:
//
//	refuted  — any item refutes (interval entirely on the wrong side of null)
//	strong   — some item supports (CI excludes null), carries a
//	           multiple-testing correction, and n_effective >= StrongNEff
//	moderate — some item has a full interval and n_effective >= ModerateNEff
//	weak     — anything else with at least one valid item
func JustifiedTier(items []Item) Tier {
	for _, it := range items {
		if it.Refutes() {
			return TierRefuted
		}
	}
	for _, it := range items {
		if it.Supports() && it.Correction != "" && it.NEffective >= StrongNEff {
			return TierStrong
		}
	}
	for _, it := range items {
		if it.CILow != nil && it.CIHigh != nil && it.NEffective >= ModerateNEff {
			return TierModerate
		}
	}
	return TierWeak
}

// Validation errors, exported so callers and tests can assert the reason.
var (
	ErrNoEvidence      = errors.New("evidence: claim has no evidence items")
	ErrItemIncomplete  = errors.New("evidence: item missing n_effective or method")
	ErrTierUnjustified = errors.New("evidence: stated tier exceeds what the items justify")
	ErrMissingField    = errors.New("evidence: missing required field")
)

// Validate rejects a claim that fails the engine's rules. A claim that
// validates is safe to persist; nothing that fails may reach the store.
func (c Claim) Validate() error {
	if c.ID == "" || c.Text == "" {
		return fmt.Errorf("%w: id and text are required", ErrMissingField)
	}
	if c.Scope.DateFrom == "" || c.Scope.DateTo == "" {
		return fmt.Errorf("%w: scope date range is required", ErrMissingField)
	}
	if c.RevalidateBy <= 0 || c.LastValidated <= 0 {
		return fmt.Errorf("%w: last_validated and revalidate_by are required", ErrMissingField)
	}
	if len(c.Items) == 0 {
		return ErrNoEvidence
	}
	for i, it := range c.Items {
		if it.NEffective <= 0 || it.Method == "" {
			return fmt.Errorf("%w (item %d, kind %q)", ErrItemIncomplete, i, it.Kind)
		}
	}
	just := JustifiedTier(c.Items)
	// A refuted body of evidence admits only the refuted tier; otherwise the
	// stated tier may be at or below what is justified, never above.
	if just == TierRefuted {
		if c.Tier != TierRefuted {
			return fmt.Errorf("%w: evidence refutes, stated tier %q", ErrTierUnjustified, c.Tier)
		}
		return nil
	}
	if c.Tier == TierRefuted || tierRank(c.Tier) > tierRank(just) {
		return fmt.Errorf("%w: stated %q, justified %q", ErrTierUnjustified, c.Tier, just)
	}
	return nil
}
