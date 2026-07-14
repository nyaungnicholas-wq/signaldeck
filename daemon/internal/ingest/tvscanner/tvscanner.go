// Package tvscanner reads TradingView's OWN technical-analysis RATING for a
// symbol from its PUBLIC scanner endpoint — no account, no API key, no auth
// (verified live 2026-07-07):
//
//	POST https://scanner.tradingview.com/{screener}/scan   (screener = "america"
//	     for US stocks, "crypto" for crypto)
//	body: {"symbols":{"tickers":["NASDAQ:NVDA",…],"query":{"types":[]}},
//	       "columns":["Recommend.All","Recommend.MA","Recommend.Other","RSI","close"]}
//	resp: {"totalCount":N,"data":[{"s":"NASDAQ:NVDA","d":[0.55,0.93,0.18,56.9,210.9]},…]}
//
// ONE POST batches every ticker for that screener; the "d" array is in column
// order and a symbol the scanner has no data for is simply absent. Tickers
// MUST be EXCHANGE:SYMBOL, so the exchange is resolved first via the equally
// public symbol-search endpoint (GET symbol-search.tradingview.com/symbol_search).
//
// HONESTY: the Recommend.* values (each in [-1,1]) are TradingView's OWN
// DESCRIPTIVE technical-analysis rating computed from a basket of moving
// averages + oscillators on DELAYED data. It is an EXTERNAL, independent
// signal — NOT SignalDeck's model and NOT advice. Every consumer carries that
// caveat verbatim.
//
// Discipline mirrors internal/ingest/alpaca + internal/ingest/finra: base URLs
// are fields so tests point at httptest servers, a declarative User-Agent on
// every request, generous request timeouts, and graceful degradation
// (resolution failures are never fatal).
package tvscanner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	defaultBaseScan   = "https://scanner.tradingview.com"
	defaultBaseSearch = "https://symbol-search.tradingview.com"
	// userAgent identifies SignalDeck to TradingView's public endpoints.
	userAgent = "signaldeck/0.1"
	// requestTimeout bounds a single scanner/search call.
	requestTimeout = 20 * time.Second
)

// scanColumns is the fixed column request order; Rating decodes "d" positionally
// against it, so the order here IS the wire contract.
var scanColumns = []string{"Recommend.All", "Recommend.MA", "Recommend.Other", "RSI", "close"}

// Rating is one symbol's scanner row: TradingView's own TA recommendation
// components (each in [-1,1]) plus the RSI and close it reported. DESCRIPTIVE,
// delayed, external — see the package caveat.
type Rating struct {
	RecoAll   float64 `json:"recoAll"`   // Recommend.All (overall)
	RecoMA    float64 `json:"recoMA"`    // Recommend.MA (moving-average basket)
	RecoOther float64 `json:"recoOther"` // Recommend.Other (oscillator basket)
	RSI       float64 `json:"rsi"`
	Close     float64 `json:"close"`
}

// Client talks to TradingView's public scanner + symbol-search endpoints.
// Zero-value BaseScan/BaseSearch fall back to production; tests override them.
type Client struct {
	HTTP       *http.Client
	BaseScan   string
	BaseSearch string
}

