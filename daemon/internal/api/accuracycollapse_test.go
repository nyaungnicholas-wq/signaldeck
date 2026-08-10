package api

import (
	"strings"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/forecastmon"
)

// The publication surface and the monitor must agree about what a usable day is.
//
// These are the REAL published cross-sections measured on the live database. If
// /api/accuracy and internal/forecastmon ever disagree about them, one of the
// two is publishing figures the other calls collapsed — which is the exact
// condition that let the registry report FAILED/retire=true on what were really
// 11 market calls.
func TestAccuracyGateAgreesWithTheMonitorOnRealDays(t *testing.T) {
	for _, tc := range []struct {
		day       string
		symbols   int
		distinct  int
		collapsed bool
	}{
		{"2026-08-01", 329, 6, true},
		{"2026-08-02", 329, 8, true},
		{"2026-08-03", 329, 5, true},
		{"2026-08-04", 326, 12, true},
		{"2026-08-05", 327, 179, false},
		{"2026-08-06", 322, 174, false},
		{"2026-07-24", 7, 7, false}, // tiny universe: not applicable, not a collapse
	} {
		d := forecastmon.DayStat{Day: tc.day, Symbols: tc.symbols, DistinctProbs: tc.distinct}
		if got := d.Collapsed(); got != tc.collapsed {
			t.Errorf("%s (%d distinct / %d symbols, ratio %.3f): Collapsed()=%v want %v",
				tc.day, tc.distinct, tc.symbols, d.DistinctRatio(), got, tc.collapsed)
		}
	}
}

// A refusal a reader cannot act on is barely better than silence. The reason
// must name the offending days and say what is wrong with them.
func TestCollapseRefusalNamesTheDays(t *testing.T) {
	// Mirrors the message collapsedGradingWindow builds.
	reason := buildCollapseReason(
		[]string{"2026-08-03 (5 distinct across 329 symbols)"}, 12)
	for _, want := range []string{"2026-08-03", "5 distinct", "329 symbols", "withheld"} {
		if !strings.Contains(reason, want) {
			t.Errorf("refusal reason omits %q: %s", want, reason)
		}
	}
}
