// Historical news backfill (sentiment-correlation wave).
//
// The live NewsFetcher asks for each symbol's latest 20 headlines, which is the
// right shape for a feed and useless for a study: it can never reach further
// back than the day it started running. Measuring whether sentiment predicts
// returns needs YEARS, and Alpaca's news endpoint will serve them — it accepts a
// start/end window, pages through results, and takes up to 50 symbols per
// request, which is what makes backfilling a ~500-name universe affordable.
//
// This file adds the ranged, paginated, multi-symbol read. It deliberately does
// not decide WHAT to fetch or how fast — the worker owns the cursor and the
// pacing.
package news

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// MaxRangeSymbols is Alpaca's cap on symbols per news request. Batching to this
// limit is the difference between ~500 requests per historical window and ten.
const MaxRangeSymbols = 50

// rangeLimit is the page size for a ranged fetch (Alpaca's maximum).
const rangeLimit = 50

// RangePage is one page of a historical fetch. NextToken is empty on the last
// page.
type RangePage struct {
	Items     []store.NewsItem
	Symbols   [][]string // per-item provider symbol lists, index-aligned with Items
	NextToken string
}

// FetchRange retrieves one page of headlines for up to MaxRangeSymbols symbols
// over [start, end). Pass the previous page's NextToken to continue; pass "" to
// begin.
//
// Items carry no SymbolID: a single article routinely tags several tickers, so
// mapping to our symbol rows is the caller's job and is why Symbols is returned
// alongside. Attributing a multi-ticker article to only its first symbol would
// quietly drop most of the coverage for smaller names.
func (c *Client) FetchRange(ctx context.Context, symbols []string, start, end time.Time, pageToken string) (RangePage, error) {
	if len(symbols) == 0 {
		return RangePage{}, fmt.Errorf("news: FetchRange needs at least one symbol")
	}
	if len(symbols) > MaxRangeSymbols {
		return RangePage{}, fmt.Errorf("news: FetchRange got %d symbols, max %d", len(symbols), MaxRangeSymbols)
	}

	q := url.Values{}
	q.Set("symbols", strings.Join(symbols, ","))
	q.Set("start", start.UTC().Format(time.RFC3339))
	q.Set("end", end.UTC().Format(time.RFC3339))
	q.Set("limit", fmt.Sprint(rangeLimit))
	// Oldest-first so a cursor that records "how far forward we have reached"
	// advances monotonically and a resumed backfill never re-reads a whole
	// window to find its place.
	q.Set("sort", "asc")
	if pageToken != "" {
		q.Set("page_token", pageToken)
	}
	u := fmt.Sprintf("%s/news?%s", c.base(), q.Encode())

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return RangePage{}, err
	}
	c.auth(req)

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return RangePage{}, err
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return RangePage{}, fmt.Errorf("news: range fetch: status %d: %s", resp.StatusCode, body)
	}

	var page newsPage
	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		return RangePage{}, fmt.Errorf("news: decode range: %w", err)
	}

	out := RangePage{
		Items:   make([]store.NewsItem, 0, len(page.News)),
		Symbols: make([][]string, 0, len(page.News)),
	}
	if page.NextPageToken != nil {
		out.NextToken = *page.NextPageToken
	}
	for _, a := range page.News {
		t, err := time.Parse(time.RFC3339, a.CreatedAt)
		if err != nil {
			// One malformed timestamp must not abandon the page: skipping the
			// article loses one headline, failing loses the whole window.
			continue
		}
		out.Items = append(out.Items, store.NewsItem{
			ID:       fmt.Sprint(a.ID),
			Ts:       t.Unix(),
			Headline: a.Headline,
			URL:      a.URL,
			Source:   a.Source,
		})
		out.Symbols = append(out.Symbols, a.Symbols)
	}
	return out, nil
}
