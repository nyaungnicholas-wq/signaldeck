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

// ── C6 residual: /api/calibration and /api/honesty ──────────────────────────
//
// The first C6 pass whitelisted /api/composite/top and /api/movers and stopped
// there. /api/calibration kept keying on r.URL.RawQuery, and /api/honesty kept
// keying on the raw VALUE of ?horizon — a whitelisted NAME with an unvalidated
// value, which mints keys just as freely.

func calibrationKey(target string) string {
	return calibrationCacheKey(httptest.NewRequest("GET", target, nil))
}

func honestyKey(target string) string {
	return honestyCacheKey(httptest.NewRequest("GET", target, nil))
}

func TestCalibrationCacheKey_JunkResolvesToTheDefaultEntry(t *testing.T) {
	base := calibrationKey("/api/calibration")
	if base != "" {
		t.Fatalf("default calibration key = %q, want %q (warm.go:123 warms \"\")", base, "")
	}
	// predict.go: h != 1d && h != 1w → 1d. Every one of these renders the 1d
	// body, so every one must share the 1d entry.
	for _, q := range []string{
		"/api/calibration?horizon=aaa1",
		"/api/calibration?horizon=aaa2",
		"/api/calibration?horizon=1d", // the default, spelled out — what the web client sends
		"/api/calibration?horizon=1h", // NOT a calibration horizon; resolves to 1d
		"/api/calibration?horizon=",
		"/api/calibration?zz=1",
		"/api/calibration?a=1&b=2&c=3&d=4&e=5",
	} {
		if got := calibrationKey(q); got != base {
			t.Fatalf("%s minted key %q; the handler resolves it to 1d, which is entry %q", q, got, base)
		}
	}
}

func TestCalibrationCacheKey_RealHorizonStaysDistinct(t *testing.T) {
	// The other half: 1w renders a different payload and must not collapse onto
	// the 1d entry, or a 1w caller is served the 1d reliability curve.
	if got := calibrationKey("/api/calibration?horizon=1w"); got == "" {
		t.Fatal("horizon=1w must key distinctly from the 1d default — it is a different payload")
	}
}

func TestHonestyCacheKey_JunkResolvesToTheDefaultEntry(t *testing.T) {
	base := honestyKey("/api/honesty")
	if base != "" {
		t.Fatalf("default honesty key = %q, want %q (warm.go:122 warms \"\")", base, "")
	}
	// api.go: h != 1h && h != 1d && h != 1w → 1d.
	//
	// ?horizon=1d is in this list ON PURPOSE. warm.go warms this route with a
	// query-less request, so the "" entry already holds the 1d body, and the web
	// client always sends ?horizon=1d (web/src/lib/api.ts:473). Giving 1d its own
	// key would strand the warmed entry and make the client's request the cold
	// build the warmer was added to absorb.
	for _, q := range []string{
		"/api/honesty?horizon=aaa1",
		"/api/honesty?horizon=aaa2",
		"/api/honesty?horizon=1d",
		"/api/honesty?horizon=1D", // handler compares case-sensitively → 1d
		"/api/honesty?horizon=",
		"/api/honesty?zz=1",
	} {
		if got := honestyKey(q); got != base {
			t.Fatalf("%s minted key %q; the handler resolves it to 1d, which is entry %q", q, got, base)
		}
	}
}

func TestHonestyCacheKey_RealHorizonsStayDistinct(t *testing.T) {
	seen := map[string]string{}
	for _, q := range []string{
		"/api/honesty",
		"/api/honesty?horizon=1h",
		"/api/honesty?horizon=1w",
	} {
		k := honestyKey(q)
		if prev, dup := seen[k]; dup {
			t.Fatalf("%s and %s collapsed onto key %q but resolve different horizons", prev, q, k)
		}
		seen[k] = q
	}
}
