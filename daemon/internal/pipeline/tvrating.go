// TradingView SCANNER RATINGS worker: tv-rating (15m tick).
//
// Persists TradingView's OWN technical-analysis RATING for our tracked symbols
// from its PUBLIC scanner endpoint (scanner.tradingview.com/{screener}/scan —
// no account, no key; verified live 2026-07-07) into tv_ratings. Each 15m pass:
//
//  1. BATCH-SCAN the "america" screener BY NAME (chunked ≤200) — the scanner
//     resolves each symbol's own exchange via a name in_range filter with
//     name+exchange columns, so nothing is hardcoded (DRAM/SNXX come back as
//     CBOE, not NASDAQ). Returned rows are matched to symbol_id by NAME and
//     upserted with Label(reco_all); the discovered exchange is opportunistically
//     cached in tv_exchange. (We scan by name rather than pre-resolving via
//     symbol-search because that host 403s server-side without browser headers.)
//  2. CRYPTO: our only crypto is BTC/USD, which the scanner keys as
//     BITSTAMP:BTCUSD on the "crypto" screener — special-cased (see
//     cryptoTicker). Its status is reported honestly in the detail string; a
//     crypto scan error never aborts the (verified) stock path.
//
// GRACEFUL DEGRADATION: a name the scanner can't place is simply absent (never
// fabricated); a scanner network error is returned so the supervisor logs it,
// but it never fails the fleet.
//
// HONESTY (carried verbatim by the API/UI): reco_* are TradingView's OWN
// descriptive TA rating on DELAYED data — an EXTERNAL, independent signal, NOT
// SignalDeck's model and NOT advice.
package pipeline

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/ingest/tvscanner"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

const (
	// tvScanChunk caps tickers per scanner POST; one POST could batch far more,
	// but ≤200 keeps each request bounded.
	tvScanChunk = 200
	// tvStockScreener / tvCryptoScreener are the scanner screeners.
	tvStockScreener  = "america"
	tvCryptoScreener = "crypto"
)

// cryptoTicker maps our canonical crypto symbols to their TradingView
// EXCHANGE:SYMBOL on the crypto screener. Only BTC/USD is tracked today; the
// map keeps the special-case honest and explicit rather than guessing an
// exchange for every crypto pair.
var cryptoTicker = map[string]string{
	"BTC/USD": "BITSTAMP:BTCUSD",
	"ETH/USD": "BITSTAMP:ETHUSD",
	"SOL/USD": "BITSTAMP:SOLUSD",
}

// TVRatingPoller is the tv-rating worker.
type TVRatingPoller struct {
	St *store.Store
	TV *tvscanner.Client
	// Now is a test hook; nil = time.Now.
	Now func() time.Time
}

func (w *TVRatingPoller) Name() string            { return "tv-rating" }
func (w *TVRatingPoller) Interval() time.Duration { return 15 * time.Minute }

func (w *TVRatingPoller) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now()
}

func (w *TVRatingPoller) Run(ctx context.Context) (string, error) {
	if w.TV == nil {
		// Shouldn't happen — the scanner needs no key — but degrade honestly.
		return "skipped: no tvscanner client", nil
	}
	ts := w.now().Unix()

	// (1+2) SCAN the america screener BY NAME — the scanner resolves each
	// symbol's exchange itself (name+exchange columns via a name in_range
	// filter), so no separate exchange-resolution step is needed. (The old
	// symbol-search resolver 403s server-side without browser headers; the
	// scanner answers a plain POST.) We match returned rows back to symbol_id
	// by NAME and opportunistically cache the discovered exchange in tv_exchange.
	stocks, err := w.St.ActiveStockSymbols(ctx, nil)
	if err != nil {
		return "", err
	}
	names := make([]string, 0, len(stocks))
	nameToID := make(map[string]int64, len(stocks))
	for _, s := range stocks {
		key := strings.ToUpper(s.Symbol)
		names = append(names, s.Symbol)
		nameToID[key] = s.ID
	}

	rated, unmatched := 0, 0
	for i := 0; i < len(names); i += tvScanChunk {
		end := i + tvScanChunk
		if end > len(names) {
			end = len(names)
		}
		rows, err := w.TV.ScanRatingsByName(ctx, tvStockScreener, names[i:end])
		if err != nil {
			return "", err // network error — supervisor logs it, fleet unaffected
		}
		for name, row := range rows {
			id, ok := nameToID[name]
			if !ok {
				unmatched++ // scanner returned a name we didn't ask for — ignore
				continue
			}
			if err := w.St.UpsertTVRating(ctx, tvRatingRow(id, ts, row.Rating)); err != nil {
				return "", err
			}
			_ = w.St.UpsertExchange(ctx, id, row.Exchange) // best-effort cache
			rated++
		}
	}
	unplaced := len(names) - rated

	// (2) CRYPTO: special-cased BTC/USD on the crypto screener.
	crypto := w.scanCrypto(ctx, ts)

	return fmt.Sprintf("%d/%d stocks rated (%d unplaced, %d foreign rows ignored), crypto: %s",
		rated, len(names), unplaced, unmatched, crypto), nil
}

// scanCrypto rates the tracked crypto symbols the cryptoTicker map covers
// (today: BTC/USD → BITSTAMP:BTCUSD on the crypto screener). Its error is
// folded into the returned status string (never returned) so a crypto hiccup
// can't abort the verified stock path.
func (w *TVRatingPoller) scanCrypto(ctx context.Context, ts int64) string {
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
	ratings, err := w.TV.ScanRatings(ctx, tvCryptoScreener, tickers)
	if err != nil {
		return "scan error: " + err.Error()
	}
	rated := 0
	for tk, rt := range ratings {
		id, ok := tickerToID[tk]
		if !ok {
			continue
		}
		if err := w.St.UpsertTVRating(ctx, tvRatingRow(id, ts, rt)); err != nil {
			return "store error: " + err.Error()
		}
		rated++
	}
	return fmt.Sprintf("%d/%d rated", rated, len(tickers))
}

// tvRatingRow builds a store row from a scanner rating, stamping the label.
func tvRatingRow(symbolID, ts int64, rt tvscanner.Rating) store.TVRatingRow {
	return store.TVRatingRow{
		SymbolID:  symbolID,
		Ts:        ts,
		RecoAll:   rt.RecoAll,
		RecoMA:    rt.RecoMA,
		RecoOther: rt.RecoOther,
		RSI:       rt.RSI,
		Close:     rt.Close,
		Label:     tvscanner.Label(rt.RecoAll),
	}
}
