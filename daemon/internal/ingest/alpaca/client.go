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
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
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

// barAdjustment is the corporate-action adjustment EVERY bar request must use.
//
// "split" adjusts price and volume together for splits and leaves dividends
// alone. Which convention is chosen matters far less than the fact that only ONE
// is: the bars table holds a single series per symbol, so two ingest paths asking
// for different modes put two price conventions in one column, and a symbol fed by
// both gets a discontinuity at the seam that is indistinguishable from a real move.
//
// That had happened. tools/alpha/fetch_delisted.py requested adjustment=all (split
// PLUS dividends) while this client requested split, so anything imported through
// the delisted staging path followed a different convention from everything
// backfilled live. Both now request this constant, and TestAdjustmentModesAgree
// reads the Python file to stop them drifting apart again.
const barAdjustment = "split"

// BarAdjustment exposes the convention for callers that must REPORT which basis
// they wrote, rather than restate a literal that could drift from it.
func BarAdjustment() string { return barAdjustment }

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

// BackfillDailyFrom pulls split-adjusted daily IEX bars from an explicit start,
// for callers that must cover a symbol's whole life rather than the rolling
// two-year window BackfillDaily assumes.
//
// It exists for the delisted-cohort re-fetch, where the earliest bar can be six
// years back and a two-year window would silently replace part of a series and
// leave the rest on the old price basis — a seam in the middle of the repair.
//
// It shares backfill() with the other two, so it inherits the same adjustment
// constant and the same vendor-pad refusal by construction, not by convention.
func (c *Client) BackfillDailyFrom(ctx context.Context, st *store.Store, symbolID int64, symbol string, start time.Time) (int, error) {
	return c.backfill(ctx, st, symbolID, symbol, "1Day", md.TF1d, start)
}

// BackfillMinute pulls ~60 days of split-adjusted minute IEX bars for symbol,
// upserts them into st, and returns the number of bars written.
func (c *Client) BackfillMinute(ctx context.Context, st *store.Store, symbolID int64, symbol string) (int, error) {
	start := time.Now().UTC().AddDate(0, 0, -60)
	return c.backfill(ctx, st, symbolID, symbol, "1Min", md.TF1m, start)
}

