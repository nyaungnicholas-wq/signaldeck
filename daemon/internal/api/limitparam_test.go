package api

import (
	"net/http/httptest"
	"strconv"
	"testing"
)

// limitParam and limitCacheParam must agree about what every ?limit= value
// MEANS, because one decides how many rows a caller gets and the other decides
// which callers share a cached body. If they disagree, two requests with
// different effective limits collide on one cache key and the second caller is
// served the first one's rows -- a wrong answer with a 200 on it.
//
// The comment on each has always asserted they "mirror exactly"; nothing
// checked it, and they drifted the moment over-max stopped collapsing to def.

// TestLimitParam_ClampsRatherThanCollapsing pins the behaviour itself.
// ?limit=1000 against a max of 500 used to return the DEFAULT (50) -- fewer
// rows than the caller could legitimately have had, and indistinguishable from
// "there are only 50".
func TestLimitParam_ClampsRatherThanCollapsing(t *testing.T) {
	const def, max = 50, 500
	for _, tt := range []struct {
		raw  string
		want int
		why  string
	}{
		{"", def, "absent falls back to the default"},
		{"abc", def, "unparseable falls back to the default"},
		{"0", def, "zero falls back to the default"},
		{"-5", def, "negative falls back to the default"},
		{"200", 200, "in-range is honoured"},
		{"500", max, "exactly max is honoured"},
		{"1000", max, "over-max CLAMPS to max, it does not collapse to def"},
	} {
		r := httptest.NewRequest("GET", "/x?limit="+tt.raw, nil)
		if got := limitParam(r, def, max); got != tt.want {
			t.Fatalf("limit=%q: got %d, want %d (%s)", tt.raw, got, tt.want, tt.why)
		}
	}
}

// TestLimitParam_MirrorsCacheParam is the invariant that matters: for every
// input, two values that resolve to the SAME effective limit must produce the
// same cache key fragment, and two that resolve differently must not.
func TestLimitParam_MirrorsCacheParam(t *testing.T) {
	const def, max = 50, 500
	norm := limitCacheParam(def, max).norm

	// key(raw) is what the cache would file this request under; effective(raw)
	// is how many rows it would actually get.
	effective := func(raw string) int {
		return limitParam(httptest.NewRequest("GET", "/x?limit="+raw, nil), def, max)
	}

	raws := []string{"", "abc", "0", "-5", "1", "49", "50", "51", "200", "499", "500", "501", "1000", "99999"}
	byKey := map[string]int{}
	for _, raw := range raws {
		k := norm(raw)
		eff := effective(raw)
		if prev, seen := byKey[k]; seen && prev != eff {
			t.Fatalf("limit=%q shares cache key %q with an effective limit of %d, but resolves to %d "+
				"-- a cached body for one would be served to the other", raw, k, prev, eff)
		}
		byKey[k] = eff
		// A non-empty key must name the effective limit, so the key is readable
		// and cannot silently encode a different number than the one served.
		if k != "" && k != strconv.Itoa(eff) {
			t.Fatalf("limit=%q: cache key %q does not name its effective limit %d", raw, k, eff)
		}
		// The empty key is reserved for "resolves to the default".
		if k == "" && eff != def {
			t.Fatalf("limit=%q: normalised to the default entry but resolves to %d, not def=%d", raw, eff, def)
		}
	}
}
