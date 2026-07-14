// FINRA BI-MONTHLY SHORT INTEREST (appended file — sibling of the daily Reg
// SHO short-volume client in finra.go, same discipline, DIFFERENT dataset).
//
//	https://cdn.finra.org/equity/otcmarket/biweekly/shrtYYYYMMDD.csv
//
// (verified live 2026-07-10 with shrt20260615.csv). YYYYMMDD is the SETTLEMENT
// date — the 15th and the last day of each month. Despite the .csv name the
// file is PIPE-delimited with a header row (see siHeader). Publication lags
// settlement by roughly 9 business days (~2 weeks), and FINRA's CDN answers a
// not-yet-published or non-existent settlement date with 403 (same behavior as
// the daily files) — both 403 and 404 map to ErrNotAvailable, an honest skip.
//
// HONESTY (every consumer carries this): short interest here is
// settlement-dated and published ~2 weeks late — DESCRIPTIVE positioning
// context, never a live signal and not advice. Unlike the daily short-sale
// VOLUME ratio (finra.go's classic trap), this IS actual short interest, but
// its staleness is the trap: by the time a file is public the positions are
// two weeks old.
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
	// siBaseURL hosts the bi-monthly short interest files (shrtYYYYMMDD.csv).
	siBaseURL = "https://cdn.finra.org/equity/otcmarket/biweekly"
	// siHeader is the exact expected header row; a mismatch means the upstream
	// format changed and MUST fail loudly, never parse garbage.
	siHeader = "accountingYearMonthNumber|symbolCode|issueName|issuerServicesGroupExchangeCode|marketClassCode|currentShortPositionQuantity|previousShortPositionQuantity|stockSplitFlag|averageDailyVolumeQuantity|daysToCoverQuantity|revisionFlag|changePercent|changePreviousNumber|settlementDate"
)

// SIRow is one symbol's bi-monthly short interest for one settlement date.
type SIRow struct {
	Symbol      string  // ticker exactly as FINRA lists it (uppercased)
	Settlement  string  // settlement date, YYYY-MM-DD
	ShortQty    float64 // current short position quantity (shares)
	PrevQty     float64 // previous period's short position quantity
	ADV         float64 // average daily volume quantity
	DaysToCover float64 // FINRA's own days-to-cover figure
	ChangePct   float64 // FINRA's period-over-period change percent
}

// SIClient is a rate-limited client for the bi-monthly short interest files.
// Zero-value BaseURL falls back to production; tests override it with an
// httptest server. Pacing mirrors the daily Client: mutex-serialized
// min-interval spacing (the worker fetches at most a handful of files/run).
type SIClient struct {
	BaseURL     string
	UA          string
	MinInterval time.Duration
	HTTP        *http.Client

	mu   sync.Mutex
	last time.Time
}

