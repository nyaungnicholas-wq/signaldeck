// GET /api/tv-status — an OPERATIONS read for the TradingView webhook wiring:
// is the shared secret configured, which public (ngrok) host is exposed and is
// it reachable, how many alerts we've received, and a per-symbol grid over the
// STREAMED hot set. Everything here is honest: the tunnel state is a best-effort
// self-probe (null when no public host is configured, never a fabricated
// "reachable"), and the webhook secret is NEVER echoed back — only whether one
// is set. The raw alert payload stays unexposed (the store omits it).
package api

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"
)

// tvStatusSymbol is one streamed symbol's received-signal tally.
type tvStatusSymbol struct {
	Symbol      string `json:"symbol"`
	Market      string `json:"market"`
	Count       int    `json:"count"`
	LastFiredAt *int64 `json:"lastFiredAt"`
}

// tvStatusResp is the GET /api/tv-status body.
type tvStatusResp struct {
	SecretConfigured bool             `json:"secretConfigured"`
	WebhookPath      string           `json:"webhookPath"`
	PublicHosts      []string         `json:"publicHosts"`
	TunnelReachable  *bool            `json:"tunnelReachable"`
	TunnelCheckedAt  *int64           `json:"tunnelCheckedAt"`
	Total            int              `json:"total"`
	Last24h          int              `json:"last24h"`
	LastAt           *int64           `json:"lastAt"`
	LastTicker       string           `json:"lastTicker"`
	Symbols          []tvStatusSymbol `json:"symbols"`
}

// tunnelProbeCache memoizes the last self-probe so we never hit the tunnel more
// than once per probeTTL regardless of how often the page polls. tunnelProbing
// + tunnelCond collapse a burst of concurrent cache-miss callers onto a SINGLE
// in-flight probe (late arrivals wait for its result instead of each firing
// their own outbound request).
var (
	tunnelMu        sync.Mutex
	tunnelCond      = sync.NewCond(&tunnelMu)
	tunnelReachable bool
	tunnelCheckedAt int64
	tunnelHost      string // host the cached result belongs to
	tunnelProbing   bool   // a probe is currently in flight
)

const probeTTL = 30 * time.Second

// tvStatus reports the TradingView webhook operational state.
func (d Deps) tvStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	now := time.Now().Unix()

	public := publicHosts(d.Cfg.AllowedHosts)

	reachable, checkedAt := probeTunnel(public)

	total, last24h, lastAt, lastTicker, err := d.St.TVSignalTotals(ctx, now-86400)
	if err != nil {
		httpInternal(w, err)
		return
	}

	streamed, err := d.St.StreamedSymbols(ctx)
	if err != nil {
		httpInternal(w, err)
		return
	}
	aggs, err := d.St.TVSignalAggregates(ctx)
	if err != nil {
		httpInternal(w, err)
		return
	}

	syms := make([]tvStatusSymbol, 0, len(streamed))
	for _, ss := range streamed {
		row := tvStatusSymbol{Symbol: ss.Symbol, Market: ss.Market}
		want := normalizeTicker(ss.Symbol)
		var last int64
		for _, a := range aggs {
			byID := a.SymbolID.Valid && a.SymbolID.Int64 == ss.ID
			byTicker := normalizeTicker(a.Ticker) == want
			if !byID && !byTicker {
				continue
			}
			row.Count += a.Count
			if a.MaxTs > last {
				last = a.MaxTs
			}
		}
		if last > 0 {
			t := last
			row.LastFiredAt = &t
		}
		syms = append(syms, row)
	}

	writeJSON(w, tvStatusResp{
		SecretConfigured: d.Cfg.TVWebhookSecret != "",
		WebhookPath:      "/api/tv-webhook",
		PublicHosts:      public,
		TunnelReachable:  reachable,
		TunnelCheckedAt:  checkedAt,
		Total:            total,
		Last24h:          last24h,
		LastAt:           lastAt,
		LastTicker:       lastTicker,
		Symbols:          syms,
	})
}

// publicHosts returns the AllowedHosts entries that are NOT loopback — i.e. the
// externally reachable (ngrok tunnel) host(s). Loopback binds are dropped.
func publicHosts(allowed []string) []string {
	out := []string{}
	for _, h := range allowed {
		host := h
		if i := strings.LastIndex(host, ":"); i >= 0 && !strings.Contains(host[i+1:], "]") {
			host = host[:i] // strip :port for the loopback test (keep original in output)
		}
		host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
		switch host {
		case "127.0.0.1", "localhost", "::1", "0.0.0.0":
			continue
		}
		out = append(out, h)
	}
	return out
}

// probeTunnel best-effort self-checks the first public host's /api/health,
// caching the result for probeTTL so repeated polls never re-probe. Returns
// (nil, nil) when no public host is configured — an honest "unknown", never a
// fabricated reachable state. The cache is process-shared, so the probe runs on
// a request-independent context (see healthProbe): a client that disconnects
// mid-probe must never poison the shared result for everyone else.
func probeTunnel(public []string) (*bool, *int64) {
	if len(public) == 0 {
		return nil, nil
	}
	host := public[0]

	tunnelMu.Lock()
	for {
		fresh := tunnelHost == host && tunnelCheckedAt > 0 &&
			time.Now().Unix()-tunnelCheckedAt < int64(probeTTL/time.Second)
		if fresh {
			r, at := tunnelReachable, tunnelCheckedAt
			tunnelMu.Unlock()
			return &r, &at
		}
		// Cache miss. If another caller is already probing, wait for its result
		// rather than firing a redundant outbound request (no thundering herd).
		if tunnelProbing {
			tunnelCond.Wait()
			continue
		}
		tunnelProbing = true
		break
	}
	tunnelMu.Unlock()

	ok := healthProbe(host)
	at := time.Now().Unix()

	tunnelMu.Lock()
	tunnelReachable, tunnelCheckedAt, tunnelHost = ok, at, host
	tunnelProbing = false
	tunnelCond.Broadcast()
	tunnelMu.Unlock()

	return &ok, &at
}

// healthProbe issues a 2s GET https://<host>/api/health with the
// ngrok-skip-browser-warning header; reachable = HTTP 200. The 2s timeout is
// derived from context.Background(), NOT any HTTP request's context: the result
// is cached and shared across all callers, so its lifetime must not be tied to
// the one request that happened to trigger the refresh (a client aborting
// mid-probe would otherwise cache a false "unreachable" for everyone).
func healthProbe(host string) bool {
	pctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(pctx, http.MethodGet, "https://"+host+"/api/health", nil)
	if err != nil {
		return false
	}
	req.Header.Set("ngrok-skip-browser-warning", "1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close() //nolint:errcheck
	return resp.StatusCode == http.StatusOK
}

// normalizeTicker canonicalizes a symbol or TradingView ticker for the
// per-symbol fallback match: uppercase, strip any "EXCH:" prefix, and remove
// "/" and "-" so "BTC/USD", "BTCUSD" and "BITSTAMP:BTCUSD" all collapse to
// "BTCUSD".
func normalizeTicker(s string) string {
	t := strings.ToUpper(strings.TrimSpace(s))
	if i := strings.LastIndex(t, ":"); i >= 0 {
		t = t[i+1:]
	}
	t = strings.ReplaceAll(t, "/", "")
	t = strings.ReplaceAll(t, "-", "")
	return t
}

func (d Deps) registerTVStatus(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/tv-status", d.tvStatus)
}
