// DATA-EXPANSION wave — crypto perp funding/OI worker: crypto-perp (15m).
//
// One POST per pass to Hyperliquid's free public info API
// (metaAndAssetCtxs — verified live 2026-07-10) snapshots funding, open
// interest, and mark price for every TRACKED crypto symbol whose base coin
// (BTC/USD → BTC) exists in Hyperliquid's perp universe. Unmapped symbols
// are counted honestly, never fabricated.
//
// HONESTY (carried verbatim by the API/UI): funding/OI from ONE venue —
// Hyperliquid, a DEX — a venue-specific positioning proxy. Descriptive
// context only, never a scored factor and not advice.
package pipeline

import (
	"context"
	"fmt"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/ingest/hyperliquid"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// CryptoPerpPoller is the crypto-perp worker.
type CryptoPerpPoller struct {
	St     *store.Store
	Client *hyperliquid.Client
	// Now is a test hook; nil = time.Now.
	Now func() time.Time
}

func (w *CryptoPerpPoller) Name() string            { return "crypto-perp" }
func (w *CryptoPerpPoller) Interval() time.Duration { return 15 * time.Minute }

func (w *CryptoPerpPoller) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now()
}

func (w *CryptoPerpPoller) Run(ctx context.Context) (string, error) {
	if w.Client == nil {
		// Shouldn't happen — Hyperliquid needs no key — but degrade honestly.
		return "skipped: no hyperliquid client", nil
	}
	all, err := w.St.ListSymbols(ctx, true)
	if err != nil {
		return "", err
	}
	var cryptos []md.Symbol
	for _, s := range all {
		if s.Market == md.Crypto {
			cryptos = append(cryptos, s)
		}
	}
	if len(cryptos) == 0 {
		return "skipped: no tracked crypto symbols", nil
	}

	stats, skipped, err := w.Client.FetchPerps(ctx)
	if err != nil {
		// Upstream hiccup: the runner records the error; nothing is stored.
		return "", fmt.Errorf("hyperliquid fetch: %w", err)
	}
	ts := w.now().Unix()
	stored, unmapped := 0, 0
	var headline string
	for _, s := range cryptos {
		coin, ok := hyperliquid.CoinForSymbol(s.Symbol)
		if !ok {
			unmapped++
			continue
		}
		st, ok := stats[coin]
		if !ok {
			unmapped++
			continue
		}
		if err := w.St.InsertCryptoPerp(ctx, store.CryptoPerpRow{
			SymbolID: s.ID, Ts: ts,
			Funding: st.Funding, OpenInterest: st.OpenInterest, MarkPx: st.MarkPx,
		}); err != nil {
			return "", err
		}
		stored++
		if headline == "" {
			headline = fmt.Sprintf("%s funding %.7f, OI %.1f", coin, st.Funding, st.OpenInterest)
		}
	}
	detail := fmt.Sprintf("stored %d perp snapshot(s)", stored)
	if headline != "" {
		detail += " (" + headline + ")"
	}
	if unmapped > 0 {
		detail += fmt.Sprintf("; %d tracked crypto symbol(s) not in Hyperliquid universe", unmapped)
	}
	if skipped > 0 {
		detail += fmt.Sprintf("; %d universe asset(s) skipped (malformed)", skipped)
	}
	return detail, nil
}
