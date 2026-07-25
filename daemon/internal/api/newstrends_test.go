// NEWS-TRENDS wave API tests: payload shape (30d series + latestZ|null with
// a stated gate reason + fleet tokens + verbatim caveat) behind the real
// middleware. Fixture data only.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

func newNewsTrendsServer(t *testing.T) (*httptest.Server, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "newstrends_api.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	d := Deps{St: st, Cfg: baseCfg(), Version: "test", Started: time.Now()}
	srv := httptest.NewUnstartedServer(nil)
	t.Cleanup(srv.Close)
	d.Cfg.AllowedHosts = []string{srv.Listener.Addr().String()}

	mux := http.NewServeMux()
	d.registerNewsTrends(mux)
	srv.Config.Handler = d.secure(mux)
	srv.Start()
	return srv, st
}

func newsTrendsGET(t *testing.T, srv *httptest.Server, path string) (int, map[string]any) {
	t.Helper()
	resp, err := srv.Client().Get(srv.URL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close() //nolint:errcheck
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return resp.StatusCode, out
}

// seedAPINews seeds n copies of headline on the UTC day daysAgo days back,
// timestamped at the END of that day (23:59:59Z, stepping backwards). End-of-day
// keeps the test deterministic at any wall-clock time: yesterday's items always
// sit inside the handler's rolling 24h fleet-token window (now-24h is yesterday
// at the current wall-clock time, which never passes 23:59:59), while day-2+
// items always sit outside it.
func seedAPINews(t *testing.T, st *store.Store, symbolID int64, daysAgo, n int, headline string) {
	t.Helper()
	now := time.Now().UTC()
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	dayEnd := midnight.AddDate(0, 0, 1-daysAgo).Add(-time.Second).Unix()
	for i := 0; i < n; i++ {
		if err := st.InsertNews(context.Background(), store.NewsItem{
			ID:       fmt.Sprintf("nta-%d-%d-%d", symbolID, daysAgo, i),
			SymbolID: symbolID, Ts: dayEnd - int64(i), Headline: headline,
			URL: "http://x", Source: "t",
		}); err != nil {
			t.Fatalf("insert news: %v", err)
		}
	}
}

func TestNewsTrendsAPI(t *testing.T) {
	srv, st := newNewsTrendsServer(t)
	ctx := context.Background()
	sym, _ := st.UpsertSymbol(ctx, "NVDA", md.Stocks, "NVIDIA")

	// Unknown symbol -> 404; missing params -> 404.
	if code, _ := newsTrendsGET(t, srv, "/api/news-trends?symbol=ZZZ&market=stocks"); code != 404 {
		t.Fatalf("unknown symbol: code %d", code)
	}

	// Thin history: latestZ must be null WITH a stated gate reason, caveat
	// verbatim, series present.
	seedAPINews(t, st, sym.ID, 0, 4, "NVDA tariffs escalate on chipmaker exports")
	seedAPINews(t, st, sym.ID, 1, 2, "NVDA tariffs weigh")
	code, out := newsTrendsGET(t, srv, "/api/news-trends?symbol=NVDA&market=stocks")
	if code != 200 {
		t.Fatalf("code %d", code)
	}
	if out["note"] != "headline-frequency trend — descriptive attention, not a forecast" {
		t.Fatalf("caveat must ship verbatim: %v", out["note"])
	}
	if out["latestZ"] != nil {
		t.Fatalf("thin baseline must serve latestZ=null, got %v", out["latestZ"])
	}
	if reason, _ := out["zGateReason"].(string); !strings.Contains(reason, "fewer than 10 prior days") {
		t.Fatalf("gate reason missing: %v", out["zGateReason"])
	}
	series := out["volumeSeries"].([]any)
	if len(series) != 2 {
		t.Fatalf("want 2 series days, got %d", len(series))
	}
	if out["todayCount"] != 4.0 {
		t.Fatalf("todayCount wrong: %v", out["todayCount"])
	}
	toks := out["fleetTokens"].([]any)
	if len(toks) == 0 {
		t.Fatalf("fleet tokens missing: %v", out)
	}
	top := toks[0].(map[string]any)
	// "tariffs" rides every seeded headline: today's 4 plus yesterday's 2,
	// both inside the rolling 24h window thanks to end-of-day seeding.
	if top["token"] != "tariffs" || top["count"].(float64) != 6 {
		t.Fatalf("top token wrong: %v", top)
	}

	// Rich history -> a real z.
	for d := 2; d <= 13; d++ {
		seedAPINews(t, st, sym.ID, d, 1+d%2, "NVDA routine coverage")
	}
	_, out = newsTrendsGET(t, srv, "/api/news-trends?symbol=NVDA&market=stocks")
	z, isNum := out["latestZ"].(float64)
	if !isNum || z <= 0 {
		t.Fatalf("expected a positive numeric z, got %v", out["latestZ"])
	}
	if _, present := out["zGateReason"]; present {
		t.Fatalf("gate reason must be absent when z is served: %v", out)
	}
}
