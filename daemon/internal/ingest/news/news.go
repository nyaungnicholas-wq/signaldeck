// Package news ingests per-symbol news headlines from Alpaca's news API (free
// with the market-data key). It fetches the latest headlines for one symbol and
// upserts them into the store, leaving sentiment tagging to a downstream LLM
// worker. Auth is header-based; Base is a field so tests can point the client at
// an httptest server.
package news

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/ingest/httpretry"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// defaultBase is Alpaca's news host (the v1beta1 API is where /news lives).
const defaultBase = "https://data.alpaca.markets/v1beta1"

// fetchLimit is the number of headlines requested per symbol.
const fetchLimit = 20

// Client is a minimal Alpaca news REST client. Zero-value Base falls back to the
// production default; override Base/HTTP in tests.
type Client struct {
	Key    string       // APCA-API-KEY-ID
	Secret string       // APCA-API-SECRET-KEY
	Base   string       // news host, includes /v1beta1
	HTTP   *http.Client // defaults to a 30s-timeout client
}

// New returns a Client wired to Alpaca's production news host.
func New(key, secret string) *Client {
	return &Client{
		Key:    key,
		Secret: secret,
		Base:   defaultBase,
		HTTP:   &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

func (c *Client) base() string {
	if c.Base != "" {
		return c.Base
	}
	return defaultBase
}

// auth stamps the Alpaca credential headers on a request.
func (c *Client) auth(req *http.Request) {
	req.Header.Set("APCA-API-KEY-ID", c.Key)
	req.Header.Set("APCA-API-SECRET-KEY", c.Secret)
}

// restArticle is the wire shape of one article in the /news response. The id is
// a JSON number; we decode it as such and stringify it when mapping to a
// store.NewsItem (whose ID is a string, matching the news table's TEXT primary
// key).
type restArticle struct {
	ID        int64    `json:"id"`
	Headline  string   `json:"headline"`
	Source    string   `json:"source"`
	URL       string   `json:"url"`
	CreatedAt string   `json:"created_at"` // RFC3339
	Symbols   []string `json:"symbols"`
}

// newsPage is one page of the /news response; next_page_token is null on the
// final page.
type newsPage struct {
	News          []restArticle `json:"news"`
	NextPageToken *string       `json:"next_page_token"`
}

// maxPages bounds one symbol's catch-up: 5 pages of fetchLimit headlines.
const maxPages = 5

// Fetch retrieves the latest page of headlines for one symbol and maps them to
// store.NewsItem values. SymbolID and Sentiment are left zero/empty for the
// caller to set (Ingest fills SymbolID; the LLM worker fills sentiment).
func (c *Client) Fetch(ctx context.Context, symbol string) ([]store.NewsItem, error) {
	return c.FetchSince(ctx, symbol, 0)
}

// FetchSince pages back through a symbol's headlines until it reaches one at
// or before since (unix seconds), the feed ends, or maxPages is hit. One page
// of 20 per poll silently dropped the rest of a busy day (audit 11-10).
// since == 0 means no stored history: the first page only.
func (c *Client) FetchSince(ctx context.Context, symbol string, since int64) ([]store.NewsItem, error) {
	var out []store.NewsItem
	token := ""
	for page := 0; page < maxPages; page++ {
		items, next, err := c.fetchPage(ctx, symbol, token)
		if err != nil {
			return nil, err
		}
		out = append(out, items...)
		if since == 0 || next == "" || len(items) == 0 || items[len(items)-1].Ts <= since {
			break
		}
		token = next
	}
	return out, nil
}

// fetchPage reads one page of the /news response (newest first).
func (c *Client) fetchPage(ctx context.Context, symbol, token string) ([]store.NewsItem, string, error) {
	q := url.Values{}
	q.Set("symbols", symbol)
	q.Set("limit", fmt.Sprint(fetchLimit))
	if token != "" {
		q.Set("page_token", token)
	}
	u := fmt.Sprintf("%s/news?%s", c.base(), q.Encode())

	resp, err := httpretry.Do(ctx, c.httpClient(), func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err == nil {
			c.auth(req)
		}
		return req, err
	})
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, "", fmt.Errorf("news: fetch %s: status %d: %s", symbol, resp.StatusCode, body)
	}

	var page newsPage
	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		return nil, "", fmt.Errorf("news: decode %s: %w", symbol, err)
	}

	out := make([]store.NewsItem, 0, len(page.News))
	for _, a := range page.News {
		t, err := time.Parse(time.RFC3339, a.CreatedAt)
		if err != nil {
			return nil, "", fmt.Errorf("news: bad created_at %q for %s: %w", a.CreatedAt, symbol, err)
		}
		out = append(out, store.NewsItem{
			ID:       fmt.Sprint(a.ID),
			Ts:       t.Unix(),
			Headline: a.Headline,
			URL:      a.URL,
			Source:   a.Source,
		})
	}
	next := ""
	if page.NextPageToken != nil {
		next = *page.NextPageToken
	}
	return out, next, nil
}

// Ingest fetches the latest headlines for symbol, stamps symbolID on each, and
// upserts them into st (InsertNews is INSERT OR IGNORE, so re-runs are
// idempotent — a re-fetched article keeps its existing sentiment). It returns
// the number of headlines fetched-and-seen (not only newly inserted), which is
// the coverage figure the caller records for the run.
func (c *Client) Ingest(ctx context.Context, st *store.Store, symbolID int64, symbol string) (int, error) {
	var since int64
	if latest, lerr := st.SymbolNews(ctx, symbolID, 1); lerr == nil && len(latest) > 0 {
		since = latest[0].Ts
	}
	items, err := c.FetchSince(ctx, symbol, since)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, it := range items {
		it.SymbolID = symbolID
		if err := st.InsertNews(ctx, it); err != nil {
			return n, fmt.Errorf("news: insert %s article %s: %w", symbol, it.ID, err)
		}
		n++
	}
	return n, nil
}
