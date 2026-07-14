// DATA-EXPANSION wave — TradingView quote-tape worker: tv-quotes (60s tick).
//
// Fills the "last 15 minutes, full market" gap the free bar feeds leave
// (Alpaca SIP is 15m-guarded; the IEX feed is volume-thin) with quote columns
// from TradingView's PUBLIC scanner (same endpoint + name-filter pattern the
// tv-rating worker already uses; verified live 2026-07-10): close is
// 15-MIN-DELAYED (update_mode "delayed_streaming_900"), volume is
// FULL-MARKET cumulative day volume, and rtc — when present — is a REAL-TIME
// Cboe One composite price. Stored price = rtc when present (realtime=1),
// else the delayed close (realtime=0) — the flag travels with the row so
// nothing delayed ever masquerades as live.
//
// Cadence: stocks are scanned only while the market is open for bars
// (marketcal.OpenForBars, like universe-live); off-hours is an honest skip.
// Crypto trades 24/7 and is scanned every tick via the same cryptoTicker map
// the tv-rating worker uses. ONE chunked POST per screener per tick.
//
// RETENTION: tv_quotes is a QUOTE TAPE, not a record — every pass inline-
// prunes rows older than ~2h. Bars are the durable history; this table only
// ever answers "what is it trading at right now".
//
// HONESTY (carried verbatim by the API/UI): a descriptive supplement to the
// bar record, not a replacement, and per-row labeled realtime-vs-delayed.
package pipeline

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/ingest/tvscanner"
	"github.com/nyaungnicholas-wq/signaldeck/internal/marketcal"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// tvQuotesRetention is the trailing tape window kept in tv_quotes.
const tvQuotesRetention = 2 * time.Hour

// TVQuotesPoller is the tv-quotes worker.
type TVQuotesPoller struct {
	St *store.Store
	TV *tvscanner.Client
	// Now is a test hook; nil = time.Now.
	Now func() time.Time
}

func (w *TVQuotesPoller) Name() string            { return "tv-quotes" }
func (w *TVQuotesPoller) Interval() time.Duration { return time.Minute }

func (w *TVQuotesPoller) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now()
}

func (w *TVQuotesPoller) Run(ctx context.Context) (string, error) {
	if w.TV == nil {
		// Shouldn't happen — the scanner needs no key — but degrade honestly.
		return "skipped: no tvscanner client", nil
	}
	now := w.now()
	ts := now.Unix()

	stocksStored, stocksTotal, realtime := 0, 0, 0
	marketOpen := marketcal.OpenForBars(now)
	if marketOpen {
		stocks, err := w.St.ActiveStockSymbols(ctx, nil)
		if err != nil {
			return "", err
		}
		names := make([]string, 0, len(stocks))
		nameToID := make(map[string]int64, len(stocks))
		for _, s := range stocks {
			names = append(names, s.Symbol)
			nameToID[strings.ToUpper(s.Symbol)] = s.ID
		}
		stocksTotal = len(names)
		for i := 0; i < len(names); i += tvScanChunk {
			end := i + tvScanChunk
			if end > len(names) {
				end = len(names)
			}
			quotes, err := w.TV.ScanQuotesByName(ctx, tvStockScreener, names[i:end])
			if err != nil {
				return "", err // network error — supervisor logs it, fleet unaffected
			}
			for name, q := range quotes {
				id, ok := nameToID[name]
				if !ok {
					continue // scanner returned a name we didn't ask for — ignore
				}
				if err := w.St.InsertTVQuote(ctx, quoteRow(id, ts, q)); err != nil {
					return "", err
				}
				stocksStored++
				if q.RTC != nil {
					realtime++
				}
			}
		}
	}

	// Crypto trades 24/7 — scanned every tick via the verified ticker map.
	cryptoNote := w.scanCryptoQuotes(ctx, ts)

	// Inline tape prune: this table only ever holds a trailing window.
	pruned, err := w.St.PruneTVQuotes(ctx, now.Add(-tvQuotesRetention).Unix())
	if err != nil {
		return "", err
	}

	var detail string
	if marketOpen {
		detail = fmt.Sprintf("stocks: %d/%d quoted (%d real-time rtc)", stocksStored, stocksTotal, realtime)
	} else {
		detail = "stocks: market closed (skipped)"
	}
	detail += "; crypto: " + cryptoNote
	if pruned > 0 {
		detail += fmt.Sprintf("; pruned %d tape row(s) older than 2h", pruned)
	}
	return detail, nil
}

// scanCryptoQuotes quotes the tracked crypto symbols the cryptoTicker map
// covers. Errors are folded into the status string (never returned) so a
// crypto hiccup can't abort the stock path — mirroring tv-rating.
func (w *TVQuotesPoller) scanCryptoQuotes(ctx context.Context, ts int64) string {
	all, err := w.St.ListSymbols(ctx, true)
	if err != nil {
		return "list error: " + err.Error()
	}
	tickers := make([]string, 0, 1)
	tickerToID := make(map[string]int64, 1)
	for _, s := range all {
		if s.Market != md.Crypto {
			continue
		}
		t, ok := cryptoTicker[s.Symbol]
		if !ok {
			continue // no verified TradingView mapping — skip honestly
		}
		tickers = append(tickers, t)
		tickerToID[t] = s.ID
	}
	if len(tickers) == 0 {
		return "none tracked"
	}
	quotes, err := w.TV.ScanQuotes(ctx, tvCryptoScreener, tickers)
	if err != nil {
		return "scan error: " + err.Error()
	}
	stored := 0
	for tk, q := range quotes {
		id, ok := tickerToID[tk]
		if !ok {
			continue
		}
		if err := w.St.InsertTVQuote(ctx, quoteRow(id, ts, q)); err != nil {
			return "store error: " + err.Error()
		}
		stored++
	}
	return fmt.Sprintf("%d/%d quoted", stored, len(tickers))
}

// quoteRow maps a scanner quote to a store row: rtc when present (realtime),
// else the delayed close — flagged so, never disguised.
func quoteRow(symbolID, ts int64, q tvscanner.Quote) store.TVQuoteRow {
	price, rt := q.Close, false
	if q.RTC != nil {
		price, rt = *q.RTC, true
	}
	return store.TVQuoteRow{
		SymbolID: symbolID, Ts: ts, Price: price, DelayedClose: q.Close,
		ChangePct: q.ChangePct, DayVolume: q.Volume, Realtime: rt,
	}
}
