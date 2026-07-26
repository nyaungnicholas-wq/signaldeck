// Package cboe ingests CBOE's free daily options market statistics — the
// market-wide put/call ratios — from the public CDN (probed + verified live
// 2026-07-10; the directory listing and the us_indices API 403, but the
// per-day stats document is open):
//
//	GET https://cdn.cboe.com/data/us/options/market_statistics/daily/
//	    {YYYY-MM-DD}_daily_options
//
// The response is JSON: {"ratios":[{"name":"TOTAL PUT/CALL RATIO","value":
// "0.86"},…], "SUM OF ALL PRODUCTS":{"call":…,"put":…,"total":…}, …}. Ratio
// values are strings; volumes are numbers. A missing day (weekend/holiday/
// not yet published) answers 403 — like FINRA's CDN — so 403 AND 404 both map
// to ErrNotAvailable, an honest skip.
//
// HONESTY (every consumer carries this): the daily put/call ratio is a
// DESCRIPTIVE market-wide positioning/hedging gauge — index puts are largely
// hedges, so a high index P/C is NOT directly bearish, and the equity-only
// ratio has a different (retail-tilted) meaning. Context, never a signal.
package cboe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/ingest/edgar"
)

const (
	// dailyBaseURL hosts the per-day options market statistics documents.
	dailyBaseURL = "https://cdn.cboe.com/data/us/options/market_statistics/daily"
	// defaultMinInterval spaces requests (one file/day + a one-time backfill).
	defaultMinInterval = 500 * time.Millisecond
)

// ErrNotAvailable means the requested day's statistics do not exist upstream —
// weekend, market holiday, or not yet published. CBOE's CDN returns 403 for
// absent documents (verified live 2026-07-10), so 403 AND 404 both map here.
var ErrNotAvailable = errors.New("cboe: daily statistics not available (weekend/holiday/not yet published)")

// Stats is one trade date's market-wide options statistics.
type Stats struct {
	Day      string  // trade date, YYYY-MM-DD
	TotalPC  float64 // TOTAL PUT/CALL RATIO
	IndexPC  float64 // INDEX PUT/CALL RATIO
	EquityPC float64 // EQUITY PUT/CALL RATIO
	VIXPC    float64 // CBOE VOLATILITY INDEX (VIX) PUT/CALL RATIO
	CallVol  float64 // SUM OF ALL PRODUCTS call volume
	PutVol   float64 // SUM OF ALL PRODUCTS put volume
	TotalVol float64 // SUM OF ALL PRODUCTS total volume
}

// Client is a paced CBOE CDN client. Zero-value BaseURL falls back to
// production; tests override it with an httptest server.
type Client struct {
	BaseURL     string
	UA          string
	MinInterval time.Duration
	HTTP        *http.Client

	mu   sync.Mutex
	last time.Time
}