// NewSI returns an SIClient wired to FINRA's production CDN, reusing the
// SEC-style declarative User-Agent (edgar.ResolveUA()).
func NewSI() *SIClient {
	return &SIClient{
		UA:          edgar.ResolveUA(),
		BaseURL:     siBaseURL,
		MinInterval: defaultMinInterval,
		HTTP:        &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *SIClient) base() string {
	if c.BaseURL != "" {
		return strings.TrimSuffix(c.BaseURL, "/")
	}
	return siBaseURL
}

func (c *SIClient) ua() string {
	if c.UA != "" {
		return c.UA
	}
	return edgar.ResolveUA()
}

func (c *SIClient) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

func (c *SIClient) minInterval() time.Duration {
	if c.MinInterval > 0 {
		return c.MinInterval
	}
	return defaultMinInterval
}

// pace blocks until minInterval has elapsed since the previous request.
func (c *SIClient) pace(ctx context.Context) error {
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

// SIURL is the bi-monthly file URL for a settlement date.
func (c *SIClient) SIURL(settlement time.Time) string {
	return fmt.Sprintf("%s/shrt%s.csv", c.base(), settlement.Format("20060102"))
}

// FetchShortInterest downloads + parses one settlement date's bi-monthly short
// interest file. A missing file (403/404 — not yet published ~9 business days
// after settlement, or no such settlement date) returns ErrNotAvailable;
// skipped counts malformed data lines tolerated (reported, never hidden).
func (c *SIClient) FetchShortInterest(ctx context.Context, settlement time.Time) (rows []SIRow, skipped int, err error) {
	if err := c.pace(ctx); err != nil {
		return nil, 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.SIURL(settlement), nil)
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
		io.Copy(io.Discard, res.Body) //nolint:errcheck
		return nil, 0, ErrNotAvailable
	default:
		io.Copy(io.Discard, res.Body) //nolint:errcheck
		return nil, 0, fmt.Errorf("finra: GET %s: unexpected status %d", c.SIURL(settlement), res.StatusCode)
	}
	return ParseShortInterest(res.Body)
}

// ParseShortInterest parses a pipe-delimited bi-monthly short interest file.
// The header row must match siHeader exactly (an upstream format change fails
// loudly); malformed data lines are skipped and counted. Optional fields
// (prevQty/adv/daysToCover/changePct) parse leniently — an empty cell becomes
// 0, matching the live files where new listings have no previous period.
func ParseShortInterest(r io.Reader) (rows []SIRow, skipped int, err error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	if !sc.Scan() {
		if err := sc.Err(); err != nil {
			return nil, 0, err
		}
		return nil, 0, errors.New("finra: empty short interest file")
	}
	header := strings.TrimSpace(sc.Text())
	if header != siHeader {
		return nil, 0, fmt.Errorf("finra: unexpected short interest header %q (format change?)", header)
	}
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		f := strings.Split(line, "|")
		if len(f) == 1 {
			// Trailer tolerance mirrors the daily parser: a lone integer is a
			// record-count line, not a malformed row.
			if _, err := strconv.Atoi(f[0]); err == nil {
				continue
			}
			skipped++
			continue
		}
		if len(f) < 14 {
			skipped++
			continue
		}
		sym := strings.ToUpper(strings.TrimSpace(f[1]))
		shortQty, qErr := strconv.ParseFloat(strings.TrimSpace(f[5]), 64)
		settle := strings.TrimSpace(f[13])
		if sym == "" || qErr != nil || !validDay(settle) {
			skipped++
			continue
		}
		rows = append(rows, SIRow{
			Symbol: sym, Settlement: settle, ShortQty: shortQty,
			PrevQty:     lenientFloat(f[6]),
			ADV:         lenientFloat(f[8]),
			DaysToCover: lenientFloat(f[9]),
			ChangePct:   lenientFloat(f[11]),
		})
	}
	return rows, skipped, sc.Err()
}

// lenientFloat parses an optional numeric cell; empty/malformed → 0 (the live
// files leave prev-period cells blank for new listings).
func lenientFloat(s string) float64 {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0
	}
	return v
}

// validDay reports whether s is a YYYY-MM-DD date.
func validDay(s string) bool {
	_, err := time.Parse("2006-01-02", s)
	return err == nil
}

// SettlementDates returns the most recent `n` bi-monthly settlement dates
// (the 15th and the last day of each month) that are ≤ now, newest first.
// Pure function — the worker's publication-lag probing is built on it.
func SettlementDates(now time.Time, n int) []time.Time {
	if n <= 0 {
		return nil
	}
	out := make([]time.Time, 0, n)
	y, m := now.Year(), now.Month()
	for len(out) < n {
		eom := time.Date(y, m+1, 0, 0, 0, 0, 0, time.UTC) // day 0 of next month = EOM
		mid := time.Date(y, m, 15, 0, 0, 0, 0, time.UTC)
		for _, d := range []time.Time{eom, mid} {
			if len(out) < n && !d.After(now) {
				out = append(out, d)
			}
		}
		m--
		if m < time.January {
			m = time.December
			y--
		}
	}
	return out
}
