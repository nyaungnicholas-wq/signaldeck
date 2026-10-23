package api

// The operator-only guard (secureWith step 6c, 23 Oct 2026): on a published
// deployment nobody but the operator receives Alpaca- or Kadoa-derived
// content. Anonymous visitors and member accounts reach the rights-cleared
// allowlist (datalicense.NonOperatorRoutes) and get the 451 contract on every
// other path, including one nobody has classified yet.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/config"
	"github.com/nyaungnicholas-wq/signaldeck/internal/datalicense"
	"github.com/nyaungnicholas-wq/signaldeck/internal/ledgeranchor"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/prereg"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// The two sentences the web tier renders, pinned word for word: the 451
// contract is shared with web/, so a reworded notice is a contract change.
const (
	wantAlpacaNotice = "Withheld: SignalDeck's prices, forecasts and statistics are derived from Alpaca " +
		"market data, which is licensed for personal use only. They are shown only to the operator " +
		"until Alpaca gives written permission."
	wantKadoaNotice = "Withheld: the congressional trading rows were compiled by Kadoa, whose terms do " +
		"not permit sharing them without written permission. Collection has stopped and the stored " +
		"rows are shown only to the operator."
)

// checkWithheld asserts one response is exactly the 451 contract for src.
func checkWithheld(t *testing.T, who, ep string, code int, ctype, body, src string) {
	t.Helper()
	if code != http.StatusUnavailableForLegalReasons {
		t.Errorf("%s %s: %d, want 451 withheld (%s): %.200s", who, ep, code, src, body)
		return
	}
	if ctype != "application/json" {
		t.Errorf("%s %s: Content-Type %q, want application/json", who, ep, ctype)
	}
	var got map[string]string
	if err := json.Unmarshal([]byte(body), &got); err != nil || len(got) != 4 {
		t.Errorf("%s %s: body is not the four-field contract: %v %s", who, ep, err, body)
		return
	}
	want := map[string]string{"error": "withheld", "reason": "licence", "source": src,
		"notice": datalicense.WithheldNotice(src)}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s %s: %s = %q, want %q", who, ep, k, got[k], v)
		}
	}
}

// startGuardOnly serves the production middleware chain in front of a stub
// that answers 200 "reached" on every path, so a scan measures exactly what
// the guards decide, with no handler side effects (a probe of /api/auth/logout
// or /api/account/delete must not end the session it is probing with).
func startGuardOnly(t *testing.T, mutate func(*config.Config)) (*httptest.Server, Deps, *http.Client, *http.Client) {
	t.Helper()
	var served Deps
	srv, st, _ := startPublished(t, mutate, true, func(d Deps) http.Handler {
		served = d
		return d.secure(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("reached"))
		}))
	})
	ctx := context.Background()
	admin, err := st.AdminUserID(ctx)
	if err != nil || admin == 0 {
		t.Fatalf("admin: %d %v", admin, err)
	}
	mid, err := st.CreateVerifiedUser(ctx, "mira", "mira@gmail.com", "x")
	if err != nil {
		t.Fatal(err)
	}
	return srv, served, sessionClient(t, st, srv, mid), sessionClient(t, st, srv, admin)
}

