// Package liverisk is the GATE that any future live-trading path must pass.
// Today SignalDeck has NO live order path at all - the only broker host in the codebase is paper-api.alpaca.markets.
// This package defines the contract one would have to satisfy; every field is REQUIRED because a partially-specified
// risk config is more dangerous than none, since it looks configured; and the zero value must never be armable.
package liverisk

import (
	"errors"
	"fmt"
	"strings"
)

var ErrNotArmed = errors.New("liverisk: LIVE_ARMED is not true; refusing to arm")

type Config struct {
	Version              int      `json:"version"`                // schema version; must be >= 1
	Broker               string   `json:"broker"`                 // broker identifier
	AccountID            string   `json:"account_id"`             // account the orders would go to
	Jurisdiction         string   `json:"jurisdiction"`           // regulatory jurisdiction
	MaxPositionUSD       float64  `json:"max_position_usd"`       // per-position sizing cap, must be > 0
	StopLossPct          float64  `json:"stop_loss_pct"`          // per-position stop, must be > 0 and <= 1
	DailyLossLimitUSD    float64  `json:"daily_loss_limit_usd"`   // must be > 0
	MaxDrawdownPct       float64  `json:"max_drawdown_pct"`       // must be > 0 and <= 1
	MaxGrossExposureUSD  float64  `json:"max_gross_exposure_usd"` // must be > 0
	MaxLeverage          float64  `json:"max_leverage"`           // must be >= 1
	Instruments          []string `json:"instruments"`            // allowed instruments; must be non-empty
	Sessions             []string `json:"sessions"`               // allowed trading sessions; must be non-empty
	KillSwitchPath       string   `json:"kill_switch_path"`       // a path whose EXISTENCE halts trading; must be non-empty
	IdempotencyKeyPrefix string   `json:"idempotency_key_prefix"` // must be non-empty
	ReconcileIntervalSec int      `json:"reconcile_interval_sec"` // must be > 0
	CanaryMaxOrderUSD    float64  `json:"canary_max_order_usd"`   // paper->tiny-canary promotion cap; must be > 0 and <= MaxPositionUSD
}

// Validate returns a non-nil error listing every validation problem found.
// It uses errors.Join to combine messages; nil only when all constraints pass.
func Validate(c Config) error {
	var errs []error

	if c.Version < 1 {
		errs = append(errs, fmt.Errorf("liverisk: Version must be >= 1, got %d", c.Version))
	}
	if strings.TrimSpace(c.Broker) == "" {
		errs = append(errs, errors.New("liverisk: Broker must be non-empty"))
	}
	if strings.TrimSpace(c.AccountID) == "" {
		errs = append(errs, errors.New("liverisk: AccountID must be non-empty"))
	}
	if strings.TrimSpace(c.Jurisdiction) == "" {
		errs = append(errs, errors.New("liverisk: Jurisdiction must be non-empty"))
	}
	if c.MaxPositionUSD <= 0 {
		errs = append(errs, fmt.Errorf("liverisk: MaxPositionUSD must be > 0, got %f", c.MaxPositionUSD))
	}
	if c.StopLossPct <= 0 || c.StopLossPct > 1 {
		errs = append(errs, fmt.Errorf("liverisk: StopLossPct must be > 0 and <= 1, got %f", c.StopLossPct))
	}
	if c.DailyLossLimitUSD <= 0 {
		errs = append(errs, fmt.Errorf("liverisk: DailyLossLimitUSD must be > 0, got %f", c.DailyLossLimitUSD))
	}
	if c.MaxDrawdownPct <= 0 || c.MaxDrawdownPct > 1 {
		errs = append(errs, fmt.Errorf("liverisk: MaxDrawdownPct must be > 0 and <= 1, got %f", c.MaxDrawdownPct))
	}
	if c.MaxGrossExposureUSD <= 0 {
		errs = append(errs, fmt.Errorf("liverisk: MaxGrossExposureUSD must be > 0, got %f", c.MaxGrossExposureUSD))
	}
	if c.MaxLeverage < 1 {
		errs = append(errs, fmt.Errorf("liverisk: MaxLeverage must be >= 1, got %f", c.MaxLeverage))
	}
	if len(c.Instruments) == 0 {
		errs = append(errs, errors.New("liverisk: Instruments must be non-empty"))
	}
	if len(c.Sessions) == 0 {
		errs = append(errs, errors.New("liverisk: Sessions must be non-empty"))
	}
	if strings.TrimSpace(c.KillSwitchPath) == "" {
		errs = append(errs, errors.New("liverisk: KillSwitchPath must be non-empty"))
	}
	if strings.TrimSpace(c.IdempotencyKeyPrefix) == "" {
		errs = append(errs, errors.New("liverisk: IdempotencyKeyPrefix must be non-empty"))
	}
	if c.ReconcileIntervalSec <= 0 {
		errs = append(errs, fmt.Errorf("liverisk: ReconcileIntervalSec must be > 0, got %d", c.ReconcileIntervalSec))
	}
	if c.CanaryMaxOrderUSD <= 0 {
		errs = append(errs, fmt.Errorf("liverisk: CanaryMaxOrderUSD must be > 0, got %f", c.CanaryMaxOrderUSD))
	}
	if c.CanaryMaxOrderUSD > c.MaxPositionUSD {
		errs = append(errs, fmt.Errorf("liverisk: CanaryMaxOrderUSD must be <= MaxPositionUSD, got %f > %f", c.CanaryMaxOrderUSD, c.MaxPositionUSD))
	}

	// errors.Join reports EVERY problem at once. A caller that fixes one field
	// per run against a partially-specified live risk config is exactly the
	// failure this package exists to prevent.
	return errors.Join(errs...)
}

// Armed returns nil only when liveArmedEnv signals true (case-insensitive, trimmed) and the config passes Validate.
// Callers must treat a non-nil error as "do not place orders". There is deliberately no override parameter.
func Armed(c Config, liveArmedEnv string) error {
	if strings.TrimSpace(strings.ToLower(liveArmedEnv)) != "true" {
		return ErrNotArmed
	}
	return Validate(c)
}
