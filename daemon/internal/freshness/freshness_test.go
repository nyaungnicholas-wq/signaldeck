package freshness

import (
	"strings"
	"testing"
	"time"
)

// This package had no test file at all, while being the gate that decides
// whether /api/desk/recommendation serves a number or refuses (api/desk.go:129).
// The defect it was written for — audit F-2, a ten-day-old close served under
// AsOf: time.Now() — is a MISSING-timestamp and a BOUNDARY defect, so those are
// what is pinned here.

var now = time.Date(2026, 8, 6, 15, 0, 0, 0, time.UTC)

// The F-2 defect itself. A zero timestamp means "we do not know when this data
// is from", and the tempting repair — treat unknown as now — is exactly what
// served week-old prices as current. Catches any change that lets an absent
// timestamp through the gate.
func TestZeroTimestampIsRefusedRatherThanTreatedAsNow(t *testing.T) {
	err := Check(now, time.Time{}, time.Hour)
	if err == nil {
		t.Fatal("a missing data_asof passed the freshness gate — this is audit F-2 exactly: " +
			"unknown provenance published as current")
	}
	if !strings.Contains(err.Error(), "missing") {
		t.Errorf("refusal does not say the timestamp is missing: %q", err)
	}
}

// The boundary. maxAge is inclusive: data exactly at the limit is still
// current, one nanosecond past it is not. Catches a > / >= flip, which either
// serves stale data for one extra window or refuses data the policy allows.
func TestFreshnessBoundaryIsInclusiveAtExactlyMaxAge(t *testing.T) {
	const maxAge = 2 * time.Hour
	if err := Check(now, now.Add(-maxAge), maxAge); err != nil {
		t.Errorf("data at exactly maxAge was refused: %v", err)
	}
	if err := Check(now, now.Add(-maxAge-time.Nanosecond), maxAge); err == nil {
		t.Error("data one nanosecond past maxAge passed the gate")
	}
	if err := Check(now, now.Add(-maxAge+time.Nanosecond), maxAge); err != nil {
		t.Errorf("data one nanosecond inside maxAge was refused: %v", err)
	}
}

// The refusal is copied verbatim into the HTTP body (api/desk.go:135). An
// operator reading it must be able to see WHICH timestamp was rejected and by
// how much; a bare "stale data" is an alert nobody can act on.
func TestRefusalNamesTheTimestampAndTheAge(t *testing.T) {
	asOf := now.Add(-72 * time.Hour)
	err := Check(now, asOf, time.Hour)
	if err == nil {
		t.Fatal("three-day-old data passed a one-hour freshness gate")
	}
	msg := err.Error()
	for _, want := range []string{asOf.UTC().Format(time.RFC3339), "72h0m0s", "1h0m0s"} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal %q does not carry %q", msg, want)
		}
	}
}

// Clock skew. A data timestamp marginally in the future is a clock problem, not
// a staleness problem: the gate must pass it, and the reported age must be 0.
// desk.go casts Age to int64 seconds straight into the JSON body, so a negative
// duration here publishes a negative stalenessSeconds.
func TestFutureTimestampPassesAndReportsZeroAgeNotNegative(t *testing.T) {
	ahead := now.Add(30 * time.Second)
	if err := Check(now, ahead, time.Hour); err != nil {
		t.Errorf("a timestamp 30s in the future was called stale: %v", err)
	}
	if got := Age(now, ahead); got != 0 {
		t.Errorf("Age = %v for future data, want 0 — a negative age is published verbatim", got)
	}
	if got := Age(now, now); got != 0 {
		t.Errorf("Age = %v for data stamped exactly now, want 0", got)
	}
}

// Age must be the real staleness when the data IS old — a floor at zero that
// swallowed every value would hide the very number the refusal reports.
func TestAgeReportsRealStalenessWhenDataIsOld(t *testing.T) {
	if got := Age(now, now.Add(-90*time.Minute)); got != 90*time.Minute {
		t.Errorf("Age = %v, want 1h30m0s", got)
	}
}
