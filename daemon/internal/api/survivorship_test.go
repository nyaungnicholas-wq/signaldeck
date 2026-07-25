// Wave-2 API tests: the survivorship label (#20) on every exposed surface,
// the /api/digest read route, and the cache warmer's shared-cache contract.
package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/briefing"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

func newWave2Deps(t *testing.T) (Deps, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "wave2.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return Deps{St: st, Cfg: baseCfg(), Version: "test", Started: time.Now()}, st
}

func decodeBody(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("json: %v\n%s", err, body)
	}
	return m
}

func assertSurvivorship(t *testing.T, m map[string]any, surface string) {
	t.Helper()
	blk, ok := m["survivorship"].(map[string]any)
	if !ok {
		t.Fatalf("%s: payload missing the survivorship block: %v", surface, m["survivorship"])
	}
	if blk["exposed"] != true {
		t.Fatalf("%s: exposed must be true", surface)
	}
	if blk["note"] != survivorshipNote {
		t.Fatalf("%s: note drifted: %q", surface, blk["note"])
	}
}

// Every surface the wave names carries the identical survivorship block.
func TestSurvivorshipLabelOnAllSurfaces(t *testing.T) {
	d, st := newWave2Deps(t)
	ctx := context.Background()

	// /api/signal-backtest (live compute path).
	rec := httptest.NewRecorder()
	d.signalBacktest(rec, httptest.NewRequest("GET", "/api/signal-backtest?horizon=1d", nil))
	assertSurvivorship(t, decodeBody(t, rec.Body.Bytes()), "signal-backtest")

	// /api/track-record: top-level (predictions) + regimes section.
	rec = httptest.NewRecorder()
	d.trackRecord(rec, httptest.NewRequest("GET", "/api/track-record?horizon=1d", nil))
	tr := decodeBody(t, rec.Body.Bytes())
	assertSurvivorship(t, tr, "track-record")
	regimes, ok := tr["regimes"].(map[string]any)
	if !ok {
		t.Fatalf("track-record missing regimes section")
	}
	assertSurvivorship(t, regimes, "track-record.regimes")

	// /api/regimes (top-level note).
	rec = httptest.NewRecorder()
	d.structuralRegimes(rec, httptest.NewRequest("GET", "/api/regimes", nil))
	assertSurvivorship(t, decodeBody(t, rec.Body.Bytes()), "regimes")

	// /api/backtest (POST, needs a symbol with bars).
	sym, err := st.UpsertSymbol(ctx, "AAA", md.Stocks, "")
	if err != nil {
		t.Fatalf("sym: %v", err)
	}
	bars := make([]md.Bar, 0, 120)
	for i := 0; i < 120; i++ {
		c := 100 + float64(i%20)
		bars = append(bars, md.Bar{SymbolID: sym.ID, TF: md.TF1d,
			Ts: int64(20000+i) * 86400, Open: c, High: c, Low: c, Close: c, Volume: 1e6})
	}
	if err := st.UpsertBars(ctx, bars); err != nil {
		t.Fatalf("bars: %v", err)
	}
	rec = httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/backtest",
		strings.NewReader(`{"symbol":"AAA","market":"stocks","text":"20/50 crossover"}`))
	d.backtestRun(rec, req)
	if rec.Code != 200 {
		t.Fatalf("backtest: %d %s", rec.Code, rec.Body.String())
	}
	assertSurvivorship(t, decodeBody(t, rec.Body.Bytes()), "backtest")

	// The crypto regime kinds' docs must ship in the regimes payload.
	rec = httptest.NewRecorder()
	d.structuralRegimes(rec, httptest.NewRequest("GET", "/api/regimes", nil))
	kinds := decodeBody(t, rec.Body.Bytes())["kinds"].(map[string]any)
	for _, k := range []string{"trend21-crypto", "liquidity21-crypto"} {
		e, ok := kinds[k].(map[string]any)
		if !ok {
			t.Fatalf("regimes kinds missing %s", k)
		}
		if cav, _ := e["caveat"].(string); cav == "" || !strings.Contains(cav, "quarterly clusters") {
			t.Fatalf("%s caveat missing the cluster disclosure: %q", k, e["caveat"])
		}
	}
}

// /api/digest: honest empty state, then the stored text with sentAt=0 when no
// transport delivered it.
func TestDigestEndpoint(t *testing.T) {
	d, st := newWave2Deps(t)
	ctx := context.Background()

	rec := httptest.NewRecorder()
	d.digest(rec, httptest.NewRequest("GET", "/api/digest", nil))
	if m := decodeBody(t, rec.Body.Bytes()); m["available"] != false {
		t.Fatalf("empty state must be available:false, got %v", m)
	}

	if err := st.SetMeta(ctx, briefing.MetaDigestLastText, "digest body"); err != nil {
		t.Fatalf("meta: %v", err)
	}
	if err := st.SetMeta(ctx, briefing.MetaDigestLastTs, strconv.Itoa(1234)); err != nil {
		t.Fatalf("meta: %v", err)
	}
	rec = httptest.NewRecorder()
	d.digest(rec, httptest.NewRequest("GET", "/api/digest", nil))
	m := decodeBody(t, rec.Body.Bytes())
	if m["available"] != true || m["text"] != "digest body" {
		t.Fatalf("digest payload wrong: %v", m)
	}
	if m["generatedAt"].(float64) != 1234 || m["sentAt"].(float64) != 0 {
		t.Fatalf("timestamps wrong: %v", m)
	}
}

// WarmCaches must leave the shared dashboard cache built and the default
// movers response-cache entry hot — exactly what the handlers then serve.
func TestWarmCachesFillsSharedCaches(t *testing.T) {
	d, _ := newWave2Deps(t)
	ctx := context.Background()

	// Reset the process-wide caches (other tests in the package share them).
	sharedDashCache.mu.Lock()
	sharedDashCache.global = nil
	sharedDashCache.mu.Unlock()
	sharedMoversCache.mu.Lock()
	sharedMoversCache.ent = map[string]*swrBodyEntry{}
	sharedMoversCache.mu.Unlock()

	if err := d.WarmCaches(ctx); err != nil {
		t.Fatalf("warm: %v", err)
	}
	sharedDashCache.mu.Lock()
	built := sharedDashCache.global != nil
	sharedDashCache.mu.Unlock()
	if !built {
		t.Fatal("dashboard cache not built by the warmer")
	}
	sharedMoversCache.mu.Lock()
	e, ok := sharedMoversCache.ent[""]
	sharedMoversCache.mu.Unlock()
	if !ok || len(e.body) == 0 {
		t.Fatal("movers default cache entry not built by the warmer")
	}
	// A subsequent handler request is a cache hit with the warmed body.
	rec := httptest.NewRecorder()
	sharedMoversCache.serve("", rec, httptest.NewRequest("GET", "/api/movers", nil), d.movers)
	if rec.Header().Get("X-Cache") != "hit" {
		t.Fatal("handler request after warm must be a cache hit")
	}
}
