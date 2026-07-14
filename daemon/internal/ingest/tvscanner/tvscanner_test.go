package tvscanner

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestScanRatings_Parsing drives ScanRatings against the exact verified wire
// shape: totalCount + data[] with a positional "d" array, and asserts a symbol
// the scanner OMITS is simply absent (not zero-filled), a short "d" row is
// skipped, and the request body carries the fixed columns + tickers.
func TestScanRatings_Parsing(t *testing.T) {
	var gotBody scanRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/america/scan") {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("content-type = %q", ct)
		}
		if ua := r.Header.Get("User-Agent"); ua != userAgent {
			t.Errorf("user-agent = %q", ua)
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		// NVDA + AAPL present; MSFT (requested) absent; a malformed short row.
		io.WriteString(w, `{"totalCount":2,"data":[
			{"s":"NASDAQ:NVDA","d":[0.5576,0.9333,0.1818,56.97,210.96]},
			{"s":"NASDAQ:AAPL","d":[-0.2,-0.3,-0.1,44.1,190.5]},
			{"s":"NASDAQ:BAD","d":[0.1,0.2]}
		]}`)
	}))
	defer srv.Close()

	c := &Client{BaseScan: srv.URL}
	got, err := c.ScanRatings(context.Background(), "america",
		[]string{"NASDAQ:NVDA", "NASDAQ:AAPL", "NASDAQ:MSFT"})
	if err != nil {
		t.Fatalf("ScanRatings: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 ratings, got %d (%v)", len(got), got)
	}
	nvda, ok := got["NASDAQ:NVDA"]
	if !ok {
		t.Fatal("NVDA missing")
	}
	if nvda.RecoAll != 0.5576 || nvda.RecoMA != 0.9333 || nvda.RecoOther != 0.1818 ||
		nvda.RSI != 56.97 || nvda.Close != 210.96 {
		t.Errorf("NVDA mismatch: %+v", nvda)
	}
	if _, ok := got["NASDAQ:MSFT"]; ok {
		t.Error("MSFT should be absent (scanner omitted it)")
	}
	if _, ok := got["NASDAQ:BAD"]; ok {
		t.Error("malformed short-d row should be skipped")
	}
	// Request shape: fixed columns + our tickers.
	if len(gotBody.Columns) != len(scanColumns) || gotBody.Columns[0] != "Recommend.All" {
		t.Errorf("columns = %v", gotBody.Columns)
	}
	if len(gotBody.Symbols.Tickers) != 3 {
		t.Errorf("tickers = %v", gotBody.Symbols.Tickers)
	}
	if gotBody.Symbols.Query.Types == nil {
		t.Error("query.types must be present (empty array)")
	}
}

// TestScanRatings_Empty short-circuits with no tickers (no HTTP call).
func TestScanRatings_Empty(t *testing.T) {
	c := &Client{BaseScan: "http://127.0.0.1:0"} // would fail if actually called
	got, err := c.ScanRatings(context.Background(), "america", nil)
	if err != nil || len(got) != 0 {
		t.Fatalf("want empty/no-error, got %v / %v", got, err)
	}
}

