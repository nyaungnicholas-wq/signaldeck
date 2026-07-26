package api

import (
	"net/http/httptest"
	"testing"
)

// C6 regression suite. The attack these pin: /api/composite/top?zz=1 was a
// cache key no cache had ever seen, so it MISSED, built inline, and held one
// of the store's four read connections for 22-45s. Five junk parameters
// exhausted the pool and stalled the daemon including /api/health.
//
// The invariant every test below encodes: a parameter the handler does not
// read cannot change the cache key, and a parameter it does read is keyed on
// the value the handler will actually RESOLVE it to — never on how it was
// spelled. If either half breaks, the DoS is back.

func compositeKey(target string) string {
	return compositeTopCacheKey(httptest.NewRequest("GET", target, nil))
}

func moversKey(target string) string {
	return moversCacheKey(httptest.NewRequest("GET", target, nil))
}

func TestCompositeTopCacheKey_UnknownParamCannotMintAKey(t *testing.T) {
	base := compositeKey("/api/composite/top")
	// The exact strings from the hostile review, plus the general shape of the
	// attack: unlimited distinct parameter NAMES, each previously a cold build.
	for _, q := range []string{
		"/api/composite/top?zz=1",
		"/api/composite/top?zz=2",
		"/api/composite/top?a=1&b=2&c=3&d=4&e=5",
		"/api/composite/top?limit=", // present but empty → handler default
		"/api/composite/top?utm_source=twitter",
	} {
		if got := compositeKey(q); got != base {
			t.Fatalf("%s minted a new cache key %q (default is %q); every novel key is a cold build on a read connection", q, got, base)
		}
	}
}

func TestCompositeTopCacheKey_NormalisesToTheHandlersResolvedValue(t *testing.T) {
	base := compositeKey("/api/composite/top")
	// compositeTop resolves each of these to its default, so they must all
	// SHARE the default entry rather than each paying for its own build.
	// limitParam: n<=0 or n>max falls back to def. compositeHorizon: anything
	// but "1w" is 1d. market: anything but crypto/stocks is the whole universe.
	for _, q := range []string{
		"/api/composite/top?limit=0",
		"/api/composite/top?limit=-5",
		"/api/composite/top?limit=abc",
		"/api/composite/top?limit=99999",
		"/api/composite/top?limit=50", // the default, spelled out
		"/api/composite/top?horizon=nonsense",
		"/api/composite/top?horizon=1d", // the default, spelled out
		"/api/composite/top?market=bogus",
		"/api/composite/top?market=CRYPTO", // handler compares case-sensitively
	} {
		if got := compositeKey(q); got != base {
			t.Fatalf("%s keyed as %q but the handler resolves it to the default (%q)", q, got, base)
		}
	}
}

func TestCompositeTopCacheKey_RealParamsStayDistinct(t *testing.T) {
	// The other half of the contract: collapsing keys that DO produce
	// different payloads would serve a stocks body to a crypto request.
	seen := map[string]string{}
	for _, q := range []string{
		"/api/composite/top",
		"/api/composite/top?market=crypto",
		"/api/composite/top?market=stocks",
		"/api/composite/top?horizon=1w",
		"/api/composite/top?limit=100",
		"/api/composite/top?market=crypto&horizon=1w&limit=100",
	} {
		k := compositeKey(q)
		if prev, dup := seen[k]; dup {
			t.Fatalf("%s and %s collapsed onto the same key %q but render different payloads", prev, q, k)
		}
		seen[k] = q
	}
}

func TestCompositeTopCacheKey_OrderAndJunkAreIrrelevant(t *testing.T) {
	want := compositeKey("/api/composite/top?market=crypto&horizon=1w&limit=100")
	for _, q := range []string{
		"/api/composite/top?horizon=1w&limit=100&market=crypto",
		"/api/composite/top?limit=100&zz=9&market=crypto&horizon=1w",
		"/api/composite/top?market=crypto&market=stocks&horizon=1w&limit=100", // Query().Get takes the first
	} {
		if got := compositeKey(q); got != want {
			t.Fatalf("%s keyed as %q, want %q — reordering or padding a query must not fork the entry", q, got, want)
		}
	}
}

func TestMoversCacheKey_UnknownParamCannotMintAKey(t *testing.T) {
	base := moversKey("/api/movers")
	if base != "" {
		t.Fatalf("the default /api/movers key must stay %q — warm.go pre-builds that exact entry, got %q", "", base)
	}
	for _, q := range []string{
		"/api/movers?zz=1",
		"/api/movers?minMcap=", // empty → ParseFloat 0 → no filter
		"/api/movers?minMcap=-1",
		"/api/movers?minMcap=NaN", // handler treats NaN as no filter
		"/api/movers?limit=0",
		"/api/movers?limit=10", // the default, spelled out
		"/api/movers?limit=999",
	} {
		if got := moversKey(q); got != base {
			t.Fatalf("%s minted key %q; the handler resolves it to the default", q, got)
		}
	}
}

func TestMoversCacheKey_EquivalentSpellingsShareAnEntry(t *testing.T) {
	// 1e9 and 1000000000 are the same float and produce byte-identical bodies,
	// so they must not each pay for a build.
	a := moversKey("/api/movers?minMcap=1e9")
	b := moversKey("/api/movers?minMcap=1000000000")
	c := moversKey("/api/movers?minMcap=%201e9%20") // handler TrimSpaces first
	if a == "" || a != b || a != c {
		t.Fatalf("equivalent minMcap spellings must share one entry: %q %q %q", a, b, c)
	}
	if moversKey("/api/movers?minMcap=2e9") == a {
		t.Fatal("different minMcap values must not share an entry — the body encodes the filter")
	}
}

func TestCompositeTopCacheKey_DefaultIsTheWarmerKey(t *testing.T) {
	// warm.go pre-builds these caches under the key "". If the default query
	// ever normalised to something else the warmer would fill an entry nobody
	// reads and the first visitor would eat the 40s cold build anyway.
	if got := compositeKey("/api/composite/top"); got != "" {
		t.Fatalf("default composite key = %q, want %q (warm.go:121 warms \"\")", got, "")
	}
}
