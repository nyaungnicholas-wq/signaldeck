package pipeline

import (
	"testing"
	"time"
)

// The by-design calibration refusal fired every pass (3,113 WARNs in 16 days);
// it must log once an hour per horizon, independently per horizon.
func TestCalRefusalLogDueIsHourlyPerHorizon(t *testing.T) {
	t0 := time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)
	if !calRefusalLogDue("test-1d", t0) || calRefusalLogDue("test-1d", t0.Add(59*time.Minute)) {
		t.Fatal("second refusal inside the hour must be quiet")
	}
	if !calRefusalLogDue("test-1w", t0.Add(time.Minute)) {
		t.Fatal("a different horizon has its own budget")
	}
	if !calRefusalLogDue("test-1d", t0.Add(61*time.Minute)) {
		t.Fatal("after an hour the refusal logs again")
	}
}
