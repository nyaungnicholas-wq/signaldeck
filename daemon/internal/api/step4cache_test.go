package api

// Step 4 (2026-10-01): member and /proof pages load fast after a restart.
// These pin the four mechanisms: the warm pass survives a failed step, a
// persisted body serves without an inline build, a cold build never holds a
// request past coldServeWait, and the ledger verify is computed once per TTL.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
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

// ── review fixes (2026-10-01): the cache findings #6-#10 and #12 ─────────────

// verifyStatus GETs the default verify and returns its status and, on a 200,
// whether it claimed intact.
func verifyStatus(t *testing.T, srv *httptest.Server) (int, bool) {
	t.Helper()
	res := ledgerGet(t, srv, "/api/ledger/verify")
	defer res.Body.Close() //nolint:errcheck
	var b struct {
		Intact bool `json:"intact"`
	}
	if res.StatusCode == http.StatusOK && json.NewDecoder(res.Body).Decode(&b) != nil {
		t.Fatal("undecodable 200 verify body")
	}
	return res.StatusCode, b.Intact
}

// tamperUnscannable appends a row after the checkpoint and makes it unscannable,
// so the verification ERRORS rather than reporting intact=false (the reviewer's
// scenario: the old cache served the last intact:true for ever).
func tamperUnscannable(t *testing.T, st *store.Store, symID int64) {
	t.Helper()
	ctx := context.Background()
	if _, err := st.AppendLedger(ctx, store.LedgerEntry{
		PredictedAt: 9000, SymbolID: symID, Horizon: md.H1d, BarTs: 9000,
		RawProb: 0.5, CalProb: 0.5, FeatureHash: "fh", ModelVersion: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().ExecContext(ctx,
		`UPDATE prediction_ledger SET raw_prob='tampered' WHERE seq=(SELECT MAX(seq) FROM prediction_ledger)`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.VerifyLedgerCached(ctx); err == nil {
		t.Fatal("setup: the tampered row did not make the verification error")
	}
}

// ageVerify backdates the cached verify entry for st (anchoring disabled, so
// its key is ...|a0) and returns it.
func ageVerify(t *testing.T, st *store.Store, by time.Duration) *swrEntry {
	t.Helper()
	sharedLedgerVerifyCache.mu.Lock()
	defer sharedLedgerVerifyCache.mu.Unlock()
	e := sharedLedgerVerifyCache.ent[st.CacheKey()+"|ledger-verify|a0"]
	if e == nil || e.payload == nil {
		t.Fatal("setup: no cached verify to age")
	}
	e.builtAt = time.Now().Add(-by)
	return e
}

// #6 (a)+(b): once the verification starts failing, the default verify stops
// answering intact:true after at most one stale read, and nothing is persisted
// for a restart to serve.
func TestLedgerVerify_AFailedRebuildStopsTheIntactAnswer(t *testing.T) {
	t.Setenv(anchorEnvDisable, "1")
	srv, st := newLedgerServer(t, nil)
	sym, err := st.UpsertSymbol(context.Background(), "AAPL", md.Stocks, "Apple")
	if err != nil {
		t.Fatal(err)
	}
	appendLedgerRows(t, st, sym.ID, 5, 0.5)
	if code, intact := verifyStatus(t, srv); code != http.StatusOK || !intact {
		t.Fatalf("first verify: %d intact=%v, want 200 intact", code, intact)
	}
	persisted := filepath.Join(filepath.Dir(st.Path()), "apicache", filepath.Base(st.Path())+".ledger-verify.json")
	if _, err := os.Stat(persisted); !os.IsNotExist(err) {
		t.Fatalf("the verify result was persisted (stat err %v): a restart would serve a previous process's proof", err)
	}

	tamperUnscannable(t, st, sym.ID)
	e := ageVerify(t, st, 3*time.Minute) // past the 2m TTL, inside maxStale
	// One stale read is allowed; it starts the refresh, which fails.
	if code, _ := verifyStatus(t, srv); code != http.StatusOK {
		t.Fatalf("stale read: %d, want the stale 200 while the refresh runs", code)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		sharedLedgerVerifyCache.mu.Lock()
		busy := e.rebuilding
		sharedLedgerVerifyCache.mu.Unlock()
		if !busy {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the refresh never finished")
		}
		time.Sleep(5 * time.Millisecond)
	}
	for i := 0; i < 3; i++ {
		if code, intact := verifyStatus(t, srv); code == http.StatusOK && intact {
			t.Fatalf("read %d after the failed rebuild: 200 intact:true — the old proof outlived its evidence", i)
		}
	}
}

// #6 (c): a verify result older than maxStale is never served, even before any
// rebuild has failed.
func TestLedgerVerify_AProofOlderThanMaxStaleIsNotServed(t *testing.T) {
	t.Setenv(anchorEnvDisable, "1")
	srv, st := newLedgerServer(t, nil)
	sym, err := st.UpsertSymbol(context.Background(), "AAPL", md.Stocks, "Apple")
	if err != nil {
		t.Fatal(err)
	}
	appendLedgerRows(t, st, sym.ID, 5, 0.5)
	if code, intact := verifyStatus(t, srv); code != http.StatusOK || !intact {
		t.Fatalf("first verify: %d intact=%v", code, intact)
	}
	tamperUnscannable(t, st, sym.ID)
	ageVerify(t, st, 11*time.Minute)
	if code, intact := verifyStatus(t, srv); code == http.StatusOK && intact {
		t.Fatal("an 11-minute-old intact:true was served: maxStale did not hold")
	}
}

// #9: the default verify build has its own bound; at it the build returns and
// gives its verify semaphore slot back.
func TestLedgerVerifyBuild_IsBoundedAndReleasesTheSemaphore(t *testing.T) {
	t.Setenv(anchorEnvDisable, "1")
	_, st := newLedgerServer(t, nil)
	sym, err := st.UpsertSymbol(context.Background(), "AAPL", md.Stocks, "Apple")
	if err != nil {
		t.Fatal(err)
	}
	appendLedgerRows(t, st, sym.ID, 5, 0.5)
	orig := ledgerVerifyBuildTimeout
	ledgerVerifyBuildTimeout = 200 * time.Millisecond
	t.Cleanup(func() { ledgerVerifyBuildTimeout = orig })

	// Every read connection taken: the verification's first query cannot start,
	// as a wedged pool or writer would leave it.
	var conns []*sql.Conn
	release := func() {
		for _, c := range conns {
			_ = c.Close()
		}
		conns = nil
	}
	defer release()
	for i := 0; i < st.DB().Stats().MaxOpenConnections; i++ {
		c, err := st.DB().Conn(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		conns = append(conns, c)
	}

	done := make(chan error, 1)
	go func() {
		// No deadline of its own, like the detached context a cache build gets.
		_, err := Deps{St: st}.buildLedgerVerify(context.Background())
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("build err = %v, want its own deadline", err)
		}
	case <-time.After(3 * time.Second):
		release()
		<-done
		t.Fatal("the verify build ran past 3s with a 200ms bound: it is unbounded")
	}
	if n := len(ledgerVerifySem); n != 0 {
		t.Fatalf("%d verify semaphore slot(s) still held after the bounded build returned", n)
	}
}

// #10: a build that writes an anchor also answers the key that anchor creates,
// so the next reader is served that result instead of verifying again.
func TestLedgerVerify_AnAnchoringBuildAnswersThePostAnchorKey(t *testing.T) {
	srv, st := newLedgerServer(t, nil) // anchoring on, key in a temp dir
	sym, err := st.UpsertSymbol(context.Background(), "AAPL", md.Stocks, "Apple")
	if err != nil {
		t.Fatal(err)
	}
	appendLedgerRows(t, st, sym.ID, 5, 0.5)
	first := getLedgerVerify(t, srv, "")
	if !first.Tamper.Anchoring.Wrote {
		t.Fatalf("setup: the first verify did not anchor (%q)", first.Tamper.Anchoring.Reason)
	}
	second := getLedgerVerify(t, srv, "")
	if !second.Tamper.Anchoring.Wrote {
		t.Fatalf("the second read ran a second verification (anchoring %q): the anchoring build was filed only under the pre-anchor key",
			second.Tamper.Anchoring.Reason)
	}
}

// #7: a persisted body is served only to the build that wrote it, and only
// while it is under persistMaxAge old.
func TestPersistedBody_AnotherBuildOrAStaleFileIsNotServed(t *testing.T) {
	shortColdServeWait(t, 2*time.Second)
	ctx := context.Background()
	file := filepath.Join(t.TempDir(), "apicache", "x.db.track-record-1d.json")
	orig := persistBuild
	t.Cleanup(func() { persistBuild = orig })
	val := func(v string) func(context.Context) (map[string]any, error) {
		return func(context.Context) (map[string]any, error) { return map[string]any{"v": v}, nil }
	}

	persistBuild = "rev-a"
	if _, err := newSWRCache(time.Minute).getAt(ctx, file, "st2|1d", val("a")); err != nil {
		t.Fatal(err)
	}
	persistBuild = "rev-b" // the next deploy, same key ("st2" on every boot)
	if got, err := newSWRCache(time.Minute).getAt(ctx, file, "st2|1d", val("b")); err != nil || got["v"] != "b" {
		t.Fatalf("after a deploy: got %v (err %v), want a cold build, not the previous build's body", got, err)
	}

	persistBody(file, "st2|1d", time.Now().Add(-25*time.Hour), []byte(`{"v":"old"}`))
	if got, err := newSWRCache(time.Minute).getAt(ctx, file, "st2|1d", val("new")); err != nil || got["v"] != "new" {
		t.Fatalf("a 25h-old body: got %v (err %v), want a cold build", got, err)
	}

	// Control: inside the limit, the same build's body is served.
	persistBody(file, "st2|1d", time.Now().Add(-23*time.Hour), []byte(`{"v":"old"}`))
	got, err := newSWRCache(time.Minute).getAt(ctx, file, "st2|1d", val("rebuilt"))
	if err != nil || got["v"] != "old" {
		t.Fatalf("a 23h-old body from this build: got %v (err %v), want it served", got, err)
	}
	deadline := time.Now().Add(2 * time.Second) // let the refresh behind it land in the temp dir
	for {
		if p, _, ok := loadPersistedPayload(file, "st2|1d"); ok && p["v"] == "rebuilt" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the refresh behind the served body never landed")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// #8: the warmer refreshes a persisted entry INLINE: when its get returns, the
// rebuild has finished and holds no cold slot, so two such refreshes can never
// fill both slots ahead of the builds behind them.
func TestWarmer_RefreshesAPersistedEntryInline(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	file := filepath.Join(dir, "apicache", "x.db.p.json")
	if _, err := newSWRCache(time.Minute).getAt(ctx, file, "k", func(context.Context) (map[string]any, error) {
		return map[string]any{"v": "disk"}, nil
	}); err != nil {
		t.Fatal(err)
	}
	var finished atomic.Bool
	got, err := newSWRCache(time.Minute).getAt(waitForBuild(ctx), file, "k", func(context.Context) (map[string]any, error) {
		time.Sleep(100 * time.Millisecond)
		finished.Store(true)
		return map[string]any{"v": "fresh"}, nil
	})
	if err != nil || !finished.Load() || got["v"] != "fresh" {
		t.Fatalf("warmer get = %v (err %v, refresh finished %v): it returned before its refresh", got, err, finished.Load())
	}
	if n := len(coldBuildSlots); n != 0 {
		t.Fatalf("%d cold slot(s) held after the warmer's get returned", n)
	}

	// The body cache, as warmBody drives it.
	bfile := filepath.Join(dir, "apicache", "x.db.b.json")
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	newSWRBodyCache(time.Minute).serveAt(bfile, "k", httptest.NewRecorder(), req, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"v": "disk"})
	})
	finished.Store(false)
	rec := httptest.NewRecorder()
	newSWRBodyCache(time.Minute).serveAt(bfile, "k", rec, req.WithContext(waitForBuild(ctx)), func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		finished.Store(true)
		writeJSON(w, map[string]any{"v": "fresh"})
	})
	if !finished.Load() || !strings.Contains(rec.Body.String(), "fresh") {
		t.Fatalf("warmer serveAt answered %q (refresh finished %v): it returned before its refresh", rec.Body.String(), finished.Load())
	}
	if n := len(coldBuildSlots); n != 0 {
		t.Fatalf("%d cold slot(s) held after the warmer's serveAt returned", n)
	}
}

// #8: attribution leads the pass, so it is built even while a member-page
// build ahead of it in the step-4 order is still running.
func TestWarmCaches_AttributionLeadsThePass(t *testing.T) {
	t.Setenv(ledgeranchor.EnvKeyPath, filepath.Join(t.TempDir(), "anchor.key"))
	d, _ := newWave2Deps(t)
	key1d := d.St.CacheKey() + "|" + string(md.H1d)
	stuck := &coldBuild{done: make(chan struct{})} // a track-record build still running
	sharedTrackCache.mu.Lock()
	sharedTrackCache.ent[key1d] = &swrEntry{building: stuck}
	sharedTrackCache.mu.Unlock()
	t.Cleanup(func() {
		sharedTrackCache.mu.Lock()
		delete(sharedTrackCache.ent, key1d)
		sharedTrackCache.mu.Unlock()
	})
	unstick := func() {
		select {
		case <-stuck.done:
		default:
			stuck.err = errors.New("injected: released by the test")
			close(stuck.done)
		}
	}
	passDone := make(chan struct{})
	go func() {
		_ = d.WarmCaches(context.Background())
		close(passDone)
	}()
	defer func() { unstick(); <-passDone }()

	built := func(h md.Horizon) bool {
		sharedAttributionLiveCache.mu.Lock()
		defer sharedAttributionLiveCache.mu.Unlock()
		e := sharedAttributionLiveCache.ent[attributionCacheKey(d.St, h)]
		return e != nil && e.payload != nil
	}
	deadline := time.Now().Add(5 * time.Second)
	for !built(md.H1d) || !built(md.H1w) {
		if time.Now().After(deadline) {
			t.Fatal("attribution was not built while the pass waited on track-record: it is not first")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// #8: with no cold slot free, /api/attribution answers 503 warming, never a 200
// carrying zero live evidence, and answers with evidence once a slot frees.
func TestAttributionRoute_NoSlotAnswersWarmingNotZeroEvidence(t *testing.T) {
	shortColdServeWait(t, 50*time.Millisecond)
	srv, st := newAttributionServer(t)
	if _, err := st.UpsertSymbol(context.Background(), "AAA", md.Stocks, ""); err != nil {
		t.Fatal(err)
	}
	// Both slots held, as two detached refreshes held them after a boot.
	for i := 0; i < maxConcurrentColdBuilds; i++ {
		coldBuildSlots <- struct{}{}
	}
	held := true
	free := func() {
		if held {
			for i := 0; i < maxConcurrentColdBuilds; i++ {
				<-coldBuildSlots
			}
			held = false
		}
	}
	defer free()

	get := func() (int, string) {
		res, err := http.Get(srv.URL + "/api/attribution?symbol=AAA&market=stocks&horizon=1d")
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close() //nolint:errcheck
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(b)
	}
	if code, body := get(); code != http.StatusServiceUnavailable || !strings.Contains(body, `"warming"`) {
		t.Fatalf("no slot: %d %s; want 503 warming, not a zero-evidence answer", code, body)
	}
	// The matched-state lookup the same handler makes: no slot is warming, not
	// "no state" (which would silently downgrade the prior).
	withState := Deps{St: st, CurrentState: func(context.Context, int64) (map[md.Horizon]string, error) {
		return map[md.Horizon]string{md.H1d: "trend|hi"}, nil
	}}
	if states, err := withState.currentStateFor(context.Background(), 999); !errors.Is(err, errWarming) {
		t.Fatalf("currentStateFor with no slot = %v, %v; want errWarming", states, err)
	}
	free()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if code, _ := get(); code == http.StatusOK {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("attribution never answered once a slot freed")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// #12: after a warm pass, every request the web actually sends for a warmed
// route (spelled as web/src spells it) is a cache hit.
func TestWarmCaches_WarmsTheKeysTheWebRequests(t *testing.T) {
	t.Setenv(ledgeranchor.EnvKeyPath, filepath.Join(t.TempDir(), "anchor.key"))
	d, _ := newWave2Deps(t)
	// The unprefixed keys are shared with other tests: start them cold.
	for _, c := range []*swrBodyCache{sharedMoversCache, sharedCompositeSWR, sharedDatastatsSWR, sharedMacroSWR} {
		c.mu.Lock()
		c.ent = map[string]*swrBodyEntry{}
		c.mu.Unlock()
	}
	if err := d.WarmCaches(context.Background()); err != nil {
		t.Logf("warm pass reported: %v", err)
	}
	mux := d.routes(newRateLimiter(1000, 6000))
	for _, path := range []string{
		"/api/movers?limit=20",                       // MoversPanel on /market/overview
		"/api/composite/top?limit=500&horizon=1d",    // CompositeLeaderboard, default horizon
		"/api/honesty?horizon=1d",                    // /lab/honesty
		"/api/honesty?horizon=1w",                    //
		"/api/calibration?horizon=1d",                // useCalibration
		"/api/calibration?horizon=1w",                //
		"/api/datastats",                             // /lab/system/quality
		"/api/macro",                                 // /market/macro
		"/api/screener",                              // screenerRows()
		"/api/paper?strategy=flagship-1d&trades=100", // paper(), /lab/paper default
		"/api/paper?strategy=flagship-1w&trades=100", //
		"/api/research-ledger",                       // /lab/research
		"/api/vol-forecast/record",                   // /volatility (server-side)
	} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Header().Get("X-Cache") != "hit" {
			t.Errorf("%s: X-Cache=%q status %d; the warmer did not build the key this request reads",
				path, rec.Header().Get("X-Cache"), rec.Code)
		}
	}
}
