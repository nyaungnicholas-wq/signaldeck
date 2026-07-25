// Visual-kit Stage 3: GET /api/dashboard tests — payload assembly (heatmap
// mcap honesty, gauge gate captions, merged kind-tagged feed), the per-user
// watchlist section (present ONLY with a session; ONE batched sparkline
// query is covered store-side by TestLastNDailyCloses), and the 60s cache
// contract (fresh hits are served from memory; expiry rebuilds).
// Separate harness file so parallel edits never collide. t.TempDir store only.
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/config"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// newDashServer wires /api/dashboard behind the real middleware with an
// injected cache so tests can drive TTL expiry deterministically.
func newDashServer(t *testing.T) (*httptest.Server, *store.Store, *dashCache) {
	t.Helper()
	srv, st, d := newTestServer(t, func(c *config.Config) {
		c.PublicReads = true
		c.CryptoSymbol = "BTC/USD"
	})
	c := newDashCache(dashboardTTL)
	mux := http.NewServeMux()
	d.registerDashboardCache(mux, c)
	srv.Config.Handler = d.secure(mux)
	return srv, st, c
}

type dashResp struct {
	AsOf      int64 `json:"asOf"`
	CacheTtlS int   `json:"cacheTtlS"`
	Tape      struct {
		Items []struct {
			Symbol string `json:"symbol"`
		} `json:"items"`
		Note string `json:"note"`
	} `json:"tape"`
	Heatmap struct {
		Items []struct {
			Symbol    string   `json:"symbol"`
			ChangePct float64  `json:"changePct"`
			Mcap      *float64 `json:"mcap"`
		} `json:"items"`
		N           int    `json:"n"`
		McapCovered int    `json:"mcapCovered"`
		SizeNote    string `json:"sizeNote"`
	} `json:"heatmap"`
	Gauges struct {
		Breadth struct {
			Pct       float64 `json:"pct"`
			Advancers int     `json:"advancers"`
			Decliners int     `json:"decliners"`
			N         int     `json:"n"`
			Caption   string  `json:"caption"`
		} `json:"breadth"`
		Vix struct {
			Level   float64 `json:"level"`
			Regime  string  `json:"regime"`
			HasData bool    `json:"hasData"`
			Caption string  `json:"caption"`
		} `json:"vix"`
		Anomalies struct {
			Count   int    `json:"count"`
			Caption string `json:"caption"`
		} `json:"anomalies"`
		Confidence struct {
			Avg       float64 `json:"avg"`
			N         int     `json:"n"`
			ResolvedN int     `json:"resolvedN"`
			Gated     bool    `json:"gated"`
			Caption   string  `json:"caption"`
		} `json:"confidence"`
	} `json:"gauges"`
	Movers struct {
		Gainers []struct {
			Symbol    string  `json:"symbol"`
			ChangePct float64 `json:"changePct"`
		} `json:"gainers"`
		Losers []struct {
			Symbol string `json:"symbol"`
		} `json:"losers"`
	} `json:"movers"`
	Feed struct {
		Items []struct {
			Kind   string  `json:"kind"`
			Ts     int64   `json:"ts"`
			Symbol string  `json:"symbol"`
			Title  string  `json:"title"`
			Z      float64 `json:"z"`
		} `json:"items"`
		Count int `json:"count"`
	} `json:"feed"`
	Watchlist *struct {
		Sparks []struct {
			Symbol       string    `json:"symbol"`
			Closes       []float64 `json:"closes"`
			LastClose    float64   `json:"lastClose"`
			DayChangePct float64   `json:"dayChangePct"`
		} `json:"sparks"`
		UnseenAlerts int `json:"unseenAlerts"`
		SparkPoints  int `json:"sparkPoints"`
	} `json:"watchlist"`
}