// New returns a Client wired to CBOE's production CDN with the shared
// declarative User-Agent.
func New() *Client {
	return &Client{
		UA:          edgar.ResolveUA(),
		BaseURL:     dailyBaseURL,
		MinInterval: defaultMinInterval,
		HTTP:        &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *Client) base() string {
	if c.BaseURL != "" {
		return strings.TrimSuffix(c.BaseURL, "/")
	}
	return dailyBaseURL
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

// DailyURL is the statistics document URL for a trade date.
func (c *Client) DailyURL(day time.Time) string {
	return fmt.Sprintf("%s/%s_daily_options", c.base(), day.Format("2006-01-02"))
}

// FetchDaily downloads + parses one trade date's statistics. A missing
// document (403/404) returns ErrNotAvailable.
func (c *Client) FetchDaily(ctx context.Context, day time.Time) (Stats, error) {
	if err := c.pace(ctx); err != nil {
		return Stats{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.DailyURL(day), nil)
	if err != nil {
		return Stats{}, err
	}
	req.Header.Set("User-Agent", c.ua())
	req.Header.Set("Accept", "application/json")
	res, err := c.httpClient().Do(req)
	if err != nil {
		return Stats{}, err
	}
	defer res.Body.Close() //nolint:errcheck
	switch res.StatusCode {
	case http.StatusOK:
		// fall through to parse
	case http.StatusForbidden, http.StatusNotFound:
		io.Copy(io.Discard, res.Body) //nolint:errcheck
		return Stats{}, ErrNotAvailable
	default:
		io.Copy(io.Discard, res.Body) //nolint:errcheck
		return Stats{}, fmt.Errorf("cboe: GET %s: unexpected status %d", c.DailyURL(day), res.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return Stats{}, err
	}
	return ParseDaily(raw, day.Format("2006-01-02"))
}

// dailyDoc mirrors the fields we consume from the statistics JSON.
type dailyDoc struct {
	Ratios []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	} `json:"ratios"`
	// Sum is json.RawMessage because Cboe has published this field in TWO
	// shapes. It was a single object; it is now an ARRAY carrying both VOLUME
	// and OPEN INTEREST. See sumVolume for why the entry is chosen by NAME.
	Sum json.RawMessage `json:"SUM OF ALL PRODUCTS"`
}

// sumEntry is one "SUM OF ALL PRODUCTS" record in either shape.
type sumEntry struct {
	Name  string  `json:"name"`
	Call  float64 `json:"call"`
	Put   float64 `json:"put"`
	Total float64 `json:"total"`
}

// sumVolume extracts the VOLUME record from the "SUM OF ALL PRODUCTS" field,
// accepting both shapes Cboe has served.
//
// The field began as one object and became an array of records — the live error
// was "cannot unmarshal array into Go struct field dailyDoc.SUM OF ALL
// PRODUCTS", and it had left the cboe_pc table at ZERO rows.
//
// The entry is selected BY NAME, never by position, and that is the load-bearing
// part rather than a stylistic nicety: the array's other record is OPEN
// INTEREST, which runs roughly fifty times larger than volume. Taking index 0
// would work today and silently poison the put/call volume — a model feature —
// the day Cboe reorders the array. A document whose shape is understood but
// which carries no VOLUME record yields ok=false, so the caller records absence
// instead of zeros.
func sumVolume(raw json.RawMessage) (sumEntry, bool) {
	if len(raw) == 0 {
		return sumEntry{}, false
	}
	// Current shape: an array of named records.
	var arr []sumEntry
	if err := json.Unmarshal(raw, &arr); err == nil {
		for _, e := range arr {
			if strings.EqualFold(strings.TrimSpace(e.Name), "VOLUME") {
				return e, true
			}
		}
		return sumEntry{}, false
	}
	// Legacy shape: a single object. Accepted so archived fixtures and any
	// cached document still parse.
	var one sumEntry
	if err := json.Unmarshal(raw, &one); err == nil {
		if one.Total > 0 || one.Call > 0 || one.Put > 0 {
			return one, true
		}
	}
	return sumEntry{}, false
}

// ParseDaily parses one statistics document (fixture-tested). A document with
// no TOTAL PUT/CALL RATIO is an error — the headline number is the point.
func ParseDaily(raw []byte, day string) (Stats, error) {
	var doc dailyDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return Stats{}, fmt.Errorf("cboe: parse daily statistics: %w", err)
	}
	s := Stats{Day: day}
	if sum, ok := sumVolume(doc.Sum); ok {
		s.CallVol, s.PutVol, s.TotalVol = sum.Call, sum.Put, sum.Total
	}
	found := false
	for _, r := range doc.Ratios {
		v, err := strconv.ParseFloat(strings.TrimSpace(r.Value), 64)
		if err != nil {
			continue
		}
		switch strings.ToUpper(strings.TrimSpace(r.Name)) {
		case "TOTAL PUT/CALL RATIO":
			s.TotalPC, found = v, true
		case "INDEX PUT/CALL RATIO":
			s.IndexPC = v
		case "EQUITY PUT/CALL RATIO":
			s.EquityPC = v
		case "CBOE VOLATILITY INDEX (VIX) PUT/CALL RATIO":
			s.VIXPC = v
		}
	}
	if !found {
		return Stats{}, errors.New("cboe: document carries no TOTAL PUT/CALL RATIO (format change?)")
	}
	return s, nil
}