// New returns a Client wired to TradingView's production hosts.
func New() *Client {
	return &Client{
		HTTP:       &http.Client{Timeout: 30 * time.Second},
		BaseScan:   defaultBaseScan,
		BaseSearch: defaultBaseSearch,
	}
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

func (c *Client) baseScan() string {
	if c.BaseScan != "" {
		return strings.TrimSuffix(c.BaseScan, "/")
	}
	return defaultBaseScan
}

func (c *Client) baseSearch() string {
	if c.BaseSearch != "" {
		return strings.TrimSuffix(c.BaseSearch, "/")
	}
	return defaultBaseSearch
}

// Label maps Recommend.All to TradingView's OWN named rating bands (verified
// against the TradingView TA widget): [0.5,1]→Strong Buy, [0.1,0.5)→Buy,
// (-0.1,0.1)→Neutral, (-0.5,-0.1]→Sell, [-1,-0.5]→Strong Sell.
func Label(recoAll float64) string {
	switch {
	case recoAll >= 0.5:
		return "Strong Buy"
	case recoAll >= 0.1:
		return "Buy"
	case recoAll > -0.1:
		return "Neutral"
	case recoAll > -0.5:
		return "Sell"
	default:
		return "Strong Sell"
	}
}

// searchItem is one symbol-search result. The symbol field may carry <em>
// highlight markup around the matched substring, stripped before comparison.
type searchItem struct {
	Symbol   string `json:"symbol"`
	Exchange string `json:"exchange"`
	Prefix   string `json:"prefix"`
	Type     string `json:"type"`
}

// ResolveExchange looks up a bare ticker's exchange (e.g. "NVDA"→"NASDAQ",
// "DRAM"→"CBOE") via TradingView's public symbol-search endpoint. It picks the
// first result whose symbol matches ours case-insensitively, PREFERRING a
// type=="stock" hit over any other. ok=false on no match OR any transport/parse
// failure — resolution is best-effort and NEVER fatal, so a caller just leaves
// the symbol unresolved and retries next sweep.
func (c *Client) ResolveExchange(ctx context.Context, symbol string) (string, bool) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	q := url.Values{}
	q.Set("text", symbol)
	q.Set("hl", "0")
	q.Set("lang", "en")
	q.Set("type", "")
	u := fmt.Sprintf("%s/symbol_search/?%s", c.baseSearch(), q.Encode())

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", false
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, resp.Body) //nolint:errcheck
		return "", false
	}
	var items []searchItem
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		return "", false
	}

	fallback := ""
	for _, it := range items {
		if !strings.EqualFold(stripEm(it.Symbol), symbol) || it.Exchange == "" {
			continue
		}
		if it.Type == "stock" {
			return it.Exchange, true // preferred hit — stop
		}
		if fallback == "" {
			fallback = it.Exchange // first non-stock match, kept as a fallback
		}
	}
	if fallback != "" {
		return fallback, true
	}
	return "", false
}

// scanRequest is the POST body for the scanner endpoint.
type scanRequest struct {
	Symbols scanSymbols `json:"symbols"`
	Columns []string    `json:"columns"`
}
type scanSymbols struct {
	Tickers []string  `json:"tickers"`
	Query   scanQuery `json:"query"`
}
type scanQuery struct {
	Types []string `json:"types"`
}

// scanResponse is the scanner reply: one row per symbol that had data, its
// values positional in the requested column order.
type scanResponse struct {
	TotalCount int `json:"totalCount"`
	Data       []struct {
		S string    `json:"s"` // EXCHANGE:SYMBOL
		D []float64 `json:"d"` // values, in scanColumns order
	} `json:"data"`
}

// ScanRatings fetches TradingView's rating for every ticker in ONE POST to the
// given screener ("america"|"crypto"). Tickers MUST be EXCHANGE:SYMBOL. The
// result is keyed by the scanner's returned "s" (EXCHANGE:SYMBOL); symbols the
// scanner has no data for are simply absent, and malformed rows (short "d")
// are skipped, never guessed.
func (c *Client) ScanRatings(ctx context.Context, screener string, tickers []string) (map[string]Rating, error) {
	out := make(map[string]Rating, len(tickers))
	if len(tickers) == 0 {
		return out, nil
	}
	body, err := json.Marshal(scanRequest{
		Symbols: scanSymbols{Tickers: tickers, Query: scanQuery{Types: []string{}}},
		Columns: scanColumns,
	})
	if err != nil {
		return nil, fmt.Errorf("tvscanner: marshal request: %w", err)
	}
	u := fmt.Sprintf("%s/%s/scan", c.baseScan(), url.PathEscape(screener))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("tvscanner: scan %s: status %d: %s", screener, resp.StatusCode, b)
	}
	var sr scanResponse
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return nil, fmt.Errorf("tvscanner: decode scan %s: %w", screener, err)
	}
	for _, row := range sr.Data {
		if row.S == "" || len(row.D) < len(scanColumns) {
			continue // malformed / partial row — skip, never fabricate
		}
		out[row.S] = Rating{
			RecoAll:   row.D[0],
			RecoMA:    row.D[1],
			RecoOther: row.D[2],
			RSI:       row.D[3],
			Close:     row.D[4],
		}
	}
	return out, nil
}

