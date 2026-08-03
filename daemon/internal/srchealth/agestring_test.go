package srchealth

import (
	"strings"
	"testing"
)

// TestAgeStringSeparatesEmptyFromFuture pins a live-feed contradiction: a
// source whose newest row was stamped AHEAD of our clock rendered as
//
//	source=crypto_perp newest row no rows old (budget 2h) — … newest row is in
//	the future; check clock skew
//
// one sentence claiming the table is both empty and holding a future row. A
// reader acts on "no rows" and goes hunting for a dead ingest that is in fact
// running — the opposite of the actual defect. Negative meant two things.
func TestAgeStringSeparatesEmptyFromFuture(t *testing.T) {
	if got := ageString(noRowsAge); got != "no rows" {
		t.Errorf("empty source rendered %q, want %q", got, "no rows")
	}
	for _, secs := range []int64{-1, -60, -3600, -86400} {
		got := ageString(secs)
		if got == "no rows" {
			t.Errorf("a row %ds ahead of the clock rendered as %q — the opposite problem", -secs, got)
		}
		if !strings.Contains(got, "ahead") {
			t.Errorf("ageString(%d) = %q, want it to say the row is ahead of our clock", secs, got)
		}
	}
	if got := ageString(90); got == "no rows" || strings.Contains(got, "ahead") {
		t.Errorf("an ordinary age rendered %q", got)
	}
}

// The sentinel must not be reachable by arithmetic: if a real age could equal
// it, a live source would be reported as empty.
func TestNoRowsSentinelIsUnreachableByRealAges(t *testing.T) {
	// A plausible age range: anything from a second to a century.
	for _, secs := range []int64{0, 1, 3600, 86400, 365 * 86400, 100 * 365 * 86400} {
		if secs == noRowsAge {
			t.Fatalf("a real age of %d collides with the no-rows sentinel", secs)
		}
		if -secs == noRowsAge && secs != 0 {
			t.Fatalf("a future age of %d collides with the no-rows sentinel", -secs)
		}
	}
}

// DetailLine is what lands in dq_events, so the contradiction must be gone at
// the point a human reads it.
func TestDetailLineNeverCallsAFutureSourceEmpty(t *testing.T) {
	line := DetailLine(Report{
		Source: "crypto_perp", AgeSecs: -7200, StaleBudgetSecs: 7200,
		Note: "Hyperliquid perp funding/OI (15m worker, crypto 24/7)",
	})
	if strings.Contains(line, "no rows") {
		t.Fatalf("detail line still calls a future-stamped source empty:\n  %s", line)
	}
	if !strings.Contains(line, "ahead of our clock") {
		t.Fatalf("detail line does not say the row is ahead of the clock:\n  %s", line)
	}

	empty := DetailLine(Report{Source: "snapshots_1s", AgeSecs: noRowsAge, StaleBudgetSecs: 600})
	if !strings.Contains(empty, "no rows") {
		t.Fatalf("a genuinely empty source no longer says so:\n  %s", empty)
	}
}
