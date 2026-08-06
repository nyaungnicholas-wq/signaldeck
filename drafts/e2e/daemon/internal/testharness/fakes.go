// TARGET FILE : signaldeck/daemon/internal/testharness/fakes.go  (NEW FILE)
// HOW TO APPLY: copy into daemon/internal/testharness/. No existing file changes.
//
// httptest.Server stand-ins for the upstreams the daemon talks to, so no test
// can make a live network call.
//
// There are two ways a test reaches these, and both are needed:
//
//  1. IN-PROCESS. internal/ingest/alpaca.Client and internal/ingest/finra.Client
//     already expose BaseData / BasePaper / BaseURL fields for exactly this
//     (alpaca/client.go:34-38, finra/finra.go:79-89), and pipeline tests already
//     use them (internal/pipeline/shorts_test.go:86). AlpacaData() / FINRADaily()
//     etc. below hand back the right base for each.
//
//  2. OUT OF PROCESS. daemon/e2e boots the real binary, so struct fields are
//     out of reach and there is NO env override for any upstream base URL:
//     cmd/signaldeckd/run.go:1289 wires finra.New() — the production CDN — with
//     no key gate at all, and run.go:1439-1450 does the same for cftc,
//     stocktwits, wikimedia, cboe, hyperliquid and tvscanner. A per-client env
//     var for each would be six knobs to forget one of. The lazy root-cause fix
//     is one hook at the transport (SIGNALDECK_HTTP_SANDBOX, added to
//     cmd/signaldeckd/main.go by 0002-main-http-sandbox.patch): every client in
//     the tree builds &http.Client{Timeout: ...} with a nil Transport — verified
//     by grep, the only Transport: literal outside _test.go is
//     internal/ingest/edgar/bulk.go:70, which INHERITS its parent's — so
//     swapping http.DefaultTransport catches all of them in one place.
//
// The sandbox transport stamps the ORIGINAL host on each redirected request, so
// the fake can report which real hosts the daemon tried to reach. That turns
// "no live network call" from a claim into an assertion (see Origins).

package testharness

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// SandboxOriginHeader carries the host a request was ORIGINALLY addressed to,
// stamped by the SIGNALDECK_HTTP_SANDBOX transport before it rewrites the URL.
// Keep this string in sync with cmd/signaldeckd/main.go.
const SandboxOriginHeader = "X-Signaldeck-Sandbox-Origin"

// Request is one call the daemon made to an upstream.
type Request struct {
	Origin string // real host it was addressed to, "" for a direct in-process call
	Method string
	Path   string
	Query  string
}

// Upstreams is one httptest.Server standing in for every third-party host.
type Upstreams struct {
	*httptest.Server

	mu   sync.Mutex
	reqs []Request
}

// Base URLs shaped exactly as the real clients expect them.
//
//	alpaca defaultBaseData  = "https://data.alpaca.markets/v2"   → includes /v2
//	alpaca defaultBasePaper = "https://paper-api.alpaca.markets" → no /v2
//	finra  dailyBaseURL     = "https://cdn.finra.org/equity/regsho/daily"
//	finra  siBaseURL        = "https://cdn.finra.org/equity/otcmarket/biweekly"
func (u *Upstreams) AlpacaData() string  { return u.URL + "/v2" }
func (u *Upstreams) AlpacaPaper() string { return u.URL }
func (u *Upstreams) FINRADaily() string  { return u.URL + "/equity/regsho/daily" }
func (u *Upstreams) FINRAShortInt() string {
	return u.URL + "/equity/otcmarket/biweekly"
}

// Requests returns every call recorded so far.
func (u *Upstreams) Requests() []Request {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]Request(nil), u.reqs...)
}

// Origins returns the sorted set of real upstream hosts the daemon tried to
// reach. Every one of them was intercepted — that is the point of the list: a
// test can print it to show WHAT would have gone out, and assert that the set
// is exactly what it expects rather than trusting that nothing did.
func (u *Upstreams) Origins() []string {
	seen := map[string]bool{}
	for _, r := range u.Requests() {
		if r.Origin != "" {
			seen[r.Origin] = true
		}
	}
	out := make([]string, 0, len(seen))
	for h := range seen {
		out = append(out, h)
	}
	sort.Strings(out)
	return out
}

func (u *Upstreams) record(r *http.Request) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.reqs = append(u.reqs, Request{
		Origin: r.Header.Get(SandboxOriginHeader),
		Method: r.Method,
		Path:   r.URL.Path,
		Query:  r.URL.RawQuery,
	})
}