// ScanRatingsByName fetches ratings for plain ticker NAMES in ONE POST, letting
// the scanner itself resolve each symbol's exchange (via a name in_range filter
// with name+exchange columns). This avoids the symbol-search endpoint entirely
// — that host 403s server-side without browser headers, whereas the scanner
// answers a plain POST. Result is keyed by upper-cased NAME; the row carries the
// resolved exchange so callers get EXCHANGE:SYMBOL for free. Names the scanner
// can't place are simply absent. A name that resolves on multiple exchanges
// keeps the first row returned (scanner orders by relevance).
func (c *Client) ScanRatingsByName(ctx context.Context, screener string, names []string) (map[string]RatingRow, error) {
	out := make(map[string]RatingRow, len(names))
	if len(names) == 0 {
		return out, nil
	}
	cols := append([]string{"name", "exchange"}, scanColumns...)
	body, err := json.Marshal(map[string]any{
		"symbols": map[string]any{"query": map[string]any{"types": []string{}}},
		"filter":  []map[string]any{{"left": "name", "operation": "in_range", "right": names}},
		"columns": cols,
		"range":   []int{0, len(names) + 50},
	})
	if err != nil {
		return nil, fmt.Errorf("tvscanner: marshal name request: %w", err)
	}
	u := fmt.Sprintf("%s/%s/scan", c.baseScan(), url.PathEscape(screener))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("tvscanner: name scan %s: status %d: %s", screener, resp.StatusCode, b)
	}
	// d[] is mixed string (name, exchange) + float (ratings), so decode loosely.
	var sr struct {
		Data []struct {
			S string            `json:"s"`
			D []json.RawMessage `json:"d"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return nil, fmt.Errorf("tvscanner: decode name scan %s: %w", screener, err)
	}
	for _, row := range sr.Data {
		if len(row.D) < len(cols) {
			continue // partial row — skip, never fabricate
		}
		var name, exch string
		if json.Unmarshal(row.D[0], &name) != nil || json.Unmarshal(row.D[1], &exch) != nil {
			continue
		}
		key := strings.ToUpper(strings.TrimSpace(name))
		if key == "" || exch == "" {
			continue
		}
		if _, seen := out[key]; seen {
			continue // keep first (most relevant) row for a multi-listing name
		}
		f := make([]float64, len(scanColumns))
		ok := true
		for i := range scanColumns {
			if json.Unmarshal(row.D[2+i], &f[i]) != nil {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		out[key] = RatingRow{
			Exchange: exch,
			Rating:   Rating{RecoAll: f[0], RecoMA: f[1], RecoOther: f[2], RSI: f[3], Close: f[4]},
		}
	}
	return out, nil
}

// RatingRow pairs a resolved exchange with a Rating (ScanRatingsByName output).
type RatingRow struct {
	Exchange string
	Rating   Rating
}

// stripEm removes the <em>…</em> highlight markup TradingView wraps around the
// matched substring in symbol-search results.
func stripEm(s string) string {
	s = strings.ReplaceAll(s, "<em>", "")
	s = strings.ReplaceAll(s, "</em>", "")
	return strings.TrimSpace(s)
}

// ─────────────────────────────────────────────────────────────────────────
// DATA-EXPANSION WAVE — REAL-TIME QUOTES (appended block). The same public
// scanner endpoint also serves QUOTE columns (verified live 2026-07-10 for
// AAPL): close (15-MINUTE-DELAYED — update_mode "delayed_streaming_900"),
// change (day change %), volume (FULL-MARKET cumulative day volume), and rtc
// (REAL-TIME Cboe One composite price — null for some rows, so it decodes to
// a *float64). This fills the gap free bar feeds leave: Alpaca SIP is
// 15m-guarded and IEX is volume-thin.
//
// HONESTY: rtc is one composite's real-time print, close is delayed, and
// neither replaces the durable bar record — every consumer labels which one
// it is showing.

// quoteColumns is the fixed quote column order; decoding is positional, so
// the order here IS the wire contract.
var quoteColumns = []string{"close", "change", "volume", "update_mode", "rtc"}

// Quote is one symbol's scanner quote row.
type Quote struct {
	Exchange   string   `json:"exchange"`
	Close      float64  `json:"close"`     // 15-min-delayed close (update_mode-flagged)
	ChangePct  float64  `json:"changePct"` // day change %
	Volume     float64  `json:"volume"`    // full-market cumulative day volume
	RTC        *float64 `json:"rtc"`       // real-time Cboe One composite; nil when absent
	UpdateMode string   `json:"updateMode"`
}

// ScanQuotesByName fetches quotes for plain ticker NAMES in ONE POST (same
// name in_range filter as ScanRatingsByName; the scanner resolves each
// symbol's exchange itself). Result is keyed by upper-cased NAME; names the
// scanner can't place are absent and malformed rows are skipped, never
// fabricated.
func (c *Client) ScanQuotesByName(ctx context.Context, screener string, names []string) (map[string]Quote, error) {
	out := make(map[string]Quote, len(names))
	if len(names) == 0 {
		return out, nil
	}
	cols := append([]string{"name", "exchange"}, quoteColumns...)
	body, err := json.Marshal(map[string]any{
		"symbols": map[string]any{"query": map[string]any{"types": []string{}}},
		"filter":  []map[string]any{{"left": "name", "operation": "in_range", "right": names}},
		"columns": cols,
		"range":   []int{0, len(names) + 50},
	})
	if err != nil {
		return nil, fmt.Errorf("tvscanner: marshal quote request: %w", err)
	}
	rows, err := c.scanRaw(ctx, screener, body, len(cols))
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		var name, exch string
		if json.Unmarshal(row.D[0], &name) != nil || json.Unmarshal(row.D[1], &exch) != nil {
			continue
		}
		key := strings.ToUpper(strings.TrimSpace(name))
		if key == "" {
			continue
		}
		if _, seen := out[key]; seen {
			continue // keep first (most relevant) row for a multi-listing name
		}
		q, ok := decodeQuote(exch, row.D[2:])
		if !ok {
			continue
		}
		out[key] = q
	}
	return out, nil
}

// ScanQuotes fetches quotes for explicit EXCHANGE:SYMBOL tickers in ONE POST
// (crypto path — same tickers body as ScanRatings). Result is keyed by the
// scanner's returned "s".
func (c *Client) ScanQuotes(ctx context.Context, screener string, tickers []string) (map[string]Quote, error) {
	out := make(map[string]Quote, len(tickers))
	if len(tickers) == 0 {
		return out, nil
	}
	cols := append([]string{"name", "exchange"}, quoteColumns...)
	body, err := json.Marshal(map[string]any{
		"symbols": map[string]any{"tickers": tickers, "query": map[string]any{"types": []string{}}},
		"columns": cols,
	})
	if err != nil {
		return nil, fmt.Errorf("tvscanner: marshal quote ticker request: %w", err)
	}
	rows, err := c.scanRaw(ctx, screener, body, len(cols))
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		if row.S == "" {
			continue
		}
		var exch string
		_ = json.Unmarshal(row.D[1], &exch) // best-effort; ticker carries it anyway
		q, ok := decodeQuote(exch, row.D[2:])
		if !ok {
			continue
		}
		out[row.S] = q
	}
	return out, nil
}

// rawRow is one loosely-decoded scanner row (mixed string/float/null "d").
type rawRow struct {
	S string            `json:"s"`
	D []json.RawMessage `json:"d"`
}

// scanRaw POSTs a prepared scan body and returns rows with at least minCols
// values (shorter rows are dropped — partial data is never fabricated).
func (c *Client) scanRaw(ctx context.Context, screener string, body []byte, minCols int) ([]rawRow, error) {
	u := fmt.Sprintf("%s/%s/scan", c.baseScan(), url.PathEscape(screener))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("tvscanner: scan %s: status %d: %s", screener, resp.StatusCode, b)
	}
	var sr struct {
		Data []rawRow `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return nil, fmt.Errorf("tvscanner: decode scan %s: %w", screener, err)
	}
	rows := sr.Data[:0]
	for _, row := range sr.Data {
		if len(row.D) >= minCols {
			rows = append(rows, row)
		}
	}
	return rows, nil
}

