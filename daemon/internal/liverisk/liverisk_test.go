package liverisk

import (
	"errors"
	"strings"
	"testing"
)

// valid returns a Config that passes, so each test can break exactly one field
// and prove that field is actually load-bearing.
func valid() Config {
	return Config{
		Version:              1,
		Broker:               "example-broker",
		AccountID:            "acct-1",
		Jurisdiction:         "US",
		MaxPositionUSD:       1000,
		StopLossPct:          0.05,
		DailyLossLimitUSD:    250,
		MaxDrawdownPct:       0.20,
		MaxGrossExposureUSD:  5000,
		MaxLeverage:          1,
		Instruments:          []string{"stocks"},
		Sessions:             []string{"regular"},
		KillSwitchPath:       "/tmp/halt",
		IdempotencyKeyPrefix: "sd-",
		ReconcileIntervalSec: 60,
		CanaryMaxOrderUSD:    50,
	}
}

// THE ONE THAT MATTERS: nothing arms by default.
func TestZeroConfigNeverArms(t *testing.T) {
	for _, env := range []string{"", "false", "0", "no", "TRUE ", "true"} {
		if err := Armed(Config{}, env); err == nil {
			t.Fatalf("the ZERO config armed with LIVE_ARMED=%q - it must never arm", env)
		}
	}
}

func TestArmedRequiresBothTheFlagAndAValidConfig(t *testing.T) {
	// Flag off, config good -> not armed, and specifically ErrNotArmed.
	if err := Armed(valid(), "false"); !errors.Is(err, ErrNotArmed) {
		t.Fatalf("want ErrNotArmed with the flag off, got %v", err)
	}
	if err := Armed(valid(), ""); !errors.Is(err, ErrNotArmed) {
		t.Fatalf("want ErrNotArmed with the flag unset, got %v", err)
	}
	// Flag on, config broken -> a VALIDATION error, not ErrNotArmed: the
	// distinction tells an operator which of the two things is missing.
	broken := valid()
	broken.MaxPositionUSD = 0
	err := Armed(broken, "true")
	if err == nil {
		t.Fatal("armed with MaxPositionUSD=0")
	}
	if errors.Is(err, ErrNotArmed) {
		t.Fatalf("a broken config must report VALIDATION failure, not ErrNotArmed: %v", err)
	}
	// Flag on, config good -> armed.
	if err := Armed(valid(), "true"); err != nil {
		t.Fatalf("a complete config with the flag on must arm, got %v", err)
	}
	// Case and surrounding space must not change the answer.
	if err := Armed(valid(), "  TRUE  "); err != nil {
		t.Fatalf("flag parsing must be trimmed and case-insensitive, got %v", err)
	}
}

// Every field is load-bearing: breaking any ONE must refuse.
func TestEveryFieldIsRequired(t *testing.T) {
	for name, breakIt := range map[string]func(*Config){
		"Version":              func(c *Config) { c.Version = 0 },
		"Broker":               func(c *Config) { c.Broker = "  " },
		"AccountID":            func(c *Config) { c.AccountID = "" },
		"Jurisdiction":         func(c *Config) { c.Jurisdiction = "" },
		"MaxPositionUSD":       func(c *Config) { c.MaxPositionUSD = 0 },
		"StopLossPct low":      func(c *Config) { c.StopLossPct = 0 },
		"StopLossPct high":     func(c *Config) { c.StopLossPct = 1.5 },
		"DailyLossLimitUSD":    func(c *Config) { c.DailyLossLimitUSD = 0 },
		"MaxDrawdownPct low":   func(c *Config) { c.MaxDrawdownPct = 0 },
		"MaxDrawdownPct high":  func(c *Config) { c.MaxDrawdownPct = 2 },
		"MaxGrossExposureUSD":  func(c *Config) { c.MaxGrossExposureUSD = 0 },
		"MaxLeverage":          func(c *Config) { c.MaxLeverage = 0.5 },
		"Instruments":          func(c *Config) { c.Instruments = nil },
		"Sessions":             func(c *Config) { c.Sessions = nil },
		"KillSwitchPath":       func(c *Config) { c.KillSwitchPath = "" },
		"IdempotencyKeyPrefix": func(c *Config) { c.IdempotencyKeyPrefix = "" },
		"ReconcileIntervalSec": func(c *Config) { c.ReconcileIntervalSec = 0 },
		"CanaryMaxOrderUSD":    func(c *Config) { c.CanaryMaxOrderUSD = 0 },
	} {
		c := valid()
		breakIt(&c)
		if err := Validate(c); err == nil {
			t.Errorf("%s: broken field accepted - that field is not actually enforced", name)
		}
		if err := Armed(c, "true"); err == nil {
			t.Errorf("%s: ARMED with a broken field", name)
		}
	}
}

// A canary larger than the position cap is not a canary.
func TestCanaryCannotExceedThePositionCap(t *testing.T) {
	c := valid()
	c.CanaryMaxOrderUSD = c.MaxPositionUSD + 1
	err := Validate(c)
	if err == nil {
		t.Fatal("canary above the position cap accepted")
	}
	if !strings.Contains(err.Error(), "CanaryMaxOrderUSD") {
		t.Fatalf("error must name the field: %v", err)
	}
}

// Validate reports EVERY problem, not just the first: an operator fixing one
// field per run against a partially-specified risk config is the exact failure
// this package exists to prevent.
func TestValidateReportsEveryProblemAtOnce(t *testing.T) {
	err := Validate(Config{})
	if err == nil {
		t.Fatal("zero config validated")
	}
	msg := err.Error()
	for _, want := range []string{
		"Version", "Broker", "AccountID", "Jurisdiction", "MaxPositionUSD",
		"StopLossPct", "DailyLossLimitUSD", "MaxDrawdownPct", "MaxGrossExposureUSD",
		"MaxLeverage", "Instruments", "Sessions", "KillSwitchPath",
		"IdempotencyKeyPrefix", "ReconcileIntervalSec", "CanaryMaxOrderUSD",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("the zero config's error omits %s - a caller would fix fields one run at a time", want)
		}
	}
}