// FakeUpstreams starts the stand-in server and shuts it down on cleanup.
func FakeUpstreams(tb testing.TB) *Upstreams {
	tb.Helper()
	u := &Upstreams{}
	mux := http.NewServeMux()

	// ── FINRA: Consolidated NMS daily short sale volume ──────────────────
	// Pipe-delimited, header row must match finra.dailyHeader EXACTLY —
	// finra.ParseDaily fails loudly on any other header, and a fake that gets
	// it wrong would test the failure path forever without saying so.
	mux.HandleFunc("/equity/regsho/daily/", func(w http.ResponseWriter, r *http.Request) {
		u.record(r)
		name := strings.TrimPrefix(r.URL.Path, "/equity/regsho/daily/")
		day, ok := strings.CutPrefix(name, "CNMSshvol")
		if !ok {
			http.Error(w, "no such file", http.StatusForbidden)
			return
		}
		day, ok = strings.CutSuffix(day, ".txt")
		if !ok || len(day) != 8 {
			// FINRA's CDN answers an absent file with 403, verified live
			// 2026-07-06 (finra/finra.go:57-60) — 403 is the honest "not there".
			http.Error(w, "no such file", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = fmt.Fprint(w, FINRADailyFile(day))
	})

	// ── FINRA: bi-weekly short interest ──────────────────────────────────
	mux.HandleFunc("/equity/otcmarket/biweekly/", func(w http.ResponseWriter, r *http.Request) {
		u.record(r)
		name := strings.TrimPrefix(r.URL.Path, "/equity/otcmarket/biweekly/")
		day, ok := strings.CutPrefix(name, "shrt")
		if !ok {
			http.Error(w, "no such file", http.StatusForbidden)
			return
		}
		day, ok = strings.CutSuffix(day, ".csv")
		if !ok || len(day) != 8 {
			http.Error(w, "no such file", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = fmt.Fprint(w, FINRAShortInterestFile(day))
	})

	// ── Alpaca: asset lookup (paper host, /v2/assets/{symbol}) ───────────
	// Must come before the /v2/stocks/ routes only in readability; ServeMux
	// picks the longest matching pattern regardless.
	mux.HandleFunc("/v2/assets/", func(w http.ResponseWriter, r *http.Request) {
		u.record(r)
		sym := strings.TrimPrefix(r.URL.Path, "/v2/assets/")
		if sym == "" {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, map[string]any{
			"name":     sym + " Test Corp",
			"tradable": true,
			"status":   "active",
		})
	})

	// ── Alpaca: multi-symbol bars (/v2/stocks/bars?symbols=A,B) ──────────
	mux.HandleFunc("/v2/stocks/bars", func(w http.ResponseWriter, r *http.Request) {
		u.record(r)
		bars := map[string][]map[string]any{}
		for _, s := range strings.Split(r.URL.Query().Get("symbols"), ",") {
			if s = strings.TrimSpace(s); s != "" {
				bars[s] = FakeBars(5)
			}
		}
		writeJSON(w, map[string]any{"bars": bars, "next_page_token": nil})
	})

	// ── Alpaca: single-symbol bars (/v2/stocks/{symbol}/bars) ────────────
	mux.HandleFunc("/v2/stocks/", func(w http.ResponseWriter, r *http.Request) {
		u.record(r)
		if !strings.HasSuffix(r.URL.Path, "/bars") {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, map[string]any{"bars": FakeBars(5), "next_page_token": nil})
	})

	// ── Everything else ──────────────────────────────────────────────────
	// 503, not 200: an unmodelled upstream must make its worker degrade
	// honestly, and the body names the path so the test failure says which
	// upstream needs a stub rather than "something returned nothing".
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		u.record(r)
		origin := r.Header.Get(SandboxOriginHeader)
		if origin == "" {
			origin = "(direct)"
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = fmt.Fprintf(w, "testharness: no fake for %s %s%s (origin %s)\n",
			r.Method, r.URL.Path, queryTail(r.URL.RawQuery), origin)
	})

	u.Server = httptest.NewServer(mux)
	tb.Cleanup(u.Server.Close)
	return u
}

func queryTail(q string) string {
	if q == "" {
		return ""
	}
	return "?" + q
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// FakeBars returns n daily bars ending yesterday, in Alpaca's REST shape
// (alpaca/client.go:108-115). Prices walk upward deterministically so a test
// that asserts on a derived indicator gets the same answer every run.
func FakeBars(n int) []map[string]any {
	out := make([]map[string]any, 0, n)
	day := time.Now().UTC().AddDate(0, 0, -n).Truncate(24 * time.Hour)
	for i := 0; i < n; i++ {
		c := 100.0 + float64(i)
		out = append(out, map[string]any{
			"t": day.AddDate(0, 0, i).Format(time.RFC3339),
			"o": c - 0.5,
			"h": c + 1,
			"l": c - 1,
			"c": c,
			"v": 1_000_000 + float64(i*1000),
		})
	}
	return out
}

// FINRADailyFile renders one CNMSshvolYYYYMMDD.txt. day is YYYYMMDD.
// The header is finra.dailyHeader verbatim (finra/finra.go:54); the body mirrors
// internal/pipeline/shorts_test.go:61-65 — one commonly-tracked symbol and one
// untracked one, so universe scoping is exercised.
func FINRADailyFile(day string) string {
	return "Date|Symbol|ShortVolume|ShortExemptVolume|TotalVolume|Market\n" +
		day + "|AAPL|100.5|1|402|B,Q,N\n" +
		day + "|MSFT|250|0|900|Q\n" +
		day + "|ZZZZ|5|0|10|Q\n"
}

// FINRAShortInterestFile renders one shrtYYYYMMDD.csv. settlement is YYYYMMDD.
// The header is finra.siHeader verbatim (finra/shortint.go:41).
func FINRAShortInterestFile(settlement string) string {
	const header = "accountingYearMonthNumber|symbolCode|issueName|issuerServicesGroupExchangeCode|" +
		"marketClassCode|currentShortPositionQuantity|previousShortPositionQuantity|stockSplitFlag|" +
		"averageDailyVolumeQuantity|daysToCoverQuantity|revisionFlag|changePercent|changePreviousNumber|settlementDate"
	iso := settlement
	if len(settlement) == 8 {
		iso = settlement[0:4] + "-" + settlement[4:6] + "-" + settlement[6:8]
	}
	ym := strings.ReplaceAll(iso[:7], "-", "")
	return header + "\n" +
		ym + "|AAPL|APPLE INC|N|NNM|1000000|900000||5000000|0.20||11.11|100000|" + iso + "\n" +
		ym + "|MSFT|MICROSOFT CORP|N|NNM|500000|550000||4000000|0.125||-9.09|-50000|" + iso + "\n"
}
