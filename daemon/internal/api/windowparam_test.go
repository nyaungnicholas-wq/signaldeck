package api

import (
	"net/http/httptest"
	"testing"
)

// Three handlers had open-coded the same bounded-integer parse with the bug
// limitParam was already fixed for: `if n <= 0 || n > max { n = def }`. An
// over-max request collapsed to the DEFAULT, so ?days=9999 against a 365 ceiling
// returned 30 -- less than the caller could legitimately have had, and, because
// /api/scores/history writes a bare row array that echoes neither the horizon nor
// the window, indistinguishable from a symbol with only 30 days of history.
func TestWindowParam(t *testing.T) {
	const def, max = 30, 365

	for _, tc := range []struct {
		raw  string
		want int
		why  string
	}{
		{"", def, "absent expresses no bound, so the default stands"},
		{"abc", def, "unparseable expresses no bound either -- there is no nearest legal value"},
		// Percent-encoded: a literal space in the URL makes httptest.NewRequest
		// panic on the request line, which says nothing about windowParam.
		{"%20%20", def, "whitespace is not a number"},
		{"7.5", def, "a non-integer is not a window"},
		{"0", def, "zero is not a window"},
		{"-5", def, "negative is not a window"},
		{"1", 1, "the smallest legal window is honoured"},
		{"7", 7, "an in-range window is honoured exactly"},
		{"365", max, "exactly max is honoured"},
		{"366", max, "one past max clamps"},
		{"9999", max, "far over max CLAMPS to max -- it must not collapse to the default, " +
			"which would hand back less than was available and look like a short history"},
	} {
		r := httptest.NewRequest("GET", "/x?days="+tc.raw, nil)
		if got := windowParam(r, "days", def, max); got != tc.want {
			t.Fatalf("windowParam(days=%q) = %d, want %d -- %s", tc.raw, got, tc.want, tc.why)
		}
	}
}

// The key is a parameter, so it must actually be read. A helper that ignored it
// and always read one hardcoded name would pass every case above.
func TestWindowParam_ReadsTheNamedKey(t *testing.T) {
	r := httptest.NewRequest("GET", "/x?days=7&seconds=120", nil)

	if got := windowParam(r, "days", 30, 365); got != 7 {
		t.Fatalf(`windowParam(r, "days") = %d, want 7`, got)
	}
	if got := windowParam(r, "seconds", 300, 3600); got != 120 {
		t.Fatalf(`windowParam(r, "seconds") = %d, want 120`, got)
	}
	// A key the request does not carry must fall back, not borrow another's value.
	if got := windowParam(r, "limit", 50, 500); got != 50 {
		t.Fatalf(`windowParam(r, "limit") = %d, want the default 50 -- the request carries no `+
			`limit, so reading 7 or 120 here would mean the helper is reading the wrong parameter`, got)
	}
}

// limitParam is now a thin delegation, so its established contract has to keep
// holding through windowParam -- including the cache-key mirror that
// TestLimitParam_MirrorsCacheParam pins.
func TestLimitParam_StillDelegatesCorrectly(t *testing.T) {
	const def, max = 50, 500
	for raw, want := range map[string]int{
		"": def, "abc": def, "0": def, "-1": def,
		"200": 200, "500": max, "1000": max,
	} {
		r := httptest.NewRequest("GET", "/x?limit="+raw, nil)
		if got := limitParam(r, def, max); got != want {
			t.Fatalf("limitParam(limit=%q) = %d, want %d after delegating to windowParam", raw, got, want)
		}
	}
}
