// Package alpaca ingests US stock bars from Alpaca Market Data (the free IEX
// feed): REST backfill of daily/minute history plus a websocket streamer for
// live minute bars. Auth is header-based; base URLs are fields so tests can
// point the client at httptest servers.
package alpaca

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// Default endpoints. BaseData already includes the /v2 prefix because every
// data route lives under it; BasePaper does not, mirroring how Alpaca
// documents the two hosts.
const (
	defaultBaseData  = "https://data.alpaca.markets/v2"
	defaultBasePaper = "https://paper-api.alpaca.markets"
)

// pagePause spaces backfill page fetches so a long history pull stays well
// under the free-tier rate limit (200 req/min). Tests shorten it.
var pagePause = 300 * time.Millisecond

// Client is a minimal Alpaca REST client. Zero-value fields fall back to
// production defaults; override BaseData/BasePaper/HTTP in tests.
type Client struct {
	Key       string       // APCA-API-KEY-ID
	Secret    string       // APCA-API-SECRET-KEY
	BaseData  string       // market-data host, includes /v2
	BasePaper string       // paper-trading host (asset validation), no /v2
	HTTP      *http.Client // defaults to a 30s-timeout client
	// Feed selects the REST bars feed. "" defaults to "sip": Alpaca's FREE
	// tier serves FULL-MARKET SIP bars for historical queries (verified live
	// 2026-07-10: NVDA 1d volume 148.3M on sip vs 5.5M on iex — 27× the tape)
	// with only the most recent ~15 minutes restricted, which sipEndGuard
	// respects. Real-time consumers (the universe-live poller) must set
	// Feed:"iex" explicitly — SIP cannot serve the trailing window.
	Feed string
}

// feed returns the effective REST bars feed.
func (c *Client) feed() string {
	if c.Feed != "" {
		return c.Feed
	}
	return "sip"
}

// sipEndGuard bounds SIP requests away from the restricted trailing window.
// Free-tier SIP rejects queries touching the most recent ~15 minutes, so SIP
// requests carry end=now−16m; IEX requests are unbounded (real-time OK). The
// guarded 16-minute tail is filled by the IEX universe-live poller and then
// HEALED to full-volume SIP bars on the next deep pass (upserts overwrite).
func (c *Client) sipEndGuard(q url.Values) {
	if c.feed() == "sip" {
		q.Set("end", time.Now().UTC().Add(-16*time.Minute).Format(time.RFC3339))
	}
}