// ─────────────────────────────────────────────────────────────────────────
// UNIVERSE-DISCOVERY EXPANSION (appended block). The same public scanner
// endpoint also answers SORTED+FILTERED queries over EVERY listed US symbol
// (verified live 2026-07-10: totalCount 1898 for volume>1M && close>$3),
// which is the whole-market discovery feed Alpaca's screener can't give us —
// Alpaca only surfaces its own most-actives/movers lists.
//
// Verified body (returns top-volume names; sortBy "change" asc/desc gives
// losers/gainers):
//
//	{"symbols":{"query":{"types":[]}},
//	 "filter":[{"left":"volume","operation":"greater","right":1000000},
//	           {"left":"close","operation":"greater","right":3}],
//	 "columns":["name","exchange","close","change","volume","market_cap_basic"],
//	 "sort":{"sortBy":"volume","sortOrder":"desc"},"range":[0,limit]}
//
// HONESTY: volume/close are the scanner's delayed day values and
// market_cap_basic is null for ETFs/funds (decoded as 0 — honest absence).
// Discovery feeds CANDIDATES only; budget/caps gate any actual add.

// discoveryColumns is the fixed discovery column order; decoding is
// positional, so the order here IS the wire contract.
var discoveryColumns = []string{"name", "exchange", "close", "change", "volume", "market_cap_basic"}

