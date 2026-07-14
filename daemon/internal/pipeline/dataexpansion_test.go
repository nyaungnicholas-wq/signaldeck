package pipeline

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/ingest/cboe"
	"github.com/nyaungnicholas-wq/signaldeck/internal/ingest/cftc"
	"github.com/nyaungnicholas-wq/signaldeck/internal/ingest/finra"
	"github.com/nyaungnicholas-wq/signaldeck/internal/ingest/hyperliquid"
	"github.com/nyaungnicholas-wq/signaldeck/internal/ingest/stocktwits"
	"github.com/nyaungnicholas-wq/signaldeck/internal/ingest/tvscanner"
	"github.com/nyaungnicholas-wq/signaldeck/internal/ingest/wikimedia"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

func openDataExpStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "dataexp_p.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// ── finra-shortint ────────────────────────────────────────────────────────

func TestShortIntOverdue(t *testing.T) {
	settle := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)
	if ShortIntOverdue(settle, settle.AddDate(0, 0, 10)) {
		t.Error("10 days after settlement is within the publication window")
	}
	if !ShortIntOverdue(settle, settle.AddDate(0, 0, 20)) {
		t.Error("20 days after settlement is overdue")
	}
}

const siWorkerFixture = `accountingYearMonthNumber|symbolCode|issueName|issuerServicesGroupExchangeCode|marketClassCode|currentShortPositionQuantity|previousShortPositionQuantity|stockSplitFlag|averageDailyVolumeQuantity|daysToCoverQuantity|revisionFlag|changePercent|changePreviousNumber|settlementDate
20260615|NVDA|NVIDIA Corp|A|NASDAQ|1000|900||500|2.0||11.1|100|2026-06-15
20260615|ZZZZ|Untracked Co|A|NYSE|5|4||1|5.0||25|1|2026-06-15
`

func TestShortInterestPollerProbesBackAndDedups(t *testing.T) {
	st := openDataExpStore(t)
	ctx := context.Background()
	nvda, _ := st.UpsertSymbol(ctx, "NVDA", md.Stocks, "NVIDIA")

	var reqs atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqs.Add(1)
		if r.URL.Path == "/shrt20260615.csv" {
			_, _ = w.Write([]byte(siWorkerFixture))
			return
		}
		w.WriteHeader(http.StatusForbidden) // newer cycles not yet published
	}))
	defer srv.Close()

	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC) // newest settle = 06-30 (unpublished)
	w := &ShortInterestPoller{
		St:     st,
		Client: &finra.SIClient{BaseURL: srv.URL, MinInterval: time.Millisecond},
		Now:    func() time.Time { return now },
	}
	detail, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(detail, "settlement 2026-06-15") || !strings.Contains(detail, "1 tracked rows") {
		t.Errorf("detail = %q", detail)
	}
	rows, _ := st.ShortInterestRecent(ctx, nvda.ID, 8)
	if len(rows) != 1 || rows[0].ShortQty != 1000 || rows[0].Settlement != "2026-06-15" {
		t.Errorf("stored rows wrong: %+v", rows)
	}

	// Second run: 06-30 still 403 ⇒ probing stops at the ingested 06-15 (only
	// ONE new request) and reports honestly.
	before := reqs.Load()
	detail2, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("run 2: %v", err)
	}
	if reqs.Load()-before != 1 {
		t.Errorf("second run made %d requests, want 1 (probe newest only)", reqs.Load()-before)
	}
	if !strings.Contains(detail2, "not yet published") || !strings.Contains(detail2, "2026-06-15") {
		t.Errorf("detail2 = %q", detail2)
	}
}

// ── crypto-perp ───────────────────────────────────────────────────────────

