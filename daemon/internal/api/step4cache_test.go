package api

// Step 4 (2026-10-01): member and /proof pages load fast after a restart.
// These pin the four mechanisms: the warm pass survives a failed step, a
// persisted body serves without an inline build, a cold build never holds a
// request past coldServeWait, and the ledger verify is computed once per TTL.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/ledgeranchor"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// shortColdServeWait shrinks the request-side wait for one test.
func shortColdServeWait(t *testing.T, d time.Duration) {
	t.Helper()
	orig := coldServeWait
	coldServeWait = d
	t.Cleanup(func() { coldServeWait = orig })
}

// P1: one early step failing must not starve every cache after it. The 1d
// track-record entry is given a build that has already failed; the pass must
// report that failure AND still build what comes after it.
func TestWarmCaches_AFailedStepDoesNotEndThePass(t *testing.T) {
	t.Setenv(ledgeranchor.EnvKeyPath, filepath.Join(t.TempDir(), "anchor.key"))
	d, _ := newWave2Deps(t)
	ctx := context.Background()

	failed := &coldBuild{done: make(chan struct{}), err: errors.New("injected track-record failure")}
	close(failed.done)
	key1d := d.St.CacheKey() + "|" + string(md.H1d)
	sharedTrackCache.mu.Lock()
	sharedTrackCache.ent[key1d] = &swrEntry{building: failed}
	sharedTrackCache.mu.Unlock()
	t.Cleanup(func() {
		sharedTrackCache.mu.Lock()
		delete(sharedTrackCache.ent, key1d)
		sharedTrackCache.mu.Unlock()
	})
	sharedDashCache.mu.Lock()
	sharedDashCache.global = nil
	sharedDashCache.mu.Unlock()

	err := d.WarmCaches(ctx)
	if err == nil || !strings.Contains(err.Error(), "injected track-record failure") {
		t.Fatalf("WarmCaches err = %v; the failed step must be reported", err)
	}

	payload := func(c *swrCache, key string) bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		e := c.ent[key]
		return e != nil && e.payload != nil
	}
	for name, ok := range map[string]bool{
		"track-record 1w": payload(sharedTrackCache, d.St.CacheKey()+"|"+string(md.H1w)),
		"track-record 1h": payload(sharedTrackCache, d.St.CacheKey()+"|"+string(md.H1h)),
		"regimes":         payload(sharedRegimesCache, d.St.CacheKey()+"|regimes"),
		"ledger-verify":   payload(sharedLedgerVerifyCache, d.ledgerVerifyKey(ctx)),
		"predictions 1d":  payload(sharedPredictionsCache, d.St.CacheKey()+"|"+string(md.H1d)),
	} {
		if !ok {
			t.Errorf("%s was not built: a failed track-record step ended the pass", name)
		}
	}
	sharedVolRecordSWR.mu.Lock()
	vol := sharedVolRecordSWR.ent[d.St.CacheKey()+"|record"]
	sharedVolRecordSWR.mu.Unlock()
	if vol == nil || vol.body == nil {
		t.Error("the volatility record was not built after the failed step")
	}
	sharedDashCache.mu.Lock()
	dash := sharedDashCache.global != nil
	sharedDashCache.mu.Unlock()
	if !dash {
		t.Error("the dashboard was not built after the failed step")
	}
}

