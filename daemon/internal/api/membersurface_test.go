package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/datalicense"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// TestMemberRoutesServeNoLicensedData: the member allowlist must never name a
// route a vendor licence governs, a MIXED route, a route with an ingestion side
// effect, or an LLM or export route. The mixed list is the 2026-09-30 audit:
// each of these is called "derived" but its payload still carries closes,
// volumes, quotes or notes that quote them. A route leaves the list only when
// its payload changes, never by editing this test.
func TestMemberRoutesServeNoLicensedData(t *testing.T) {
	neverMember := map[string]string{
		"/api/dashboard":      "tape and heatmap prices, news titles, watchlist sparks",
		"/api/screener":       "lastClose, dayChangePct and up to 30 daily closes",
		"/api/symbol":         "insight and score notes quote the close; crypto bid/ask",
		"/api/signal-report":  "tradeContext carries lastClose, 52-week range and SMA",
		"/api/explain":        "close plus entry/stop/target price levels",
		"/api/composite":      "factor evidence quotes TradingView ratings and breakout levels",
		"/api/trend":          "trendline price endpoints",
		"/api/chart-overlays": "breakout marker text quotes close and volume",
		"/api/breakouts":      "detail quotes close, N-bar high/low and volume",
		"/api/anomalies":      "detail quotes absolute volume",
		"/api/regime":         "note quotes price and SMA",
		"/api/alerts":         "breakout alerts quote close and volume",
		"/api/movers":         "price, dayChangePct and mcap",
		"/api/candidates":     "dollar volume",
		"/api/subscribe":      "starts global ingestion for a symbol not yet tracked",
		"/api/unsubscribe":    "deactivates a feed when its last watcher leaves",
	}
	live := map[string]bool{}
	for _, p := range registeredRoutes(t) {
		live[p] = true
	}
	for p := range memberRoutes {
		if src, governed := datalicense.RestrictedRoutes[p]; governed {
			t.Errorf("memberRoutes names %s, which the %s licence governs", p, src)
		}
		if why, mixed := neverMember[p]; mixed {
			t.Errorf("memberRoutes names %s: %s", p, why)
		}
		if strings.HasPrefix(p, "/api/ai/") || strings.HasPrefix(p, "/api/export/") {
			t.Errorf("memberRoutes names %s: LLM spend or raw export", p)
		}
		if !live[p] {
			t.Errorf("memberRoutes names %q, which no handler registers", p)
		}
	}
}

