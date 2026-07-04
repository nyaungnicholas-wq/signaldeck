// Package congress ingests US congressional stock-transaction disclosures
// from the FREE public Stock Watcher mirrors (community JSON dumps of the
// Senate eFD and House Clerk disclosure systems on S3):
//
//	Senate: https://senate-stock-watcher-data.s3-us-west-2.amazonaws.com/aggregate/all_transactions.json
//	House:  https://house-stock-watcher-data.s3-us-west-2.amazonaws.com/data/all_transactions.json
//
// MIRROR STATUS (verified 2026-07-04 via Firecrawl): senatestockwatcher.com and
// housestockwatcher.com no longer resolve in DNS, and BOTH S3 objects above
// return 403 AccessDenied — the mirrors are currently DEAD. The client keeps
// the canonical URLs (overridable via SIGNALDECK_SENATE_TRADES_URL /
// SIGNALDECK_HOUSE_TRADES_URL in run.go) and the poller DEGRADES GRACEFULLY:
// a dead mirror records a dq event and an honest status note; it never fails
// the fleet and never fabricates data.
//
// HONESTY: this is public-domain government data (STOCK Act disclosures), free
// to store and show. It LAGS BY LAW — members have up to 30-45 days to file,
// so these are never real-time trades; the API note says so. Amounts are the
// RANGES reported on the disclosure ("$1,001 - $15,000"), not exact values.
package congress

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/ingest/edgar"
)

const (
	// DefaultSenateURL / DefaultHouseURL are the canonical mirror locations
	// (dead as of 2026-07-04 — see package comment — but kept as defaults so a
	// revived mirror starts working again with zero code change).
	DefaultSenateURL = "https://senate-stock-watcher-data.s3-us-west-2.amazonaws.com/aggregate/all_transactions.json"
	DefaultHouseURL  = "https://house-stock-watcher-data.s3-us-west-2.amazonaws.com/data/all_transactions.json"

	// defaultMinInterval spaces the (two) requests per run — plain politeness
	// toward a community mirror, mirroring the edgar limiter pattern.
	defaultMinInterval = 500 * time.Millisecond

	// maxBody caps a dump read (the real dumps are tens of MB; 256MB is a
	// hard safety stop against a misbehaving endpoint, not a real limit).
	maxBody = 256 << 20

	// maxAttempts bounds transient-failure retries per fetch.
	maxAttempts = 2
)

// Chamber names as stored.
const (
	ChamberSenate = "senate"
	ChamberHouse  = "house"
)

// Client fetches the two mirror dumps. Zero-value URLs fall back to the
// canonical mirrors; tests point them at httptest servers.
type Client struct {
	SenateURL   string
	HouseURL    string
	UA          string
	MinInterval time.Duration
	HTTP        *http.Client

	mu   sync.Mutex
	last time.Time
}

// New returns a Client wired to the canonical mirror URLs.
func New() *Client {
	return &Client{
		SenateURL: DefaultSenateURL,
		HouseURL:  DefaultHouseURL,
		// Same declarative identity as the edgar/fred clients (env-driven
		// contact email) — WAFs increasingly drop anonymous-looking UAs.
		UA:          edgar.ResolveUA(),
		MinInterval: defaultMinInterval,
		HTTP:        &http.Client{Timeout: 120 * time.Second},
	}
}

func (c *Client) ua() string {
	if c.UA != "" {
		return c.UA
	}
	return edgar.ResolveUA()
}

func (c *Client) senateURL() string {
	if c.SenateURL != "" {
		return c.SenateURL
	}
	return DefaultSenateURL
}