// syntheticDailyPad reports a vendor pad: a DAILY bar carrying no volume and no
// range at all.
//
// Alpaca fabricates a session when the requested feed saw no trade, carrying a
// price forward with volume 0 and open=high=low=close. A US-listed equity that
// genuinely trades zero shares produces no bar from the exchange, so a daily bar
// shaped like this is the vendor's fill rather than a market fact. Stored, it
// reads downstream as a real session returning exactly 0.0%: SBNY accumulated 509
// of them after Signature Bank was seized, and every one sat in the point-in-time
// universe as a live name. 30,771 such bars were quarantined across 144 symbols
// before this filter existed; without it they return on the next backfill.
//
// DAILY ONLY, and that restriction is load-bearing rather than cautious. A minute
// with no trades is ordinary — crypto alone holds 58,980 legitimate flat
// zero-volume 1m bars, and stocks another 248 — so widening this to other
// timeframes would delete real data. Daily crypto has none at all.
//
// Dropping a bar rather than storing a fake one leaves a GAP, which is the honest
// shape: a close-to-close return across the gap is the real move, where a padded
// day would have reported no move at all.
func syntheticDailyPad(tf md.Timeframe, b md.Bar) bool {
	return tf == md.TF1d && b.Volume == 0 &&
		b.Open == b.High && b.High == b.Low && b.Low == b.Close
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
			bar := md.Bar{
				SymbolID: symbolID, TF: tf, Ts: ts.Unix(),
				Open: rb.O, High: rb.H, Low: rb.L, Close: rb.C, Volume: rb.V,
			}
			if syntheticDailyPad(tf, bar) {
				continue // vendor fill, not a session — see syntheticDailyPad
			}
			bars = append(bars, bar)
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

// fetchBarsPage GETs one page of bars, retrying on HTTP 429 via getRetrying
// (Retry-After when the server sends one, jittered exponential back-off
// otherwise, bounded by maxBackfillRetries).
func (c *Client) fetchBarsPage(ctx context.Context, symbol, timeframe string, start time.Time, pageToken string) (barsPage, error) {
	var page barsPage
	q := url.Values{}
	q.Set("timeframe", timeframe)
	q.Set("start", start.Format(time.RFC3339))
	q.Set("limit", "10000")
	q.Set("adjustment", barAdjustment)
	q.Set("feed", c.feed())
	c.sipEndGuard(q)
	if pageToken != "" {
		q.Set("page_token", pageToken)
	}
	u := fmt.Sprintf("%s/stocks/%s/bars?%s", c.baseData(), url.PathEscape(symbol), q.Encode())
	resp, err := c.getRetrying(ctx, u, "bars "+symbol)
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

// backfillMultiBatch fetches+persists every page for a single <=MaxBatchSymbols
// batch, accumulating per-symbol counts into counts.
//
// VENDOR-REJECTED SYMBOLS DO NOT KILL THE BATCH. Alpaca 400s the WHOLE
// multi-symbol request when any one symbol is invalid, so a single delisted
// name destroyed bars for up to MaxBatchSymbols symbols and failed the run.
// Measured live: universe-poller failed every day on ATC.220816 (delisted
// 2022-08-16), while poller.go's own comment claimed such names were
// "simply absent from the backfill - never fatal". They were fatal.
// We now drop exactly the symbol the vendor named and refetch, bounded by
// the batch size, and say so - a dropped symbol is logged, never silent.
func invalidSymbolFromErr(err error) string {
	if err == nil {
		return ""
	}
	const substr = "invalid symbol: "
	s := err.Error()
	i := strings.Index(s, substr)
	if i == -1 {
		return ""
	}
	s = s[i+len(substr):]
	var j int
	for j < len(s) {
		c := s[j]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '.' || c == '-' {
			j++
		} else {
			break
		}
	}
	symbol := s[:j]
	if symbol == "" {
		return ""
	}
	return strings.ToUpper(symbol)
}

func (c *Client) backfillMultiBatch(ctx context.Context, st *store.Store, batch []string, resolve func(sym string) (int64, bool), timeframe string, tf md.Timeframe, start time.Time, counts map[string]int) error {
	working := make([]string, len(batch))
	copy(working, batch)
	drops := 0
	pageToken := ""
	for {
		page, err := c.fetchMultiBarsPage(ctx, working, timeframe, start, pageToken)
		if err != nil {
			bad := invalidSymbolFromErr(err)
			if bad == "" {
				return err
			}
			found := false
			for _, sym := range working {
				if strings.EqualFold(sym, bad) {
					found = true
					break
				}
			}
			if !found {
				return err
			}
			newWorking := make([]string, 0, len(working))
			for _, sym := range working {
				if !strings.EqualFold(sym, bad) {
					newWorking = append(newWorking, sym)
				}
			}
			working = newWorking
			slog.Warn("alpaca: dropping symbol the vendor rejects", "symbol", bad, "timeframe", timeframe, "remaining", len(working))
			if len(working) == 0 {
				return nil
			}
			drops++
			if drops > len(batch) {
				return err
			}
			continue
		}
		var bars []md.Bar
		for sym, rbs := range page.Bars {
			id, ok := resolve(sym)
			if !ok {
				continue
			}
			for _, rb := range rbs {
				ts, err := time.Parse(time.RFC3339, rb.T)
				if err != nil {
					return fmt.Errorf("alpaca: bad bar time %q for %s: %w", rb.T, sym, err)
				}
				bar := md.Bar{SymbolID: id, TF: tf, Ts: ts.Unix(), Open: rb.O, High: rb.H, Low: rb.L, Close: rb.C, Volume: rb.V}
				if syntheticDailyPad(tf, bar) {
					continue
				}
				bars = append(bars, bar)
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
// on HTTP 429 via getRetrying (up to maxBackfillRetries).
func (c *Client) fetchMultiBarsPage(ctx context.Context, symbols []string, timeframe string, start time.Time, pageToken string) (multiBarsPage, error) {
	q := url.Values{}
	q.Set("symbols", strings.Join(symbols, ","))
	q.Set("timeframe", timeframe)
	q.Set("start", start.Format(time.RFC3339))
	q.Set("limit", "10000")
	q.Set("adjustment", barAdjustment)
	q.Set("feed", c.feed())
	c.sipEndGuard(q)
	if pageToken != "" {
		q.Set("page_token", pageToken)
	}
	u := fmt.Sprintf("%s/stocks/bars?%s", c.baseData(), q.Encode())

	var page multiBarsPage
	resp, err := c.getRetrying(ctx, u, "multi bars")
	if err != nil {
		return page, err
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return page, fmt.Errorf("alpaca: multi bars: status %d: %s", resp.StatusCode, body)
	}
	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		return page, fmt.Errorf("alpaca: decode multi bars: %w", err)
	}
	return page, nil
}

// ─────────────────────────────────────────────────────────────────────────
// SHARED 429 BACK-OFF (appended block — see the placement note above).
//
// Both bars paths route every GET through getRetrying, so throttling is
// handled once, identically, and VISIBLY. Before this, only the multi-symbol
// path retried: fetchBarsPage turned a 429 straight into an error and its
// caller re-drove the whole symbol, producing an unpaced retry storm against
// an endpoint that was already telling us to slow down.

// maxRetryAfter caps a server-supplied Retry-After so an absurd or hostile
// header cannot wedge a worker for hours; beyond it we use our own back-off.
const maxRetryAfter = 60 * time.Second

// parseRetryAfter reads an RFC 9110 Retry-After header in either accepted
// form — delay-seconds ("30") or HTTP-date — relative to now. ok is false when
// the header is absent, unparseable, negative, or already in the past, and the
// caller then falls back to jittered exponential back-off.
func parseRetryAfter(h string, now time.Time) (time.Duration, bool) {
	h = strings.TrimSpace(h)
	if h == "" {
		return 0, false
	}
	if secs, err := strconv.Atoi(h); err == nil {
		if secs < 0 {
			return 0, false
		}
		return min(time.Duration(secs)*time.Second, maxRetryAfter), true
	}
	t, err := http.ParseTime(h)
	if err != nil {
		return 0, false
	}
	d := t.Sub(now)
	if d < 0 {
		return 0, false
	}
	return min(d, maxRetryAfter), true
}

// jitter spreads a back-off uniformly over [d/2, d] so several workers that
// hit the limit on the same tick do not all retry on the same tick.
func jitter(d time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	return d/2 + time.Duration(rand.Int64N(int64(d/2)+1))
}

// getRetrying performs an authenticated GET, retrying ONLY on HTTP 429 —
// honouring Retry-After when the server sends one and using jittered
// exponential back-off (backoff429, doubling) when it does not — bounded by
// maxBackfillRetries. Every sleep is ctx-aware, so shutdown is never delayed
// by a back-off. Each throttle is logged at WARN and exhaustion returns an
// error logged at ERROR: throttling is never silent and the final failure is
// never swallowed.
//
// On a nil error the caller owns resp.Body and must close it. Non-429 statuses
// (including other non-200s) are returned as-is for the caller to interpret.
func (c *Client) getRetrying(ctx context.Context, u, what string) (*http.Response, error) {
	wait := backoff429
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, err
		}
		c.auth(req)
		resp, err := c.httpClient().Do(req)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusTooManyRequests {
			return resp, nil
		}
		hdr := resp.Header.Get("Retry-After")
		resp.Body.Close() //nolint:errcheck
		if attempt >= maxBackfillRetries {
			slog.Error("alpaca rate limited, giving up",
				"what", what, "requests", attempt+1, "retries", attempt)
			return nil, fmt.Errorf("alpaca: %s: rate limited after %d retries", what, attempt)
		}
		d, fromHeader := parseRetryAfter(hdr, time.Now())
		if !fromHeader {
			d = jitter(wait)
		}
		slog.Warn("alpaca rate limited, backing off",
			"what", what, "attempt", attempt+1, "wait", d.String(), "retry_after_honoured", fromHeader)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(d):
		}
		wait *= 2
	}
}
