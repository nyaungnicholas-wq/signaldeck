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
//
// 11, not 10: SIGNALDECK_RISK_FLATTEN_DRAWDOWN joined the record. It was the
// terminal rung — the one that force-liquidates the whole book — and the only
// SIGNALDECK_RISK_* key parsed outside this function, so a rejected override of
// it was discarded in complete silence while this very summary line claimed to
// count every limit.
func TestSummaryCountsEveryLimit(t *testing.T) {
	p := DescribeLimits()
	if len(p.Sources) != 11 {
		t.Fatalf("want all 11 limits described, got %d", len(p.Sources))
	}
	if !strings.HasPrefix(p.String(), "risk-limits: 11 default, 0 env, 0 rejected") {
		t.Errorf("unexpected summary line:\n%s", strings.SplitN(p.String(), "\n", 2)[0])
	}
	var seen bool
	for _, s := range p.Sources {
		if s.Key == flattenDrawdownKey {
			seen = true
		}
	}
	if !seen {
		t.Errorf("%s is absent from the provenance record; it is the rung that closes the book", flattenDrawdownKey)
	}
}

// A rejected flatten override must reach the record AND the count, and the book
// must fall back to the default rather than to the operator's bad number. The
// live failure this pins: typing 15 for 15% instead of 0.15 ran the 0.25 default
// with nothing in the log saying so.
func TestRejectedFlattenOverrideIsOnTheRecord(t *testing.T) {
	t.Setenv(flattenDrawdownKey, "15")
	p := DescribeLimits()

	if p.Rejections != 1 {
		t.Fatalf("want 1 rejection for an out-of-domain flatten override, got %d", p.Rejections)
	}
	if !strings.Contains(p.String(), flattenDrawdownKey+"=0.25 (env REJECTED") {
		t.Errorf("rejection not attributed in the summary:\n%s", p.String())
	}
	// FlattenLimit must agree with the record: same parse, same verdict.
	if got := FlattenLimit(Defaults()); got != DefaultFlattenDrawdown {
		t.Errorf("FlattenLimit honoured a rejected override: got %v, want %v", got, DefaultFlattenDrawdown)
	}
}

// The two parsers used to disagree on the domain: the table accepted (0,1] and
// FlattenLimit accepted (0,1), so exactly 1.0 was honoured for MAX_DRAWDOWN and
// silently dropped here. One shared parse means one verdict.
func TestFlattenDomainAgreesBetweenRecordAndBehaviour(t *testing.T) {
	for _, raw := range []string{"0", "1", "1.0", "-0.1", "abc"} {
		t.Setenv(flattenDrawdownKey, raw)
		p := DescribeLimits()
		var src LimitSource
		for _, s := range p.Sources {
			if s.Key == flattenDrawdownKey {
				src = s
			}
		}
		if !src.Rejected {
			t.Errorf("raw %q was accepted by the record", raw)
		}
		if got := FlattenLimit(Defaults()); got != DefaultFlattenDrawdown {
			t.Errorf("raw %q: FlattenLimit returned %v, want the %v default", raw, got, DefaultFlattenDrawdown)
		}
	}
	// And an in-domain value is honoured by both, floored at MaxDrawdown.
	t.Setenv(flattenDrawdownKey, "0.30")
	if got := FlattenLimit(Defaults()); got != 0.30 {
		t.Errorf("a valid override was dropped: got %v, want 0.30", got)
	}
}
