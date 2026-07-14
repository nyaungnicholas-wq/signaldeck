// CANDLESTICK-PATTERNS wave trend API tests: /api/trend payload shape
// (classification + slope + fitted trendlines + channel + verbatim caveat) on
// the ungated path, and the thin-history gated path (classification null + a
// stated gate reason). Unknown-symbol and bad-tf paths too. Fixture data only.
package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

func newTrendServer(t *testing.T) (*httptest.Server, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "trend_api.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	d := Deps{St: st, Cfg: baseCfg(), Version: "test", Started: time.Now()}
	srv := httptest.NewUnstartedServer(nil)
	t.Cleanup(srv.Close)
	d.Cfg.AllowedHosts = []string{srv.Listener.Addr().String()}

	mux := http.NewServeMux()
	d.registerTrendRead(mux)
	srv.Config.Handler = d.secure(mux)
	srv.Start()
	return srv, st
}

// seedZigzagDaily writes an ascending zig-zag (higher highs + higher lows) so
// the trend read classifies uptrend with a fitted support line.
func seedZigzagDaily(t *testing.T, st *store.Store, id int64, n int) {
	t.Helper()
	var bars []md.Bar
	for i := 0; i < n; i++ {
		phase := i % 8
		var tri float64
		if phase <= 4 {
			tri = float64(phase) / 4.0
		} else {
			tri = float64(8-phase) / 4.0
		}
		c := 100.0 + 0.5*float64(i) + 8*tri
		bars = append(bars, md.Bar{SymbolID: id, TF: md.TF1d, Ts: int64(i) * 86400, Open: c, High: c + 1, Low: c - 1, Close: c, Volume: 1000})
	}
	if err := st.UpsertBars(context.Background(), bars); err != nil {
		t.Fatalf("seed bars: %v", err)
	}
}

func TestTrendAPIUngated(t *testing.T) {
	srv, st := newTrendServer(t)
	ctx := context.Background()
	sym, _ := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	seedZigzagDaily(t, st, sym.ID, 48)

	// Unknown symbol -> 404; bad tf -> 400.
	if code, _ := apiGET(t, srv, "/api/trend?symbol=ZZZ&market=stocks"); code != 404 {
		t.Fatalf("unknown symbol: code %d", code)
	}
	if code, _ := apiGET(t, srv, "/api/trend?symbol=AAPL&market=stocks&tf=1h"); code != 400 {
		t.Fatalf("bad tf: code %d", code)
	}

	code, out := apiGET(t, srv, "/api/trend?symbol=AAPL&market=stocks")
	if code != 200 {
		t.Fatalf("code %d", code)
	}
	if out["note"] != trendAPINote {
		t.Fatalf("caveat must ship verbatim: %v", out["note"])
	}
	if out["classification"] != "uptrend" {
		t.Fatalf("classification = %v, want uptrend", out["classification"])
	}
	if slope, _ := out["slopePctPerBar"].(float64); slope <= 0 {
		t.Fatalf("slopePctPerBar = %v, want positive", out["slopePctPerBar"])
	}
	lines, _ := out["trendlines"].([]any)
	if len(lines) == 0 {
		t.Fatalf("expected fitted trendlines, got none: %v", out)
	}
	foundSupport := false
	for _, l := range lines {
		lm := l.(map[string]any)
		if lm["kind"] == "support" {
			foundSupport = true
			if tc, _ := lm["touchCount"].(float64); tc < 2 {
				t.Fatalf("support touchCount = %v, want >= 2", lm["touchCount"])
			}
		}
	}
	if !foundSupport {
		t.Fatal("expected a support trendline for an ascending series")
	}
	if _, present := out["gateReason"]; present {
		t.Fatalf("no gate reason should appear on the ungated path: %v", out)
	}
}

func TestTrendAPIThinHistoryGated(t *testing.T) {
	srv, st := newTrendServer(t)
	ctx := context.Background()
	sym, _ := st.UpsertSymbol(ctx, "MSFT", md.Stocks, "Microsoft")
	seedZigzagDaily(t, st, sym.ID, 10) // below trend.MinBars

	code, out := apiGET(t, srv, "/api/trend?symbol=MSFT&market=stocks")
	if code != 200 {
		t.Fatalf("code %d", code)
	}
	if out["classification"] != nil {
		t.Fatalf("thin history must serve classification=null, got %v", out["classification"])
	}
	if reason, _ := out["gateReason"].(string); reason == "" {
		t.Fatalf("thin history must state a gate reason: %v", out)
	}
	if lines, _ := out["trendlines"].([]any); len(lines) != 0 {
		t.Fatalf("thin history must serve empty trendlines, got %v", lines)
	}
	if out["note"] != trendAPINote {
		t.Fatalf("caveat must ship verbatim even when gated: %v", out["note"])
	}
}
