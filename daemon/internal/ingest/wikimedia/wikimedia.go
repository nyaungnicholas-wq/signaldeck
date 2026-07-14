// Package wikimedia ingests Wikipedia page-view counts from the official,
// free Wikimedia REST API — no key, no registration (verified live
// 2026-07-10 with en.wikipedia/Nvidia):
//
//	GET https://wikimedia.org/api/rest_v1/metrics/pageviews/per-article/
//	    en.wikipedia/all-access/user/{Article}/daily/{YYYYMMDD00}/{YYYYMMDD00}
//
// One request returns the whole requested day range ({items:[{article,
// timestamp:"YYYYMMDD00", views},…]}). agent=user excludes bots/spiders.
// A 404 means the article doesn't exist (or has no views in range) and maps
// to ErrNotFound — callers use it to try the next resolution candidate and to
// CACHE failures so unresolved names are never re-hammered.
//
// HONESTY (every consumer carries this): page views are a PUBLIC ATTENTION
// proxy — not a trading signal, not sentiment, and article-resolution is a
// name heuristic that can pick the wrong page. Descriptive context only.
//
// Discipline mirrors the other ingest clients: declarative User-Agent,
// mutex-serialized min-interval pacing (default 200ms), request timeout,
// graceful degradation everywhere.
package wikimedia

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/ingest/edgar"
)

const (
	// pvBaseURL hosts the per-article pageviews metrics.
	pvBaseURL = "https://wikimedia.org/api/rest_v1/metrics/pageviews/per-article/en.wikipedia/all-access/user"
	// defaultMinInterval spaces requests; Wikimedia asks for ≤100 req/s from
	// anonymous clients, so 200ms is far politer than required.
	defaultMinInterval = 200 * time.Millisecond
)

// ErrNotFound means the article has no pageview data (unknown title or no
// views in range). Callers treat it as a resolution miss, never a failure.
var ErrNotFound = errors.New("wikimedia: article not found")

// DayViews is one article-day's user (non-bot) view count.
type DayViews struct {
	Day   string // YYYY-MM-DD
	Views int64
}

// Client is a paced Wikimedia pageviews client. Zero-value BaseURL falls back
// to production; tests override it with an httptest server.
type Client struct {
	BaseURL     string
	UA          string
	MinInterval time.Duration
	HTTP        *http.Client

	mu   sync.Mutex
	last time.Time
}

// New returns a Client wired to the production Wikimedia REST API with the
// shared declarative User-Agent.
func New() *Client {
	return &Client{
		UA:          edgar.ResolveUA(),
		BaseURL:     pvBaseURL,
		MinInterval: defaultMinInterval,
		HTTP:        &http.Client{Timeout: 15 * time.Second},
	}
}

func (c *Client) base() string {
	if c.BaseURL != "" {
		return strings.TrimSuffix(c.BaseURL, "/")
	}
	return pvBaseURL
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

// pvResp mirrors the pageviews response.
type pvResp struct {
	Items []struct {
		Article   string `json:"article"`
		Timestamp string `json:"timestamp"` // YYYYMMDD00
		Views     int64  `json:"views"`
	} `json:"items"`
}

// FetchDaily returns the article's daily user views for [from, to] (inclusive,
// UTC days) in ONE request. 404 → ErrNotFound.
func (c *Client) FetchDaily(ctx context.Context, article string, from, to time.Time) ([]DayViews, error) {
	if err := c.pace(ctx); err != nil {
		return nil, err
	}
	u := fmt.Sprintf("%s/%s/daily/%s00/%s00", c.base(),
		url.PathEscape(article), from.Format("20060102"), to.Format("20060102"))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.ua())
	req.Header.Set("Accept", "application/json")
	res, err := c.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close() //nolint:errcheck
	switch res.StatusCode {
	case http.StatusOK:
		// fall through to parse
	case http.StatusNotFound:
		io.Copy(io.Discard, res.Body) //nolint:errcheck
		return nil, ErrNotFound
	default:
		snippet, _ := io.ReadAll(io.LimitReader(res.Body, 256))
		return nil, fmt.Errorf("wikimedia: %s: status %d: %s", article, res.StatusCode, strings.TrimSpace(string(snippet)))
	}
	var pr pvResp
	if err := json.NewDecoder(res.Body).Decode(&pr); err != nil {
		return nil, fmt.Errorf("wikimedia: parse pageviews for %s: %w", article, err)
	}
	out := make([]DayViews, 0, len(pr.Items))
	for _, it := range pr.Items {
		ts := strings.TrimSpace(it.Timestamp)
		if len(ts) < 8 {
			continue
		}
		d, err := time.Parse("20060102", ts[:8])
		if err != nil {
			continue
		}
		out = append(out, DayViews{Day: d.Format("2006-01-02"), Views: it.Views})
	}
	return out, nil
}

// corpSuffixes are stripped (repeatedly) from the end of a company name when
// deriving Wikipedia article candidates. Order-insensitive; comparison is
// case-insensitive and tolerant of trailing commas/periods.
var corpSuffixes = []string{
	"inc", "inc.", "incorporated", "corp", "corp.", "corporation",
	"co", "co.", "company", "ltd", "ltd.", "limited", "plc", "p.l.c.",
	"holdings", "group", "sa", "s.a.", "nv", "n.v.", "ag", "se",
	"lp", "l.p.", "llc", "l.l.c.", "the",
}

// ArticleCandidates derives Wikipedia article-title candidates from an SEC
// company name: strip corporate suffixes, title-shape the remainder, convert
// spaces to underscores, and offer both "<Name>" and "<Name>_(company)".
// Pure function (heavily tested) — a wrong pick is possible by construction,
// which is why the caller labels the whole dataset a heuristic proxy.
func ArticleCandidates(name string) []string {
	base := StripCorpSuffixes(name)
	if base == "" {
		return nil
	}
	article := strings.ReplaceAll(base, " ", "_")
	return []string{article, article + "_(company)"}
}

// StripCorpSuffixes removes trailing corporate designators ("Inc.", "Corp",
// ", Ltd." …) from a company name, repeatedly, preserving the core name's
// original casing. Pure function.
func StripCorpSuffixes(name string) string {
	s := strings.Join(strings.Fields(strings.TrimSpace(name)), " ")
	for {
		trimmed := strings.TrimRight(s, " ,.")
		words := strings.Fields(trimmed)
		if len(words) <= 1 {
			return trimmed
		}
		last := strings.ToLower(strings.TrimRight(words[len(words)-1], ",."))
		matched := false
		for _, suf := range corpSuffixes {
			if last == strings.TrimRight(suf, ".") || last == suf {
				matched = true
				break
			}
		}
		if !matched {
			return trimmed
		}
		s = strings.Join(words[:len(words)-1], " ")
	}
}
