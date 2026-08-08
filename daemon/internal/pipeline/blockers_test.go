package pipeline

import (
	"strings"
	"testing"
)

// ONE LINE PER FLOOR, not one per shortfall.
//
// The first version keyed its tally on a formatted per-symbol string and the
// live run detail came back as a histogram:
//
//	blocked on: rows 20/40 (87), rows 14/40 (84), rows 24/40 (82),
//	            rows 30/40 (79), rows 25/40 (42), rows 31/40 (39), ...
//
// Twenty-odd buckets, all the same floor, and nothing that says how close the
// fleet is to clearing it. This pins the corrected shape against the REAL
// observed distribution: 658 symbol-horizons blocked on the row floor, the
// nearest at 34 of 40.
func TestFormatBlockersIsOneLinePerFloor(t *testing.T) {
	got := formatBlockers(map[string]*blockStat{
		"rows": {n: 658, best: 34, need: 40},
	})
	if strings.Count(got, "rows") != 1 {
		t.Errorf("the row floor appears %d times; it must appear once: %s",
			strings.Count(got, "rows"), got)
	}
	for _, want := range []string{"658", "34/40", "nearest"} {
		if !strings.Contains(got, want) {
			t.Errorf("output omits %q: %s", want, got)
		}
	}
	// One FLOOR means one entry. Counting "nearest" counts entries; counting
	// commas does not, because a single entry legitimately contains one
	// ("symbol-horizon(s), nearest 34/40").
	if n := strings.Count(got, "nearest"); n != 1 {
		t.Errorf("one floor rendered %d entries: %s", n, got)
	}
}

// Worst-first, so the floor holding the most symbols reads first.
func TestFormatBlockersOrdersWorstFirst(t *testing.T) {
	got := formatBlockers(map[string]*blockStat{
		"adaptive weights empty": {n: 12, best: 16, need: 20},
		"rows":                   {n: 658, best: 34, need: 40},
		"distinct days":          {n: 40, best: 29, need: 30},
	})
	ri := strings.Index(got, "rows")
	di := strings.Index(got, "distinct days")
	ai := strings.Index(got, "adaptive weights")
	if !(ri < di && di < ai) {
		t.Errorf("not ordered worst-first (rows=%d, days=%d, adaptive=%d): %s", ri, di, ai, got)
	}
	if n := strings.Count(got, "nearest"); n != 3 {
		t.Errorf("expected 3 floors, got %d: %s", n, got)
	}
}

// Nothing blocked must produce no clause at all, rather than "blocked on: ".
func TestFormatBlockersEmptyWhenNothingBlocks(t *testing.T) {
	if got := formatBlockers(nil); got != "" {
		t.Errorf("an empty tally rendered %q", got)
	}
}
