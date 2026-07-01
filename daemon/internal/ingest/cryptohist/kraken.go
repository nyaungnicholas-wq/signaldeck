// Package cryptohist backfills historical crypto OHLC bars from Kraken's
// public REST API (no auth required). One Backfill call seeds three
// timeframes for a symbol: ~2y of 1d, ~30d of 1h and ~12h of 1m. Those spans
// are exactly what Kraken's ~720-row-per-response cap yields at intervals
// 1440/60/1 minutes, so a single request per timeframe suffices — no paging.
package cryptohist

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

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// pause spaces the public-API calls: Kraken's public rate limiter tolerates
// roughly one request per second, so 500ms keeps a three-call backfill
// comfortably inside the limit.
const pause = 500 * time.Millisecond

// maxBody bounds response reads so a misbehaving endpoint cannot exhaust
// memory; a full 720-row OHLC payload is well under 1MB.
const maxBody = 8 << 20

// backfillPlan orders the interval→timeframe fetches. Kraken caps OHLC
// responses at ~720 rows, so the interval alone fixes the lookback:
// 720×1440m ≈ 2y, 720×60m = 30d, 720×1m = 12h.
var backfillPlan = []struct {
	interval int // Kraken bar length in minutes
	tf       md.Timeframe
}{
	{1440, md.TF1d},
	{60, md.TF1h},
	{1, md.TF1m},
}

// PairFor converts a canonical SignalDeck symbol ("BTC/USD") to the pair
// name Kraken's REST API accepts ("XBTUSD"). Rule: split on "/", rename BTC
// to Kraken's legacy XBT code (on either side of the pair), and concatenate.
// Kraken accepts these short names on requests even though it may key the
// response under its longer classified form (e.g. XXBTZUSD), which is why
// Backfill never matches the response key against this value.
func PairFor(symbol string) string {
	parts := strings.Split(symbol, "/")
	for i, p := range parts {
		if p == "BTC" {
			parts[i] = "XBT"
		}
	}
	return strings.Join(parts, "")
}

// Client talks to Kraken's public market-data REST endpoints.
type Client struct {
	// Base is the API root ("https://api.kraken.com"); overridable so tests
	// can point at an httptest server. Empty falls back to production.
	Base string
	// HTTP is the underlying client; nil falls back to http.DefaultClient.
	HTTP *http.Client
}

// New returns a Client with production defaults. The timeout is generous
// because a 720-row OHLC payload over a cold TLS connection can be slow.
func New() *Client {
	return &Client{
		Base: "https://api.kraken.com",
		HTTP: &http.Client{Timeout: 30 * time.Second},
	}
}

// Backfill fetches 1d (~2y), 1h (~30d) and 1m (~12h) history for symbol from
// Kraken and upserts it into st under symbolID. It returns the number of bars
// written per timeframe. The three public calls are spaced 500ms apart to
// respect Kraken's rate limit; a mid-run failure returns the counts persisted
// so far alongside the error, so callers can report partial coverage.
func (c *Client) Backfill(ctx context.Context, st *store.Store, symbolID int64, symbol string) (map[md.Timeframe]int, error) {
	counts := make(map[md.Timeframe]int, len(backfillPlan))
	pair := PairFor(symbol)
	for i, step := range backfillPlan {
		if i > 0 {
			select {
			case <-ctx.Done():
				return counts, ctx.Err()
			case <-time.After(pause):
			}
		}
		bars, err := c.fetchOHLC(ctx, pair, step.interval, symbolID, step.tf)
		if err != nil {
			return counts, fmt.Errorf("kraken ohlc %s interval=%d: %w", pair, step.interval, err)
		}
		if err := st.UpsertBars(ctx, bars); err != nil {
			return counts, fmt.Errorf("upsert %s bars: %w", step.tf, err)
		}
		counts[step.tf] = len(bars)
	}
	return counts, nil
}

// ohlcEnvelope is Kraken's standard {error, result} wrapper. The bar array
// sits under a pair key we cannot predict (Kraken may answer a XBTUSD request
// with its classified name XXBTZUSD) next to a "last" pagination cursor, so
// result values stay raw until the pair key is located.
type ohlcEnvelope struct {
	Error  []string                   `json:"error"`
	Result map[string]json.RawMessage `json:"result"`
}

// barsRaw returns the OHLC array: the first result key that is not the
// "last" cursor. Kraken returns exactly one pair per single-pair request, so
// map iteration order does not matter.
func (e *ohlcEnvelope) barsRaw() (json.RawMessage, error) {
	for k, v := range e.Result {
		if k != "last" {
			return v, nil
		}
	}
	return nil, fmt.Errorf("no pair key in result")
}

// fetchOHLC performs one GET /0/public/OHLC call and parses the rows into
// bars stamped with symbolID and tf.
func (c *Client) fetchOHLC(ctx context.Context, pair string, interval int, symbolID int64, tf md.Timeframe) ([]md.Bar, error) {
	base := c.Base
	if base == "" {
		base = "https://api.kraken.com"
	}
	httpc := c.HTTP
	if httpc == nil {
		httpc = http.DefaultClient
	}
	u := fmt.Sprintf("%s/0/public/OHLC?pair=%s&interval=%d", base, url.QueryEscape(pair), interval)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := httpc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close() //nolint:errcheck
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected http status %d", resp.StatusCode)
	}
	var env ohlcEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	// Kraken signals failures in-band with HTTP 200; the error array is the
	// source of truth, not the status code.
	if len(env.Error) > 0 {
		return nil, fmt.Errorf("api error: %s", strings.Join(env.Error, "; "))
	}
	raw, err := env.barsRaw()
	if err != nil {
		return nil, err
	}
	return parseBars(raw, symbolID, tf)
}

// parseBars decodes Kraken OHLC rows. Each row is
// [ts, "open", "high", "low", "close", "vwap", "volume", count]:
// ts is unix seconds of the bar OPEN (a JSON number), prices and volume are
// strings, so rows decode as mixed-type arrays. vwap (index 5) and trade
// count (index 7) have no Bar field and are dropped.
func parseBars(raw json.RawMessage, symbolID int64, tf md.Timeframe) ([]md.Bar, error) {
	var rows [][]any
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, fmt.Errorf("decode ohlc rows: %w", err)
	}
	bars := make([]md.Bar, 0, len(rows))
	for i, r := range rows {
		if len(r) < 7 {
			return nil, fmt.Errorf("ohlc row %d: want >=7 fields, got %d", i, len(r))
		}
		ts, ok := r[0].(float64) // unix seconds fit float64 exactly (< 2^53)
		if !ok {
			return nil, fmt.Errorf("ohlc row %d: ts is %T, want number", i, r[0])
		}
		b := md.Bar{SymbolID: symbolID, TF: tf, Ts: int64(ts)}
		for j, dst := range []*float64{&b.Open, &b.High, &b.Low, &b.Close} {
			v, err := fieldFloat(r[1+j])
			if err != nil {
				return nil, fmt.Errorf("ohlc row %d field %d: %w", i, 1+j, err)
			}
			*dst = v
		}
		vol, err := fieldFloat(r[6])
		if err != nil {
			return nil, fmt.Errorf("ohlc row %d volume: %w", i, err)
		}
		b.Volume = vol
		bars = append(bars, b)
	}
	return bars, nil
}

// fieldFloat parses one of Kraken's string-encoded decimals.
func fieldFloat(v any) (float64, error) {
	s, ok := v.(string)
	if !ok {
		return 0, fmt.Errorf("want string-encoded number, got %T", v)
	}
	return strconv.ParseFloat(s, 64)
}