func (c *Client) houseURL() string {
	if c.HouseURL != "" {
		return c.HouseURL
	}
	return DefaultHouseURL
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

// pace blocks until minInterval has elapsed since the previous request
// (politeness spacing; respects ctx cancellation).
func (c *Client) pace(ctx context.Context) error {
	c.mu.Lock()
	wait := time.Until(c.last.Add(c.minInterval()))
	if wait > 0 {
		c.last = c.last.Add(c.minInterval())
	} else {
		c.last = time.Now()
	}
	c.mu.Unlock()
	if wait <= 0 {
		return nil
	}
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// get performs a paced GET with one retry on transient statuses. A non-200
// (including the mirrors' current 403 AccessDenied) is an error the caller
// degrades on — never a panic, never fabricated data.
func (c *Client) get(ctx context.Context, u string) ([]byte, error) {
	var lastErr error
	backoff := time.Second
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if err := c.pace(ctx); err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", c.ua())
		resp, err := c.httpClient().Do(req)
		if err != nil {
			lastErr = err
		} else {
			body, rerr := io.ReadAll(io.LimitReader(resp.Body, maxBody))
			resp.Body.Close() //nolint:errcheck
			if resp.StatusCode == http.StatusOK && rerr == nil {
				return body, nil
			}
			if rerr != nil {
				lastErr = rerr
			} else {
				snippet := strings.TrimSpace(string(body[:min(len(body), 160)]))
				lastErr = fmt.Errorf("congress: status %d from %s: %s", resp.StatusCode, u, snippet)
			}
			// Only transient statuses are worth a retry.
			if resp.StatusCode != http.StatusTooManyRequests &&
				resp.StatusCode != http.StatusServiceUnavailable &&
				resp.StatusCode != http.StatusBadGateway {
				return nil, lastErr
			}
		}
		t := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			t.Stop()
			return nil, ctx.Err()
		case <-t.C:
		}
		backoff *= 2
	}
	return nil, fmt.Errorf("congress: exhausted retries: %w", lastErr)
}

// Trade is one normalized congressional stock transaction.
type Trade struct {
	ID          string // deterministic content hash (dedup key)
	Chamber     string // senate | house
	Member      string // senator / representative name as disclosed
	Ticker      string // sanitized uppercase ticker (never empty here)
	Asset       string // asset description as disclosed
	TxType      string // normalized: purchase | sale_full | sale_partial | sale | exchange | …
	Amount      string // the disclosed RANGE ("$1,001 - $15,000"), never an exact value
	TxTs        int64  // transaction date (unix s); 0 = unparseable on the disclosure
	DisclosedTs int64  // disclosure/filing date (unix s); lags the trade 30-45d by law
}

// senateRow mirrors one entry of the Senate mirror's all_transactions.json.
type senateRow struct {
	TransactionDate string `json:"transaction_date"` // MM/DD/YYYY
	Owner           string `json:"owner"`
	Ticker          string `json:"ticker"`
	AssetDesc       string `json:"asset_description"`
	AssetType       string `json:"asset_type"`
	Type            string `json:"type"` // "Purchase" / "Sale (Full)" / …
	Amount          string `json:"amount"`
	Senator         string `json:"senator"`
	DisclosureDate  string `json:"disclosure_date"` // MM/DD/YYYY
}

// houseRow mirrors one entry of the House mirror's all_transactions.json.
type houseRow struct {
	DisclosureDate  string `json:"disclosure_date"`  // MM/DD/YYYY
	TransactionDate string `json:"transaction_date"` // YYYY-MM-DD
	Owner           string `json:"owner"`
	Ticker          string `json:"ticker"`
	AssetDesc       string `json:"asset_description"`
	Type            string `json:"type"` // "purchase" / "sale_full" / …
	Amount          string `json:"amount"`
	Representative  string `json:"representative"`
}

// ParseSenate parses the Senate mirror dump. Rows without a usable ticker
// (bonds, funds, "--") are dropped — this feed is the STOCK-transaction view.
// Pure function; fixture-tested.
func ParseSenate(data []byte) ([]Trade, error) {
	var rows []senateRow
	if err := json.Unmarshal(data, &rows); err != nil {
		return nil, fmt.Errorf("congress: parse senate dump: %w", err)
	}
	out := make([]Trade, 0, len(rows))
	for _, r := range rows {
		ticker := SanitizeTicker(r.Ticker)
		if ticker == "" {
			continue
		}
		t := Trade{
			Chamber: ChamberSenate,
			Member:  strings.TrimSpace(r.Senator),
			Ticker:  ticker,
			Asset:   strings.TrimSpace(r.AssetDesc),
			TxType:  NormalizeTxType(r.Type),
			Amount:  strings.TrimSpace(r.Amount),
		}
		t.TxTs, _ = parseUSDate(r.TransactionDate)
		t.DisclosedTs, _ = parseUSDate(r.DisclosureDate)
		t.ID = TradeID(t.Chamber, t.Member, t.Ticker, t.TxType, t.Amount,
			r.TransactionDate, r.DisclosureDate, r.Owner)
		out = append(out, t)
	}
	return out, nil
}

