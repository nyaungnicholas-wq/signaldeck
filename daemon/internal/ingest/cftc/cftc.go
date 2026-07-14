// Package cftc ingests the CFTC's weekly Commitments of Traders (COT) legacy
// futures-only report from the free public Socrata API — no key, no
// registration (verified live 2026-07-10):
//
//	https://publicreporting.cftc.gov/resource/6dca-aqww.json
//
// Rows arrive as JSON objects with STRING-typed numbers; report_date is an
// ISO timestamp ("2026-07-07T00:00:00.000") normalized here to YYYY-MM-DD.
// The client fetches a recent report-date window server-side ($where +
// $limit) and leaves contract selection to the caller (client-side name
// filter — the Socrata dataset's naming is stable enough to substring-match
// but not worth encoding into a fragile SoQL expression).
//
// HONESTY (every consumer carries this): COT positioning is reported WEEKLY
// (Tuesday positions, published Friday — a 3-day lag) and is positioning, NOT
// prediction; large trader categories hedge as much as they speculate. Stored
// and served as descriptive context only, never a scored factor.
package cftc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/ingest/edgar"
)

// legacyURL is the Socrata resource for the legacy futures-only COT report.
const legacyURL = "https://publicreporting.cftc.gov/resource/6dca-aqww.json"

// maxRows caps one request; the dataset publishes ~250 markets/week, so a
// 90-day window (~13 weeks) stays well under it.
const maxRows = 5000

// Row is one contract's legacy futures-only COT report for one week.
type Row struct {
	Market       string  // market_and_exchange_names (full name incl. exchange)
	Contract     string  // contract_market_name (short name)
	ReportDate   string  // YYYY-MM-DD (Tuesday as-of date)
	NoncommLong  float64 // non-commercial (speculator) long contracts
	NoncommShort float64 // non-commercial short contracts
	CommLong     float64 // commercial (hedger) long contracts
	CommShort    float64 // commercial short contracts
	OpenInterest float64 // total open interest
}

// Client is a CFTC Socrata client. Zero-value BaseURL falls back to
// production; tests override it with an httptest server.
type Client struct {
	BaseURL string
	UA      string
	HTTP    *http.Client
}

// New returns a Client wired to the production Socrata endpoint with the
// shared declarative User-Agent.
func New() *Client {
	return &Client{
		UA:   edgar.ResolveUA(),
		HTTP: &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *Client) base() string {
	if c.BaseURL != "" {
		return c.BaseURL
	}
	return legacyURL
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

// rawRow mirrors the Socrata JSON (all values are strings).
type rawRow struct {
	Market       string `json:"market_and_exchange_names"`
	Contract     string `json:"contract_market_name"`
	ReportDate   string `json:"report_date_as_yyyy_mm_dd"`
	NoncommLong  string `json:"noncomm_positions_long_all"`
	NoncommShort string `json:"noncomm_positions_short_all"`
	CommLong     string `json:"comm_positions_long_all"`
	CommShort    string `json:"comm_positions_short_all"`
	OpenInterest string `json:"open_interest_all"`
}

// FetchWindow returns every legacy futures-only report row with since ≤
// report_date < until (server-side $where, $limit=5000; a zero `until` means
// no upper bound). Callers chunk long backfills into ≤~13-week windows so the
// ~250-market weekly dataset stays under the row cap. skipped counts rows
// whose required fields didn't parse (reported honestly, never hidden).
func (c *Client) FetchWindow(ctx context.Context, since, until time.Time) (rows []Row, skipped int, err error) {
	where := fmt.Sprintf("report_date_as_yyyy_mm_dd >= '%s'", since.Format("2006-01-02"))
	if !until.IsZero() {
		where += fmt.Sprintf(" AND report_date_as_yyyy_mm_dd < '%s'", until.Format("2006-01-02"))
	}
	q := url.Values{}
	q.Set("$limit", strconv.Itoa(maxRows))
	q.Set("$where", where)
	q.Set("$order", "report_date_as_yyyy_mm_dd")
	u := c.base() + "?" + q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("User-Agent", c.ua())
	req.Header.Set("Accept", "application/json")
	res, err := c.httpClient().Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer res.Body.Close() //nolint:errcheck
	if res.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(res.Body, 256))
		return nil, 0, fmt.Errorf("cftc: status %d: %s", res.StatusCode, strings.TrimSpace(string(snippet)))
	}
	var raw []rawRow
	if err := json.NewDecoder(res.Body).Decode(&raw); err != nil {
		return nil, 0, fmt.Errorf("cftc: parse socrata json: %w", err)
	}
	for _, r := range raw {
		day := normalizeReportDate(r.ReportDate)
		contract := strings.TrimSpace(r.Contract)
		ncl, e1 := strconv.ParseFloat(strings.TrimSpace(r.NoncommLong), 64)
		ncs, e2 := strconv.ParseFloat(strings.TrimSpace(r.NoncommShort), 64)
		cl, e3 := strconv.ParseFloat(strings.TrimSpace(r.CommLong), 64)
		cs, e4 := strconv.ParseFloat(strings.TrimSpace(r.CommShort), 64)
		oi, e5 := strconv.ParseFloat(strings.TrimSpace(r.OpenInterest), 64)
		if day == "" || contract == "" || e1 != nil || e2 != nil || e3 != nil || e4 != nil || e5 != nil {
			skipped++
			continue
		}
		rows = append(rows, Row{
			Market: strings.TrimSpace(r.Market), Contract: contract, ReportDate: day,
			NoncommLong: ncl, NoncommShort: ncs, CommLong: cl, CommShort: cs,
			OpenInterest: oi,
		})
	}
	return rows, skipped, nil
}

// normalizeReportDate turns Socrata's "2026-07-07T00:00:00.000" into
// "2026-07-07" ("" when malformed).
func normalizeReportDate(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, 'T'); i >= 0 {
		s = s[:i]
	}
	if _, err := time.Parse("2006-01-02", s); err != nil {
		return ""
	}
	return s
}
