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
	q.Set("feed", "iex")
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
