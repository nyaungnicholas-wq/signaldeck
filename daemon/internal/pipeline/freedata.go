// Free-data workers (Stage 2): FRED macro poller + SEC EDGAR fundamentals
// fetcher. Both are zero-cost, no-vendor context sources that degrade
// gracefully when their upstream is unavailable — a fetch error is returned to
// the runner (recorded as a worker error) but never corrupts the store, and a
// nil client makes the worker a clean no-op.
package pipeline

import (
	"context"
	"fmt"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/ingest/edgar"
	"github.com/nyaungnicholas-wq/signaldeck/internal/ingest/fred"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// FredPoller refreshes the FRED macro series (~6h). It works with NO key via
// the keyless CSV endpoint; an optional key switches the underlying client to
// the JSON API. Insert is idempotent so re-polls are cheap.
type FredPoller struct {
	St     *store.Store
	Client *fred.Client // never nil in practice (fred.New works keyless)
	Series []string     // defaults to fred.DefaultSeries when empty
}

func (w *FredPoller) Name() string            { return "fred-poller" }
func (w *FredPoller) Interval() time.Duration { return 6 * time.Hour }

func (w *FredPoller) Run(ctx context.Context) (string, error) {
	if w.Client == nil {
		return "skipped: no FRED client", nil
	}
	list := w.Series
	if len(list) == 0 {
		list = fred.DefaultSeries
	}
	n, err := w.Client.Ingest(ctx, w.St, list)
	if err != nil {
		return "", err
	}
	// Surface the freshest VIX in the run message (the headline cross-asset
	// level the feature layer wants first) — honest "n/a" when none yet.
	vixNote := "vix n/a"
	if v, ok, _ := w.St.LatestVIX(ctx); ok {
		vixNote = fmt.Sprintf("vix %.2f", v)
	}
	return fmt.Sprintf("ingested %d macro obs across %d series (%s)", n, len(list), vixNote), nil
}

// EdgarFetcher pulls SEC EDGAR fundamentals for universe stocks (~24h). SEC
// requires a descriptive User-Agent + <=10 req/s, both enforced by the client.
// Degrades gracefully: a per-symbol failure is recorded and skipped; an
// upstream outage returns an error the runner records without corrupting data.
type EdgarFetcher struct {
	St     *store.Store
	Client *edgar.Client // nil ⇒ no-op
	// MaxPerRun bounds how many symbols are refreshed each pass so a large
	// universe is swept over several days rather than hammered in one run
	// (cursor persisted in meta). 0 ⇒ default.
	MaxPerRun int
}

func (w *EdgarFetcher) Name() string            { return "edgar-fetcher" }
func (w *EdgarFetcher) Interval() time.Duration { return 24 * time.Hour }

// edgarCursorKey tracks how far through the (alphabetical) universe we've swept
// so successive daily runs rotate coverage instead of always starting at 'A'.
const edgarCursorKey = "edgar_sweep_cursor"

func (w *EdgarFetcher) Run(ctx context.Context) (string, error) {
	if w.Client == nil {
		return "skipped: no EDGAR client", nil
	}
	// Every active stock (streamed hot set + broad daily-only universe) — SEC
	// fundamentals are per-company, not per-feed, so the whole equity universe
	// is eligible. Crypto has no filings and is excluded.
	all, err := w.St.ListSymbols(ctx, true)
	if err != nil {
		return "", err
	}
	stocks := make([]md.Symbol, 0, len(all))
	for _, s := range all {
		if s.Market == md.Stocks {
			stocks = append(stocks, s)
		}
	}
	if len(stocks) == 0 {
		return "no stocks to fetch", nil
	}

	batch := w.MaxPerRun
	if batch <= 0 {
		batch = 50 // conservative default; paced at ~150ms ⇒ a batch is ~8s
	}
	// Rotate the sweep window using a persisted cursor so, over multiple daily
	// runs, the whole universe gets refreshed even if it exceeds one batch.
	window := selectWindow(ctx, w.St, stocks, batch)

	written, missing, err := w.Client.Ingest(ctx, w.St, window)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("fundamentals: %d rows across %d symbols (%d without coverage), swept %d/%d",
		written, len(window), missing, len(window), len(stocks)), nil
}

// selectWindow returns the next batch of symbols to sweep, advancing a persisted
// alphabetical cursor so successive runs rotate through the universe. It sorts
// stocks by symbol for a stable order, resumes after the last-swept symbol, and
// wraps to the start when it reaches the end.
func selectWindow(ctx context.Context, st *store.Store, stocks []md.Symbol, batch int) []md.Symbol {
	// stocks are already stable-orderable by Symbol; sort defensively.
	sortSymbols(stocks)
	if len(stocks) <= batch {
		return stocks
	}
	cursor, _ := st.GetMeta(ctx, edgarCursorKey)
	start := 0
	if cursor != "" {
		// resume at the first symbol strictly greater than the cursor
		for i, s := range stocks {
			if s.Symbol > cursor {
				start = i
				break
			}
			// if cursor is >= last symbol, start stays 0 (wrap to beginning)
		}
	}
	end := start + batch
	var window []md.Symbol
	if end <= len(stocks) {
		window = stocks[start:end]
	} else {
		// wrap around the end
		window = append(window, stocks[start:]...)
		window = append(window, stocks[:end-len(stocks)]...)
	}
	// advance the cursor to the last symbol we're about to sweep
	if len(window) > 0 {
		_ = st.SetMeta(ctx, edgarCursorKey, window[len(window)-1].Symbol)
	}
	return window
}

// sortSymbols sorts a symbol slice in place by Symbol (stable universe order).
func sortSymbols(s []md.Symbol) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1].Symbol > s[j].Symbol; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}