// sessionClient is a client carrying a fresh session for uid.
func sessionClient(t *testing.T, st *store.Store, srv *httptest.Server, uid int64) *http.Client {
	t.Helper()
	tok := fmt.Sprintf("%064x", uid)
	if err := st.CreateSession(context.Background(), tok, uid, time.Now().Add(time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	c := newClient(t)
	u, _ := url.Parse(srv.URL)
	c.Jar.SetCookies(u, []*http.Cookie{{Name: sessionCookie, Value: tok}})
	return c
}

// call sends method to path as c (POSTs carry the CSRF header and {}).
func call(t *testing.T, c *http.Client, srv *httptest.Server, method, path string) (int, string, string) {
	t.Helper()
	req, _ := http.NewRequest(method, srv.URL+path, nil)
	if method == http.MethodPost {
		req, _ = http.NewRequest(method, srv.URL+path, bytes.NewReader([]byte("{}")))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(csrfHeader, "1")
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close() //nolint:errcheck
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header.Get("Content-Type"), string(b)
}

// TestNonOperatorGuardScansEveryRoute walks EVERY registered endpoint (plus
// /mcp) on both published postures as an anonymous caller, a member and the
// operator. A non-operator must get the 451 contract on every path the earlier
// tiers let through that is not on the allowlist, never a 451 on an allowlisted
// one, and the earlier tiers' 401/403 exactly where they answered before. The
// operator must reach every path the old licence guard did not refuse.
func TestNonOperatorGuardScansEveryRoute(t *testing.T) {
	for _, posture := range []struct {
		name   string
		mutate func(*config.Config)
	}{
		{"public-surface", nil},
		// Public reads open on a tunnelled box: the earlier tiers admit almost
		// every read anonymously, so here the 451 is all that stands.
		{"tunnel-public-reads", func(c *config.Config) { c.PublicSurface, c.PublicReads = false, true }},
	} {
		t.Run(posture.name, func(t *testing.T) {
			srv, d, member, owner := startGuardOnly(t, posture.mutate)
			anon := newClient(t)
			eps := []string{"GET /mcp", "POST /mcp"}
			for ep := range registeredAPIEndpoints(t) {
				eps = append(eps, ep)
			}
			sort.Strings(eps)
			counts := map[string]int{}
			for _, ep := range eps {
				method, path, _ := strings.Cut(ep, " ")
				reqPath := strings.ReplaceAll(path, "{id}", "x")
				src, cleared := datalicense.NonOperatorSource(path)
				for _, c := range []struct {
					who    string
					client *http.Client
					tier   int // the earlier tiers' refusal, 0 = they admit this caller
				}{
					{"anonymous", anon, map[bool]int{true: http.StatusUnauthorized}[d.requiresAuth(path)]},
					{"member", member, map[bool]int{false: http.StatusForbidden}[d.memberMay(path)]},
				} {
					code, ctype, body := call(t, c.client, srv, method, reqPath)
					switch {
					case c.tier != 0:
						if code != c.tier {
							t.Errorf("%s %s: %d, want the earlier tier's %d: %.200s", c.who, ep, code, c.tier, body)
						}
						counts["tier"]++
					case cleared:
						if code != http.StatusOK || body != "reached" {
							t.Errorf("%s %s: %d, want the allowlisted route to reach its handler: %.200s", c.who, ep, code, body)
						}
						counts["cleared"]++
					default:
						checkWithheld(t, c.who, ep, code, ctype, body, src)
						counts["withheld-"+c.who]++
					}
				}
				code, _, body := call(t, owner, srv, method, reqPath)
				if _, ok, governed := datalicense.RouteRedistributable(path); governed && !ok {
					if code != http.StatusUnavailableForLegalReasons || strings.Contains(body, `"error":"withheld"`) {
						t.Errorf("operator %s: %d %.200s, want the raw-data licence refusal unchanged", ep, code, body)
					}
				} else if code != http.StatusOK || body != "reached" {
					t.Errorf("operator %s: %d, want it to reach its handler: %.200s", ep, code, body)
				}
			}
			// The scan must have exercised every branch, or it proves nothing.
			if len(eps) < 150 || counts["cleared"] < 20 || counts["withheld-anonymous"] < 10 || counts["withheld-member"] < 20 {
				t.Errorf("scan too thin to mean anything: %d endpoints, %v", len(eps), counts)
			}
			t.Logf("%s: %d endpoints, %v", posture.name, len(eps), counts)
		})
	}
}

// TestUnclassifiedRouteFailsClosed: a route nobody has put on the allowlist is
// withheld from a non-operator under Alpaca's terms, and still reaches the
// operator. Before 23 Oct 2026 an ungoverned route failed OPEN.
func TestUnclassifiedRouteFailsClosed(t *testing.T) {
	if src, cleared := datalicense.NonOperatorSource("/api/added-next-month"); cleared || src != "alpaca" {
		t.Fatalf("NonOperatorSource(unlisted) = %q, %v; want alpaca, withheld", src, cleared)
	}
	srv, _, _, owner := startGuardOnly(t, func(c *config.Config) { c.PublicSurface, c.PublicReads = false, true })
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		code, ctype, body := call(t, newClient(t), srv, method, "/api/added-next-month")
		checkWithheld(t, "anonymous", method+" /api/added-next-month", code, ctype, body, "alpaca")
		if code, _, body := call(t, owner, srv, method, "/api/added-next-month"); code != 200 || body != "reached" {
			t.Errorf("operator %s unclassified route: %d %s", method, code, body)
		}
	}
	if !strings.Contains(datalicense.WithheldNotice("alpaca"), "Alpaca") {
		t.Fatal("unreachable")
	}
}

// TestWithheldNoticesAreTheContract pins the two notices web/ renders.
func TestWithheldNoticesAreTheContract(t *testing.T) {
	if got := datalicense.WithheldNotice("alpaca"); got != wantAlpacaNotice {
		t.Errorf("alpaca notice drifted from the contract:\n got %q\nwant %q", got, wantAlpacaNotice)
	}
	if got := datalicense.WithheldNotice("kadoa"); got != wantKadoaNotice {
		t.Errorf("kadoa notice drifted from the contract:\n got %q\nwant %q", got, wantKadoaNotice)
	}
}

// TestNonOperatorRoutesAreRegistered: a stale allowlist entry is a decision
// about a route that no longer exists.
func TestNonOperatorRoutesAreRegistered(t *testing.T) {
	live := registeredAPIRoutes(t)
	for p := range datalicense.NonOperatorRoutes {
		if !live[p] {
			t.Errorf("NonOperatorRoutes names %s, which no handler registers", p)
		}
	}
}

// TestOperatorOnlyOnTheProductionMux drives the real handlers: the ledger range
// is withheld from an anonymous visitor, congress from a member (under Kadoa's
// terms), and both, with the other data routes, still answer the operator.
func TestOperatorOnlyOnTheProductionMux(t *testing.T) {
	srv, st, mb, _ := newProductionServer(t, nil, writeRegistry(t, thinWindowRegistry))
	freshHeartbeat(t, st)
	ctx := context.Background()
	if _, err := st.InsertCongressTrade(ctx, store.CongressTradeRow{ID: "op1", Chamber: "house",
		Member: "Rep. Operator Only", Symbol: "AAPL", TxType: "purchase", AmountRange: "$1,001 - $15,000",
		TxTs: time.Now().Unix() - 86400, DisclosedTs: time.Now().Unix()}); err != nil {
		t.Fatal(err)
	}
	// The poller's last stored status said ok (the Kadoa fallback worked);
	// it must not be served as a live source.
	if err := st.SetJSON(ctx, "congress_mirror_status", map[string]any{"checkedTs": 1,
		"senate": map[string]any{"ok": true}, "house": map[string]any{"ok": true}}); err != nil {
		t.Fatal(err)
	}
	acme, err := st.UpsertSymbol(ctx, "ACME", md.Stocks, "Acme Corp")
	if err != nil {
		t.Fatal(err)
	}
	day := time.Now().Unix() / 86400 * 86400
	if err := st.UpsertBars(ctx, []md.Bar{{SymbolID: acme.ID, TF: md.TF1d, Ts: day, Open: 10, High: 12, Low: 9, Close: 11, Volume: 2000}}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertCompanies(ctx, []store.CompanyRow{{CIK: 1, Ticker: "ACME", Name: "Acme Corp", Exchange: "NYSE"}}); err != nil {
		t.Fatal(err)
	}
	member := signupVerified(t, srv, mb, "nell", "nell@gmail.com")
	owner := newClient(t)
	if code, body := acctPost(t, owner, srv.URL+"/api/auth/login",
		map[string]string{"username": "owner", "password": "adminpass123"}); code != 200 {
		t.Fatalf("owner login: %d %s", code, body)
	}
	anon := newClient(t)

	code, ctype, body := call(t, anon, srv, "GET", "/api/ledger/range")
	checkWithheld(t, "anonymous", "/api/ledger/range", code, ctype, body, "alpaca")
	code, ctype, body = call(t, member, srv, "GET", "/api/congress")
	checkWithheld(t, "member", "/api/congress", code, ctype, body, "kadoa")
	for _, p := range []string{"/api/accuracy", "/api/track-record", "/api/regimes", "/api/symbol-agent?symbol=ACME&market=stocks"} {
		code, ctype, body := call(t, member, srv, "GET", p)
		checkWithheld(t, "member", p, code, ctype, body, "alpaca")
	}

	// The operator: unchanged.
	if code, _, body := call(t, owner, srv, "GET", "/api/ledger/range"); code != 200 || !strings.Contains(body, `"entries"`) {
		t.Errorf("operator ledger/range: %d %.300s", code, body)
	}
	code, _, body = call(t, owner, srv, "GET", "/api/congress")
	var cg struct {
		Count  int            `json:"count"`
		Note   string         `json:"note"`
		Source map[string]any `json:"source"`
	}
	if err := json.Unmarshal([]byte(body), &cg); code != 200 || err != nil || cg.Count != 1 {
		t.Fatalf("operator congress: %d %v %.300s", code, err, body)
	}
	if cg.Source["retired"] != true || strings.Contains(cg.Note, "ingested via the free Stock Watcher") ||
		!strings.Contains(cg.Note, "Kadoa") {
		t.Errorf("congress must say retired and credit Kadoa: note %q source %v", cg.Note, cg.Source)
	}
	for _, ch := range []string{"senate", "house"} {
		if m, _ := cg.Source[ch].(map[string]any); m == nil || m["ok"] != false {
			t.Errorf("congress source.%s = %v: a frozen ok from the retired poller is served", ch, cg.Source[ch])
		}
	}
	for _, p := range []string{"/api/watchlist", "/api/dashboard", "/api/regimes", "/api/accuracy", "/api/prereg"} {
		if code, _, body := call(t, owner, srv, "GET", p); code != 200 {
			t.Errorf("operator %s: %d %.300s", p, code, body)
		}
	}
	if code, _, body := call(t, owner, srv, "GET", "/api/companies?q=ACME"); code != 200 || !strings.Contains(body, `"price":11`) {
		t.Errorf("operator companies lost its price: %d %.300s", code, body)
	}
}

// TestNonOperatorPayloadsAreStripped: the allowlisted routes that carry a
// derived field for the operator strip it for everyone else. The prereg chain
// keeps its hashes but loses the measured numbers; the account export loses
// the journal grade; the member watchlist is identity only.
func TestNonOperatorPayloadsAreStripped(t *testing.T) {
	srv, st, mb, _ := newProductionServer(t, nil, writeRegistry(t, thinWindowRegistry))
	freshHeartbeat(t, st)
	ctx := context.Background()
	member := signupVerified(t, srv, mb, "ora", "ora@gmail.com")
	owner := newClient(t)
	if code, body := acctPost(t, owner, srv.URL+"/api/auth/login",
		map[string]string{"username": "owner", "password": "adminpass123"}); code != 200 {
		t.Fatalf("owner login: %d %s", code, body)
	}
	u, _, err := st.GetUserByName(ctx, "ora")
	if err != nil {
		t.Fatal(err)
	}
	sntj, err := st.UpsertSymbol(ctx, "SNTJ", md.Stocks, "Sentinel Journal Inc")
	if err != nil {
		t.Fatal(err)
	}
	seedResolvedJournalCall(t, st, u.ID, sntj.ID)
	// A structural claim with bands and a known weakness, and an amendment
	// whose evidence quotes a measured number: what the strip must remove.
	for _, rec := range []prereg.Record{
		{Kind: "trend21", Ts: 1784000000, SpecHash: "h-structural", Note: "initial registration",
			SpecJSON: `{"kind":"trend21","question":"Still above the 200-day mean in 21 sessions?","horizonDays":21,` +
				`"resolution":"compare the side 21 sessions later","bands":[{"minConviction":0.5,"claimedAccuracy":0.9}],` +
				`"baseline":"persistence","knownWeakness":"the top band measured a negative mean forward return"}`},
		{Kind: "data-integrity-amendment", Ts: 1784100000, SpecHash: "h-amend", Note: "AMENDMENT: 83.6% of rows",
			SpecJSON: `{"kind":"data-integrity-amendment","evidence":"83.6% agreement"}`},
	} {
		if _, err := st.AppendPrereg(ctx, rec); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.AddMemberSymbol(ctx, u.ID, sntj.ID); err != nil {
		t.Fatal(err)
	}

	// prereg: hashes kept, numbers gone. The operator's view proves the store
	// holds bands and notes to strip.
	type preregBody struct {
		Records      []map[string]any `json:"records"`
		LiveResolved any              `json:"liveResolved"`
		Withheld     string           `json:"withheld"`
	}
	read := func(c *http.Client) (preregBody, string) {
		t.Helper()
		code, _, body := call(t, c, srv, "GET", "/api/prereg")
		var pb preregBody
		if code != 200 || json.Unmarshal([]byte(body), &pb) != nil || len(pb.Records) == 0 {
			t.Fatalf("prereg: %d %.300s", code, body)
		}
		return pb, body
	}
	op, opBody := read(owner)
	if !strings.Contains(opBody, `"claimedAccuracy"`) || !strings.Contains(opBody, `"note"`) || op.Withheld != "" {
		t.Fatalf("operator prereg is not the full record, so the strip below proves nothing: %.400s", opBody)
	}
	for who, c := range map[string]*http.Client{"member": member, "anonymous": newClient(t)} {
		pb, body := read(c)
		for _, bad := range []string{`"claimedAccuracy"`, `"bands"`, `"knownWeakness"`, `"baseline"`, `"note"`, `"liveResolvedByKind":{`} {
			if strings.Contains(body, bad) {
				t.Errorf("%s prereg carries %s: %.400s", who, bad, body)
			}
		}
		if len(pb.Records) != len(op.Records) || pb.Withheld == "" || pb.LiveResolved != nil {
			t.Errorf("%s prereg: %d records (operator %d), withheld %q, liveResolved %v",
				who, len(pb.Records), len(op.Records), pb.Withheld, pb.LiveResolved)
		}
		questions := 0
		for i, r := range pb.Records {
			for _, k := range []string{"entryHash", "prevHash", "specHash", "seq"} {
				if r[k] != op.Records[i][k] {
					t.Errorf("%s prereg record %d: %s %v, operator %v", who, i, k, r[k], op.Records[i][k])
				}
			}
			if s, _ := r["spec"].(map[string]any); s != nil && s["question"] != nil {
				questions++
			}
		}
		if questions == 0 {
			t.Errorf("%s prereg kept no claim wording at all: %.400s", who, body)
		}
	}

	// account export: the grade goes, the call stays.
	code, _, body := call(t, member, srv, "GET", "/api/account/export")
	if code != 200 || !strings.Contains(body, `"member_calls":[{`) || strings.Contains(body, `"outcome"`) ||
		!strings.Contains(body, `"withheld"`) {
		t.Errorf("member export: %d %.500s", code, body)
	}
	// watchlist: identity only, no bar time.
	code, _, body = call(t, member, srv, "GET", "/api/watchlist")
	if code != 200 || !strings.Contains(body, `"symbol":"SNTJ"`) || strings.Contains(body, "latestBarTs") {
		t.Errorf("member watchlist: %d %.300s", code, body)
	}
}

// TestAnonymousReadsAreStrippedWhenReadsAreOpen: on a tunnelled box with public
// reads open, an anonymous visitor reaches the allowlisted EDGAR routes too.
// They strip for every non-operator, not only for a signed-in member: no price,
// volume or market cap on /api/companies and no LLM summary on the profile.
func TestAnonymousReadsAreStrippedWhenReadsAreOpen(t *testing.T) {
	stub := &countingLLM{}
	publishedLLM = stub
	t.Cleanup(func() { publishedLLM = nil })
	srv, st, _, _ := newProductionServer(t, func(c *config.Config) { c.PublicSurface, c.PublicReads = false, true },
		writeRegistry(t, thinWindowRegistry))
	ctx := context.Background()
	acme, err := st.UpsertSymbol(ctx, "ACME", md.Stocks, "Acme Corp")
	if err != nil {
		t.Fatal(err)
	}
	day := time.Now().Unix() / 86400 * 86400
	if err := st.UpsertBars(ctx, []md.Bar{{SymbolID: acme.ID, TF: md.TF1d, Ts: day, Open: 10, High: 12, Low: 9, Close: 11, Volume: 2000}}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertCompanies(ctx, []store.CompanyRow{{CIK: 1, Ticker: "ACME", Name: "Acme Corp", Exchange: "NYSE"}}); err != nil {
		t.Fatal(err)
	}
	anon := newClient(t)
	code, _, body := call(t, anon, srv, "GET", "/api/companies?q=ACME")
	if code != 200 || !strings.Contains(body, "Acme Corp") {
		t.Fatalf("anonymous companies: %d %.300s", code, body)
	}
	for _, k := range []string{`"price":11`, `"volume":2000`, `"barTs":`} {
		if strings.Contains(body, k) {
			t.Errorf("anonymous companies carries %s: %.400s", k, body)
		}
	}
	if code, _, body := call(t, anon, srv, "GET", "/api/company/profile?symbol=ACME&summary=1"); code != 200 || stub.calls.Load() != 0 {
		t.Errorf("anonymous profile: %d, %d LLM call(s): %.300s", code, stub.calls.Load(), body)
	}
}

// memberLogicOnly serves mux behind the identity step and the auth and member
// tiers of secureWith, WITHOUT the operator-only guard (step 6c). It exists for
// the tests of member-facing logic inside handlers that the guard now withholds
// from members (the journal, symbol-agent, the regimes member view): that
// logic is defence in depth for the day Alpaca's written permission clears a
// route, so it stays under test. TestNonOperatorGuardScansEveryRoute proves a
// member cannot reach any of it through the real chain today.
func memberLogicOnly(d Deps, mux http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uid := d.resolveUser(r)
		if uid == 0 && d.requiresAuth(r.URL.Path) {
			httpErr(w, http.StatusUnauthorized, "authentication required")
			return
		}
		if uid != 0 && d.published() && !d.memberMay(r.URL.Path) && !d.isAdminUID(r.Context(), uid) {
			httpErr(w, http.StatusForbidden, "not available to member accounts")
			return
		}
		mux.ServeHTTP(w, withUser(r, uid))
	})
}

// newMemberLogicServer is newProductionServer behind memberLogicOnly.
func newMemberLogicServer(t *testing.T, mutate func(*config.Config), registry string) (*httptest.Server, *store.Store, *mailbox, Deps) {
	t.Helper()
	t.Setenv("SIGNALDECK_ARCHIVE_DIR", filepath.Join(t.TempDir(), "archive"))
	t.Setenv(ledgeranchor.EnvKeyPath, filepath.Join(t.TempDir(), "anchor.key"))
	var served Deps
	srv, st, mb := startPublished(t, mutate, true, func(d Deps) http.Handler {
		d.Cfg.DBPath = d.St.Path()
		d.RegistryPath = registry
		served = d
		return memberLogicOnly(d, d.routes(newRateLimiter(d.Cfg.RateRPS, d.Cfg.RateBurst)))
	})
	return srv, st, mb, served
}