// New returns a Client wired to Alpaca's production hosts.
func New(key, secret string) *Client {
	return &Client{
		Key:       key,
		Secret:    secret,
		BaseData:  defaultBaseData,
		BasePaper: defaultBasePaper,
		HTTP:      &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

func (c *Client) baseData() string {
	if c.BaseData != "" {
		return c.BaseData
	}
	return defaultBaseData
}

func (c *Client) basePaper() string {
	if c.BasePaper != "" {
		return c.BasePaper
	}
	return defaultBasePaper
}

// auth stamps the Alpaca credential headers on a request.
func (c *Client) auth(req *http.Request) {
	req.Header.Set("APCA-API-KEY-ID", c.Key)
	req.Header.Set("APCA-API-SECRET-KEY", c.Secret)
}

// restBar is the wire shape of one bar in the /bars response.
type restBar struct {
	T string  `json:"t"` // RFC3339 bar open time
	O float64 `json:"o"`
	H float64 `json:"h"`
	L float64 `json:"l"`
	C float64 `json:"c"`
	V float64 `json:"v"`
}

// barsPage is one page of the /bars response; next_page_token is null on the
// final page.
type barsPage struct {
	Bars          []restBar `json:"bars"`
	NextPageToken *string   `json:"next_page_token"`
}

// BackfillDaily pulls ~2 years of split-adjusted daily IEX bars for symbol,
// upserts them into st, and returns the number of bars written.
func (c *Client) BackfillDaily(ctx context.Context, st *store.Store, symbolID int64, symbol string) (int, error) {
	start := time.Now().UTC().AddDate(-2, 0, 0)
	return c.backfill(ctx, st, symbolID, symbol, "1Day", md.TF1d, start)
}

// BackfillMinute pulls ~60 days of split-adjusted minute IEX bars for symbol,
// upserts them into st, and returns the number of bars written.
func (c *Client) BackfillMinute(ctx context.Context, st *store.Store, symbolID int64, symbol string) (int, error) {
	start := time.Now().UTC().AddDate(0, 0, -60)
	return c.backfill(ctx, st, symbolID, symbol, "1Min", md.TF1m, start)
}

// backfill walks the paginated /stocks/{symbol}/bars endpoint until
// next_page_token runs out, persisting each page as it arrives so a mid-run
// failure still leaves everything fetched so far in the store.
func (c *Client) backfill(ctx context.Context, st *store.Store, symbolID int64, symbol, timeframe string, tf md.Timeframe, start time.Time) (int, error) {
	total := 0
	pageToken := ""
	for {
		page, err := c.fetchBarsPage(ctx, symbol, timeframe, start, pageToken)
		if err != nil {
			return total, err
		}
		bars := make([]md.Bar, 0, len(page.Bars))
		for _, rb := range page.Bars {
			ts, err := time.Parse(time.RFC3339, rb.T)
			if err != nil {
				return total, fmt.Errorf("alpaca: bad bar time %q for %s: %w", rb.T, symbol, err)
			}
			bars = append(bars, md.Bar{
				SymbolID: symbolID, TF: tf, Ts: ts.Unix(),
				Open: rb.O, High: rb.H, Low: rb.L, Close: rb.C, Volume: rb.V,
			})
		}
		if err := st.UpsertBars(ctx, bars); err != nil {
			return total, fmt.Errorf("alpaca: upsert %s bars: %w", symbol, err)
		}
		total += len(bars)
		if page.NextPageToken == nil || *page.NextPageToken == "" {
			return total, nil
		}
		pageToken = *page.NextPageToken
		// Pace between pages; a ctx-aware wait so shutdown isn't delayed.
		select {
		case <-ctx.Done():
			return total, ctx.Err()
		case <-time.After(pagePause):
		}
	}
}

// fetchBarsPage GETs one page of bars.
func (c *Client) fetchBarsPage(ctx context.Context, symbol, timeframe string, start time.Time, pageToken string) (barsPage, error) {
	var page barsPage
	q := url.Values{}
	q.Set("timeframe", timeframe)
	q.Set("start", start.Format(time.RFC3339))
	q.Set("limit", "10000")
	q.Set("adjustment", "split")
	q.Set("feed", c.feed())
	c.sipEndGuard(q)
	if pageToken != "" {
		q.Set("page_token", pageToken)
	}
	u := fmt.Sprintf("%s/stocks/%s/bars?%s", c.baseData(), url.PathEscape(symbol), q.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return page, err
	}
	c.auth(req)
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return page, err
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return page, fmt.Errorf("alpaca: bars %s: status %d: %s", symbol, resp.StatusCode, body)
	}
	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		return page, fmt.Errorf("alpaca: decode bars %s: %w", symbol, err)
	}
	return page, nil
}

