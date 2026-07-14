// STRATEGY-LAB wave API tests: fleet table without a symbol, per-symbol rows
// with honesty flags + fleet aggregates with one, empty-state note, and the
// verbatim caveat in every payload. Fixture data only.
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

func newStratLabServer(t *testing.T) (*httptest.Server, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "stratlab_api.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	d := Deps{St: st, Cfg: baseCfg(), Version: "test", Started: time.Now()}
	srv := httptest.NewUnstartedServer(nil)
	t.Cleanup(srv.Close)
	d.Cfg.AllowedHosts = []string{srv.Listener.Addr().String()}

	mux := http.NewServeMux()
	d.registerStrategyLab(mux)
	srv.Config.Handler = d.secure(mux)
	srv.Start()
	return srv, st
}

func stratLabGET(t *testing.T, srv *httptest.Server, path string) (int, map[string]any) {
	t.Helper()
	resp, err := srv.Client().Get(srv.URL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close() //nolint:errcheck
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return resp.StatusCode, out
}

const wantStratNote = "classic published strategies backtested walk-forward on our own bars with costs — in-sample history, not live performance and not advice; a strategy is only as good as its next trade"

func TestStrategyLabAPI(t *testing.T) {
	srv, st := newStratLabServer(t)
	ctx := context.Background()
	nvda, _ := st.UpsertSymbol(ctx, "NVDA", md.Stocks, "NVIDIA")
	aapl, _ := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")

	// Empty fleet: caveat still verbatim, fleet empty.
	code, out := stratLabGET(t, srv, "/api/strategy-lab")
	if code != 200 || out["note"] != wantStratNote {
		t.Fatalf("empty fleet payload wrong: %d %v", code, out["note"])
	}
	if out["fleet"] != nil && len(out["fleet"].([]any)) != 0 {
		t.Fatalf("fleet must start empty: %v", out["fleet"])
	}

	seed := func(id int64, strat string, sharpe, ret float64, trades int, cagrOK bool) {
		t.Helper()
		if err := st.UpsertStrategyResult(ctx, store.StrategyResult{
			SymbolID: id, Strategy: strat, Ts: 1000, TotalReturn: ret,
			CAGR: 0.1, Sharpe: sharpe, MaxDD: 0.2, WinRate: 0.6,
			NTrades: trades, CAGRReported: cagrOK, WinRateOK: trades >= 2, NBars: 504,
		}); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	seed(nvda.ID, "donchian_20", 1.2, 0.3, 25, true)
	seed(nvda.ID, "rsi2_meanrev", 0.4, -0.1, 1, false)
	seed(aapl.ID, "donchian_20", 0.8, 0.1, 22, true)

	// Per-symbol: rows sorted by strategy, honesty flags carried per row.
	code, out = stratLabGET(t, srv, "/api/strategy-lab?symbol=NVDA&market=stocks")
	if code != 200 {
		t.Fatalf("code %d", code)
	}
	rows := out["strategies"].([]any)
	if len(rows) != 2 {
		t.Fatalf("want 2 rows, got %d", len(rows))
	}
	first := rows[0].(map[string]any)
	if first["strategy"] != "donchian_20" || first["cagrReported"] != true {
		t.Fatalf("first row wrong: %v", first)
	}
	second := rows[1].(map[string]any)
	if second["strategy"] != "rsi2_meanrev" || second["cagrReported"] != false ||
		second["winRateMeaningful"] != false {
		t.Fatalf("honesty flags must ride each row: %v", second)
	}
	fleet := out["fleet"].([]any)
	if len(fleet) != 2 {
		t.Fatalf("want 2 fleet strategies, got %d", len(fleet))
	}
	topAgg := fleet[0].(map[string]any)
	if topAgg["strategy"] != "donchian_20" || topAgg["nSymbols"] != 2.0 ||
		topAgg["medianSharpe"] != 1.0 || topAgg["pctProfitable"] != 1.0 {
		t.Fatalf("fleet agg wrong: %v", topAgg)
	}
	if out["note"] != wantStratNote {
		t.Fatalf("caveat must ship verbatim: %v", out["note"])
	}

	// Symbol with no rows: honest empty note.
	tsla, _ := st.UpsertSymbol(ctx, "TSLA", md.Stocks, "Tesla")
	_ = tsla
	_, out = stratLabGET(t, srv, "/api/strategy-lab?symbol=TSLA&market=stocks")
	if out["emptyNote"] == nil {
		t.Fatalf("expected emptyNote for a symbol without rows: %v", out)
	}
	// Unknown symbol -> 404.
	if code, _ := stratLabGET(t, srv, "/api/strategy-lab?symbol=ZZZ&market=stocks"); code != 404 {
		t.Fatalf("unknown symbol: code %d", code)
	}
}
