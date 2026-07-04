// Package fred ingests free macro series from the St. Louis Fed (FRED).
//
// It uses the KEYLESS CSV endpoint (fredgraph.csv?id=<SERIES>) so it works with
// NO API key at all — the perfect fit for SignalDeck's free-data doctrine. An
// optional SIGNALDECK_FRED_KEY unlocks FRED's JSON API instead (same data,
// slightly fresher, but a key is never required). Series covered: VIXCLS (VIX
// close), DGS10 (10y Treasury yield), T10Y2Y (10y-2y spread), DFF (effective
// fed funds). Missing observations (FRED emits "." on holidays) are skipped, so
// a stored row always carries a real number.
//
// The worker degrades gracefully: an upstream error is returned to the runner
// (which records it as a worker error) but never corrupts the store, and a
// series with no valid rows is simply a no-op for that series.
package fred

import (
	"bufio"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// DefaultSeries is the macro set SignalDeck tracks by default.
var DefaultSeries = []string{"VIXCLS", "DGS10", "T10Y2Y", "DFF"}

// csvBase is the keyless CSV endpoint host+path (no key required).
const csvBase = "https://fred.stlouisfed.org/graph/fredgraph.csv"

// jsonBase is the keyed JSON API host+path (used only when a key is set).
const jsonBase = "https://api.stlouisfed.org/fred/series/observations"

// Client fetches FRED observations. Zero-value CSVBase/JSONBase fall back to the
// production hosts; tests override them to point at an httptest server. Key is
// optional: empty ⇒ keyless CSV path; set ⇒ JSON API path.
type Client struct {
	Key      string       // optional SIGNALDECK_FRED_KEY (JSON API); "" ⇒ keyless CSV
	CSVBase  string       // keyless CSV endpoint override (tests)
	JSONBase string       // keyed JSON endpoint override (tests)
	HTTP     *http.Client // defaults to a 30s-timeout client
}

// New returns a Client. A non-empty key switches it to the JSON API; an empty
// key keeps it on the keyless CSV endpoint (the default, no-key path).
func New(key string) *Client {
	return &Client{
		Key:      strings.TrimSpace(key),
		CSVBase:  csvBase,
		JSONBase: jsonBase,
		HTTP:     &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

func (c *Client) csvEndpoint() string {
	if c.CSVBase != "" {
		return c.CSVBase
	}
	return csvBase
}

func (c *Client) jsonEndpoint() string {
	if c.JSONBase != "" {
		return c.JSONBase
	}
	return jsonBase
}

// Fetch retrieves observations for one series as MacroPoints (day epoch + value)
// with missing/"." observations skipped. It uses the JSON API when a key is
// configured, else the keyless CSV endpoint. Returned points are in the
// upstream's order (chronological for FRED).
func (c *Client) Fetch(ctx context.Context, series string) ([]store.MacroPoint, error) {
	if c.Key != "" {
		return c.fetchJSON(ctx, series)
	}
	return c.fetchCSV(ctx, series)
}

// fetchCSV pulls the keyless fredgraph.csv?id=<series> file and parses it. The
// CSV shape is a header row (observation_date,<SERIES>) then rows of
// YYYY-MM-DD,<value|.>. Header column names have varied historically
// (observation_date vs DATE), so we key off column INDEX, not name.
func (c *Client) fetchCSV(ctx context.Context, series string) ([]store.MacroPoint, error) {
	q := url.Values{}
	q.Set("id", series)
	u := c.csvEndpoint() + "?" + q.Encode()

	body, err := c.get(ctx, u)
	if err != nil {
		return nil, err
	}
	defer body.Close() //nolint:errcheck

	r := csv.NewReader(bufio.NewReader(body))
	r.FieldsPerRecord = -1 // tolerate stray blank/ragged rows
	rows, err := r.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("fred: parse csv for %s: %w", series, err)
	}
	out := make([]store.MacroPoint, 0, len(rows))
	for i, row := range rows {
		if i == 0 || len(row) < 2 { // skip header + short rows
			continue
		}
		date := strings.TrimSpace(row[0])
		raw := strings.TrimSpace(row[1])
		if date == "" || raw == "" || raw == "." { // "." = missing observation
			continue
		}
		ts, ok := parseDay(date)
		if !ok {
			continue
		}
		val, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			continue // non-numeric ⇒ skip, don't fail the whole series
		}
		out = append(out, store.MacroPoint{Series: series, Ts: ts, Value: val})
	}
	return out, nil
}

// jsonResp is the shape of the FRED JSON observations API.
type jsonResp struct {
	Observations []struct {
		Date  string `json:"date"`
		Value string `json:"value"` // "." for missing
	} `json:"observations"`
}

// fetchJSON pulls observations via the keyed JSON API.
func (c *Client) fetchJSON(ctx context.Context, series string) ([]store.MacroPoint, error) {
	q := url.Values{}
	q.Set("series_id", series)
	q.Set("api_key", c.Key)
	q.Set("file_type", "json")
	u := c.jsonEndpoint() + "?" + q.Encode()

	body, err := c.get(ctx, u)
	if err != nil {
		return nil, err
	}
	defer body.Close() //nolint:errcheck

	var resp jsonResp
	if err := json.NewDecoder(body).Decode(&resp); err != nil {
		return nil, fmt.Errorf("fred: decode json for %s: %w", series, err)
	}
	out := make([]store.MacroPoint, 0, len(resp.Observations))
	for _, o := range resp.Observations {
		raw := strings.TrimSpace(o.Value)
		if raw == "" || raw == "." {
			continue
		}
		ts, ok := parseDay(strings.TrimSpace(o.Date))
		if !ok {
			continue
		}
		val, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			continue
		}
		out = append(out, store.MacroPoint{Series: series, Ts: ts, Value: val})
	}
	return out, nil
}

// get performs the GET, stamping a descriptive User-Agent (good manners for any
// public data source) and returning the body on 200. Non-200 is an error whose
// message includes a snippet of the body.
func (c *Client) get(ctx context.Context, u string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "SignalDeck/1.0 (free macro ingest; contact: local)")
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		resp.Body.Close() //nolint:errcheck
		return nil, fmt.Errorf("fred: status %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet)))
	}
	return resp.Body, nil
}

// Ingest fetches every series in list and upserts every observation, returning
// the number of rows inserted-or-seen. A per-series fetch error aborts that
// series but the caller decides whether to fail the whole run (Ingest returns
// the first error so callers degrade gracefully). Insert is idempotent, so
// re-runs are cheap.
func (c *Client) Ingest(ctx context.Context, st *store.Store, list []string) (int, error) {
	n := 0
	for _, series := range list {
		pts, err := c.Fetch(ctx, series)
		if err != nil {
			return n, fmt.Errorf("fred: fetch %s: %w", series, err)
		}
		for _, p := range pts {
			if err := st.InsertMacro(ctx, series, p.Ts, p.Value); err != nil {
				return n, fmt.Errorf("fred: insert %s@%d: %w", series, p.Ts, err)
			}
			n++
		}
	}
	return n, nil
}

// parseDay parses a FRED YYYY-MM-DD date into a UTC-midnight epoch. ok=false on
// a malformed date (skip the row rather than fail).
func parseDay(s string) (int64, bool) {
	t, err := time.ParseInLocation("2006-01-02", s, time.UTC)
	if err != nil {
		return 0, false
	}
	return t.Unix(), true
}