// ParseHouse parses the House mirror dump; same drop-if-no-ticker rule.
func ParseHouse(data []byte) ([]Trade, error) {
	var rows []houseRow
	if err := json.Unmarshal(data, &rows); err != nil {
		return nil, fmt.Errorf("congress: parse house dump: %w", err)
	}
	out := make([]Trade, 0, len(rows))
	for _, r := range rows {
		ticker := SanitizeTicker(r.Ticker)
		if ticker == "" {
			continue
		}
		t := Trade{
			Chamber: ChamberHouse,
			Member:  strings.TrimSpace(r.Representative),
			Ticker:  ticker,
			Asset:   strings.TrimSpace(r.AssetDesc),
			TxType:  NormalizeTxType(r.Type),
			Amount:  strings.TrimSpace(r.Amount),
		}
		t.TxTs, _ = parseISODate(r.TransactionDate)
		t.DisclosedTs, _ = parseUSDate(r.DisclosureDate)
		t.ID = TradeID(t.Chamber, t.Member, t.Ticker, t.TxType, t.Amount,
			r.TransactionDate, r.DisclosureDate, r.Owner)
		out = append(out, t)
	}
	return out, nil
}

// FetchSenate downloads and parses the Senate mirror.
func (c *Client) FetchSenate(ctx context.Context) ([]Trade, error) {
	body, err := c.get(ctx, c.senateURL())
	if err != nil {
		return nil, err
	}
	return ParseSenate(body)
}

// FetchHouse downloads and parses the House mirror.
func (c *Client) FetchHouse(ctx context.Context) ([]Trade, error) {
	body, err := c.get(ctx, c.houseURL())
	if err != nil {
		return nil, err
	}
	return ParseHouse(body)
}

// TradeID is the deterministic dedup hash over the disclosure's identifying
// fields. Identical rows (re-downloads, mirror duplicates) collapse to one;
// any differing field (date, amount, owner, type) yields a distinct ID.
func TradeID(fields ...string) string {
	h := sha256.New()
	for _, f := range fields {
		h.Write([]byte(strings.ToUpper(strings.TrimSpace(f))))
		h.Write([]byte{0}) // field separator so ("ab","c") != ("a","bc")
	}
	return hex.EncodeToString(h.Sum(nil))[:40]
}

// htmlTagRe strips markup — some mirror rows historically carried tickers
// wrapped in <a> tags.
var htmlTagRe = regexp.MustCompile(`<[^>]*>`)

// SanitizeTicker uppercases and strips markup; placeholder values ("--",
// "N/A") and implausibly long strings map to "" (no usable ticker).
func SanitizeTicker(s string) string {
	s = htmlTagRe.ReplaceAllString(s, "")
	s = strings.ToUpper(strings.TrimSpace(s))
	switch s {
	case "", "--", "N/A", "NA", "NONE", "UNKNOWN":
		return ""
	}
	if len(s) > 12 { // real US tickers (incl. BRK.B style) are far shorter
		return ""
	}
	return s
}

// NormalizeTxType maps both mirrors' spellings onto one vocabulary:
// "Purchase"→purchase, "Sale (Full)"/"sale_full"→sale_full,
// "Sale (Partial)"/"sale_partial"→sale_partial, bare "Sale"→sale,
// "Exchange"→exchange; anything else is lowercased with spaces→underscores
// (stored raw-ish, never invented).
func NormalizeTxType(s string) string {
	t := strings.ToLower(strings.TrimSpace(s))
	t = strings.NewReplacer("(", "", ")", "", " ", "_").Replace(t)
	t = strings.Trim(t, "_")
	for strings.Contains(t, "__") {
		t = strings.ReplaceAll(t, "__", "_")
	}
	switch {
	case strings.HasPrefix(t, "purchase"):
		return "purchase"
	case strings.HasPrefix(t, "sale_full"):
		return "sale_full"
	case strings.HasPrefix(t, "sale_partial"):
		return "sale_partial"
	case strings.HasPrefix(t, "sale"):
		return "sale"
	case strings.HasPrefix(t, "exchange"):
		return "exchange"
	}
	return t
}

// parseUSDate parses MM/DD/YYYY to a UTC-midnight epoch.
func parseUSDate(s string) (int64, bool) {
	t, err := time.ParseInLocation("01/02/2006", strings.TrimSpace(s), time.UTC)
	if err != nil {
		return 0, false
	}
	return t.Unix(), true
}

// parseISODate parses YYYY-MM-DD to a UTC-midnight epoch.
func parseISODate(s string) (int64, bool) {
	t, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(s), time.UTC)
	if err != nil {
		return 0, false
	}
	return t.Unix(), true
}