func TestDashboardEndpoint(t *testing.T) {
	srv, st, _ := newDashServer(t)
	ctx := context.Background()
	now := time.Now().Unix()

	// Universe: AAA +5% with EDGAR shares (mcap known), BBB -3% (mcap null),
	// SPY tape ETF (+20% but a basket — excluded from heatmap/movers/breadth).
	aaa, _ := st.UpsertSymbol(ctx, "AAA", md.Stocks, "Alpha Corp")
	seedDaily(t, st, aaa.ID, 100, 105, now)
	bbb, _ := st.UpsertSymbol(ctx, "BBB", md.Stocks, "Beta Inc")
	seedDaily(t, st, bbb.ID, 100, 97, now)
	spy, _ := st.UpsertSymbol(ctx, "SPY", md.Stocks, "")
	seedDaily(t, st, spy.ID, 100, 120, now)
	_ = st.UpsertFundamental(ctx, store.FundamentalRow{
		SymbolID: aaa.ID, Metric: "SharesOutstanding", Value: 1e9, AsOf: now - 86400, FetchedAt: now,
	})

	// Gauges: VIX 18.5 (band "normal"); one anomaly in window, one out; one
	// 1d prediction at 0.8 (conf 0.6) with ZERO resolutions → gated.
	_ = st.InsertMacro(ctx, "VIXCLS", now-2*86400, 17)
	_ = st.InsertMacro(ctx, "VIXCLS", now-86400, 18.5)
	_, _ = st.InsertAnomaly(ctx, store.AnomalyRow{SymbolID: aaa.ID, Ts: now - 3600, Kind: "anomaly_vol", Z: 3.5, Detail: "vol z=3.5 vs 30d baseline"})
	_, _ = st.InsertAnomaly(ctx, store.AnomalyRow{SymbolID: aaa.ID, Ts: now - 3*86400, Kind: "anomaly_vol", Z: 3.1, Detail: "old"})
	_ = st.UpsertPrediction(ctx, store.Prediction{SymbolID: aaa.ID, Horizon: md.H1d, Ts: now - 60, RawProb: 0.8, CalProb: 0.8})

	// Feed sources: news + filing + briefing (anomaly seeded above).
	_ = st.InsertNews(ctx, store.NewsItem{ID: "n1", SymbolID: aaa.ID, Ts: now - 100, Headline: "Alpha beats", URL: "http://x", Source: "test"})
	_, _ = st.InsertFiling(ctx, store.FilingRow{ID: "f1", SymbolID: bbb.ID, Form: "8-K", FiledTs: now - 200, Title: "8-K", URL: "http://sec", Label: "8-K — earnings release (Item 2.02)"})
	_ = st.InsertInsight(ctx, md.Insight{Scope: "market", Ts: now - 300, Headline: "Daily briefing", Body: "quiet tape", Data: `{"kind":"daily_briefing"}`})

	var body dashResp
	if code := s8Get(t, srv.URL+"/api/dashboard", &body); code != 200 {
		t.Fatalf("status = %d", code)
	}

	// Tape present with its not-live note.
	if len(body.Tape.Items) == 0 || !strings.Contains(body.Tape.Note, "NOT live quotes") {
		t.Errorf("tape = %d items, note %q", len(body.Tape.Items), body.Tape.Note)
	}

	// Heatmap: AAA + BBB only (SPY excluded), sorted by symbol, mcap honesty.
	if body.Heatmap.N != 2 || len(body.Heatmap.Items) != 2 {
		t.Fatalf("heatmap n = %d items = %d, want 2", body.Heatmap.N, len(body.Heatmap.Items))
	}
	if body.Heatmap.Items[0].Symbol != "AAA" || body.Heatmap.Items[1].Symbol != "BBB" {
		t.Errorf("heatmap order = %+v", body.Heatmap.Items)
	}
	if body.Heatmap.Items[0].Mcap == nil || *body.Heatmap.Items[0].Mcap != 1e9*105 {
		t.Errorf("AAA mcap = %v, want 105e9", body.Heatmap.Items[0].Mcap)
	}
	if body.Heatmap.Items[1].Mcap != nil {
		t.Errorf("BBB mcap must be null (EDGAR gap): %v", *body.Heatmap.Items[1].Mcap)
	}
	if body.Heatmap.McapCovered != 1 || !strings.Contains(body.Heatmap.SizeNote, "mcap unavailable") {
		t.Errorf("mcapCovered = %d, sizeNote = %q", body.Heatmap.McapCovered, body.Heatmap.SizeNote)
	}

	// Breadth: 1 up, 1 down over 2 symbols → 50%, caption names the n.
	br := body.Gauges.Breadth
	if br.N != 2 || br.Advancers != 1 || br.Decliners != 1 || br.Pct != 50 {
		t.Errorf("breadth = %+v", br)
	}
	if !strings.Contains(br.Caption, "breadth over 2 symbols") {
		t.Errorf("breadth caption = %q", br.Caption)
	}

	// VIX: 18.5 → "normal" band, FRED-lag caption.
	vx := body.Gauges.Vix
	if !vx.HasData || vx.Level != 18.5 || vx.Regime != "normal" || !strings.Contains(vx.Caption, "FRED") {
		t.Errorf("vix = %+v", vx)
	}

	// Anomalies: only the in-window row counts; caption stays descriptive.
	an := body.Gauges.Anomalies
	if an.Count != 1 || !strings.Contains(an.Caption, "NOT predictions") {
		t.Errorf("anomalies = %+v", an)
	}

	// Confidence: avg 0.6 over 1 symbol, GATED at n=0/30 with the gate caption.
	cf := body.Gauges.Confidence
	if cf.N != 1 || cf.Avg < 0.599 || cf.Avg > 0.601 {
		t.Errorf("confidence = %+v", cf)
	}
	if !cf.Gated || cf.ResolvedN != 0 || !strings.Contains(cf.Caption, "n=0/30") {
		t.Errorf("confidence gate = %+v (caption %q)", cf, cf.Caption)
	}

	// Movers: AAA best, BBB worst; SPY never appears.
	if len(body.Movers.Gainers) == 0 || body.Movers.Gainers[0].Symbol != "AAA" {
		t.Errorf("gainers = %+v", body.Movers.Gainers)
	}
	if len(body.Movers.Losers) == 0 || body.Movers.Losers[0].Symbol != "BBB" {
		t.Errorf("losers = %+v", body.Movers.Losers)
	}

	// Feed: all four kinds merged, newest first, capped count.
	kinds := map[string]bool{}
	for i, it := range body.Feed.Items {
		kinds[it.Kind] = true
		if i > 0 && body.Feed.Items[i-1].Ts < it.Ts {
			t.Errorf("feed not newest-first at %d", i)
		}
	}
	for _, k := range []string{"news", "filing", "anomaly", "briefing"} {
		if !kinds[k] {
			t.Errorf("feed missing kind %q (have %v)", k, kinds)
		}
	}
	if body.Feed.Count > dashFeedLimit {
		t.Errorf("feed count = %d > %d", body.Feed.Count, dashFeedLimit)
	}

	// Anonymous ⇒ NO per-user section.
	if body.Watchlist != nil {
		t.Errorf("watchlist must be null without a session")
	}
}

