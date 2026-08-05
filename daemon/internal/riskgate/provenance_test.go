package riskgate

import (
	"strings"
	"testing"
)

// DescribeLimits resolves the envelope a SECOND time, from its own table. Two
// copies of the same defaults is the exact shape of the defect this whole
// exercise exists to remove, so the copies are pinned together here: if anyone
// edits Defaults() without editing the provenance table, this fails rather than
// letting the audit record quietly describe an envelope the gate is not using.
func TestProvenanceMatchesDefaults(t *testing.T) {
	t.Setenv("SIGNALDECK_RISK_MAX_DRAWDOWN", "")
	got := DescribeLimits().Limits
	want := Defaults()
	if got != want {
		t.Fatalf("provenance table has drifted from Defaults()\n got: %+v\nwant: %+v", got, want)
	}
}

// An out-of-range override must be REPORTED, not silently swallowed. The old
// behaviour returned the default and said nothing, so a misconfigured box ran a
// different envelope than its operator believed and nothing in the record said so.
func TestRejectedOverrideIsRecordedNotSilent(t *testing.T) {
	t.Setenv("SIGNALDECK_RISK_MAX_GROSS_EXPOSURE", "5.0") // leverage, outside (0,1]
	p := DescribeLimits()

	if p.Limits.MaxGrossExposure != Defaults().MaxGrossExposure {
		t.Errorf("a rejected override must not change the limit, got %v", p.Limits.MaxGrossExposure)
	}
	if p.Rejections != 1 {
		t.Fatalf("want 1 rejection, got %d", p.Rejections)
	}
	rej := p.Rejected()
	if len(rej) != 1 || rej[0].Key != "SIGNALDECK_RISK_MAX_GROSS_EXPOSURE" {
		t.Fatalf("wrong rejection recorded: %+v", rej)
	}
	if rej[0].RawEnv != "5.0" {
		t.Errorf("the rejected raw value must be kept for the record, got %q", rej[0].RawEnv)
	}
	if !strings.Contains(p.String(), "REJECTED") {
		t.Errorf("the rendered provenance must say REJECTED:\n%s", p.String())
	}
}

// An accepted override is recorded as coming from the environment, so a past
// run's envelope can be reconstructed rather than assumed to be the defaults.
func TestAcceptedOverrideIsAttributedToEnv(t *testing.T) {
	t.Setenv("SIGNALDECK_RISK_MAX_POSITIONS", "4")
	p := DescribeLimits()

	if p.Limits.MaxPositions != 4 {
		t.Fatalf("want MaxPositions 4, got %d", p.Limits.MaxPositions)
	}
	if p.Rejections != 0 {
		t.Fatalf("a valid override is not a rejection, got %d", p.Rejections)
	}
	if !strings.Contains(p.String(), "SIGNALDECK_RISK_MAX_POSITIONS=4 (env)") {
		t.Errorf("env attribution missing:\n%s", p.String())
	}
}

// The summary line is what lands in a log, so it has to count honestly.
func TestSummaryCountsEveryLimit(t *testing.T) {
	p := DescribeLimits()
	if len(p.Sources) != 10 {
		t.Fatalf("want all 10 limits described, got %d", len(p.Sources))
	}
	if !strings.HasPrefix(p.String(), "risk-limits: 10 default, 0 env, 0 rejected") {
		t.Errorf("unexpected summary line:\n%s", strings.SplitN(p.String(), "\n", 2)[0])
	}
}
