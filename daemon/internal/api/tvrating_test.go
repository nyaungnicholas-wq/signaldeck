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

// newTVRatingServer stands up GET /api/tv-rating behind the real middleware
// against a temp store (PublicReads on, like /api/shorts).
func newTVRatingServer(t *testing.T) (*httptest.Server, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "tvrating_api.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	d := Deps{St: st, Cfg: baseCfg(), Version: "test", Started: time.Now()}
	srv := httptest.NewUnstartedServer(nil)
	t.Cleanup(srv.Close)
	// The host allowlist denies everything not listed; add the loopback bind.
	d.Cfg.AllowedHosts = []string{srv.Listener.Addr().String()}

	mux := http.NewServeMux()
	d.registerTVRating(mux)
	srv.Config.Handler = d.secure(mux)
	srv.Start()
	return srv, st
}

func getTVRating(t *testing.T, srv *httptest.Server, query string) map[string]any {
	t.Helper()
	resp, err := srv.Client().Get(srv.URL + "/api/tv-rating" + query)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out
}

func TestTVRating_AvailableAndUnavailable(t *testing.T) {
	srv, st := newTVRatingServer(t)
	ctx := context.Background()

	nvda, err := st.UpsertSymbol(ctx, "NVDA", md.Stocks, "NVIDIA")
	if err != nil {
		t.Fatalf("seed NVDA: %v", err)
	}

	// Unknown symbol → available:false with a reason (never a 500).
	if out := getTVRating(t, srv, "?symbol=ZZZZ&market=stocks"); out["available"] != false {
		t.Errorf("unknown symbol should be unavailable: %v", out)
	}

	// Tracked but no rating stored yet → available:false, symbol echoed.
	out := getTVRating(t, srv, "?symbol=NVDA&market=stocks")
	if out["available"] != false {
		t.Errorf("no-rating should be unavailable: %v", out)
	}
	if out["symbol"] != "NVDA" || out["note"] == nil {
		t.Errorf("expected symbol + note: %v", out)
	}

	// Store a rating → available:true with the full shape.
	if err := st.UpsertTVRating(ctx, store.TVRatingRow{
		SymbolID: nvda.ID, Ts: 1720000000, RecoAll: 0.5576, RecoMA: 0.9333,
		RecoOther: 0.1818, RSI: 56.97, Close: 210.96, Label: "Strong Buy",
	}); err != nil {
		t.Fatalf("upsert rating: %v", err)
	}
	out = getTVRating(t, srv, "?symbol=NVDA&market=stocks")
	if out["available"] != true {
		t.Fatalf("expected available: %v", out)
	}
	if out["symbol"] != "NVDA" || out["market"] != "stocks" || out["label"] != "Strong Buy" {
		t.Errorf("shape mismatch: %v", out)
	}
	if out["recoAll"].(float64) != 0.5576 || out["rsi"].(float64) != 56.97 ||
		out["close"].(float64) != 210.96 {
		t.Errorf("values mismatch: %v", out)
	}
	if out["ts"].(float64) != 1720000000 {
		t.Errorf("ts mismatch: %v", out["ts"])
	}
	if out["note"] != tvRatingNote {
		t.Errorf("note mismatch: %v", out["note"])
	}
}