func TestDashboardWatchlistSection(t *testing.T) {
	srv, st, _ := newDashServer(t)
	ctx := context.Background()
	now := time.Now().Unix()

	// 70 daily bars on AAA → the spark must cap at dashSparkPoints (60).
	aaa, _ := st.UpsertSymbol(ctx, "AAA", md.Stocks, "Alpha Corp")
	var bars []md.Bar
	for i := 0; i < 70; i++ {
		ts := now - int64(69-i)*86400
		bars = append(bars, md.Bar{SymbolID: aaa.ID, TF: md.TF1d, Ts: ts, Close: float64(100 + i)})
	}
	if err := st.UpsertBars(ctx, bars); err != nil {
		t.Fatalf("seed bars: %v", err)
	}

	uid, err := st.CreateUser(ctx, "wluser", "hash", false)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := st.AddUserSymbol(ctx, uid, aaa.ID); err != nil {
		t.Fatalf("watch: %v", err)
	}
	if err := st.CreateSession(ctx, "dash-session-token", uid, now+3600); err != nil {
		t.Fatalf("session: %v", err)
	}
	if err := st.InsertAlert(ctx, store.Alert{UserID: uid, Kind: "breakout", Detail: "x", Ts: now}); err != nil {
		t.Fatalf("alert: %v", err)
	}

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/dashboard", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "dash-session-token"})
	res, err := newClient(t).Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer res.Body.Close() //nolint:errcheck
	if res.StatusCode != 200 {
		t.Fatalf("status = %d", res.StatusCode)
	}
	var body dashResp
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if body.Watchlist == nil {
		t.Fatal("watchlist section missing with a valid session")
	}
	if body.Watchlist.UnseenAlerts != 1 {
		t.Errorf("unseenAlerts = %d, want 1", body.Watchlist.UnseenAlerts)
	}
	if body.Watchlist.SparkPoints != dashSparkPoints {
		t.Errorf("sparkPoints = %d, want %d", body.Watchlist.SparkPoints, dashSparkPoints)
	}
	if len(body.Watchlist.Sparks) != 1 {
		t.Fatalf("sparks = %+v, want 1 row", body.Watchlist.Sparks)
	}
	sp := body.Watchlist.Sparks[0]
	if sp.Symbol != "AAA" || len(sp.Closes) != dashSparkPoints {
		t.Fatalf("spark = %s with %d closes, want AAA with %d", sp.Symbol, len(sp.Closes), dashSparkPoints)
	}
	// Chronological: newest close (169) last; window starts at 100+10=110.
	if sp.Closes[0] != 110 || sp.Closes[len(sp.Closes)-1] != 169 || sp.LastClose != 169 {
		t.Errorf("spark closes window = [%v … %v] last %v", sp.Closes[0], sp.Closes[len(sp.Closes)-1], sp.LastClose)
	}
}

