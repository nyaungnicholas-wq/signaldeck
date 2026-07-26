// Cache-key construction for the SWR response caches.
//
// C6 (2026-07-26 hostile review): /api/composite/top, /api/movers and
// /api/calibration keyed their cache entry on `r.URL.RawQuery` — an
// attacker-controlled string that no code had ever validated. Any query the
// process had not seen before was therefore a COLD miss, and a cold miss
// builds INLINE, holding one of the store's four read connections
// (store.go:53) for the 22-45s the build takes. Measured on the live daemon:
//
//	/api/composite/top        200 X-Cache:hit   0.13s
//	/api/composite/top?zz=1   TIMEOUT          60.0s
//	/api/composite/top?zz=2   200 (cold)       28.5s
//
// Five junk parameters took the whole read pool and the daemon stopped
// answering, /api/health included. No authentication, no request body, and
// reachable by anyone who can reach the tunnel.
//
// The rule this file enforces: a cache key may only ever be built from
// parameters the handler actually READS, each normalised to the value the
// handler will RESOLVE it to. Two consequences, and both are load-bearing:
//
//   - An unknown parameter is dropped, so `?zz=1` cannot mint a key. This is
//     what closes the attack — the key space stops being "every string".
//   - A known parameter spelled differently (`?limit=0`, `?limit=abc`,
//     `?horizon=nonsense`) normalises onto the entry it would have rendered
//     anyway, so junk that LOOKS legitimate is also not a new build.
//
// A parameter that resolves to the handler's default is omitted from the key
// entirely. That keeps the default key `""` — the exact key warm.go pre-builds
// — so the warmer still fills the entry a first visitor reads.
//
// The normalisers must track their handler exactly. Where a key says
// `limit=100` the handler must be about to render 100 rows; if the two drift,
// the cache serves one request's body to another request, which is a worse bug
// than the one this file fixes. That is why the limits below are shared
// constants rather than repeated literals.
package api

import (
	"net/http"
	"strconv"
	"strings"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// cacheParam is one whitelisted query parameter. norm returns the canonical
// value to key on, or "" when the raw input resolves to the handler's default
// (in which case the parameter is left out of the key altogether).
type cacheParam struct {
	name string
	norm func(raw string) string
}

// canonicalCacheKey renders the whitelisted parameters of r as a stable key.
// Parameters are emitted in declaration order, never in the order the client
// sent them, so `?a=1&b=2` and `?b=2&a=1` share one entry. Anything not in
// params is ignored — that is the whole point.
func canonicalCacheKey(r *http.Request, params ...cacheParam) string {
	q := r.URL.Query()
	var b strings.Builder
	for _, p := range params {
		// Query().Get takes the FIRST value for a repeated parameter, which is
		// what every handler here does too.
		v := p.norm(q.Get(p.name))
		if v == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('&')
		}
		b.WriteString(p.name)
		b.WriteByte('=')
		b.WriteString(v)
	}
	return b.String()
}

// enumParam keys on raw only when it is one of allowed, byte for byte. The
// comparison is deliberately case-sensitive: the handlers compare against
// md.Crypto/md.Stocks the same way, so "CRYPTO" really does fall through to
// their default and must key as the default.
func enumParam(name string, allowed ...string) cacheParam {
	return cacheParam{name: name, norm: func(raw string) string {
		for _, a := range allowed {
			if raw == a {
				return raw
			}
		}
		return ""
	}}
}

// limitCacheParam mirrors limitParam(r, def, max) exactly — n <= 0, n > max
// and an unparseable value all fall back to def, and def itself is omitted so
// `?limit=<default>` shares the default entry.
func limitCacheParam(def, max int) cacheParam {
	return cacheParam{name: "limit", norm: func(raw string) string {
		n, _ := strconv.Atoi(raw)
		if n <= 0 || n > max || n == def {
			return ""
		}
		return strconv.Itoa(n)
	}}
}

// floorParam mirrors the "positive threshold or no filter at all" reading the
// movers handler applies to ?minMcap: ParseFloat's error value is 0, and 0,
// negatives and NaN all mean no filter. Distinct spellings of the same number
// ("1e9", "1000000000", " 1e9 ") canonicalise to one key because they render
// one body.
//
// Note what this does NOT bound: a float parameter has an effectively
// unlimited value space, so a determined caller can still ask for many
// legitimate keys. That residue is what the cold-build ceiling and the bounded
// entry map in slowcache.go exist to absorb; the whitelist alone is not the
// whole fix.
func floorParam(name string) cacheParam {
	return cacheParam{name: name, norm: func(raw string) string {
		f, _ := strconv.ParseFloat(strings.TrimSpace(raw), 64)
		if !(f > 0) { // false for <= 0 AND for NaN
			return ""
		}
		return strconv.FormatFloat(f, 'g', -1, 64)
	}}
}

// compositeTopCacheKey whitelists the three parameters compositeTop reads:
// ?market (crypto/stocks, anything else is the full universe), ?horizon (1w,
// anything else is 1d per compositeHorizon) and ?limit.
func compositeTopCacheKey(r *http.Request) string {
	return canonicalCacheKey(r,
		enumParam("market", string(md.Crypto), string(md.Stocks)),
		enumParam("horizon", string(md.H1w)),
		limitCacheParam(compositeTopDefaultLimit, compositeTopMaxLimit),
	)
}

// moversCacheKey whitelists the two parameters the movers handler reads:
// ?limit and the ?minMcap floor.
func moversCacheKey(r *http.Request) string {
	return canonicalCacheKey(r,
		limitCacheParam(moversDefaultLimit, moversMaxLimit),
		floorParam("minMcap"),
	)
}