// TestResolveExchange covers a match (preferring type=="stock", stripping <em>
// highlight markup) and a clean no-match (ok=false, never fatal).
func TestResolveExchange(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/symbol_search/") {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		switch r.URL.Query().Get("text") {
		case "DRAM":
			// A non-stock (CBOE index-ish) hit appears first, then the stock —
			// the stock must win, and <em> markup on symbol must be stripped.
			io.WriteString(w, `[
				{"symbol":"DR<em>AM</em>X","exchange":"NASDAQ","prefix":"","type":"fund"},
				{"symbol":"<em>DRAM</em>","exchange":"CBOE","prefix":"","type":"stock"}
			]`)
		case "NVDA":
			io.WriteString(w, `[{"symbol":"NVDA","exchange":"NASDAQ","prefix":"","type":"stock"}]`)
		default:
			io.WriteString(w, `[]`)
		}
	}))
	defer srv.Close()

	c := &Client{BaseSearch: srv.URL}
	ctx := context.Background()

	if exch, ok := c.ResolveExchange(ctx, "NVDA"); !ok || exch != "NASDAQ" {
		t.Errorf("NVDA => %q,%v want NASDAQ,true", exch, ok)
	}
	if exch, ok := c.ResolveExchange(ctx, "DRAM"); !ok || exch != "CBOE" {
		t.Errorf("DRAM => %q,%v want CBOE,true (stock preferred, <em> stripped)", exch, ok)
	}
	if exch, ok := c.ResolveExchange(ctx, "NOSUCH"); ok || exch != "" {
		t.Errorf("no-match => %q,%v want \"\",false", exch, ok)
	}
}

// TestLabelBands pins TradingView's own recommendation bands at the boundaries.
func TestLabelBands(t *testing.T) {
	cases := []struct {
		v    float64
		want string
	}{
		{1.0, "Strong Buy"},
		{0.5, "Strong Buy"}, // >=0.5 boundary
		{0.4999, "Buy"},     // just below
		{0.1, "Buy"},        // >=0.1 boundary
		{0.0999, "Neutral"}, // just below
		{0.0, "Neutral"},
		{-0.0999, "Neutral"}, // just above -0.1
		{-0.1, "Sell"},       // -0.1 boundary → Sell
		{-0.4999, "Sell"},
		{-0.5, "Strong Sell"}, // -0.5 boundary → Strong Sell
		{-1.0, "Strong Sell"},
	}
	for _, c := range cases {
		if got := Label(c.v); got != c.want {
			t.Errorf("Label(%v) = %q, want %q", c.v, got, c.want)
		}
	}
}

// TestScanRatingsByName covers the name-filter scan that lets the scanner
// resolve exchanges itself (d[] mixes string name/exchange + float ratings).
func TestScanRatingsByName(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"totalCount":2,"data":[
			{"s":"CBOE:DRAM","d":["DRAM","CBOE",-0.27,-0.3,-0.2,44.1,3.2]},
			{"s":"NASDAQ:NVDA","d":["NVDA","NASDAQ",0.557,0.933,0.181,56.9,210.9]}]}`))
	}))
	defer srv.Close()
	c := &Client{BaseScan: srv.URL}
	got, err := c.ScanRatingsByName(context.Background(), "america", []string{"DRAM", "NVDA"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 rows, got %d", len(got))
	}
	if got["DRAM"].Exchange != "CBOE" || got["NVDA"].Exchange != "NASDAQ" {
		t.Errorf("exchange resolution wrong: %+v", got)
	}
	if got["NVDA"].Rating.RecoAll != 0.557 {
		t.Errorf("NVDA recoAll = %v", got["NVDA"].Rating.RecoAll)
	}
}

// ── DATA-EXPANSION wave: quote scanning ───────────────────────────────────

func TestScanQuotesByName(t *testing.T) {
	// Mirrors the live shape (verified 2026-07-10): mixed string/float "d",
	// rtc present for AAPL, null for THIN, malformed row dropped.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/america/scan" {
			t.Errorf("path = %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"totalCount":3,"data":[
			{"s":"NASDAQ:AAPL","d":["AAPL","NASDAQ",315.32,-1.4,34132321,"delayed_streaming_900",314.96]},
			{"s":"CBOE:THIN","d":["THIN","CBOE",10.5,0.2,1200,"delayed_streaming_900",null]},
			{"s":"NYSE:BAD","d":["BAD","NYSE","not-a-number",0,0,"x",null]}
		]}`))
	}))
	defer srv.Close()
	c := &Client{BaseScan: srv.URL}
	quotes, err := c.ScanQuotesByName(context.Background(), "america", []string{"AAPL", "THIN", "BAD"})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(quotes) != 2 {
		t.Fatalf("quotes = %d, want 2 (malformed BAD dropped)", len(quotes))
	}
	aapl := quotes["AAPL"]
	if aapl.Close != 315.32 || aapl.ChangePct != -1.4 || aapl.Volume != 34132321 ||
		aapl.UpdateMode != "delayed_streaming_900" || aapl.Exchange != "NASDAQ" {
		t.Errorf("AAPL quote wrong: %+v", aapl)
	}
	if aapl.RTC == nil || *aapl.RTC != 314.96 {
		t.Errorf("AAPL rtc wrong: %v", aapl.RTC)
	}
	if thin := quotes["THIN"]; thin.RTC != nil {
		t.Errorf("null rtc must decode to nil, got %v", thin.RTC)
	}
}