// P2: a fresh cache (a new process) pointed at a persisted body serves it at
// once, as stale, and rebuilds behind it; a different key never reads it.
func TestSWRCache_PersistedPayloadServesWithoutAnInlineBuild(t *testing.T) {
	shortColdServeWait(t, 200*time.Millisecond)
	ctx := context.Background()
	file := filepath.Join(t.TempDir(), "apicache", "x.db.track-record-1d.json")

	first := newSWRCache(time.Minute)
	if _, err := first.getAt(ctx, file, "st9|1d", func(context.Context) (map[string]any, error) {
		return map[string]any{"v": 1, "big": int64(1) << 60}, nil
	}); err != nil {
		t.Fatalf("first build: %v", err)
	}

	release := make(chan struct{})
	started := make(chan struct{}, 1)
	slow := func(context.Context) (map[string]any, error) {
		started <- struct{}{}
		<-release
		return map[string]any{"v": 2}, nil
	}
	fresh := newSWRCache(time.Minute) // the daemon after a restart
	t0 := time.Now()
	got, err := fresh.getAt(ctx, file, "st9|1d", slow)
	if err != nil {
		t.Fatalf("read after restart: %v (the persisted body was not served)", err)
	}
	if time.Since(t0) > 100*time.Millisecond {
		t.Errorf("the persisted body took %v: it waited on the builder", time.Since(t0))
	}
	if got["v"] != json.Number("1") || got["big"] != json.Number("1152921504606846976") {
		t.Fatalf("served %v, want the persisted payload with exact numbers", got)
	}
	select {
	case <-started: // served STALE: the rebuild runs behind it
	case <-time.After(2 * time.Second):
		t.Fatal("serving the persisted body did not start a background rebuild")
	}
	close(release)
	deadline := time.Now().Add(2 * time.Second)
	for {
		if p, _, ok := loadPersistedPayload(file, "st9|1d"); ok && p["v"] == json.Number("2") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the rebuilt payload was never persisted")
		}
		time.Sleep(5 * time.Millisecond)
	}

	other := newSWRCache(time.Minute)
	got, err = other.getAt(ctx, file, "st10|1d", func(context.Context) (map[string]any, error) {
		return map[string]any{"v": "built"}, nil
	})
	if err != nil || got["v"] != "built" {
		t.Fatalf("another key read the persisted file: got=%v err=%v", got, err)
	}
}

