// Package stocktwits ingests retail message sentiment from StockTwits' free
// public symbol streams — no key, no registration (verified live 2026-07-10):
//
//	GET https://api.stocktwits.com/api/2/streams/symbol/{SYM}.json
//
// One page is the ~30 newest messages for a symbol; each message MAY carry a
// user-selected sentiment tag (entities.sentiment.basic = "Bullish"/"Bearish",
// null when untagged). This client counts one page into a snapshot —
// bullish/bearish/untagged/total — and reports the newest message id.
//
// HONESTY (every consumer carries this): this is RETAIL MESSAGE sentiment
// from a self-selected crowd on one platform, counted over ONE page (~30
// messages) per fetch — a "page snapshot", not a complete census. Descriptive
// context only, never a scored factor and never advice.
//
// Discipline mirrors the finra client: declarative User-Agent, mutex-
// serialized min-interval pacing (default 2s — the public API is unauthed and
// politeness is the only budget), request timeout, 404 mapped to ErrUnknown
// so an unlisted symbol is an honest skip, never a failure.
package stocktwits

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/ingest/edgar"
)

const (
	// streamBaseURL hosts the public symbol streams ({SYM}.json appended).
	streamBaseURL = "https://api.stocktwits.com/api/2/streams/symbol"
	// defaultMinInterval spaces requests (≤1 req/2s — plain politeness on an
	// unauthenticated public API).
	defaultMinInterval = 2 * time.Second
)

// ErrUnknown means StockTwits has no stream for the requested symbol (404).
// Callers treat it as an honest skip, never a failure.
var ErrUnknown = errors.New("stocktwits: unknown symbol (no public stream)")

// Snapshot is one page's sentiment tally for one symbol at fetch time.
type Snapshot struct {
	Bullish  int   // messages tagged Bullish on the page
	Bearish  int   // messages tagged Bearish on the page
	Untagged int   // messages with no sentiment tag
	Total    int   // messages on the page (~30)
	NewestID int64 // newest message id seen (0 when the page is empty)
}

// Client is a paced StockTwits public-stream client. Zero-value BaseURL falls
// back to production; tests override it with an httptest server.
type Client struct {
	BaseURL     string
	UA          string
	MinInterval time.Duration
	HTTP        *http.Client

	mu   sync.Mutex
	last time.Time
}

// New returns a Client wired to StockTwits' production API with the shared
// declarative User-Agent.
func New() *Client {
	return &Client{
		UA:          edgar.ResolveUA(),
		BaseURL:     streamBaseURL,
		MinInterval: defaultMinInterval,
		HTTP:        &http.Client{Timeout: 15 * time.Second},
	}
}

func (c *Client) base() string {
	if c.BaseURL != "" {
		return strings.TrimSuffix(c.BaseURL, "/")
	}
	return streamBaseURL
}

func (c *Client) ua() string {
	if c.UA != "" {
		return c.UA
	}
	return edgar.ResolveUA()
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

func (c *Client) minInterval() time.Duration {
	if c.MinInterval > 0 {
		return c.MinInterval
	}
	return defaultMinInterval
}

// pace blocks until minInterval has elapsed since the previous request.
func (c *Client) pace(ctx context.Context) error {
	c.mu.Lock()
	wait := c.minInterval() - time.Since(c.last)
	if wait > 0 {
		c.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
		c.mu.Lock()
	}
	c.last = time.Now()
	c.mu.Unlock()
	return nil
}

// streamResp mirrors the fields we consume from the stream JSON.
type streamResp struct {
	Messages []struct {
		ID       int64 `json:"id"`
		Entities struct {
			Sentiment *struct {
				Basic string `json:"basic"`
			} `json:"sentiment"`
		} `json:"entities"`
	} `json:"messages"`
}

// FetchSymbol pulls one symbol's public stream page and tallies it. 404 →
// ErrUnknown (symbol not on StockTwits — honest skip); other non-200s error.
func (c *Client) FetchSymbol(ctx context.Context, symbol string) (Snapshot, error) {
	if err := c.pace(ctx); err != nil {
		return Snapshot{}, err
	}
	u := fmt.Sprintf("%s/%s.json", c.base(), strings.ToUpper(strings.TrimSpace(symbol)))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return Snapshot{}, err
	}
	req.Header.Set("User-Agent", c.ua())
	req.Header.Set("Accept", "application/json")
	res, err := c.httpClient().Do(req)
	if err != nil {
		return Snapshot{}, err
	}
	defer res.Body.Close() //nolint:errcheck
	switch res.StatusCode {
	case http.StatusOK:
		// fall through to parse
	case http.StatusNotFound:
		io.Copy(io.Discard, res.Body) //nolint:errcheck
		return Snapshot{}, ErrUnknown
	default:
		snippet, _ := io.ReadAll(io.LimitReader(res.Body, 256))
		return Snapshot{}, fmt.Errorf("stocktwits: %s: status %d: %s", symbol, res.StatusCode, strings.TrimSpace(string(snippet)))
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if err != nil {
		return Snapshot{}, err
	}
	return Tally(raw)
}

// Tally parses one stream page's JSON and counts it into a Snapshot
// (fixture-tested). A decode failure is an error, never a fabricated zero.
func Tally(raw []byte) (Snapshot, error) {
	var sr streamResp
	if err := json.Unmarshal(raw, &sr); err != nil {
		return Snapshot{}, fmt.Errorf("stocktwits: parse stream: %w", err)
	}
	return tally(sr), nil
}

// tally counts one decoded page.
func tally(sr streamResp) Snapshot {
	var s Snapshot
	for _, m := range sr.Messages {
		s.Total++
		if m.ID > s.NewestID {
			s.NewestID = m.ID
		}
		if m.Entities.Sentiment == nil {
			s.Untagged++
			continue
		}
		switch strings.ToLower(strings.TrimSpace(m.Entities.Sentiment.Basic)) {
		case "bullish":
			s.Bullish++
		case "bearish":
			s.Bearish++
		default:
			s.Untagged++
		}
	}
	return s
}
