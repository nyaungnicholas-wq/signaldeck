// CANDLESTICK-PATTERNS wave API tests: /api/candle-patterns payload shape
// (firing bars only, measured edge attached where a gated stat exists else
// null, verbatim caveat) behind the real middleware; unknown-symbol, thin-
// history and bad-tf paths. Fixture data only.
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

func newCandlePatternsServer(t *testing.T) (*httptest.Server, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "candlepatterns_api.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	d := Deps{St: st, Cfg: baseCfg(), Version: "test", Started: time.Now()}
	srv := httptest.NewUnstartedServer(nil)
	t.Cleanup(srv.Close)
	d.Cfg.AllowedHosts = []string{srv.Listener.Addr().String()}

	mux := http.NewServeMux()
	d.registerCandlePatterns(mux)
	srv.Config.Handler = d.secure(mux)
	srv.Start()
	return srv, st
}

func apiGET(t *testing.T, srv *httptest.Server, path string) (int, map[string]any) {
	t.Helper()
	resp, err := srv.Client().Get(srv.URL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close() //nolint:errcheck
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// seedEngulfingDaily writes ~180 daily bars whose motif prints a bullish
// engulfing at each dip (mirrors the pattern-stats worker's fixture).
func seedEngulfingDaily(t *testing.T, st *store.Store, id int64, motifs int) {
	t.Helper()
	var bars []md.Bar
	ts := int64(1)
	L := 100.0
	push := func(o, h, l, c float64) {
		bars = append(bars, md.Bar{SymbolID: id, TF: md.TF1d, Ts: ts * 86400, Open: o, High: h, Low: l, Close: c, Volume: 1000})
		ts++
	}
	for m := 0; m < motifs; m++ {
		push(L+2, L+2.5, L-0.5, L)
		push(L-1, L+4, L-1.5, L+3.5)
		push(L+3, L+5, L+2.5, L+4.5)
		push(L+4, L+6, L+3.5, L+5.5)
		push(L+5, L+7, L+4.5, L+6.5)
		push(L+6, L+8, L+5.5, L+7.5)
		L += 7
	}
	if err := st.UpsertBars(context.Background(), bars); err != nil {
		t.Fatalf("seed bars: %v", err)
	}
}

func TestCandlePatternsAPI(t *testing.T) {
	srv, st := newCandlePatternsServer(t)
	ctx := context.Background()
	sym, _ := st.UpsertSymbol(ctx, "BTC/USD", md.Crypto, "Bitcoin")
	seedEngulfingDaily(t, st, sym.ID, 30)
	// A measured edge for one pattern; another firing pattern will have none.
	if err := st.UpsertPatternStat(ctx, store.PatternStat{
		SymbolID: sym.ID, Pattern: "bullish_engulfing", Horizon: 5, HitRate: 0.63, MeanFwd: 0.02, N: 29,
	}); err != nil {
		t.Fatalf("upsert stat: %v", err)
	}

	// Unknown symbol -> 404.
	if code, _ := apiGET(t, srv, "/api/candle-patterns?symbol=ZZZ&market=stocks"); code != 404 {
		t.Fatalf("unknown symbol: code %d", code)
	}
	// Bad tf -> 400.
	if code, _ := apiGET(t, srv, "/api/candle-patterns?symbol=BTC/USD&market=crypto&tf=1h"); code != 400 {
		t.Fatalf("bad tf: code %d", code)
	}

	code, out := apiGET(t, srv, "/api/candle-patterns?symbol=BTC/USD&market=crypto")
	if code != 200 {
		t.Fatalf("code %d", code)
	}
	if out["note"] != candlePatternsNote {
		t.Fatalf("caveat must ship verbatim: %v", out["note"])
	}
	if out["tf"] != "1d" {
		t.Fatalf("tf should default to 1d, got %v", out["tf"])
	}
	bars, _ := out["bars"].([]any)
	if len(bars) == 0 {
		t.Fatalf("expected firing bars, got none: %v", out)
	}
	// Every returned bar must carry at least one pattern (no-pattern bars omit).
	foundMeasured, foundNull := false, false
	for _, b := range bars {
		bm := b.(map[string]any)
		pats, _ := bm["patterns"].([]any)
		if len(pats) == 0 {
			t.Fatalf("a returned bar had no patterns: %v", bm)
		}
		for _, p := range pats {
			pm := p.(map[string]any)
			if pm["name"] == "bullish_engulfing" {
				if m, ok := pm["measured"].(map[string]any); ok && m != nil {
					foundMeasured = true
					if m["n"].(float64) != 29 {
						t.Fatalf("measured n wrong: %v", m)
					}
				}
			} else if pm["measured"] == nil {
				foundNull = true
			}
		}
	}
	if !foundMeasured {
		t.Fatal("bullish_engulfing should carry its measured edge")
	}
	if !foundNull {
		t.Fatal("a pattern without a stored stat should carry measured:null")
	}
}

func TestCandlePatternsAPIThinHistory(t *testing.T) {
	srv, st := newCandlePatternsServer(t)
	ctx := context.Background()
	sym, _ := st.UpsertSymbol(ctx, "ETH/USD", md.Crypto, "Ether")
	// Only a handful of flat bars — nothing fires.
	var bars []md.Bar
	for i := int64(1); i <= 5; i++ {
		bars = append(bars, md.Bar{SymbolID: sym.ID, TF: md.TF1d, Ts: i * 86400, Open: 100, High: 100, Low: 100, Close: 100, Volume: 1})
	}
	if err := st.UpsertBars(ctx, bars); err != nil {
		t.Fatalf("seed: %v", err)
	}
	code, out := apiGET(t, srv, "/api/candle-patterns?symbol=ETH/USD&market=crypto")
	if code != 200 {
		t.Fatalf("code %d", code)
	}
	if b, _ := out["bars"].([]any); len(b) != 0 {
		t.Fatalf("flat thin history should fire nothing, got %d bars", len(b))
	}
	if out["note"] != candlePatternsNote {
		t.Fatalf("caveat must ship verbatim even when empty: %v", out["note"])
	}
}
