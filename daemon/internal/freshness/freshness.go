// Package freshness answers one question: is this data young enough to be
// presented as current?
//
// It exists because /api/recommendation served a ten-day-old close under
// AsOf: time.Now() (audit F-2, 2026-08-03). Nothing was corrupt — the price was
// a real price and the timestamp was a real timestamp; they were simply not the
// same timestamp. A response that stamps itself with the moment it was
// generated, while carrying data from a week ago, is the most convincing kind
// of wrong output a system can produce.
//
// The rule: generated_at and data_asof are different facts and must never be
// collapsed into one field.
package freshness

import (
	"fmt"
	"time"
)

// Check returns nil when dataAsOf is within maxAge of now, and a describing
// error otherwise. A zero dataAsOf is an error, not a pass: an absent timestamp
// is exactly the state that produced F-2, and defaulting it to now would
// reintroduce the defect.
func Check(now, dataAsOf time.Time, maxAge time.Duration) error {
	if dataAsOf.IsZero() {
		return fmt.Errorf("missing data_asof timestamp")
	}
	age := now.Sub(dataAsOf)
	if age > maxAge {
		return fmt.Errorf("stale data: data_asof=%s age=%s max_age=%s",
			dataAsOf.UTC().Format(time.RFC3339), age.Round(time.Second), maxAge)
	}
	return nil
}

// Age is the reported staleness, floored at zero so a clock skew that puts the
// data marginally in the future reports 0 rather than a negative age.
func Age(now, dataAsOf time.Time) time.Duration {
	if d := now.Sub(dataAsOf); d > 0 {
		return d
	}
	return 0
}