// P2, the body cache: the same contract for the volatility record's cache type.
func TestSWRBodyCache_PersistedBodyServesWithoutAnInlineBuild(t *testing.T) {
	shortColdServeWait(t, 200*time.Millisecond)
	file := filepath.Join(t.TempDir(), "apicache", "x.db.vol-record.json")
	req := httptest.NewRequest(http.MethodGet, "/api/vol-forecast/record", nil)

	first := newSWRBodyCache(time.Minute)
	first.serveAt(file, "st9|record", httptest.NewRecorder(), req, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"asOf": 1})
	})

	release := make(chan struct{})
	fresh := newSWRBodyCache(time.Minute)
	rec := httptest.NewRecorder()
	fresh.serveAt(file, "st9|record", rec, req, func(w http.ResponseWriter, r *http.Request) {
		<-release
		writeJSON(w, map[string]any{"asOf": 2})
	})
	close(release)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"asOf":1`) {
		t.Fatalf("after a restart: %d %s, want the persisted body", rec.Code, rec.Body.String())
	}
	// The background rebuild lands and is persisted (and is done with the temp
	// dir before the test removes it).
	deadline := time.Now().Add(2 * time.Second)
	for {
		if b, _, ok := loadPersistedBody(file, "st9|record"); ok && strings.Contains(string(b), `"asOf":2`) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the rebuilt body was never persisted")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// P3: a cold build past coldServeWait answers 503 "warming" with Retry-After
// and keeps building, so the retry is served.
func TestSWRCache_ColdBuildPastTheServeWaitAnswersWarming(t *testing.T) {
	shortColdServeWait(t, 50*time.Millisecond)
	ctx := context.Background()
	c := newSWRCache(time.Minute)
	release := make(chan struct{})
	build := func(context.Context) (map[string]any, error) {
		<-release
		return map[string]any{"v": 1}, nil
	}
	if _, err := c.get(ctx, "k", build); !errors.Is(err, errWarming) {
		t.Fatalf("err = %v, want errWarming once coldServeWait passed", err)
	}
	close(release)
	deadline := time.Now().Add(2 * time.Second)
	for {
		got, err := c.get(ctx, "k", build)
		if err == nil && got["v"] == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the detached build never landed: got=%v err=%v", got, err)
		}
		time.Sleep(5 * time.Millisecond)
	}

	// The warmer is the exception: it waits the build out.
	slow := make(chan struct{})
	go func() { time.Sleep(150 * time.Millisecond); close(slow) }()
	got, err := c.get(waitForBuild(ctx), "warmer", func(context.Context) (map[string]any, error) {
		<-slow
		return map[string]any{"v": 3}, nil
	})
	if err != nil || got["v"] != 3 {
		t.Fatalf("warmer get = %v, %v; it must wait for the build", got, err)
	}
}

// P3 at the route: what the web client sees while /api/track-record is cold.
func TestTrackRecordRoute_ColdAnswersWarmingWithRetryAfter(t *testing.T) {
	shortColdServeWait(t, 50*time.Millisecond)
	d, _ := newWave2Deps(t)
	key := d.St.CacheKey() + "|" + string(md.H1w)
	inflight := &coldBuild{done: make(chan struct{})} // a build still running
	sharedTrackCache.mu.Lock()
	sharedTrackCache.ent[key] = &swrEntry{building: inflight}
	sharedTrackCache.mu.Unlock()
	t.Cleanup(func() {
		sharedTrackCache.mu.Lock()
		delete(sharedTrackCache.ent, key)
		sharedTrackCache.mu.Unlock()
	})

	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		d.trackRecordCached(rec, httptest.NewRequest(http.MethodGet, "/api/track-record?horizon=1w", nil))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the request waited on the cold build instead of answering warming")
	}
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") != "30" ||
		strings.TrimSpace(rec.Body.String()) != `{"error":"warming"}` {
		t.Fatalf("cold route answered %d Retry-After=%q %s; want 503, 30, {\"error\":\"warming\"}",
			rec.Code, rec.Header().Get("Retry-After"), rec.Body.String())
	}
}

// P4: the default verify is computed once per TTL. Rows appended after the
// first request are not counted by a second one (it is the cached result,
// with the same computedAt), while ?full=1 stays live and sees them.
func TestLedgerVerify_DefaultResultIsComputedOncePerTTL(t *testing.T) {
	t.Setenv(anchorEnvDisable, "1") // a new anchor rightly re-keys the cache; keep it still
	srv, st := newLedgerServer(t, nil)
	sym, err := st.UpsertSymbol(context.Background(), "AAPL", md.Stocks, "Apple")
	if err != nil {
		t.Fatal(err)
	}
	appendLedgerRows(t, st, sym.ID, 5, 0.5)

	computedAt := func(path string) (int64, string) {
		res := ledgerGet(t, srv, path)
		defer res.Body.Close() //nolint:errcheck
		var b struct {
			Count      int64  `json:"count"`
			ComputedAt string `json:"computedAt"`
		}
		if res.StatusCode != http.StatusOK || json.NewDecoder(res.Body).Decode(&b) != nil {
			t.Fatalf("%s: status %d", path, res.StatusCode)
		}
		return b.Count, b.ComputedAt
	}
	n1, at1 := computedAt("/api/ledger/verify")
	if n1 != 5 || at1 == "" {
		t.Fatalf("first verify: count=%d computedAt=%q, want 5 and a timestamp", n1, at1)
	}
	if _, err := st.AppendLedger(context.Background(), store.LedgerEntry{
		PredictedAt: 9000, SymbolID: sym.ID, Horizon: md.H1d, BarTs: 9000,
		RawProb: 0.5, CalProb: 0.5, FeatureHash: "fh", ModelVersion: 1,
	}); err != nil {
		t.Fatal(err)
	}
	n2, at2 := computedAt("/api/ledger/verify")
	if n2 != 5 || at2 != at1 {
		t.Fatalf("second verify: count=%d computedAt=%q; want the first result (5, %q) — it ran the verification again", n2, at2, at1)
	}
	if n3, _ := computedAt("/api/ledger/verify?full=1"); n3 != 6 {
		t.Fatalf("?full=1 count=%d, want 6: the auditor's walk must stay live", n3)
	}
}

// The member view of a regimes payload must drop crypto rows in BOTH shapes:
// the typed build, and the decoded JSON a persisted payload comes back as.
func TestWithoutCryptoForecasts_TypedAndPersistedShapes(t *testing.T) {
	typed := map[string]any{"forecasts": map[string][]any{
		"trend21": {
			store.RegimeForecast{Symbol: "AAPL", Market: string(md.Stocks)},
			store.RegimeForecast{Symbol: "BTC/USD", Market: string(md.Crypto)},
		},
		"cryptoOnly": {store.RegimeForecast{Symbol: "ETH/USD", Market: string(md.Crypto)}},
	}}
	raw, err := json.Marshal(typed)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	for name, resp := range map[string]map[string]any{"typed": typed, "persisted": decoded} {
		out, err := json.Marshal(withoutCryptoForecasts(resp))
		if err != nil {
			t.Fatal(err)
		}
		s := string(out)
		if !strings.Contains(s, `"AAPL"`) || strings.Contains(s, "BTC/USD") || strings.Contains(s, "ETH/USD") {
			t.Errorf("%s payload, member view = %s; want AAPL kept and every crypto row dropped", name, s)
		}
	}
}
