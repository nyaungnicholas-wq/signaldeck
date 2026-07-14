package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// newTVStatusServer stands up GET /api/tv-status behind the real middleware.
// AllowedHosts holds only the (loopback) test-server addr, so publicHosts is
// empty and the tunnel is never probed — tunnelReachable stays null, exactly
// as the contract requires (no real network probe in the test).
func newTVStatusServer(t *testing.T, secret string) (*httptest.Server, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "tvstatus_api.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	d := Deps{St: st, Cfg: baseCfg(), Version: "test", Started: time.Now()}
	srv := httptest.NewUnstartedServer(nil)
	t.Cleanup(srv.Close)
	// A loopback host plus explicit localhost entries — all must be excluded
	// from publicHosts (proving the filter drops loopback binds).
	d.Cfg.AllowedHosts = []string{srv.Listener.Addr().String(), "localhost:8322", "127.0.0.1:8322"}
	d.Cfg.TVWebhookSecret = secret

	mux := http.NewServeMux()
	d.registerTVStatus(mux)
	srv.Config.Handler = d.secure(mux)
	srv.Start()
	return srv, st
}

func TestTVStatus(t *testing.T) {
	const secret = "s3cr3t-token"
	srv, st := newTVStatusServer(t, secret)
	ctx := context.Background()
	now := time.Now().Unix()

	// A streamed stock and a streamed crypto pair ("BTC/USD"). UpsertSymbol
	// defaults stream=0, so promote both into the hot set.
	nvda, err := st.UpsertSymbol(ctx, "NVDA", md.Stocks, "")
	if err != nil {
		t.Fatalf("seed NVDA: %v", err)
	}
	btc, err := st.UpsertSymbol(ctx, "BTC/USD", md.Crypto, "")
	if err != nil {
		t.Fatalf("seed BTC/USD: %v", err)
	}
	for _, id := range []int64{nvda.ID, btc.ID} {
		if err := st.SetSymbolStream(ctx, id, true); err != nil {
			t.Fatalf("set stream %d: %v", id, err)
		}
	}

	// Three received signals:
	//  1. resolved by symbol_id (ticker "NASDAQ:NVDA" → NVDA), recent.
	//  2. crypto matched only by normalized-ticker fallback ("BTCUSD" vs the
	//     stored "BTC/USD" — symbol_id stays NULL), newest.
	//  3. an untracked ticker, older than 24h — counts toward total but not
	//     last24h, and is excluded from the streamed grid.
	seed := func(ticker string, ts int64) {
		if _, err := st.InsertTVSignal(ctx, store.TVSignal{Ticker: ticker, Action: "buy", Ts: ts}); err != nil {
			t.Fatalf("insert signal %q: %v", ticker, err)
		}
	}
	seed("NASDAQ:NVDA", now-10)
	seed("BTCUSD", now)
	seed("PLTR", now-100000) // untracked + stale

	getResp, err := http.Get(srv.URL + "/api/tv-status")
	if err != nil {
		t.Fatalf("get tv-status: %v", err)
	}
	body := drain(t, getResp)
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("tv-status: %d (%s)", getResp.StatusCode, body)
	}

	var resp struct {
		SecretConfigured bool     `json:"secretConfigured"`
		WebhookPath      string   `json:"webhookPath"`
		PublicHosts      []string `json:"publicHosts"`
		TunnelReachable  *bool    `json:"tunnelReachable"`
		TunnelCheckedAt  *int64   `json:"tunnelCheckedAt"`
		Total            int      `json:"total"`
		Last24h          int      `json:"last24h"`
		LastAt           *int64   `json:"lastAt"`
		LastTicker       string   `json:"lastTicker"`
		Symbols          []struct {
			Symbol      string `json:"symbol"`
			Market      string `json:"market"`
			Count       int    `json:"count"`
			LastFiredAt *int64 `json:"lastFiredAt"`
		} `json:"symbols"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}

	if !resp.SecretConfigured {
		t.Errorf("secretConfigured=false, want true (secret is set)")
	}
	if resp.WebhookPath != "/api/tv-webhook" {
		t.Errorf("webhookPath=%q", resp.WebhookPath)
	}
	// No public host configured → probe skipped, honest nulls.
	if len(resp.PublicHosts) != 0 {
		t.Errorf("publicHosts=%v, want [] (loopback excluded)", resp.PublicHosts)
	}
	if resp.TunnelReachable != nil {
		t.Errorf("tunnelReachable=%v, want null (no public host)", *resp.TunnelReachable)
	}
	if resp.TunnelCheckedAt != nil {
		t.Errorf("tunnelCheckedAt=%v, want null", *resp.TunnelCheckedAt)
	}

	if resp.Total != 3 {
		t.Errorf("total=%d, want 3", resp.Total)
	}
	if resp.Last24h != 2 {
		t.Errorf("last24h=%d, want 2 (stale PLTR excluded)", resp.Last24h)
	}
	if resp.LastAt == nil || *resp.LastAt != now {
		t.Errorf("lastAt=%v, want %d", resp.LastAt, now)
	}
	if resp.LastTicker != "BTCUSD" {
		t.Errorf("lastTicker=%q, want BTCUSD (newest)", resp.LastTicker)
	}

	// Grid: only the two streamed symbols, ordered by symbol ("BTC/USD" < "NVDA").
	if len(resp.Symbols) != 2 {
		t.Fatalf("symbols=%d, want 2 (untracked PLTR excluded from grid)", len(resp.Symbols))
	}
	if resp.Symbols[0].Symbol != "BTC/USD" || resp.Symbols[1].Symbol != "NVDA" {
		t.Fatalf("symbol order=%q,%q want BTC/USD,NVDA", resp.Symbols[0].Symbol, resp.Symbols[1].Symbol)
	}
	crypto, stock := resp.Symbols[0], resp.Symbols[1]
	if crypto.Market != string(md.Crypto) {
		t.Errorf("crypto market=%q", crypto.Market)
	}
	if crypto.Count != 1 { // matched via normalized-ticker fallback
		t.Errorf("BTC/USD count=%d, want 1 (ticker fallback)", crypto.Count)
	}
	if crypto.LastFiredAt == nil || *crypto.LastFiredAt != now {
		t.Errorf("BTC/USD lastFiredAt=%v, want %d", crypto.LastFiredAt, now)
	}
	if stock.Count != 1 { // matched via symbol_id
		t.Errorf("NVDA count=%d, want 1 (by symbol_id)", stock.Count)
	}
	if stock.LastFiredAt == nil || *stock.LastFiredAt != now-10 {
		t.Errorf("NVDA lastFiredAt=%v, want %d", stock.LastFiredAt, now-10)
	}
}

func TestTVStatusSecretUnset(t *testing.T) {
	srv, _ := newTVStatusServer(t, "") // no secret configured
	getResp, err := http.Get(srv.URL + "/api/tv-status")
	if err != nil {
		t.Fatalf("get tv-status: %v", err)
	}
	body := drain(t, getResp)
	var resp struct {
		SecretConfigured bool `json:"secretConfigured"`
		Total            int  `json:"total"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if resp.SecretConfigured {
		t.Errorf("secretConfigured=true, want false (no secret)")
	}
	if resp.Total != 0 {
		t.Errorf("total=%d, want 0 (no signals)", resp.Total)
	}
}