func TestCryptoPerpPollerStoresTrackedCoins(t *testing.T) {
	st := openDataExpStore(t)
	ctx := context.Background()
	btc, _ := st.UpsertSymbol(ctx, "BTC/USD", md.Crypto, "Bitcoin")
	_, _ = st.UpsertSymbol(ctx, "ZZZ/USD", md.Crypto, "Not on Hyperliquid")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[
			{"universe":[{"name":"BTC"},{"name":"ETH"}]},
			[{"funding":"0.0000125","openInterest":"35315.99","markPx":"64135.0"},
			 {"funding":"0.0000200","openInterest":"779258.09","markPx":"1796.8"}]
		]`))
	}))
	defer srv.Close()

	now := time.Unix(1760000000, 0)
	w := &CryptoPerpPoller{
		St:     st,
		Client: &hyperliquid.Client{BaseURL: srv.URL},
		Now:    func() time.Time { return now },
	}
	detail, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(detail, "stored 1 perp snapshot(s)") ||
		!strings.Contains(detail, "1 tracked crypto symbol(s) not in Hyperliquid universe") {
		t.Errorf("detail = %q", detail)
	}
	series, _ := st.CryptoPerpSeries(ctx, btc.ID, 0)
	if len(series) != 1 || series[0].Funding != 0.0000125 || series[0].Ts != now.Unix() {
		t.Errorf("stored series wrong: %+v", series)
	}
}

// ── cot-poller ────────────────────────────────────────────────────────────

func TestWantCOT(t *testing.T) {
	cases := []struct {
		market, contract string
		want             bool
	}{
		{"E-MINI S&P 500 - CHICAGO MERCANTILE EXCHANGE", "E-MINI S&P 500", true},
		{"NASDAQ-100 STOCK INDEX (MINI) - CME", "NASDAQ MINI", true},
		{"BITCOIN - CHICAGO MERCANTILE EXCHANGE", "BITCOIN", true},
		{"MICRO ETHER - CHICAGO MERCANTILE EXCHANGE", "MICRO ETHER", true},
		{"GULF # 6 FUEL OIL CRACK - NYMEX", "GULF # 6 FUEL OIL CRACK", false},
		{"WHEAT-SRW - CHICAGO BOARD OF TRADE", "WHEAT-SRW", false},
	}
	for _, c := range cases {
		if got := WantCOT(c.market, c.contract); got != c.want {
			t.Errorf("WantCOT(%q,%q) = %v, want %v", c.market, c.contract, got, c.want)
		}
	}
}

func TestCOTPollerBackfillThenSteadyState(t *testing.T) {
	st := openDataExpStore(t)
	ctx := context.Background()

	var reqs atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqs.Add(1)
		_, _ = w.Write([]byte(`[
		  {"market_and_exchange_names":"E-MINI S&P 500 - CME","contract_market_name":"E-MINI S&P 500",
		   "report_date_as_yyyy_mm_dd":"2026-07-07T00:00:00.000",
		   "noncomm_positions_long_all":"1","noncomm_positions_short_all":"2",
		   "comm_positions_long_all":"3","comm_positions_short_all":"4","open_interest_all":"5"},
		  {"market_and_exchange_names":"WHEAT-SRW - CBOT","contract_market_name":"WHEAT-SRW",
		   "report_date_as_yyyy_mm_dd":"2026-07-07T00:00:00.000",
		   "noncomm_positions_long_all":"1","noncomm_positions_short_all":"1",
		   "comm_positions_long_all":"1","comm_positions_short_all":"1","open_interest_all":"1"}
		]`))
	}))
	defer srv.Close()

	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	w := &COTPoller{St: st, Client: &cftc.Client{BaseURL: srv.URL}, Now: func() time.Time { return now }}

	detail, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	// 370d backfill in 90d chunks = 5 windows; the wheat contract is filtered
	// out client-side.
	if reqs.Load() != 5 {
		t.Errorf("backfill made %d requests, want 5", reqs.Load())
	}
	if !strings.Contains(detail, "1 contract(s)") {
		t.Errorf("detail = %q", detail)
	}
	rows, _ := st.COTSince(ctx, "2020-01-01")
	if len(rows) != 1 || rows[0].Contract != "E-MINI S&P 500" || rows[0].ReportDate != "2026-07-07" {
		t.Errorf("stored rows wrong: %+v", rows)
	}

	// Steady state: ONE trailing-window request.
	before := reqs.Load()
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("run 2: %v", err)
	}
	if reqs.Load()-before != 1 {
		t.Errorf("steady-state run made %d requests, want 1", reqs.Load()-before)
	}
}

// ── attention scope ───────────────────────────────────────────────────────

func TestAttentionScope(t *testing.T) {
	st := openDataExpStore(t)
	ctx := context.Background()
	streamed, _ := st.UpsertSymbol(ctx, "NVDA", md.Stocks, "")
	_ = st.SetSymbolStream(ctx, streamed.ID, true)
	watched, _ := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "")
	_, _ = st.UpsertSymbol(ctx, "XYZ", md.Stocks, "") // broad universe: out of scope
	crypto, _ := st.UpsertSymbol(ctx, "BTC/USD", md.Crypto, "")
	_ = crypto
	uid, err := st.CreateUser(ctx, "u", "h", false)
	if err != nil {
		t.Fatalf("user: %v", err)
	}
	if err := st.AddUserSymbol(ctx, uid, watched.ID); err != nil {
		t.Fatalf("watch: %v", err)
	}

	scope, err := attentionScope(ctx, st)
	if err != nil {
		t.Fatalf("scope: %v", err)
	}
	var syms []string
	for _, s := range scope {
		syms = append(syms, s.Symbol)
	}
	if len(syms) != 2 || syms[0] != "AAPL" || syms[1] != "NVDA" {
		t.Errorf("scope = %v, want [AAPL NVDA] (watched + streamed stocks only, sorted)", syms)
	}
}

// ── stocktwits-fetcher ────────────────────────────────────────────────────

func TestStocktwitsFetcherStoresAndCachesUnknown(t *testing.T) {
	st := openDataExpStore(t)
	ctx := context.Background()
	nvda, _ := st.UpsertSymbol(ctx, "NVDA", md.Stocks, "")
	_ = st.SetSymbolStream(ctx, nvda.ID, true)
	zzzz, _ := st.UpsertSymbol(ctx, "ZZZZ", md.Stocks, "")
	_ = st.SetSymbolStream(ctx, zzzz.ID, true)

	var reqs atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqs.Add(1)
		if r.URL.Path == "/NVDA.json" {
			_, _ = w.Write([]byte(`{"messages":[
				{"id":1,"entities":{"sentiment":{"basic":"Bullish"}}},
				{"id":2,"entities":{"sentiment":null}}
			]}`))
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	now := time.Unix(1760000000, 0)
	w := &StocktwitsFetcher{
		St:     st,
		Client: &stocktwits.Client{BaseURL: srv.URL, MinInterval: time.Millisecond},
		Now:    func() time.Time { return now },
	}
	detail, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(detail, "stored 1 page snapshot(s)") || !strings.Contains(detail, "1 not on StockTwits") {
		t.Errorf("detail = %q", detail)
	}
	series, _ := st.StocktwitsSeries(ctx, nvda.ID, 0)
	if len(series) != 1 || series[0].Bullish != 1 || series[0].Untagged != 1 || series[0].Total != 2 {
		t.Errorf("stored series wrong: %+v", series)
	}

	// Second run: the 404'd symbol is cached — only NVDA is requested.
	before := reqs.Load()
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("run 2: %v", err)
	}
	if reqs.Load()-before != 1 {
		t.Errorf("second run made %d requests, want 1 (unknown cached)", reqs.Load()-before)
	}
}

// ── wiki-attention ────────────────────────────────────────────────────────

func TestWikiAttentionResolvesCachesAndSkips(t *testing.T) {
	st := openDataExpStore(t)
	ctx := context.Background()
	nvda, _ := st.UpsertSymbol(ctx, "NVDA", md.Stocks, "")
	_ = st.SetSymbolStream(ctx, nvda.ID, true)
	junk, _ := st.UpsertSymbol(ctx, "JUNK", md.Stocks, "")
	_ = st.SetSymbolStream(ctx, junk.ID, true)
	noname, _ := st.UpsertSymbol(ctx, "NONAME", md.Stocks, "")
	_ = st.SetSymbolStream(ctx, noname.ID, true)
	if err := st.UpsertCompanies(ctx, []store.CompanyRow{
		{CIK: 1045810, Ticker: "NVDA", Name: "NVIDIA Corp", UpdatedTs: 1},
		{CIK: 999999, Ticker: "JUNK", Name: "Totally Unmappable Zzz Inc", UpdatedTs: 1},
	}); err != nil {
		t.Fatalf("companies: %v", err)
	}

	var reqs atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqs.Add(1)
		// First candidate "NVIDIA" 404s; "NVIDIA_(company)" resolves.
		if strings.HasPrefix(r.URL.Path, "/NVIDIA_(company)/daily/") {
			_, _ = w.Write([]byte(`{"items":[
				{"article":"NVIDIA_(company)","timestamp":"2026070800","views":4700},
				{"article":"NVIDIA_(company)","timestamp":"2026070900","views":4826}
			]}`))
			return
		}
		http.Error(w, `{"type":"not_found"}`, http.StatusNotFound)
	}))
	defer srv.Close()

	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	w := &WikiAttention{
		St:     st,
		Client: &wikimedia.Client{BaseURL: srv.URL, MinInterval: time.Millisecond},
		Now:    func() time.Time { return now },
	}
	detail, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(detail, "1 newly resolved") ||
		!strings.Contains(detail, "1 unresolved") ||
		!strings.Contains(detail, "1 without a directory name") {
		t.Errorf("detail = %q", detail)
	}
	views, _ := st.WikiViewsSeries(ctx, nvda.ID, 90)
	if len(views) != 2 || views[1].Day != "2026-07-09" || views[1].Views != 4826 {
		t.Errorf("views wrong: %+v", views)
	}
	wa, found, _ := st.GetWikiArticle(ctx, nvda.ID)
	if !found || !wa.OK || wa.Article != "NVIDIA_(company)" {
		t.Errorf("resolution cache wrong: %+v", wa)
	}
	if wa, found, _ := st.GetWikiArticle(ctx, junk.ID); !found || wa.OK {
		t.Errorf("failed resolution must be cached: found=%v %+v", found, wa)
	}
	if _, found, _ := st.GetWikiArticle(ctx, noname.ID); found {
		t.Error("symbol without a directory name must NOT be cached as failed (companies-sync may not have run yet)")
	}

	// Second run: cached article ⇒ 1 request for NVDA; JUNK never re-hammered.
	before := reqs.Load()
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("run 2: %v", err)
	}
	if reqs.Load()-before != 1 {
		t.Errorf("second run made %d requests, want 1 (cached resolution, cached failure)", reqs.Load()-before)
	}
}

// ── cboe-pc ───────────────────────────────────────────────────────────────

func TestCboePCPollerBackfillAndDedup(t *testing.T) {
	st := openDataExpStore(t)
	ctx := context.Background()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Serve every requested trading day with the same headline ratios.
		if strings.HasSuffix(r.URL.Path, "_daily_options") {
			fmt.Fprint(w, `{"ratios":[{"name":"TOTAL PUT/CALL RATIO","value":"0.86"},
				{"name":"INDEX PUT/CALL RATIO","value":"1.01"},
				{"name":"EQUITY PUT/CALL RATIO","value":"0.57"}],
				"SUM OF ALL PRODUCTS":{"call":100,"put":86,"total":186}}`)
			return
		}
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	// Thursday 2026-07-09 20:00 ET is past the ~18:30 publish gate.
	now := time.Date(2026, 7, 9, 20, 0, 0, 0, time.FixedZone("ET", -4*3600))
	w := &CboePCPoller{
		St:           st,
		Client:       &cboe.Client{BaseURL: srv.URL, MinInterval: time.Millisecond},
		BackfillDays: 3,
		Now:          func() time.Time { return now },
	}
	detail, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(detail, "backfilled 3 trading day(s)") {
		t.Errorf("detail = %q", detail)
	}
	series, _ := st.CboePCSeries(ctx, 90)
	if len(series) != 3 || series[2].Day != "2026-07-09" || series[2].TotalPC != 0.86 {
		t.Errorf("series wrong: %+v", series)
	}

	// Second run: day-key dedup.
	detail2, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("run 2: %v", err)
	}
	if !strings.Contains(detail2, "up to date (2026-07-09)") {
		t.Errorf("detail2 = %q", detail2)
	}
}

func TestCboePCPollerUnavailableRecordsDQ(t *testing.T) {
	st := openDataExpStore(t)
	ctx := context.Background()
	// Mark backfill done so the single-day path runs.
	_ = st.SetMeta(ctx, "cboe_pc_backfill_v1", "done")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	now := time.Date(2026, 7, 9, 20, 0, 0, 0, time.FixedZone("ET", -4*3600))
	w := &CboePCPoller{
		St:     st,
		Client: &cboe.Client{BaseURL: srv.URL, MinInterval: time.Millisecond},
		Now:    func() time.Time { return now },
	}
	detail, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("run must never fail the fleet: %v", err)
	}
	if !strings.Contains(detail, "not available yet") || !strings.Contains(detail, "will retry") {
		t.Errorf("detail = %q", detail)
	}
	events, _ := st.RecentDQ(ctx, 10)
	found := false
	for _, e := range events {
		if e.Kind == "cboe_pc_unavailable" {
			found = true
		}
	}
	if !found {
		t.Error("missing file past deadline must record a dq event")
	}
}

// ── tv-quotes ─────────────────────────────────────────────────────────────

func TestTVQuotesPollerMarketHoursAndPrune(t *testing.T) {
	st := openDataExpStore(t)
	ctx := context.Background()
	nvda, _ := st.UpsertSymbol(ctx, "NVDA", md.Stocks, "")
	btc, _ := st.UpsertSymbol(ctx, "BTC/USD", md.Crypto, "")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/america/scan":
			// rtc present ⇒ price is the real-time composite.
			fmt.Fprint(w, `{"data":[{"s":"NASDAQ:NVDA","d":["NVDA","NASDAQ",315.32,-1.4,34132321,"delayed_streaming_900",314.96]}]}`)
		case "/crypto/scan":
			// rtc null ⇒ price falls back to the (labeled) delayed close.
			fmt.Fprint(w, `{"data":[{"s":"BITSTAMP:BTCUSD","d":["BTCUSD","BITSTAMP",64135.0,2.1,9999.5,"streaming",null]}]}`)
		default:
			http.Error(w, "bad path", http.StatusNotFound)
		}
	}))
	defer srv.Close()

	// Thursday 2026-07-09 14:00 ET — market open.
	open := time.Date(2026, 7, 9, 14, 0, 0, 0, time.FixedZone("ET", -4*3600))
	w := &TVQuotesPoller{St: st, TV: &tvscanner.Client{BaseScan: srv.URL}, Now: func() time.Time { return open }}
	detail, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(detail, "stocks: 1/1 quoted (1 real-time rtc)") ||
		!strings.Contains(detail, "crypto: 1/1 quoted") {
		t.Errorf("detail = %q", detail)
	}
	q, ok, _ := st.LatestTVQuote(ctx, nvda.ID)
	if !ok || q.Price != 314.96 || !q.Realtime || q.DelayedClose != 315.32 || q.DayVolume != 34132321 {
		t.Errorf("NVDA quote wrong: %+v ok=%v", q, ok)
	}
	bq, ok, _ := st.LatestTVQuote(ctx, btc.ID)
	if !ok || bq.Price != 64135.0 || bq.Realtime {
		t.Errorf("BTC quote must fall back to delayed close, flagged: %+v", bq)
	}

	// Off-hours: stocks honestly skipped, crypto still taped, old rows pruned.
	night := time.Date(2026, 7, 9, 23, 0, 0, 0, time.FixedZone("ET", -4*3600))
	w.Now = func() time.Time { return night }
	detail2, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("run 2: %v", err)
	}
	if !strings.Contains(detail2, "stocks: market closed (skipped)") ||
		!strings.Contains(detail2, "crypto: 1/1 quoted") ||
		!strings.Contains(detail2, "pruned 2 tape row(s)") {
		t.Errorf("detail2 = %q", detail2)
	}
	// The 14:00 rows (9h old) are gone; only the fresh crypto row remains.
	if _, ok, _ := st.LatestTVQuote(ctx, nvda.ID); ok {
		t.Error("stale stock quote must be pruned from the tape")
	}
	if q, ok, _ := st.LatestTVQuote(ctx, btc.ID); !ok || q.Ts != night.Unix() {
		t.Errorf("fresh crypto quote missing after prune: %+v ok=%v", q, ok)
	}
}
