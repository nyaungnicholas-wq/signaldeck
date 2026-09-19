package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/config"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// An EMPTY database must not make a handler crash, and must not make it
// confident.
//
// This is the contract the whole product rests on -- every payload in this
// package is built to report an honest absence rather than a number it cannot
// support -- and the analytical read routes below had no test of any kind
// (internal/api/desk.go, quant.go, smartmoney.go, marketregimes.go, explain.go
// and most of capstones.go were at 0% coverage). Those are exactly the
// handlers where "no rows" is the normal state on a fresh deployment and where
// a naive aggregate silently divides by zero.
//
// What this asserts, for each route, against a store holding ONE tracked
// symbol and one admin user and nothing else -- no bars, no predictions, no
// scores, no filings:
//
//  1. No 5xx. An empty table is an expected state, not an internal error.
//     httpInternal exists for a broken database, not for a new one.
//  2. The body parses as JSON. A handler that dies mid-encode leaves a
//     truncated body that still carries a 200.
//  3. No fabricated confidence. A payload that has no data must not claim a
//     verdict: the honest shapes are available:false, a null, or an explicit
//     reason -- never "healthy", "PASS" or a bare 0 presented as a measurement.
//
// It deliberately does NOT assert a specific status code per route. Several of
// these legitimately answer 404 (unknown symbol) or 400 (a required parameter),
// and pinning each one here would duplicate their own tests and make this file
// fragile. The claim is narrower and more load-bearing: an empty database is
// survivable and never produces a confident lie.

// emptyDBRoutes are read routes whose handlers are reached with no rows behind
// them. Paths that need a ?symbol= carry the TRACKED one, so the request gets
// past the parameter guard and into the body that has to cope with no data --
// an unknown symbol is rejected early and proves much less.
var emptyDBRoutes = []string{
	// desk.go
	"/api/world-model",
	"/api/world-model/shocks",
	"/api/recommendation?symbol=AAPL&market=stocks",
	"/api/recommendation/top",
	// quant.go
	"/api/forecast?symbol=AAPL&market=stocks",
	"/api/correlation",
	"/api/portfolio",
	"/api/model-forecasts?symbol=AAPL&market=stocks",
	// smartmoney.go
	"/api/smart-money?symbol=AAPL&market=stocks",
	"/api/smart-money/top",
	// marketregimes.go
	"/api/market-regimes",
	// explain.go
	"/api/explain?symbol=AAPL&market=stocks",
	// capstones.go
	"/api/scenario?symbol=AAPL&market=stocks",
	"/api/portfolio/optimize?symbols=AAPL,MSFT",
	"/api/graph?symbol=AAPL&market=stocks",
	"/api/market-memory",
	"/api/company/profile?symbol=AAPL&market=stocks",
	"/api/portfolio/rebalance?symbols=AAPL,MSFT",

	// ANONYMOUS AND UNCONSUMED. These seven are on security.go's publicRoutes
	// allowlist -- reachable with no credential on a published deployment -- and
	// measured 2026-09-13 they are referenced by zero files across web/src, ops,
	// docs and tools. No page fetches them and no script calls them.
	//
	// That combination is exactly why they belong here. They are deliberately
	// part of the published record (a skeptic with curl is the reader they are
	// for), but no in-app traffic exercises them, so a handler that crashes or
	// fabricates on an empty database would never be noticed in ordinary use.
	// The rest of this file's routes at least get hit by a page.
	"/api/model-health",
	"/api/canary",
	"/api/postmortems",
	"/api/lineage",
	"/api/dataset-versions",
	"/api/evidence",
	"/api/research-loop",
}

// confidentWords are verdict-ish tokens that must never appear in a payload
// built from nothing. Checked case-insensitively against the raw body.
var confidentWords = []string{
	`"verdict":"pass"`,
	`"verdict":"healthy"`,
	`"status":"healthy"`,
	`"healthy":true`,
	`"significant":true`,
}

// confidentIfDisclosed maps a route to the disclosure its payload MUST also
// carry for a confident token to be acceptable. Both must be present; the token
// alone still fails.
//
// One entry, and it is a genuine distinction rather than an escape hatch.
// /api/research-loop reports engineHealth.healthy, which answers "is the engine
// MISBEHAVING?", not "has anything been established?". Those come apart exactly
// once: a corpus below the 2,000-observation search floor. There, declining to
// search is the refusal working -- the engine is not broken and must not be
// reported broken, or an empty deployment pages someone on day one -- and
// LoopEngineHealth says so in two other fields on the same object,
// state:"corpus-below-search-floor" and a detail naming the row count and the
// floor it sits below.
//
// The blunt substring scan cannot see that structure, so it read a correct
// payload as a fabricated one. Requiring the disclosure keeps the rule strict:
// if that state ever stops being reported, the token is no longer
// excused and this test fails.
var confidentIfDisclosed = map[string]string{
	"/api/research-loop": `"state":"corpus-below-search-floor"`,
}

func newEmptyDBServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv, st, d := newTestServer(t, func(c *config.Config) {
		// These are analytical reads, not the anonymous surface; the token
		// keeps the probe about the HANDLER rather than about auth.
		c.APIToken = emptyDBToken
	})
	ctx := context.Background()
	// The bearer token resolves through store.AdminUserID, which a fresh temp
	// database has no row for -- without this the user-scoped routes answer 401
	// and the handler under test never runs at all. newLedgerServer seeds an
	// admin for exactly this reason.
	if _, err := st.CreateUser(ctx, "admin", "hash", true); err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	// A TRACKED symbol with no bars, no predictions and no scores. This is the
	// state a real deployment is in for its first hours -- the universe seed has
	// run and nothing has been ingested yet -- and it is strictly harder than an
	// unknown symbol, which most of these handlers reject at the parameter guard
	// before reaching the code that could divide by zero.
	// Three symbols, and SPY specifically. The portfolio routes refuse below two
	// tracked names and /api/market-memory refuses without SPY as its market
	// proxy, so with fewer than these the request stops at the guard and the
	// aggregate code underneath -- the part that has to survive zero rows -- is
	// never reached.
	for _, sym := range []string{"AAPL", "MSFT", "SPY"} {
		if _, err := st.UpsertSymbol(ctx, sym, md.Stocks, sym); err != nil {
			t.Fatalf("seed symbol %s: %v", sym, err)
		}
	}
	mux := http.NewServeMux()
	d.registerDesk(mux)
	d.registerQuant(mux)
	d.registerSmartMoney(mux)
	d.registerMarketRegimes(mux)
	d.registerExplain(mux)
	d.registerCapstones(mux)
	// The anonymous-and-unconsumed seven. Four have registrars; postmortems and
	// research-loop are registered inline in Serve, so they are wired by hand
	// here -- which is itself the reason they were easy to leave untested.
	d.registerModelHealth(mux)
	d.registerHonestyGaps(mux)
	d.registerLineage(mux)
	d.registerEvidence(mux)
	mux.HandleFunc("GET /api/postmortems", d.postmortems)
	mux.HandleFunc("GET /api/research-loop", d.researchLoop)
	srv.Config.Handler = d.secure(mux)
	return srv
}

const emptyDBToken = "empty-db-probe-token"

func TestEmptyDatabase_ReadsDoNotFailOrFabricate(t *testing.T) {
	srv := newEmptyDBServer(t)
	client := newClient(t)

	for _, route := range emptyDBRoutes {
		t.Run(strings.TrimPrefix(route, "/api/"), func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, srv.URL+route, nil)
			if err != nil {
				t.Fatalf("new request: %v", err)
			}
			req.Header.Set("Authorization", "Bearer "+emptyDBToken)
			res, err := client.Do(req)
			if err != nil {
				t.Fatalf("GET %s: %v", route, err)
			}
			defer res.Body.Close() //nolint:errcheck
			body, err := io.ReadAll(res.Body)
			if err != nil {
				t.Fatalf("read body: %v", err)
			}

			if res.StatusCode >= 500 {
				t.Fatalf("GET %s = %d, want < 500 (an empty table is not an internal error): %s",
					route, res.StatusCode, truncate(string(body)))
			}

			// A 204 carries no body by definition; everything else here is JSON.
			if res.StatusCode != http.StatusNoContent && len(body) > 0 {
				var any1 any
				if err := json.Unmarshal(body, &any1); err != nil {
					t.Fatalf("GET %s returned %d with a body that is not JSON (%v): %s",
						route, res.StatusCode, err, truncate(string(body)))
				}
			}

			// Only successful payloads can fabricate; a 4xx is already saying no.
			if res.StatusCode == http.StatusOK {
				flat := strings.ToLower(strings.ReplaceAll(string(body), " ", ""))
				for _, w := range confidentWords {
					if !strings.Contains(flat, w) {
						continue
					}
					// A confident token is allowed ONLY where the same payload
					// also states, in the same breath, that it has no data. The
					// disclosure is checked too, so this can never wave through a
					// payload that simply asserts the token and stops.
					if req, ok := confidentIfDisclosed[route]; ok && strings.Contains(flat, req) {
						continue
					}
					t.Fatalf("GET %s claimed %q with an empty database: %s",
						route, w, truncate(string(body)))
				}
			}
		})
	}
}

func truncate(s string) string {
	const max = 300
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