// TestMemberSurfaceStripsVendorFieldsAndSideEffects drives a real verified
// member against a seeded symbol that has bars, a directory row and a share
// count: everything a vendor-derived field could be built from.
func TestMemberSurfaceStripsVendorFieldsAndSideEffects(t *testing.T) {
	srv, st, mb := newPublishedServer(t)
	ctx := context.Background()
	acme, err := st.UpsertSymbol(ctx, "ACME", md.Stocks, "Acme Corp")
	if err != nil {
		t.Fatal(err)
	}
	day := time.Now().Unix() / 86400 * 86400
	if err := st.UpsertBars(ctx, []md.Bar{
		{SymbolID: acme.ID, TF: md.TF1d, Ts: day - 86400, Open: 10, High: 11, Low: 9, Close: 10, Volume: 1000},
		{SymbolID: acme.ID, TF: md.TF1d, Ts: day, Open: 10, High: 12, Low: 10, Close: 11, Volume: 2000},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertCompanies(ctx, []store.CompanyRow{{CIK: 1, Ticker: "ACME", Name: "Acme Corp", Exchange: "NYSE"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertFundamental(ctx, store.FundamentalRow{SymbolID: acme.ID, Metric: "SharesOutstanding", Value: 1e6, AsOf: day, FetchedAt: day}); err != nil {
		t.Fatal(err)
	}
	idle, err := st.UpsertSymbol(ctx, "IDLE", md.Stocks, "Idle Inc")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetSymbolActive(ctx, idle.ID, false); err != nil {
		t.Fatal(err)
	}

	member := signupVerified(t, srv, mb, "erin", "erin@gmail.com")
	owner := newClient(t)
	if code, body := acctPost(t, owner, srv.URL+"/api/auth/login",
		map[string]string{"username": "owner", "password": "adminpass123"}); code != 200 {
		t.Fatalf("owner login: %d %s", code, body)
	}
	// The web picks the member UI from this flag, so it must be the daemon's
	// verdict: true for the member, false for the operator.
	if code, body := getAs(t, member, srv.URL+"/api/auth/me"); code != 200 || !strings.Contains(body, `"member":true`) {
		t.Errorf("member /api/auth/me: %d %s", code, body)
	}
	if code, body := getAs(t, owner, srv.URL+"/api/auth/me"); code != 200 || !strings.Contains(body, `"member":false`) {
		t.Errorf("owner /api/auth/me: %d %s", code, body)
	}
	watch := func(c *http.Client, path, sym string) int {
		t.Helper()
		resp := postJSON(t, c, srv.URL+path, map[string]string{"symbol": sym, "market": "stocks"})
		drain(t, resp)
		return resp.StatusCode
	}

	// Member mutations reach only the member's own list.
	if code := watch(member, "/api/watch", "ACME"); code != 200 {
		t.Fatalf("member watch of a tracked symbol: %d", code)
	}
	if code := watch(member, "/api/watch", "IDLE"); code != 422 {
		t.Errorf("member watch of an untracked symbol: %d, want 422", code)
	}
	if code := watch(member, "/api/watch", "NOPE"); code != 404 {
		t.Errorf("member watch of an unknown symbol: %d, want 404", code)
	}
	for _, path := range []string{"/api/subscribe", "/api/unsubscribe"} {
		if code := watch(member, path, "ACME"); code != 403 {
			t.Errorf("member %s: %d, want 403 (ingestion side effects are operator-only)", path, code)
		}
	}

	// The member's watchlist: identity and freshness, no vendor numbers.
	code, body := getAs(t, member, srv.URL+"/api/watchlist")
	if code != 200 {
		t.Fatalf("member watchlist: %d %s", code, body)
	}
	var mrows []map[string]any
	if err := json.Unmarshal([]byte(body), &mrows); err != nil || len(mrows) != 1 {
		t.Fatalf("member watchlist: %v, %d rows: %s", err, len(mrows), body)
	}
	for _, k := range []string{"lastClose", "dayChangePct", "spark", "scores", "calProb1d"} {
		if _, has := mrows[0][k]; has {
			t.Errorf("member watchlist row carries %q: %s", k, body)
		}
	}
	if mrows[0]["symbol"] != "ACME" || mrows[0]["latestBarTs"] != float64(day) {
		t.Errorf("member watchlist row lost identity or freshness: %s", body)
	}

	// Unwatching as the ONLY watcher leaves the feed on.
	if code := watch(member, "/api/unwatch", "ACME"); code != 200 {
		t.Fatalf("member unwatch: %d", code)
	}
	if s, err := st.GetSymbol(ctx, "ACME", md.Stocks); err != nil || !s.Active {
		t.Errorf("a member unwatch deactivated ingestion: active=%v err=%v", s.Active, err)
	}

	// Company search: no price, volume or market cap, and a market-cap filter
	// that would exclude the row is ignored rather than used as a price oracle.
	code, body = getAs(t, member, srv.URL+"/api/companies?q=ACME&mcapMin=1e15")
	if code != 200 {
		t.Fatalf("member companies: %d %s", code, body)
	}
	var page struct {
		Companies []map[string]any `json:"companies"`
	}
	if err := json.Unmarshal([]byte(body), &page); err != nil || len(page.Companies) != 1 {
		t.Fatalf("member companies: %v, %d rows (mcap filter must be ignored): %s", err, len(page.Companies), body)
	}
	for _, k := range []string{"price", "dayChangePct", "volume", "mcap"} {
		if v := page.Companies[0][k]; v != nil {
			t.Errorf("member companies row carries %s=%v", k, v)
		}
	}

	// The operator's views are unchanged.
	if code := watch(owner, "/api/watch", "ACME"); code != 200 {
		t.Fatalf("owner watch: %d", code)
	}
	code, body = getAs(t, owner, srv.URL+"/api/watchlist")
	if code != 200 || !strings.Contains(body, `"lastClose":11`) {
		t.Errorf("owner watchlist lost its closes: %d %s", code, body)
	}
	code, body = getAs(t, owner, srv.URL+"/api/companies?q=ACME")
	if code != 200 || !strings.Contains(body, `"price":11`) {
		t.Errorf("owner companies lost its price: %d %s", code, body)
	}
}
