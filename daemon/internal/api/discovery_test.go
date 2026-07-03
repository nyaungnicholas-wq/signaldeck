// Tests for the universe-discovery endpoints (discovery wave): auth + CSRF
// enforcement, the 409-at-cap contract, and the add/dismiss status flows.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// newDiscoveryServer extends the shared test harness with the discovery routes.
func newDiscoveryServer(t *testing.T) (string, *store.Store, *http.Client) {
	t.Helper()
	srv, st, d := newTestServer(t, nil)
	// Re-wire the handler with the discovery routes added (same middleware).
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", d.health)
	d.registerAuth(mux)
	mux.HandleFunc("GET /api/watchlist", d.watchlist)
	d.registerDiscovery(mux)
	srv.Config.Handler = d.secure(mux)

	// A logged-in session for the happy paths.
	c := newClient(t)
	resp := postJSON(t, c, srv.URL+"/api/auth/register", map[string]string{"username": "dana", "password": "password123"})
	if resp.StatusCode != 200 {
		t.Fatalf("register: %d %s", resp.StatusCode, drain(t, resp))
	}
	drain(t, resp)
	return srv.URL, st, c
}

func TestCandidatesListShapeAndStatusFilter(t *testing.T) {
	url, st, c := newDiscoveryServer(t)
	ctx := context.Background()
	_ = st.UpsertCandidate(ctx, store.Candidate{Symbol: "COIN", Market: md.Stocks, LastSeenTs: 1, DollarVol: 1.2e9, PctChange: 4})
	_ = st.UpsertCandidate(ctx, store.Candidate{Symbol: "GME", Market: md.Stocks, LastSeenTs: 1, DollarVol: 3e8})
	_ = st.SetCandidateStatus(ctx, "GME", md.Stocks, "dismissed")

	resp, err := c.Get(url + "/api/candidates")
	if err != nil {
		t.Fatalf("get candidates: %v", err)
	}
	var out struct {
		Candidates []store.Candidate `json:"candidates"`
		Active     int               `json:"active"`
		Cap        int               `json:"cap"`
	}
	if err := json.Unmarshal([]byte(drain(t, resp)), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Candidates) != 1 || out.Candidates[0].Symbol != "COIN" {
		t.Fatalf("default status=new list: %+v", out.Candidates)
	}
	if out.Cap <= 0 {
		t.Fatalf("cap missing: %+v", out)
	}

	resp, _ = c.Get(url + "/api/candidates?status=dismissed")
	body := drain(t, resp)
	if !strings.Contains(body, `"GME"`) || strings.Contains(body, `"COIN"`) {
		t.Fatalf("status=dismissed list: %s", body)
	}

	resp, _ = c.Get(url + "/api/candidates?status=bogus")
	if resp.StatusCode != 400 {
		t.Fatalf("bogus status: %d want 400", resp.StatusCode)
	}
	drain(t, resp)
}

func TestCandidateAddRequiresAuthAndCSRF(t *testing.T) {
	url, _, _ := newDiscoveryServer(t)

	// Anonymous POST (with CSRF header) → 401: both mutation paths are in
	// requiresAuth even with PublicReads=true.
	for _, path := range []string{"/api/candidates/add", "/api/candidates/dismiss"} {
		resp := postJSON(t, newClient(t), url+path, map[string]string{"symbol": "COIN", "market": "stocks"})
		if resp.StatusCode != 401 {
			t.Fatalf("anon %s: %d want 401 (%s)", path, resp.StatusCode, drain(t, resp))
		}
		drain(t, resp)
	}

	// Authed but WITHOUT the CSRF header → 403 from the middleware.
	c := newClient(t)
	resp := postJSON(t, c, url+"/api/auth/register", map[string]string{"username": "erin", "password": "password123"})
	drain(t, resp)
	b, _ := json.Marshal(map[string]string{"symbol": "COIN", "market": "stocks"})
	req, _ := http.NewRequest("POST", url+"/api/candidates/add", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("no-csrf add: %v", err)
	}
	if resp.StatusCode != 403 {
		t.Fatalf("add without CSRF header: %d want 403", resp.StatusCode)
	}
	drain(t, resp)
}

