// Package hyperliquid ingests crypto PERP funding + open interest from
// Hyperliquid's free public info API — no key, no registration, no geo-block
// (verified live 2026-07-10):
//
//	POST https://api.hyperliquid.xyz/info  {"type":"metaAndAssetCtxs"}
//
// The response is a 2-element array: [0] = meta {universe:[{name:"BTC",…},…]},
// [1] = per-asset contexts [{funding,openInterest,markPx,…},…] — the two
// arrays are INDEX-ALIGNED (asset i's context is ctxs[i]). Numbers arrive as
// JSON strings and are parsed leniently; an asset with an unparsable field is
// skipped and counted, never fabricated.
//
// HONESTY (carried verbatim by every consumer): funding/OI here are from ONE
// venue — Hyperliquid, a DEX — so they are a venue-specific positioning
// PROXY, descriptive context only, never a scored factor and never advice.
//
// Discipline mirrors the other ingest clients: declarative User-Agent
// (edgar.ResolveUA()), request timeout, graceful degradation on any upstream
// change (a shape mismatch is an error the worker records, never a panic).
package hyperliquid

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/ingest/edgar"
)

// infoURL is Hyperliquid's public info endpoint.
const infoURL = "https://api.hyperliquid.xyz/info"

// PerpStat is one coin's perp context at fetch time.
type PerpStat struct {
	Coin         string  // Hyperliquid universe name, e.g. "BTC"
	Funding      float64 // current hourly funding rate (fraction, e.g. 0.0000125)
	OpenInterest float64 // open interest in coin units
	MarkPx       float64 // mark price (USD)
}

// Client is a Hyperliquid info-API client. Zero-value BaseURL falls back to
// production; tests override it with an httptest server.
type Client struct {
	BaseURL string
	UA      string
	HTTP    *http.Client
}

// New returns a Client wired to Hyperliquid's production API with the shared
// declarative User-Agent.
func New() *Client {
	return &Client{
		UA:   edgar.ResolveUA(),
		HTTP: &http.Client{Timeout: 15 * time.Second},
	}
}

func (c *Client) url() string {
	if c.BaseURL != "" {
		return c.BaseURL
	}
	return infoURL
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

// metaAndCtxs mirrors the two response elements we consume.
type metaResp struct {
	Universe []struct {
		Name string `json:"name"`
	} `json:"universe"`
}

type assetCtx struct {
	Funding      string `json:"funding"`
	OpenInterest string `json:"openInterest"`
	MarkPx       string `json:"markPx"`
}

// FetchPerps POSTs metaAndAssetCtxs and returns coin → PerpStat for every
// universe asset whose numeric fields all parse. skipped counts assets with
// malformed numbers (reported honestly, never hidden).
func (c *Client) FetchPerps(ctx context.Context) (stats map[string]PerpStat, skipped int, err error) {
	body := bytes.NewBufferString(`{"type":"metaAndAssetCtxs"}`)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url(), body)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", c.ua())
	res, err := c.httpClient().Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer res.Body.Close() //nolint:errcheck
	if res.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(res.Body, 256))
		return nil, 0, fmt.Errorf("hyperliquid: status %d: %s", res.StatusCode, strings.TrimSpace(string(snippet)))
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, 16<<20))
	if err != nil {
		return nil, 0, err
	}
	return ParsePerps(raw)
}

// ParsePerps decodes the [meta, ctxs] response pair. The arrays are
// index-aligned per Hyperliquid's contract; a length mismatch is tolerated by
// iterating the shorter side (extra entries are counted as skipped).
func ParsePerps(raw []byte) (map[string]PerpStat, int, error) {
	var pair []json.RawMessage
	if err := json.Unmarshal(raw, &pair); err != nil {
		return nil, 0, fmt.Errorf("hyperliquid: parse response array: %w", err)
	}
	if len(pair) < 2 {
		return nil, 0, fmt.Errorf("hyperliquid: expected [meta, assetCtxs], got %d element(s)", len(pair))
	}
	var meta metaResp
	if err := json.Unmarshal(pair[0], &meta); err != nil {
		return nil, 0, fmt.Errorf("hyperliquid: parse meta: %w", err)
	}
	var ctxs []assetCtx
	if err := json.Unmarshal(pair[1], &ctxs); err != nil {
		return nil, 0, fmt.Errorf("hyperliquid: parse asset contexts: %w", err)
	}
	n := len(meta.Universe)
	skipped := 0
	if len(ctxs) < n {
		skipped += n - len(ctxs)
		n = len(ctxs)
	}
	out := make(map[string]PerpStat, n)
	for i := 0; i < n; i++ {
		name := strings.ToUpper(strings.TrimSpace(meta.Universe[i].Name))
		if name == "" {
			skipped++
			continue
		}
		funding, fErr := strconv.ParseFloat(strings.TrimSpace(ctxs[i].Funding), 64)
		oi, oErr := strconv.ParseFloat(strings.TrimSpace(ctxs[i].OpenInterest), 64)
		px, pErr := strconv.ParseFloat(strings.TrimSpace(ctxs[i].MarkPx), 64)
		if fErr != nil || oErr != nil || pErr != nil {
			skipped++
			continue
		}
		out[name] = PerpStat{Coin: name, Funding: funding, OpenInterest: oi, MarkPx: px}
	}
	return out, skipped, nil
}

// CoinForSymbol maps a SignalDeck crypto pair (BASE/QUOTE, e.g. "BTC/USD") to
// the Hyperliquid universe coin name ("BTC"). ok=false for anything that isn't
// a BASE/QUOTE pair.
func CoinForSymbol(symbol string) (string, bool) {
	base, _, found := strings.Cut(strings.ToUpper(strings.TrimSpace(symbol)), "/")
	if !found || base == "" {
		return "", false
	}
	return base, true
}
