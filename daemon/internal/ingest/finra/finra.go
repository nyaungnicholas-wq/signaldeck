// Package finra ingests FINRA's FREE Reg SHO DAILY short sale volume files —
// no API key, no registration (verified live 2026-07-06):
//
//	https://cdn.finra.org/equity/regsho/daily/CNMSshvolYYYYMMDD.txt
//
// (the "Consolidated NMS" file combining TRF/ADF-reported short-sale volume
// for exchange-listed securities; posted no later than ~6:00pm ET on the
// trade date per finra.org "Daily Short Sale Volume Files"). Format is
// pipe-delimited with a header row:
//
//	Date|Symbol|ShortVolume|ShortExemptVolume|TotalVolume|Market
//
// Volumes can be FRACTIONAL (fractional-share trades appear as-is in the
// live files), hence float64 throughout. FINRA's CDN answers a MISSING day
// (weekend/holiday/not-yet-published) with 403 — not 404 — so both map to
// ErrNotAvailable, which callers treat as an honest skip, never a failure.
//
// HONESTY (the classic retail trap this package refuses to feed): daily
// short-sale volume is NOT short interest. It counts trades where the SELLER
// was short — including market-maker liquidity provision — so a high
// short-volume ratio is NOT directly bearish. Every consumer of this data
// carries that caveat verbatim.
//
// Discipline mirrors internal/ingest/edgar: a declarative User-Agent (reused
// via edgar.ResolveUA()) on every request, a mutex-serialized min-interval
// pacer (default 500ms — far politer than needed for ~1 file/day + a one-time
// 30-file backfill), and graceful degradation everywhere.
package finra

import (
	"bufio"
	"context"
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
	// dailyBaseURL hosts the Consolidated NMS daily files (CNMSshvolYYYYMMDD.txt).
	dailyBaseURL = "https://cdn.finra.org/equity/regsho/daily"
	// defaultMinInterval spaces requests; there is no published FINRA CDN rate
	// limit, so this is plain politeness (a 30-file backfill takes ~15s).
	defaultMinInterval = 500 * time.Millisecond
	// dailyHeader is the exact expected header row; a mismatch means the
	// upstream format changed and MUST fail loudly, never parse garbage.
	dailyHeader = "Date|Symbol|ShortVolume|ShortExemptVolume|TotalVolume|Market"
)

// ErrNotAvailable means the requested day's file does not exist upstream —
// weekend, market holiday, or not yet published. FINRA's CDN returns 403 for
// absent files (verified live 2026-07-06), so 403 AND 404 both map here.
var ErrNotAvailable = errors.New("finra: daily file not available (weekend/holiday/not yet published)")

// Row is one symbol's aggregated short-sale volume for one trade date.
type Row struct {
	Day         string  // trade date, YYYY-MM-DD
	Symbol      string  // ticker exactly as FINRA lists it
	ShortVol    float64 // media-reported short volume (may be fractional)
	ShortExempt float64 // short-exempt volume
	TotalVol    float64 // total media-reported volume
}

// Ratio is the daily short sale volume ratio short/total, 0 when total<=0.
// It is a DESCRIPTIVE number — see the package caveat: NOT short interest.
func Ratio(shortVol, totalVol float64) float64 {
	if totalVol <= 0 {
		return 0
	}
	return shortVol / totalVol
}

// Client is a rate-limited FINRA CDN client. Zero-value BaseURL falls back to
// the production host; tests override it with an httptest server.
type Client struct {
	BaseURL     string
	UA          string
	MinInterval time.Duration
	HTTP        *http.Client

	mu   sync.Mutex
	last time.Time
}

// New returns a Client wired to FINRA's production CDN, reusing the SEC-style
// declarative User-Agent (edgar.ResolveUA(): SIGNALDECK_EDGAR_UA override,
// else "SignalDeck/0.1 (<contact email>)").
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

// pace blocks until minInterval has elapsed since the previous request —
// same mutex-serialized min-spacing limiter as the edgar client.
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

// DailyURL is the Consolidated NMS file URL for a trade date.
func (c *Client) DailyURL(day time.Time) string {
	return fmt.Sprintf("%s/CNMSshvol%s.txt", c.base(), day.Format("20060102"))
}

// FetchDaily downloads + parses one trade date's Consolidated NMS daily short
// sale volume file. A missing file (403/404 — weekend, holiday, not yet
// published) returns ErrNotAvailable; skipped is the count of malformed data
// lines tolerated (reported honestly, never hidden).
func (c *Client) FetchDaily(ctx context.Context, day time.Time) (rows []Row, skipped int, err error) {
	if err := c.pace(ctx); err != nil {
		return nil, 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.DailyURL(day), nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("User-Agent", c.ua())
	res, err := c.httpClient().Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer res.Body.Close() //nolint:errcheck
	switch res.StatusCode {
	case http.StatusOK:
		// fall through to parse
	case http.StatusForbidden, http.StatusNotFound:
		// FINRA's CDN 403s absent files; 404 handled for good measure.
		io.Copy(io.Discard, res.Body) //nolint:errcheck
		return nil, 0, ErrNotAvailable
	default:
		io.Copy(io.Discard, res.Body) //nolint:errcheck
		return nil, 0, fmt.Errorf("finra: GET %s: unexpected status %d", c.DailyURL(day), res.StatusCode)
	}
	return ParseDaily(res.Body)
}

// ParseDaily parses a pipe-delimited daily short sale volume file. The header
// row must match dailyHeader exactly (an upstream format change fails loudly);
// malformed data lines are skipped and counted, never silently dropped.
func ParseDaily(r io.Reader) (rows []Row, skipped int, err error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	if !sc.Scan() {
		if err := sc.Err(); err != nil {
			return nil, 0, err
		}
		return nil, 0, errors.New("finra: empty file")
	}
	header := strings.TrimSpace(sc.Text())
	if header != dailyHeader {
		return nil, 0, fmt.Errorf("finra: unexpected header %q (format change?)", header)
	}
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		f := strings.Split(line, "|")
		if len(f) == 1 {
			// The live files end with a bare record-count trailer line
			// (verified 2026-07-06: last line "12240" = the data-row count).
			// A lone integer is that trailer, not a malformed row.
			if _, err := strconv.Atoi(f[0]); err == nil {
				continue
			}
			skipped++
			continue
		}
		if len(f) < 5 {
			skipped++
			continue
		}
		day, dErr := normalizeDay(f[0])
		shortVol, sErr := strconv.ParseFloat(f[2], 64)
		exempt, eErr := strconv.ParseFloat(f[3], 64)
		total, tErr := strconv.ParseFloat(f[4], 64)
		sym := strings.TrimSpace(f[1])
		if dErr != nil || sErr != nil || eErr != nil || tErr != nil || sym == "" {
			skipped++
			continue
		}
		rows = append(rows, Row{
			Day: day, Symbol: strings.ToUpper(sym),
			ShortVol: shortVol, ShortExempt: exempt, TotalVol: total,
		})
	}
	return rows, skipped, sc.Err()
}

// normalizeDay converts the file's YYYYMMDD date to YYYY-MM-DD.
func normalizeDay(s string) (string, error) {
	s = strings.TrimSpace(s)
	t, err := time.Parse("20060102", s)
	if err != nil {
		return "", err
	}
	return t.Format("2006-01-02"), nil
}
