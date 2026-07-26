// Package edgar ingests free company fundamentals from SEC EDGAR — no API key,
// but SEC policy REQUIRES a descriptive User-Agent header and rate-limits
// clients to <=10 requests/second. This client stamps a UA on every request,
// spaces requests with a token-free min-interval limiter (default 150ms ⇒ well
// under 10/s), and retries a 429/503 with exponential backoff. Everything
// degrades gracefully: a missing key isn't a concept here (there is no key),
// and an unavailable upstream returns an error the worker records but which
// never corrupts the store.
//
// Two endpoints:
//  1. company_tickers.json  — ticker → CIK map (fetched once, then cached in
//     the store's meta table for a day so we don't re-pull it per symbol).
//  2. companyfacts/CIK##########.json — XBRL company facts; we extract a few
//     headline metrics (Revenues, EPS, shares outstanding) plus the latest
//     filing date, taking the most recent annual/quarterly value of each.
package edgar

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

const (
	tickersURL   = "https://www.sec.gov/files/company_tickers.json"
	factsBaseURL = "https://data.sec.gov/api/xbrl/companyfacts"
	// defaultContact is the last-resort contact when neither SIGNALDECK_EDGAR_UA
	// nor SIGNALDECK_CONTACT_EMAIL is configured. SEC's WAF 403s UAs it cannot
	// attribute — set a REAL deliverable email in daemon/.env for production.
	defaultContact = "signaldeck@localhost"
	// defaultMinInterval spaces requests to stay comfortably under SEC's 10/s
	// ceiling (150ms ⇒ ~6.6/s), leaving headroom for other clients on the host.
	defaultMinInterval = 150 * time.Millisecond
	// maxRetries bounds 429/503 backoff attempts.
	maxRetries = 3
)

// Client is a rate-limited, backoff-retrying SEC EDGAR client. Zero-value
// TickersURL/FactsBase fall back to the production hosts; tests override them.
// The mutex + last serialize the min-interval limiter across goroutines (the
// worker calls sequentially, but this keeps it correct if ever parallelized).
type Client struct {
	UA          string
	TickersURL  string
	FactsBase   string
	// ExchangeURL overrides the company_tickers_exchange.json endpoint (the
	// companies-directory map; see companies.go). Zero value = production.
	ExchangeURL string
	// BulkURL overrides the nightly bulk submissions.zip endpoint (the SIC
	// bulk sync; see bulk.go). Zero value = production.
	BulkURL string
	MinInterval time.Duration
	HTTP        *http.Client

	mu   sync.Mutex
	last time.Time
}

// New returns a Client wired to SEC's production hosts.
func New() *Client {
	return &Client{
		UA:          ResolveUA(),
		TickersURL:  tickersURL,
		FactsBase:   factsBaseURL,
		MinInterval: defaultMinInterval,
		HTTP:        &http.Client{Timeout: 30 * time.Second},
	}
}

// ResolveUA builds the SEC-compliant declarative User-Agent. SEC's fair-access
// policy (and its Akamai WAF, which 403s anonymous-looking clients) requires a
// UA that identifies the app AND a deliverable contact, e.g.
// "Sample Company Name AdminContact@sample.com". Precedence:
//  1. SIGNALDECK_EDGAR_UA — full override, used verbatim.
//  2. SIGNALDECK_CONTACT_EMAIL — "SignalDeck/0.1 (<email>)".
//  3. Fallback "SignalDeck/0.1 (signaldeck@localhost)" — works only until the
//     WAF decides otherwise; configure a real email in daemon/.env.
//
// config.Load() exports both vars from daemon/.env into the process env (the
// daemon runs under launchd, which loads no .env), so this sees them either way.
func ResolveUA() string {
	if ua := strings.TrimSpace(os.Getenv("SIGNALDECK_EDGAR_UA")); ua != "" {
		return ua
	}
	contact := strings.TrimSpace(os.Getenv("SIGNALDECK_CONTACT_EMAIL"))
	if contact == "" {
		contact = defaultContact
	}
	return "SignalDeck/0.1 (" + contact + ")"
}

func (c *Client) ua() string {
	if c.UA != "" {
		return c.UA
	}
	return ResolveUA()
}

func (c *Client) tickersEndpoint() string {
	if c.TickersURL != "" {
		return c.TickersURL
	}
	return tickersURL
}