// DiscoveryRow is one whole-market screener hit.
type DiscoveryRow struct {
	Name      string  // bare ticker, e.g. "NVDA"
	Exchange  string  // e.g. "NASDAQ"
	Close     float64 // delayed day close
	ChangePct float64 // day change %
	Volume    float64 // cumulative day share volume
	MarketCap float64 // market_cap_basic; 0 when the scanner reports null (ETFs)
}

// ScanDiscovery screens the WHOLE screener universe (liquidity-filtered:
// volume > 1M shares && close > $3, cutting illiquid/penny noise) sorted by
// sortBy ("volume"|"change") in sortOrder ("desc"|"asc"), returning at most
// limit rows. Malformed rows are skipped, never fabricated.
func (c *Client) ScanDiscovery(ctx context.Context, screener, sortBy, sortOrder string, limit int) ([]DiscoveryRow, error) {
	if limit <= 0 {
		return nil, nil
	}
	body, err := json.Marshal(map[string]any{
		"symbols": map[string]any{"query": map[string]any{"types": []string{}}},
		"filter": []map[string]any{
			{"left": "volume", "operation": "greater", "right": 1000000},
			{"left": "close", "operation": "greater", "right": 3},
		},
		"columns": discoveryColumns,
		"sort":    map[string]any{"sortBy": sortBy, "sortOrder": sortOrder},
		"range":   []int{0, limit},
	})
	if err != nil {
		return nil, fmt.Errorf("tvscanner: marshal discovery request: %w", err)
	}
	rows, err := c.scanRaw(ctx, screener, body, len(discoveryColumns))
	if err != nil {
		return nil, err
	}
	out := make([]DiscoveryRow, 0, len(rows))
	for _, row := range rows {
		var dr DiscoveryRow
		if json.Unmarshal(row.D[0], &dr.Name) != nil || json.Unmarshal(row.D[1], &dr.Exchange) != nil {
			continue
		}
		dr.Name = strings.ToUpper(strings.TrimSpace(dr.Name))
		if dr.Name == "" {
			continue
		}
		if json.Unmarshal(row.D[2], &dr.Close) != nil ||
			json.Unmarshal(row.D[3], &dr.ChangePct) != nil ||
			json.Unmarshal(row.D[4], &dr.Volume) != nil {
			continue // core numerics must be numbers — skip, never guess
		}
		_ = json.Unmarshal(row.D[5], &dr.MarketCap) // null → 0 (honest absence)
		out = append(out, dr)
	}
	return out, nil
}

// decodeQuote decodes the positional quote values (close, change, volume,
// update_mode, rtc). close/change/volume must be numbers; update_mode is a
// best-effort string; rtc tolerates JSON null (nil pointer — verified live:
// some rows carry no real-time composite print).
func decodeQuote(exchange string, d []json.RawMessage) (Quote, bool) {
	if len(d) < len(quoteColumns) {
		return Quote{}, false
	}
	var closePx, change, volume float64
	if json.Unmarshal(d[0], &closePx) != nil ||
		json.Unmarshal(d[1], &change) != nil ||
		json.Unmarshal(d[2], &volume) != nil {
		return Quote{}, false
	}
	q := Quote{Exchange: exchange, Close: closePx, ChangePct: change, Volume: volume}
	_ = json.Unmarshal(d[3], &q.UpdateMode) // "" on mismatch — honest absence
	var rtc float64
	if json.Unmarshal(d[4], &rtc) == nil && string(d[4]) != "null" {
		q.RTC = &rtc
	}
	return q, true
}