func TestScanQuotesTickers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"totalCount":1,"data":[
			{"s":"BITSTAMP:BTCUSD","d":["BTCUSD","BITSTAMP",64135.0,2.1,9999.5,"streaming",null]}
		]}`))
	}))
	defer srv.Close()
	c := &Client{BaseScan: srv.URL}
	quotes, err := c.ScanQuotes(context.Background(), "crypto", []string{"BITSTAMP:BTCUSD"})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	q, ok := quotes["BITSTAMP:BTCUSD"]
	if !ok || q.Close != 64135.0 || q.RTC != nil || q.UpdateMode != "streaming" {
		t.Errorf("BTC quote wrong: %+v (ok=%v)", q, ok)
	}
}

// ── UNIVERSE-DISCOVERY wave: whole-market discovery scanning ─────────────

func TestScanDiscovery(t *testing.T) {
	// Mirrors the live shape (verified 2026-07-10): mixed string/float "d",
	// market_cap_basic null for ETFs, malformed row dropped. Also asserts the
	// wire body carries the verified filter + sort.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/america/scan" {
			t.Errorf("path = %s", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		for _, want := range []string{
			`"sort":{"sortBy":"volume","sortOrder":"desc"}`,
			`{"left":"volume","operation":"greater","right":1000000}`,
			`{"left":"close","operation":"greater","right":3}`,
			`"range":[0,5]`,
		} {
			if !strings.Contains(string(body), want) {
				t.Errorf("body missing %s: %s", want, body)
			}
		}
		_, _ = w.Write([]byte(`{"totalCount":1898,"data":[
			{"s":"AMEX:SOXS","d":["SOXS","AMEX",4.08,0.245,526045775,null]},
			{"s":"NASDAQ:NVDA","d":["NVDA","NASDAQ",210.96,4.03,148418266,5105231969267.3]},
			{"s":"NYSE:BAD","d":["BAD","NYSE","not-a-number",0,0,null]}
		]}`))
	}))
	defer srv.Close()
	c := &Client{BaseScan: srv.URL}
	rows, err := c.ScanDiscovery(context.Background(), "america", "volume", "desc", 5)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2 (malformed BAD dropped)", len(rows))
	}
	soxs := rows[0]
	if soxs.Name != "SOXS" || soxs.Exchange != "AMEX" || soxs.Close != 4.08 ||
		soxs.ChangePct != 0.245 || soxs.Volume != 526045775 {
		t.Errorf("SOXS row wrong: %+v", soxs)
	}
	if soxs.MarketCap != 0 {
		t.Errorf("null market cap must decode to 0, got %v", soxs.MarketCap)
	}
	if nvda := rows[1]; nvda.Name != "NVDA" || nvda.MarketCap != 5105231969267.3 {
		t.Errorf("NVDA row wrong: %+v", nvda)
	}
	// Zero/negative limit is a no-op, not a request.
	if rows, err := c.ScanDiscovery(context.Background(), "america", "volume", "desc", 0); err != nil || rows != nil {
		t.Errorf("limit 0: %v %v", rows, err)
	}
}