// ValidateSymbol checks that symbol exists as an active asset on Alpaca's
// paper-trading API. A 404 is a clean "no such symbol" (ok=false, err=nil);
// any other non-200 status is an error.
func (c *Client) ValidateSymbol(ctx context.Context, symbol string) (name string, ok bool, err error) {
	u := fmt.Sprintf("%s/v2/assets/%s", c.basePaper(), url.PathEscape(symbol))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", false, err
	}
	c.auth(req)
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return "", false, err
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode == http.StatusNotFound {
		return "", false, nil
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", false, fmt.Errorf("alpaca: asset %s: status %d: %s", symbol, resp.StatusCode, body)
	}
	var asset struct {
		Name     string `json:"name"`
		Tradable bool   `json:"tradable"`
		Status   string `json:"status"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&asset); err != nil {
		return "", false, fmt.Errorf("alpaca: decode asset %s: %w", symbol, err)
	}
	return asset.Name, asset.Status == "active", nil
}

// ─────────────────────────────────────────────────────────────────────────
// BROAD-UNIVERSE WAVE (appended block — keep new client methods at the END of
// this file so parallel edits by other agents never collide).
//
// Multi-symbol daily bars for the broad REST-only universe (Stage 2). Alpaca's
// data API supports a MULTI-SYMBOL bars form:
//
//	GET /v2/stocks/bars?symbols=A,B,C,…&timeframe=1Day&adjustment=split&feed=iex
//
// whose response keys bars BY SYMBOL (a map) rather than the flat array the
// single-symbol /stocks/{symbol}/bars route returns:
//
//	{"bars": {"AAPL": [ {t,o,h,l,c,v}, … ], "MSFT": [ … ]},
//	 "next_page_token": "…"|null}
//
// One request therefore backfills up to MaxBatchSymbols symbols at once, which
// is how the universe-poller stays FREE: 500 symbols ÷ 100/batch = 5 requests
// per page (usually 1 page for a short daily window), spaced by pagePause —
// far under Alpaca's free 200 req/min ceiling. See the rate-budget note in
// internal/universe.

// MaxBatchSymbols is the largest number of symbols packed into one
// multi-symbol bars request. Alpaca accepts long symbol lists; 100 keeps the
// URL well within limits and the per-request payload bounded, and makes the
// request-budget math easy (500 symbols = 5 requests/page).
const MaxBatchSymbols = 100

// maxBackfillRetries bounds the 429 back-off loop per page request so a
// persistently rate-limited server can't wedge a run forever.
const maxBackfillRetries = 4

// backoff429 is the base back-off applied when Alpaca answers 429; it doubles
// each retry. A var so tests shrink it.
var backoff429 = 2 * time.Second

// multiBarsPage is one page of the multi-symbol /stocks/bars response: bars
// keyed by symbol, plus the shared pagination token (null on the last page).
type multiBarsPage struct {
	Bars          map[string][]restBar `json:"bars"`
	NextPageToken *string              `json:"next_page_token"`
}

// BackfillDailyMulti pulls split-adjusted DAILY IEX bars for MANY symbols in
// as few requests as possible (batched by MaxBatchSymbols), upserts them, and
// returns per-symbol bar counts. Symbols the batch omits (no data / delisted)
// simply don't appear in the returned map. resolve maps a symbol string to its
// store symbol_id; symbols it can't resolve are skipped (never fatal). start
// defaults to ~2y ago when zero.
//
// It reuses the single-conn write path via st.UpsertBars and paces requests
// with the same pagePause the single-symbol backfill uses, so it shares the
// one free-tier rate budget. On HTTP 429 it backs off (backoff429, doubling)
// up to maxBackfillRetries before surfacing the error.
func (c *Client) BackfillDailyMulti(ctx context.Context, st *store.Store, symbols []string, resolve func(sym string) (int64, bool), start time.Time) (map[string]int, error) {
	return c.backfillMulti(ctx, st, symbols, resolve, "1Day", md.TF1d, start)
}

// BackfillHourlyMulti is BackfillDailyMulti for HOURLY bars — same batching,
// pacing, and 429 back-off; only the timeframe differs. Used by the
// broad-universe poller so daily-only symbols get current 1h coverage too.
func (c *Client) BackfillHourlyMulti(ctx context.Context, st *store.Store, symbols []string, resolve func(sym string) (int64, bool), start time.Time) (map[string]int, error) {
	return c.backfillMulti(ctx, st, symbols, resolve, "1Hour", md.TF1h, start)
}

// BackfillMinuteMulti is BackfillDailyMulti for MINUTE bars. Minute pages are
// the bulky ones (a 100-symbol batch spans multiple 10k-bar pages per trading
// day), so callers keep the start window short — the pagination + pacing here
// already keeps each run far under the free 200 req/min ceiling.
func (c *Client) BackfillMinuteMulti(ctx context.Context, st *store.Store, symbols []string, resolve func(sym string) (int64, bool), start time.Time) (map[string]int, error) {
	return c.backfillMulti(ctx, st, symbols, resolve, "1Min", md.TF1m, start)
}

// backfillMulti is the shared batched multi-symbol backfill used by the
// broad-universe poller. timeframe is the Alpaca timeframe string ("1Day",
// "1Hour"); tf is its store enum.
func (c *Client) backfillMulti(ctx context.Context, st *store.Store, symbols []string, resolve func(sym string) (int64, bool), timeframe string, tf md.Timeframe, start time.Time) (map[string]int, error) {
	if start.IsZero() {
		start = time.Now().UTC().AddDate(-2, 0, 0)
	}
	counts := map[string]int{}
	for i := 0; i < len(symbols); i += MaxBatchSymbols {
		end := i + MaxBatchSymbols
		if end > len(symbols) {
			end = len(symbols)
		}
		batch := symbols[i:end]
		if err := c.backfillMultiBatch(ctx, st, batch, resolve, timeframe, tf, start, counts); err != nil {
			return counts, err
		}
		// Pace between batches (ctx-aware) so a full universe pull stays well
		// under the free-tier rate limit. Skip the wait after the final batch.
		if end < len(symbols) {
			select {
			case <-ctx.Done():
				return counts, ctx.Err()
			case <-time.After(pagePause):
			}
		}
	}
	return counts, nil
}

// backfillMultiBatch fetches+persists every page for a single ≤MaxBatchSymbols
// batch, accumulating per-symbol counts into counts.
func (c *Client) backfillMultiBatch(ctx context.Context, st *store.Store, batch []string, resolve func(sym string) (int64, bool), timeframe string, tf md.Timeframe, start time.Time, counts map[string]int) error {
	pageToken := ""
	for {
		page, err := c.fetchMultiBarsPage(ctx, batch, timeframe, start, pageToken)
		if err != nil {
			return err
		}
		var bars []md.Bar
		for sym, rbs := range page.Bars {
			id, ok := resolve(sym)
			if !ok {
				continue // universe symbol not registered in the store — skip
			}
			for _, rb := range rbs {
				ts, err := time.Parse(time.RFC3339, rb.T)
				if err != nil {
					return fmt.Errorf("alpaca: bad bar time %q for %s: %w", rb.T, sym, err)
				}
				bars = append(bars, md.Bar{
					SymbolID: id, TF: tf, Ts: ts.Unix(),
					Open: rb.O, High: rb.H, Low: rb.L, Close: rb.C, Volume: rb.V,
				})
				counts[sym]++
			}
		}
		if err := st.UpsertBars(ctx, bars); err != nil {
			return fmt.Errorf("alpaca: upsert multi bars: %w", err)
		}
		if page.NextPageToken == nil || *page.NextPageToken == "" {
			return nil
		}
		pageToken = *page.NextPageToken
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pagePause):
		}
	}
}

// fetchMultiBarsPage GETs one page of the multi-symbol bars endpoint, retrying
// with exponential back-off on HTTP 429 (up to maxBackfillRetries).
func (c *Client) fetchMultiBarsPage(ctx context.Context, symbols []string, timeframe string, start time.Time, pageToken string) (multiBarsPage, error) {
	q := url.Values{}
	q.Set("symbols", strings.Join(symbols, ","))
	q.Set("timeframe", timeframe)
	q.Set("start", start.Format(time.RFC3339))
	q.Set("limit", "10000")
	q.Set("adjustment", "split")
	q.Set("feed", c.feed())
	c.sipEndGuard(q)
	if pageToken != "" {
		q.Set("page_token", pageToken)
	}
	u := fmt.Sprintf("%s/stocks/bars?%s", c.baseData(), q.Encode())

	wait := backoff429
	for attempt := 0; ; attempt++ {
		var page multiBarsPage
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return page, err
		}
		c.auth(req)
		resp, err := c.httpClient().Do(req)
		if err != nil {
			return page, err
		}
		if resp.StatusCode == http.StatusTooManyRequests {
			resp.Body.Close() //nolint:errcheck
			if attempt >= maxBackfillRetries {
				return page, fmt.Errorf("alpaca: multi bars: rate limited after %d retries", attempt)
			}
			select {
			case <-ctx.Done():
				return page, ctx.Err()
			case <-time.After(wait):
			}
			wait *= 2
			continue
		}
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			resp.Body.Close() //nolint:errcheck
			return page, fmt.Errorf("alpaca: multi bars: status %d: %s", resp.StatusCode, body)
		}
		err = json.NewDecoder(resp.Body).Decode(&page)
		resp.Body.Close() //nolint:errcheck
		if err != nil {
			return page, fmt.Errorf("alpaca: decode multi bars: %w", err)
		}
		return page, nil
	}
}