func TestDashboardCache(t *testing.T) {
	srv, st, c := newDashServer(t)
	ctx := context.Background()
	now := time.Now().Unix()

	aaa, _ := st.UpsertSymbol(ctx, "AAA", md.Stocks, "Alpha Corp")
	seedDaily(t, st, aaa.ID, 100, 105, now)

	var first dashResp
	if code := s8Get(t, srv.URL+"/api/dashboard", &first); code != 200 {
		t.Fatalf("status = %d", code)
	}
	if first.Heatmap.N != 1 {
		t.Fatalf("initial heatmap n = %d, want 1", first.Heatmap.N)
	}

	// New symbol lands in the store — but the cache is fresh, so the payload
	// must NOT change yet (that's the 60s contract the payload advertises).
	bbb, _ := st.UpsertSymbol(ctx, "BBB", md.Stocks, "Beta Inc")
	seedDaily(t, st, bbb.ID, 100, 97, now)

	var second dashResp
	if code := s8Get(t, srv.URL+"/api/dashboard", &second); code != 200 {
		t.Fatalf("status = %d", code)
	}
	if second.Heatmap.N != 1 || second.AsOf != first.AsOf {
		t.Errorf("cached hit changed: n=%d asOf %d vs %d", second.Heatmap.N, second.AsOf, first.AsOf)
	}
	if second.CacheTtlS != int(dashboardTTL/time.Second) {
		t.Errorf("cacheTtlS = %d, want %d", second.CacheTtlS, int(dashboardTTL/time.Second))
	}

	// Force expiry → stale-while-revalidate: the next request serves the
	// STALE payload instantly (users never block behind a rebuild) and kicks
	// a background rebuild that picks up BBB.
	c.mu.Lock()
	c.builtAt = time.Now().Add(-dashboardTTL - time.Second)
	c.mu.Unlock()

	var third dashResp
	if code := s8Get(t, srv.URL+"/api/dashboard", &third); code != 200 {
		t.Fatalf("status = %d", code)
	}
	if third.Heatmap.N != 1 {
		t.Errorf("stale-serve heatmap n = %d, want 1 (stale copy)", third.Heatmap.N)
	}

	// The background rebuild lands shortly; poll the cache, then re-request.
	deadline := time.Now().Add(5 * time.Second)
	for {
		c.mu.Lock()
		fresh := time.Since(c.builtAt) < c.ttl
		c.mu.Unlock()
		if fresh || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	var fourth dashResp
	if code := s8Get(t, srv.URL+"/api/dashboard", &fourth); code != 200 {
		t.Fatalf("status = %d", code)
	}
	if fourth.Heatmap.N != 2 {
		t.Errorf("post-rebuild heatmap n = %d, want 2", fourth.Heatmap.N)
	}
}