func (c *Client) factsEndpoint() string {
	if c.FactsBase != "" {
		return strings.TrimSuffix(c.FactsBase, "/")
	}
	return factsBaseURL
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

// pace blocks until at least minInterval has elapsed since the previous
// request, then records the new request time — a simple min-spacing rate
// limiter honoring SEC's <=10 req/s policy. It respects ctx cancellation.
func (c *Client) pace(ctx context.Context) error {
	c.mu.Lock()
	wait := time.Until(c.last.Add(c.minInterval()))
	// Reserve this slot immediately so concurrent callers space off each other.
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

// get performs a paced, backoff-retrying GET and returns the body bytes on 200.
// 429/503 are retried with exponential backoff; other non-200s error out.
func (c *Client) get(ctx context.Context, u string) ([]byte, error) {
	var lastErr error
	backoff := 500 * time.Millisecond
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if err := c.pace(ctx); err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", c.ua())
		// A permissive Accept keeps SEC's WAF happy on JSON endpoints while
		// remaining correct for the XML/HTML archive documents we also fetch.
		req.Header.Set("Accept", "application/json, text/html, application/xml;q=0.9, */*;q=0.8")
		// Deliberately do NOT set Accept-Encoding: Go's transport adds gzip and
		// transparently decompresses only when it owns that header. Setting it
		// ourselves would hand back a compressed body we'd have to unzip.
		resp, err := c.httpClient().Do(req)
		if err != nil {
			lastErr = err
			// network error — back off and retry
			if !sleep(ctx, backoff) {
				return nil, ctx.Err()
			}
			backoff *= 2
			continue
		}
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable {
			resp.Body.Close() //nolint:errcheck
			lastErr = fmt.Errorf("edgar: status %d (retrying)", resp.StatusCode)
			if !sleep(ctx, backoff) {
				return nil, ctx.Err()
			}
			backoff *= 2
			continue
		}
		if resp.StatusCode != http.StatusOK {
			snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
			resp.Body.Close() //nolint:errcheck
			return nil, fmt.Errorf("edgar: status %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet)))
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close() //nolint:errcheck
		if err != nil {
			return nil, err
		}
		return body, nil
	}
	return nil, fmt.Errorf("edgar: exhausted retries: %w", lastErr)
}

// sleep waits d respecting ctx; returns false if ctx was canceled.
func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// tickerRow is one entry in company_tickers.json (the JSON is an object keyed by
// stringified row index, each value shaped like this).
type tickerRow struct {
	CIK    int64  `json:"cik_str"`
	Ticker string `json:"ticker"`
	Title  string `json:"title"`
}

// CIKMap fetches (and returns) the ticker→CIK map. The keys are uppercased
// tickers; the values are zero-padded-friendly int CIKs.
func (c *Client) CIKMap(ctx context.Context) (map[string]int64, error) {
	body, err := c.get(ctx, c.tickersEndpoint())
	if err != nil {
		return nil, err
	}
	// The file is a JSON object of {"0": {...}, "1": {...}, ...}.
	var raw map[string]tickerRow
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("edgar: parse company_tickers.json: %w", err)
	}
	out := make(map[string]int64, len(raw))
	for _, row := range raw {
		if row.Ticker == "" || row.CIK == 0 {
			continue
		}
		out[strings.ToUpper(row.Ticker)] = row.CIK
	}
	return out, nil
}

// Fundamentals is the extracted headline company-facts for one symbol.
type Fundamentals struct {
	CIK               int64
	Revenues          *Fact // latest annual/quarterly revenue
	EPS               *Fact // latest diluted EPS
	SharesOutstanding *Fact // latest common shares outstanding
	EntityPublicFloat *Fact // latest public float (USD, dei cover-page fact)
	LatestFilingDate  int64 // most recent "filed" date across facts (epoch)
}

// Fact is one XBRL data point: a value and the period-end it reports.
type Fact struct {
	Value float64
	AsOf  int64 // period end, epoch seconds
}

// factsResp is the (partial) shape of the companyfacts JSON we consume.
type factsResp struct {
	CIK   flexInt64 `json:"cik"`
	Facts struct {
		USGAAP map[string]conceptUnits `json:"us-gaap"`
		DEI    map[string]conceptUnits `json:"dei"`
	} `json:"facts"`
}

// flexInt64 accepts a JSON number OR a quoted number.
//
// EDGAR emits companyfacts `cik` both ways — most issuers as a bare number, some
// as a string — and a plain int64 field makes the string form a hard parse
// failure for the WHOLE document. That was costing ~106 fundamentals_error dq
// events a day, each one discarding an entire issuer's fundamentals over a field
// we barely use (the CIK is already known: it is what we requested). Accepting
// both shapes is the correct read of a provider that has never promised one.
//
// A malformed or absent value yields 0, which the caller already treats as
// "keep the CIK we asked with" — so a bad cik can never silently retarget the
// fundamentals onto a different company.
type flexInt64 int64

func (f *flexInt64) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "null" || s == `""` || s == "" {
		*f = 0
		return nil
	}
	s = strings.Trim(s, `"`)
	if s == "" {
		*f = 0
		return nil
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		// Not fatal: the document is still usable and the CIK is already known.
		*f = 0
		return nil
	}
	*f = flexInt64(n)
	return nil
}

type conceptUnits struct {
	Units map[string][]unitPoint `json:"units"`
}

type unitPoint struct {
	End   string  `json:"end"`   // period end YYYY-MM-DD
	Val   float64 `json:"val"`   // reported value
	Form  string  `json:"form"`  // 10-K / 10-Q / …
	Filed string  `json:"filed"` // filing date YYYY-MM-DD
	FP    string  `json:"fp"`    // FY / Q1 / …
	Frame *string `json:"frame"` // present on standardized frames
}

// CIKPadded formats a CIK as SEC's 10-digit zero-padded string (CIK0000320193).
func CIKPadded(cik int64) string {
	return fmt.Sprintf("CIK%010d", cik)
}

// CompanyFacts fetches and extracts headline fundamentals for a CIK. Concepts
// are tried in preference order (issuers tag revenue differently); the LATEST
// period-end value of the first matching concept wins. Missing concepts are
// simply left nil — a company that never tags EPS just has no EPS fact.
func (c *Client) CompanyFacts(ctx context.Context, cik int64) (Fundamentals, error) {
	u := fmt.Sprintf("%s/%s.json", c.factsEndpoint(), CIKPadded(cik))
	body, err := c.get(ctx, u)
	if err != nil {
		return Fundamentals{}, err
	}
	var resp factsResp
	if err := json.Unmarshal(body, &resp); err != nil {
		return Fundamentals{}, fmt.Errorf("edgar: parse companyfacts CIK %d: %w", cik, err)
	}
	out := Fundamentals{CIK: cik}
	if out.CIK == 0 {
		out.CIK = int64(resp.CIK)
	}

	// Revenue: issuers use several concept names; take the first that resolves.
	revConcepts := []string{
		"RevenueFromContractWithCustomerExcludingAssessedTax",
		"Revenues",
		"SalesRevenueNet",
		"RevenueFromContractWithCustomerIncludingAssessedTax",
	}
	out.Revenues = latestFact(resp.Facts.USGAAP, revConcepts, "USD")

	// EPS: prefer diluted, fall back to basic.
	epsConcepts := []string{"EarningsPerShareDiluted", "EarningsPerShareBasic"}
	out.EPS = latestFact(resp.Facts.USGAAP, epsConcepts, "USD/shares")

	// Shares outstanding: dei carries the entity-level cover-page count; us-gaap
	// carries the balance-sheet count. Try dei first (usually freshest).
	shareConcepts := []string{"EntityCommonStockSharesOutstanding"}
	out.SharesOutstanding = latestFact(resp.Facts.DEI, shareConcepts, "shares")
	if out.SharesOutstanding == nil {
		out.SharesOutstanding = latestFact(resp.Facts.USGAAP,
			[]string{"CommonStockSharesOutstanding", "WeightedAverageNumberOfDilutedSharesOutstanding"}, "shares")
	}

	// Public float: a dei cover-page fact in USD (companies-directory wave).
	// Missing on funds/foreign filers — stays nil, honest absence.
	out.EntityPublicFloat = latestFact(resp.Facts.DEI, []string{"EntityPublicFloat"}, "USD")

	// Latest filing date across every point we looked at (best-effort recency).
	out.LatestFilingDate = latestFiled(resp.Facts.USGAAP, resp.Facts.DEI)
	return out, nil
}

// latestFact returns the most-recent (by period end) value of the first concept
// in prefer that exists under the given unit, or nil if none match.
func latestFact(concepts map[string]conceptUnits, prefer []string, unit string) *Fact {
	for _, name := range prefer {
		cu, ok := concepts[name]
		if !ok {
			continue
		}
		pts, ok := cu.Units[unit]
		if !ok || len(pts) == 0 {
			continue
		}
		best := (*unitPoint)(nil)
		var bestTs int64
		for i := range pts {
			ts, ok := parseDay(pts[i].End)
			if !ok {
				continue
			}
			if best == nil || ts > bestTs {
				best, bestTs = &pts[i], ts
			}
		}
		if best != nil {
			return &Fact{Value: best.Val, AsOf: bestTs}
		}
	}
	return nil
}

// latestFiled returns the max "filed" date across all points in the maps.
func latestFiled(maps ...map[string]conceptUnits) int64 {
	var max int64
	for _, m := range maps {
		for _, cu := range m {
			for _, pts := range cu.Units {
				for i := range pts {
					if ts, ok := parseDay(pts[i].Filed); ok && ts > max {
						max = ts
					}
				}
			}
		}
	}
	return max
}

// Ingest resolves CIKs for the given stock symbols and upserts their headline
// fundamentals. It fetches the ticker map once, then one companyfacts request
// per symbol (paced). A per-symbol failure is recorded as a dq_event and
// skipped — one delisted/absent issuer never aborts the whole sweep. Returns
// the number of metric rows written and the number of symbols with no coverage.
func (c *Client) Ingest(ctx context.Context, st *store.Store, syms []md.Symbol) (written, missing int, err error) {
	cikMap, err := c.cachedCIKMap(ctx, st)
	if err != nil {
		return 0, 0, err
	}
	now := time.Now().Unix()
	// Deterministic order for stable pacing/tests.
	sort.Slice(syms, func(i, j int) bool { return syms[i].Symbol < syms[j].Symbol })
	for _, s := range syms {
		cik, ok := cikMap[strings.ToUpper(s.Symbol)]
		if !ok {
			missing++
			continue
		}
		facts, ferr := c.CompanyFacts(ctx, cik)
		if ferr != nil {
			missing++
			// Record but continue — graceful degradation on one bad symbol.
			id := s.ID
			_ = st.InsertDQ(ctx, md.DQEvent{
				SymbolID: &id,
				Ts:       time.Now().Unix(),
				Kind:     "fundamentals_error",
				Detail:   fmt.Sprintf("%s: %v", s.Symbol, ferr),
			})
			continue
		}
		rows := factRows(s.ID, cik, facts, now)
		for _, r := range rows {
			if uerr := st.UpsertFundamental(ctx, r); uerr != nil {
				return written, missing, fmt.Errorf("edgar: upsert %s %s: %w", s.Symbol, r.Metric, uerr)
			}
			written++
		}
	}
	return written, missing, nil
}

// cachedCIKMap returns the ticker→CIK map, caching it in the store's meta table
// for a day so a 24h sweep doesn't re-pull the (large) file every symbol.
func (c *Client) cachedCIKMap(ctx context.Context, st *store.Store) (map[string]int64, error) {
	const key = "edgar_cik_map"
	const dayKey = "edgar_cik_map_day"
	today := time.Now().UTC().Format("2006-01-02")
	if day, _ := st.GetMeta(ctx, dayKey); day == today {
		if raw, _ := st.GetJSONRaw(ctx, key); raw != "" {
			var m map[string]int64
			if json.Unmarshal([]byte(raw), &m) == nil && len(m) > 0 {
				return m, nil
			}
		}
	}
	m, err := c.CIKMap(ctx)
	if err != nil {
		// Fall back to any stale cached copy rather than failing the sweep.
		if raw, _ := st.GetJSONRaw(ctx, key); raw != "" {
			var cached map[string]int64
			if json.Unmarshal([]byte(raw), &cached) == nil && len(cached) > 0 {
				return cached, nil
			}
		}
		return nil, err
	}
	_ = st.SetJSON(ctx, key, m)
	_ = st.SetMeta(ctx, dayKey, today)
	return m, nil
}

// factRows maps extracted Fundamentals to store rows. Present-only: a nil fact
// produces no row (honest — we never fabricate a zero).
func factRows(symbolID, cik int64, f Fundamentals, fetchedAt int64) []store.FundamentalRow {
	var out []store.FundamentalRow
	add := func(metric string, fact *Fact) {
		if fact == nil {
			return
		}
		out = append(out, store.FundamentalRow{
			SymbolID: symbolID, Metric: metric, Value: fact.Value,
			AsOf: fact.AsOf, FetchedAt: fetchedAt,
		})
	}
	add("Revenues", f.Revenues)
	add("EPS", f.EPS)
	add("SharesOutstanding", f.SharesOutstanding)
	add("EntityPublicFloat", f.EntityPublicFloat)
	// CIK + latest filing date are recorded with as_of = the fact date so they
	// upsert cleanly; store the CIK for traceability.
	if cik > 0 {
		out = append(out, store.FundamentalRow{
			SymbolID: symbolID, Metric: "CIK", Value: float64(cik),
			AsOf: 0, FetchedAt: fetchedAt,
		})
	}
	if f.LatestFilingDate > 0 {
		out = append(out, store.FundamentalRow{
			SymbolID: symbolID, Metric: "LatestFilingDate", Value: float64(f.LatestFilingDate),
			AsOf: f.LatestFilingDate, FetchedAt: fetchedAt,
		})
	}
	return out
}

// parseDay parses YYYY-MM-DD to a UTC-midnight epoch; ok=false on malformed.
func parseDay(s string) (int64, bool) {
	t, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(s), time.UTC)
	if err != nil {
		return 0, false
	}
	return t.Unix(), true
}