func TestCandidateAddHappyPathAndDismiss(t *testing.T) {
	url, st, c := newDiscoveryServer(t)
	ctx := context.Background()
	_ = st.UpsertCandidate(ctx, store.Candidate{Symbol: "COIN", Market: md.Stocks, LastSeenTs: 1, DollarVol: 1.2e9})
	_ = st.UpsertCandidate(ctx, store.Candidate{Symbol: "GME", Market: md.Stocks, LastSeenTs: 1, DollarVol: 2e8})

	// Add: symbol becomes active, lands on THIS user's watchlist, candidate
	// flips to added.
	resp := postJSON(t, c, url+"/api/candidates/add", map[string]string{"symbol": "COIN", "market": "stocks"})
	if resp.StatusCode != 200 {
		t.Fatalf("add: %d %s", resp.StatusCode, drain(t, resp))
	}
	drain(t, resp)
	s, err := st.GetSymbol(ctx, "COIN", md.Stocks)
	if err != nil || !s.Active {
		t.Fatalf("COIN not active: %v", err)
	}
	resp, _ = c.Get(url + "/api/watchlist")
	if got := drain(t, resp); !strings.Contains(got, `"COIN"`) {
		t.Fatalf("watchlist missing COIN: %s", got)
	}
	added, _ := st.Candidates(ctx, "added")
	if len(added) != 1 || added[0].Symbol != "COIN" {
		t.Fatalf("candidate not marked added: %+v", added)
	}

	// Dismiss the other one.
	resp = postJSON(t, c, url+"/api/candidates/dismiss", map[string]string{"symbol": "GME", "market": "stocks"})
	if resp.StatusCode != 200 {
		t.Fatalf("dismiss: %d %s", resp.StatusCode, drain(t, resp))
	}
	drain(t, resp)
	dismissed, _ := st.Candidates(ctx, "dismissed")
	if len(dismissed) != 1 || dismissed[0].Symbol != "GME" {
		t.Fatalf("candidate not dismissed: %+v", dismissed)
	}

	// Bad payloads are 400s.
	resp = postJSON(t, c, url+"/api/candidates/add", map[string]string{"symbol": "", "market": "stocks"})
	if resp.StatusCode != 400 {
		t.Fatalf("empty symbol add: %d want 400", resp.StatusCode)
	}
	drain(t, resp)
}

func TestCandidateAdd409AtCap(t *testing.T) {
	url, st, c := newDiscoveryServer(t)
	ctx := context.Background()
	// Broad-universe wave: the cap now governs the STREAMED hot set, so the
	// seeded symbol must be streamed to be "at cap".
	t.Setenv("SIGNALDECK_STREAM_CAP", "1")
	spy, err := st.UpsertSymbol(ctx, "SPY", md.Stocks, "")
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := st.SetSymbolStream(ctx, spy.ID, true); err != nil {
		t.Fatalf("seed stream flag: %v", err)
	}
	_ = st.UpsertCandidate(ctx, store.Candidate{Symbol: "COIN", Market: md.Stocks, LastSeenTs: 1, DollarVol: 1e9})

	resp := postJSON(t, c, url+"/api/candidates/add", map[string]string{"symbol": "COIN", "market": "stocks"})
	if resp.StatusCode != 409 {
		t.Fatalf("add at cap: %d want 409 (%s)", resp.StatusCode, drain(t, resp))
	}
	if body := drain(t, resp); !strings.Contains(body, "stream cap reached (1/1 streamed)") {
		t.Fatalf("409 message unclear: %s", body)
	}
	// Nothing was added.
	if _, err := st.GetSymbol(ctx, "COIN", md.Stocks); err == nil {
		t.Fatalf("COIN created despite cap")
	}

	// Adding an ALREADY-STREAMED symbol is allowed at cap (watchlist-only op).
	resp = postJSON(t, c, url+"/api/candidates/add", map[string]string{"symbol": "SPY", "market": "stocks"})
	if resp.StatusCode != 200 {
		t.Fatalf("re-add streamed symbol at cap: %d %s", resp.StatusCode, drain(t, resp))
	}
	drain(t, resp)
}
