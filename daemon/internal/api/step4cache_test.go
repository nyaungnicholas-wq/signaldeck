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
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/config"
	"github.com/nyaungnicholas-wq/signaldeck/internal/ledgeranchor"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// shortColdServeWait shrinks the request-side wait for one test.
// eventuallyWait bounds every "wait until it lands" loop below. Each loop
// returns the moment its condition holds, so the bound only matters for a
// failure; 2-5 s flaked under a full ./... run on a loaded host
// (TestPersistedBody_ServedAcrossBuildsOfOneFormatOnly, 2026-10-02) while the
// same test passed 10/10 alone.
const eventuallyWait = 30 * time.Second

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
		"ledger-verify":   payload(sharedLedgerVerifyCache, verifyKey(t, d)),
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

// H7 (2026-10-02): a pass whose run deadline expires must not come back nil.
// The worker runner files a nil return "ok", so everything the pass never
// reached went unreported. The deadline here has passed before the pass
// starts, the limiting case of one that expires part-way.
func TestWarmCaches_ADeadlineCutPassIsReported(t *testing.T) {
	t.Setenv(ledgeranchor.EnvKeyPath, filepath.Join(t.TempDir(), "anchor.key"))
	d, _ := newWave2Deps(t)
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	err := d.WarmCaches(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("WarmCaches err = %v; a pass cut by its deadline must return ctx.Err()", err)
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
	deadline := time.Now().Add(eventuallyWait)
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
	deadline := time.Now().Add(eventuallyWait)
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
	deadline := time.Now().Add(eventuallyWait)
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

// P4 / round 3 (b): the default verify is computed once per TTL, and an append
// alone does not force a cold verify: the head is not in the key (see
// ledgerVerifyKey). Reads on either side of an append inside the TTL are the one
// build, and the payload says what it verified: the pre-append head seq and
// hash. The append reaches the answer at the next rebuild, one TTL on; ?full=1
// stays live throughout.
func TestLedgerVerify_DefaultResultIsComputedOncePerTTL(t *testing.T) {
	t.Setenv(anchorEnvDisable, "1") // a new anchor rightly re-keys the cache; keep it still
	srv, st := newLedgerServer(t, nil)
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	if err != nil {
		t.Fatal(err)
	}
	appendLedgerRows(t, st, sym.ID, 5, 0.5)
	head5, _, err := st.LedgerHead(ctx)
	if err != nil {
		t.Fatal(err)
	}

	type verified struct {
		Count      int64  `json:"count"`
		HeadSeq    int64  `json:"headSeq"`
		Head       string `json:"head"`
		ComputedAt string `json:"computedAt"`
	}
	read := func(path string) verified {
		t.Helper()
		res := ledgerGet(t, srv, path)
		defer res.Body.Close() //nolint:errcheck
		var b verified
		if res.StatusCode != http.StatusOK || json.NewDecoder(res.Body).Decode(&b) != nil {
			t.Fatalf("%s: status %d", path, res.StatusCode)
		}
		return b
	}
	first := read("/api/ledger/verify")
	if first.Count != 5 || first.HeadSeq != 5 || first.Head != head5.EntryHash || first.ComputedAt == "" {
		t.Fatalf("first verify: %+v; want count 5, headSeq 5, head %q and a computedAt", first, head5.EntryHash)
	}
	time.Sleep(1100 * time.Millisecond) // computedAt has whole seconds: a recomputation must show
	if again := read("/api/ledger/verify"); again != first {
		t.Fatalf("second verify: %+v; want the first result %+v — it ran the verification again", again, first)
	}
	if _, err := st.AppendLedger(ctx, store.LedgerEntry{
		PredictedAt: 9000, SymbolID: sym.ID, Horizon: md.H1d, BarTs: 9000,
		RawProb: 0.5, CalProb: 0.5, FeatureHash: "fh", ModelVersion: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if after := read("/api/ledger/verify"); after != first {
		t.Fatalf("verify after an append: %+v; want the one build %+v, which states it covers head seq 5 — the append forced a cold verify",
			after, first)
	}

	// The next rebuild reflects the append: one TTL on, the stale read refreshes
	// behind itself, and the read after it covers seq 6.
	e := ageVerify(t, st, 3*time.Minute)
	if stale := read("/api/ledger/verify"); stale != first {
		t.Fatalf("stale read: %+v, want the first result while its refresh runs", stale)
	}
	waitNotRebuilding(t, e)
	if next := read("/api/ledger/verify"); next.Count != 6 || next.HeadSeq != 6 || next.ComputedAt == first.ComputedAt {
		t.Fatalf("after the TTL's rebuild: %+v; want count 6, headSeq 6, a new computedAt", next)
	}
	if full := read("/api/ledger/verify?full=1"); full.Count != 6 || full.HeadSeq != 6 {
		t.Fatalf("?full=1: %+v; want count 6, headSeq 6: the auditor's walk must stay live", full)
	}
}

// waitNotRebuilding waits for e's background refresh to finish.
func waitNotRebuilding(t *testing.T, e *swrEntry) {
	t.Helper()
	deadline := time.Now().Add(eventuallyWait)
	for {
		sharedLedgerVerifyCache.mu.Lock()
		busy := e.rebuilding
		sharedLedgerVerifyCache.mu.Unlock()
		if !busy {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the refresh never finished")
		}
		time.Sleep(5 * time.Millisecond)
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

// tamperUnscannable makes the FIRST row unscannable and drops the verify
// checkpoint, so the next verification walks from genesis and ERRORS rather
// than reporting intact=false (the reviewer's scenario: the old cache served the
// last intact:true for ever). The anchors are untouched (the tests that use it
// write none), so the verify cache key stands still and the cached entry is what
// is tested.
func tamperUnscannable(t *testing.T, st *store.Store) {
	t.Helper()
	ctx := context.Background()
	for _, q := range []string{
		`UPDATE prediction_ledger SET raw_prob='tampered' WHERE seq=(SELECT MIN(seq) FROM prediction_ledger)`,
		`DELETE FROM meta WHERE k='ledger_verify_checkpoint'`,
	} {
		if _, err := st.DB().ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if _, _, err := st.VerifyLedgerCached(ctx); err == nil {
		t.Fatal("setup: the tampered row did not make the verification error")
	}
}

// verifyKey is the key the default verify of d's store is cached under now.
func verifyKey(t *testing.T, d Deps) string {
	t.Helper()
	k, err := d.ledgerVerifyKey(context.Background())
	if err != nil {
		t.Fatalf("setup: the ledger verify key could not be read: %v", err)
	}
	return k
}

// ageVerify backdates the cached verify entry for st's current chain and
// returns it.
func ageVerify(t *testing.T, st *store.Store, by time.Duration) *swrEntry {
	t.Helper()
	key := verifyKey(t, Deps{St: st})
	sharedLedgerVerifyCache.mu.Lock()
	defer sharedLedgerVerifyCache.mu.Unlock()
	e := sharedLedgerVerifyCache.ent[key]
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

	tamperUnscannable(t, st)
	e := ageVerify(t, st, 3*time.Minute) // past the 2m TTL, inside maxStale
	// One stale read is allowed; it starts the refresh, which fails.
	if code, _ := verifyStatus(t, srv); code != http.StatusOK {
		t.Fatalf("stale read: %d, want the stale 200 while the refresh runs", code)
	}
	deadline := time.Now().Add(eventuallyWait)
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

// unreadableLedgerKey inserts an anchor row whose created_at will not scan, so
// the verify key's newest-anchor lookup fails with a FAULT (a scan error), not
// a timeout. Any database writer can do it without the signing key.
func unreadableLedgerKey(t *testing.T, st *store.Store) {
	t.Helper()
	ctx := context.Background()
	if _, err := st.DB().ExecContext(ctx, `INSERT INTO ledger_anchors
		(created_at, ledger_seq, ledger_count, head_hash, alg, pub_key, sig, digest)
		VALUES ('not-a-time', 1, 1, 'h', 'ed25519', 'pk', 'sig', 'd')`); err != nil {
		t.Fatal(err)
	}
	if _, err := (Deps{St: st}).ledgerVerifyKey(ctx); err == nil || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("setup: the verify key read: err %v, want a scan fault", err)
	}
}

// starveLedgerReads holds every read connection, as a loaded daemon's pool is
// held, so the verify key lookup runs out its bound (shortened to 100ms here)
// with a deadline. The returned release frees them; cleanup does too, and ends
// the key-timeout streak the test ran up, so no later test inherits it.
func starveLedgerReads(t *testing.T, st *store.Store) (release func()) {
	t.Helper()
	orig := ledgerVerifyKeyTimeout
	ledgerVerifyKeyTimeout = 100 * time.Millisecond
	t.Cleanup(func() { ledgerVerifyKeyTimeout = orig; ledgerKeyTimeouts.Store(0) })
	var conns []*sql.Conn
	release = func() {
		for _, c := range conns {
			_ = c.Close()
		}
		conns = nil
	}
	t.Cleanup(func() { release() })
	for i := 0; i < st.DB().Stats().MaxOpenConnections; i++ {
		c, err := st.DB().Conn(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		conns = append(conns, c)
	}
	if _, err := (Deps{St: st}).ledgerVerifyKey(context.Background()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("setup: the key read with every read connection held: err %v, want a deadline", err)
	}
	return release
}

// anonVerify GETs the default verify with no credentials (nothing on that path
// needs the read pool, so it answers while the pool is starved).
func anonVerify(t *testing.T, srv *httptest.Server) (int, []byte) {
	t.Helper()
	res, err := newClient(t).Get(srv.URL + "/api/ledger/verify")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close() //nolint:errcheck
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res.StatusCode, body
}

// ledgerCheckpoint is the verify checkpoint row. Every verification of a chain
// that grew advances it, so an unchanged one means none ran.
func ledgerCheckpoint(t *testing.T, st *store.Store) string {
	t.Helper()
	v, err := st.GetMeta(context.Background(), "ledger_verify_checkpoint")
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// Round 3 (c), round 4 (T2): when the key read TIMES OUT (every read connection
// held), the default verify starts NO verification and refreshes nothing. It
// serves the last BUILT answer while maxStale allows, stale or not, and
// otherwise (none built, or too old) answers warming at once. Rows are appended
// before the pool is held, so a verification that ran anyway would move the
// checkpoint once the pool is free.
func TestLedgerVerify_AKeyReadThatTimesOutServesTheLastBuildOrWarming(t *testing.T) {
	t.Setenv(anchorEnvDisable, "1")
	srv, st := newLedgerServer(t, nil)
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	if err != nil {
		t.Fatal(err)
	}
	appendLedgerRows(t, st, sym.ID, 5, 0.5)
	warming := func(when string) {
		t.Helper()
		if code, body := anonVerify(t, srv); code != http.StatusServiceUnavailable || !strings.Contains(string(body), `"warming"`) {
			t.Fatalf("%s: %d %s; want 503 warming", when, code, body)
		}
	}

	// Nothing built yet for THIS store (another store's last build is not its
	// answer): warming, and no verification.
	foreign := "st-other|ledger-verify|a|s"
	sharedLedgerVerifyCache.put(foreign, map[string]any{"intact": true})
	lastLedgerVerifyKey.Store(foreign)
	t.Cleanup(func() {
		sharedLedgerVerifyCache.mu.Lock()
		delete(sharedLedgerVerifyCache.ent, foreign)
		sharedLedgerVerifyCache.mu.Unlock()
	})
	release := starveLedgerReads(t, st)
	warming("key read timed out, nothing built")
	release()
	time.Sleep(200 * time.Millisecond) // anything started behind that read has had time to write
	if ck := ledgerCheckpoint(t, st); ck != "" {
		t.Fatalf("a verification ran with nothing built: checkpoint %s", ck)
	}

	// One build while the key reads, then the chain grows and the pool is held.
	key := verifyKey(t, Deps{St: st})
	built := getLedgerVerify(t, srv, "")
	ck := ledgerCheckpoint(t, st)
	appendLedgerRows(t, st, sym.ID, 3, 0.5)
	release = starveLedgerReads(t, st)

	age := func(by time.Duration) *swrEntry {
		sharedLedgerVerifyCache.mu.Lock()
		defer sharedLedgerVerifyCache.mu.Unlock()
		e := sharedLedgerVerifyCache.ent[key]
		if e == nil || e.payload == nil {
			t.Fatal("setup: the built answer is gone")
		}
		e.builtAt = time.Now().Add(-by)
		return e
	}
	for _, by := range []time.Duration{0, 3 * time.Minute} { // fresh, then past the TTL
		e := age(by)
		var got struct {
			Count int64  `json:"count"`
			Head  string `json:"head"`
		}
		if code, body := anonVerify(t, srv); code != http.StatusOK || json.Unmarshal(body, &got) != nil ||
			got.Count != 5 || got.Head != built.Head {
			t.Fatalf("aged %s: %d %s; want the last built answer (count 5, head %s)", by, code, body, built.Head)
		}
		sharedLedgerVerifyCache.mu.Lock()
		refreshing := e.rebuilding
		sharedLedgerVerifyCache.mu.Unlock()
		if refreshing {
			t.Fatalf("aged %s: the read started a refresh of the last built answer", by)
		}
	}
	age(11 * time.Minute) // past maxStale
	warming("key read timed out, last build past maxStale")
	release()
	time.Sleep(200 * time.Millisecond) // anything started behind a read has had time to write
	if now := ledgerCheckpoint(t, st); now != ck {
		t.Fatalf("checkpoint moved %s -> %s: a verification ran while the key read timed out", ck, now)
	}
}

// Round 3 (e), round 4 (T2): the warmer's ledger step with a key read that
// timed out is a skipped step (errWarming: INFO, not counted), even with a last
// built answer on hand, and it runs no verification. (A key that fails for any
// other reason is counted: TestLedgerVerify_AnUnreadableKeyIsAFaultNotWarming.)
func TestWarmCaches_AKeyReadThatTimesOutIsSkippedWithoutAVerify(t *testing.T) {
	t.Setenv(ledgeranchor.EnvKeyPath, filepath.Join(t.TempDir(), "anchor.key"))
	t.Setenv(anchorEnvDisable, "1")
	d, st := newWave2Deps(t)
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	if err != nil {
		t.Fatal(err)
	}
	appendLedgerRows(t, st, sym.ID, 5, 0.5)
	if _, err := d.cachedLedgerVerify(ctx); err != nil {
		t.Fatalf("setup: the first verify: %v", err)
	}
	ck := ledgerCheckpoint(t, st)
	appendLedgerRows(t, st, sym.ID, 3, 0.5)

	release := starveLedgerReads(t, st)
	if _, err := d.cachedLedgerVerify(waitForBuild(ctx)); !errors.Is(err, errWarming) {
		t.Fatalf("the warmer's ledger step with a key read that timed out: err %v, want errWarming (skipped)", err)
	}
	release()
	time.Sleep(200 * time.Millisecond) // anything started behind that read has had time to write
	if now := ledgerCheckpoint(t, st); now != ck {
		t.Fatalf("checkpoint moved %s -> %s: the warmer verified with a key read that timed out", ck, now)
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
	tamperUnscannable(t, st)
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
		_, _, err := Deps{St: st}.buildLedgerVerify(context.Background())
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

// Round 3 (L1): the newest anchor's SIGNATURE is in the key. A rotated key
// signing the same head lands at the same ledger seq over the same stored hash,
// so only the signature tells the cached answer (one anchor, one public key)
// from the chain's current state (two of each).
func TestLedgerVerify_ANewAnchorOverTheSameHeadIsANewKey(t *testing.T) {
	srv, st := newLedgerServer(t, nil) // anchoring on, key in a temp dir
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	if err != nil {
		t.Fatal(err)
	}
	appendLedgerRows(t, st, sym.ID, 5, 0.5)
	if v := getLedgerVerify(t, srv, ""); !v.Tamper.Anchoring.Wrote || v.Tamper.AnchorCount != 1 {
		t.Fatalf("setup: the first read: wrote=%v anchors=%d", v.Tamper.Anchoring.Wrote, v.Tamper.AnchorCount)
	}
	rotated, err := ledgeranchor.LoadOrCreateSigner(filepath.Join(t.TempDir(), "rotated.key"))
	if err != nil {
		t.Fatal(err)
	}
	ver, err := st.VerifyLedger(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rec, wrote, reason, err := st.MaybeAnchorLedger(ctx, rotated, ver, store.AnchorPolicy{}, time.Now()); err != nil || !wrote || rec.LedgerSeq != 5 {
		t.Fatalf("setup: the rotated key's anchor: wrote=%v seq=%d reason=%q err=%v", wrote, rec.LedgerSeq, reason, err)
	}
	if v := getLedgerVerify(t, srv, ""); v.Tamper.AnchorCount != 2 {
		t.Fatalf("read after a second anchor over the same head: anchorCount=%d, want 2 — the answer cached before it was served", v.Tamper.AnchorCount)
	}
}

// Round 3 (L4): a build that wrote an anchor files its result under that
// anchor's key only if its own anchor check saw the anchor. A trigger removes
// the anchor the moment it lands, so the check never sees it; the identical
// record is then put back. The next read must verify it (anchorCount 1), not be
// served the result that never checked it (anchorCount 0).
func TestLedgerVerify_AResultIsNeverFiledUnderAnAnchorItDidNotCheck(t *testing.T) {
	srv, st := newLedgerServer(t, nil) // anchoring on, key in a temp dir
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	if err != nil {
		t.Fatal(err)
	}
	appendLedgerRows(t, st, sym.ID, 5, 0.5)
	exec := func(q string) {
		t.Helper()
		if _, err := st.DB().ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`CREATE TRIGGER zz_vanishing_anchor AFTER INSERT ON ledger_anchors
		BEGIN DELETE FROM ledger_anchors WHERE seq = NEW.seq; END`)
	res := ledgerGet(t, srv, "/api/ledger/verify")
	var first struct {
		Tamper struct {
			AnchorCount int64 `json:"anchorCount"`
			Anchoring   struct {
				Wrote  bool                `json:"wrote"`
				Record ledgeranchor.Record `json:"record"`
			} `json:"anchoring"`
		} `json:"tamperEvidence"`
	}
	err = json.NewDecoder(res.Body).Decode(&first)
	res.Body.Close() //nolint:errcheck
	if err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("first verify: %d %v", res.StatusCode, err)
	}
	if !first.Tamper.Anchoring.Wrote || first.Tamper.AnchorCount != 0 {
		t.Fatalf("setup: wrote=%v anchorCount=%d; want an anchor written and gone before the check",
			first.Tamper.Anchoring.Wrote, first.Tamper.AnchorCount)
	}
	exec(`DROP TRIGGER zz_vanishing_anchor`)
	if ok, err := st.AppendLedgerAnchor(ctx, first.Tamper.Anchoring.Record); err != nil || !ok {
		t.Fatalf("setup: putting the anchor back: ok=%v err=%v", ok, err)
	}
	if v := getLedgerVerify(t, srv, ""); v.Tamper.AnchorCount != 1 || !v.Tamper.LocalAnchorsReproduce {
		t.Fatalf("read after the anchor came back: anchorCount=%d localAnchorsReproduce=%v; want 1/true — "+
			"it was served a result filed under that anchor's key by a build that never checked it",
			v.Tamper.AnchorCount, v.Tamper.LocalAnchorsReproduce)
	}
}

// #7 / F: a persisted body is served across deploys (no build identity), only
// to a build with the same persist format, and only while under persistMaxAge.
func TestPersistedBody_ServedAcrossBuildsOfOneFormatOnly(t *testing.T) {
	shortColdServeWait(t, 2*time.Second)
	ctx := context.Background()
	d := Deps{Cfg: config.Config{DBPath: filepath.Join(t.TempDir(), "x.db")}}
	f1, f2 := d.cacheFile("track-record-1d", 1), d.cacheFile("track-record-1d", 2)
	val := func(v string) func(context.Context) (map[string]any, error) {
		return func(context.Context) (map[string]any, error) { return map[string]any{"v": v}, nil }
	}
	waitPersisted := func(file, want string) {
		t.Helper()
		deadline := time.Now().Add(eventuallyWait) // the refresh behind a served body lands in the temp dir
		for {
			if p, _, ok := loadPersistedPayload(file, "st2|1d"); ok && p["v"] == want {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("the refresh behind the served body never landed (%s)", want)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}

	// A file the previous deploy wrote, build stamp and all: served.
	if err := writeFileAtomic(f1, []byte(`{"key":"st2|1d","build":"rev-a","computedAt":"`+
		time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)+`","body":{"v":"a"}}`)); err != nil {
		t.Fatal(err)
	}
	if got, err := newSWRCache(time.Minute).getAt(ctx, f1, "st2|1d", val("b")); err != nil || got["v"] != "a" {
		t.Fatalf("after a deploy, same format: got %v (err %v), want the previous build's body served", got, err)
	}
	waitPersisted(f1, "b")

	// The same cache at another format reads nothing of it.
	if f1 == f2 {
		t.Fatalf("formats 1 and 2 share the file %s", f1)
	}
	if got, err := newSWRCache(time.Minute).getAt(ctx, f2, "st2|1d", val("f2")); err != nil || got["v"] != "f2" {
		t.Fatalf("another format: got %v (err %v), want a cold build", got, err)
	}

	persistBody(f1, "st2|1d", time.Now().Add(-25*time.Hour), []byte(`{"v":"old"}`))
	if got, err := newSWRCache(time.Minute).getAt(ctx, f1, "st2|1d", val("new")); err != nil || got["v"] != "new" {
		t.Fatalf("a 25h-old body: got %v (err %v), want a cold build", got, err)
	}
	persistBody(f1, "st2|1d", time.Now().Add(-23*time.Hour), []byte(`{"v":"old"}`))
	if got, err := newSWRCache(time.Minute).getAt(ctx, f1, "st2|1d", val("rebuilt")); err != nil || got["v"] != "old" {
		t.Fatalf("a 23h-old body: got %v (err %v), want it served", got, err)
	}
	waitPersisted(f1, "rebuilt")
}

// F (3): a body a previous process persisted stands in while its refreshes fail
// only inside fromDiskMaxStale of the boot; past it, never.
func TestPersistedBody_AFailingRefreshStopsTheDiskCopyAfterTheWindow(t *testing.T) {
	shortColdServeWait(t, 2*time.Second)
	ctx := context.Background()
	dir := t.TempDir()
	fail := func(context.Context) (map[string]any, error) {
		return nil, errors.New("injected: rebuild keeps failing")
	}

	file := filepath.Join(dir, "apicache", "x.db.p.f1.json")
	persistBody(file, "k", time.Now().Add(-time.Hour), []byte(`{"v":"disk"}`))
	c := newSWRCache(time.Minute)
	idle := func() {
		t.Helper()
		deadline := time.Now().Add(eventuallyWait)
		for {
			c.mu.Lock()
			busy := c.ent["k"] != nil && c.ent["k"].rebuilding
			c.mu.Unlock()
			if !busy {
				return
			}
			if time.Now().After(deadline) {
				t.Fatal("the refresh never finished")
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	for i := 0; i < 2; i++ { // inside the window: served while each refresh fails
		if got, err := c.getAt(ctx, file, "k", fail); err != nil || got["v"] != "disk" {
			t.Fatalf("read %d inside the window: %v, %v; want the disk copy", i, got, err)
		}
		idle()
	}
	c.mu.Lock()
	c.created = time.Now().Add(-fromDiskMaxStale - time.Second)
	c.mu.Unlock()
	if got, err := c.getAt(ctx, file, "k", fail); err == nil || got != nil {
		t.Fatalf("past the window: %v, %v; want the build's error, never the previous process's payload", got, err)
	}

	// The body cache, as the volatility record uses it.
	bfile := filepath.Join(dir, "apicache", "x.db.b.f1.json")
	persistBody(bfile, "k", time.Now().Add(-time.Hour), []byte(`{"v":"disk"}`))
	bc := newSWRBodyCache(time.Minute)
	broken := func(w http.ResponseWriter, r *http.Request) { httpErr(w, http.StatusInternalServerError, "injected") }
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	rec := httptest.NewRecorder()
	bc.serveAt(bfile, "k", rec, req, broken)
	if !strings.Contains(rec.Body.String(), "disk") {
		t.Fatalf("inside the window: %d %s, want the disk body", rec.Code, rec.Body.String())
	}
	deadline := time.Now().Add(eventuallyWait)
	for {
		bc.mu.Lock()
		busy := bc.ent["k"].rebuilding
		bc.mu.Unlock()
		if !busy {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the body refresh never finished")
		}
		time.Sleep(5 * time.Millisecond)
	}
	bc.mu.Lock()
	bc.created = time.Now().Add(-fromDiskMaxStale - time.Second)
	bc.mu.Unlock()
	rec = httptest.NewRecorder()
	bc.serveAt(bfile, "k", rec, req, broken)
	if rec.Code == http.StatusOK || strings.Contains(rec.Body.String(), "disk") {
		t.Fatalf("past the window: %d %s, want the render's error, never the disk body", rec.Code, rec.Body.String())
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
	deadline := time.Now().Add(eventuallyWait)
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
	deadline := time.Now().Add(eventuallyWait)
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
		"/api/honesty?horizon=1h",                    // (the page's HORIZONS offer 1h too)
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

// H: only an entry loaded from disk is refreshed inline by the warmer. A stale
// entry this process built keeps stale-while-revalidate for the warmer too:
// served at once, refreshed detached.
func TestWarmer_ServesAnInMemoryStaleEntryAndRefreshesItDetached(t *testing.T) {
	ctx := waitForBuild(context.Background())
	release := make(chan struct{})
	started := make(chan struct{}, 2)
	var released atomic.Bool
	free := func() {
		if released.CompareAndSwap(false, true) {
			close(release)
		}
	}
	defer free()
	// within runs f and fails the test if it is still running after 2s, after
	// letting the refresh it waits on go.
	within := func(what string, f func()) {
		t.Helper()
		done := make(chan struct{})
		go func() { f(); close(done) }()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			free()
			<-done
			t.Fatalf("%s: the warmer awaited the refresh of an in-memory stale entry", what)
		}
	}

	c := newSWRCache(time.Millisecond)
	if _, err := c.get(context.Background(), "k", func(context.Context) (map[string]any, error) {
		return map[string]any{"v": "old"}, nil
	}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond) // stale
	var got map[string]any
	var err error
	within("swrCache", func() {
		got, err = c.get(ctx, "k", func(context.Context) (map[string]any, error) {
			started <- struct{}{}
			<-release
			return map[string]any{"v": "new"}, nil
		})
	})
	if err != nil || got["v"] != "old" {
		t.Fatalf("warmer get = %v, %v; want the stale payload at once", got, err)
	}

	bc := newSWRBodyCache(time.Millisecond)
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	bc.serve("k", httptest.NewRecorder(), req, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"v": "old"})
	})
	time.Sleep(5 * time.Millisecond)
	rec := httptest.NewRecorder()
	within("swrBodyCache", func() {
		bc.serve("k", rec, req.WithContext(ctx), func(w http.ResponseWriter, r *http.Request) {
			started <- struct{}{}
			<-release
			writeJSON(w, map[string]any{"v": "new"})
		})
	})
	if !strings.Contains(rec.Body.String(), "old") {
		t.Fatalf("warmer serve = %q; want the stale body at once", rec.Body.String())
	}
	for i := 0; i < 2; i++ { // both refreshes did start, behind the stale answers
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatal("a stale entry served to the warmer started no refresh")
		}
	}
	free()
}

// H: a warm step that only lost the race for a build slot, or found a ledger
// verification already running, is skipped, not counted; a real failure in the
// same pass still is.
func TestWarmCaches_ASlotRaceOrABusyVerifyIsSkippedNotCounted(t *testing.T) {
	t.Setenv(ledgeranchor.EnvKeyPath, filepath.Join(t.TempDir(), "anchor.key"))
	d, _ := newWave2Deps(t)
	inject := func(h md.Horizon, err error) {
		b := &coldBuild{done: make(chan struct{}), err: err}
		close(b.done)
		key := d.St.CacheKey() + "|" + string(h)
		sharedTrackCache.mu.Lock()
		sharedTrackCache.ent[key] = &swrEntry{building: b}
		sharedTrackCache.mu.Unlock()
		t.Cleanup(func() {
			sharedTrackCache.mu.Lock()
			delete(sharedTrackCache.ent, key)
			sharedTrackCache.mu.Unlock()
		})
	}
	inject(md.H1d, errWarming)
	inject(md.H1w, errors.New("injected: a real build failure"))
	for i := 0; i < ledgerVerifyConcurrency; i++ { // /anchors callers hold every verify slot
		ledgerVerifySem <- struct{}{}
	}
	err := d.WarmCaches(context.Background())
	for i := 0; i < ledgerVerifyConcurrency; i++ {
		<-ledgerVerifySem
	}
	if err == nil || !strings.Contains(err.Error(), "injected: a real build failure") {
		t.Fatalf("WarmCaches err = %v; the real failure must still be counted", err)
	}
	for _, skipped := range []string{"track-record 1d", "ledger-verify"} {
		if strings.Contains(err.Error(), skipped) {
			t.Errorf("WarmCaches counted %q as failed: %v", skipped, err)
		}
	}
}

// I (1): a verify that never ran because the semaphore was full (an anonymous
// /api/ledger/anchors caller can fill it) proved nothing either way, so it
// never evicts the cached proof; maxStale alone bounds that copy. Round 5 (F1):
// nor does it renew the copy's age, so the copy is not served once maxStale has
// passed since the build that MADE it.
func TestLedgerVerify_ABusySemaphoreNeverEvictsTheProof(t *testing.T) {
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
	e := ageVerify(t, st, 3*time.Minute) // past the TTL, inside maxStale
	sharedLedgerVerifyCache.mu.Lock()
	born := e.builtAt
	sharedLedgerVerifyCache.mu.Unlock()
	held := true
	free := func() {
		if held {
			for i := 0; i < ledgerVerifyConcurrency; i++ {
				<-ledgerVerifySem
			}
			held = false
		}
	}
	for i := 0; i < ledgerVerifyConcurrency; i++ {
		ledgerVerifySem <- struct{}{}
	}
	t.Cleanup(free)
	// Each stale read serves the proof and starts a refresh the full semaphore
	// refuses. Had the first refusal renewed the age, the second read would be
	// fresh and start none.
	for i := 0; i < 2; i++ {
		if code, intact := verifyStatus(t, srv); code != http.StatusOK || !intact {
			t.Fatalf("stale read %d with the semaphore full: %d intact=%v, want the kept proof", i, code, intact)
		}
		waitNotRebuilding(t, e)
	}
	sharedLedgerVerifyCache.mu.Lock()
	kept, at := e.payload != nil, e.builtAt
	sharedLedgerVerifyCache.mu.Unlock()
	if !kept {
		t.Fatal("a refresh refused by the busy semaphore evicted the proof")
	}
	if !at.Equal(born) {
		t.Fatalf("a refresh refused by the busy semaphore renewed the proof's age: builtAt %v, want the original build's %v", at, born)
	}
	sharedLedgerVerifyCache.mu.Lock()
	e.builtAt = e.builtAt.Add(-(sharedLedgerVerifyCache.maxStale - 3*time.Minute) - time.Second)
	sharedLedgerVerifyCache.mu.Unlock()
	if code, intact := verifyStatus(t, srv); code == http.StatusOK && intact {
		t.Fatal("the kept proof was served past maxStale from its original build")
	}
	free()
	if code, intact := verifyStatus(t, srv); code != http.StatusOK || !intact {
		t.Fatalf("after the semaphore freed: %d intact=%v, want a fresh verify", code, intact)
	}
}

// I (2): the attribution handler answers warming when the matched-state lookup
// found no build slot, even while the live-evidence lookup after it is a hit. A
// handler that dropped currentStateFor's error would answer 200 with the prior
// silently downgraded to the unmatched base rate.
func TestAttributionRoute_AStateLookupWithNoSlotAnswersWarming(t *testing.T) {
	shortColdServeWait(t, 50*time.Millisecond)
	_, st := newAttributionServer(t)
	if _, err := st.UpsertSymbol(context.Background(), "AAB", md.Stocks, ""); err != nil {
		t.Fatal(err)
	}
	d := Deps{St: st, CurrentState: func(context.Context, int64) (map[md.Horizon]string, error) {
		return map[md.Horizon]string{md.H1d: "trend|hi"}, nil
	}}
	liveKey := attributionCacheKey(st, md.H1d)
	sharedAttributionLiveCache.put(liveKey, map[string]any{attributionLiveField: attributionLive{}})
	t.Cleanup(func() {
		sharedAttributionLiveCache.mu.Lock()
		delete(sharedAttributionLiveCache.ent, liveKey)
		sharedAttributionLiveCache.mu.Unlock()
	})
	for i := 0; i < maxConcurrentColdBuilds; i++ {
		coldBuildSlots <- struct{}{}
	}
	defer func() {
		for i := 0; i < maxConcurrentColdBuilds; i++ {
			<-coldBuildSlots
		}
	}()
	rec := httptest.NewRecorder()
	d.attribution(rec, httptest.NewRequest(http.MethodGet,
		"/api/attribution?symbol=AAB&market=stocks&horizon=1d", nil))
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), `"warming"`) {
		t.Fatalf("state lookup with no slot: %d %s; want 503 warming", rec.Code, rec.Body.String())
	}
}

// J: a cached default verify whose build ran out its ledgerVerifyBuildTimeout
// answers the reader 503 (retry), as ?full=1 does, not the opaque 500.
func TestLedgerVerify_ABuildPastItsBoundAnswers503(t *testing.T) {
	t.Setenv(anchorEnvDisable, "1")
	srv, st := newLedgerServer(t, nil)
	sym, err := st.UpsertSymbol(context.Background(), "AAPL", md.Stocks, "Apple")
	if err != nil {
		t.Fatal(err)
	}
	appendLedgerRows(t, st, sym.ID, 5, 0.5)
	key := verifyKey(t, Deps{St: st})
	timedOut := &coldBuild{done: make(chan struct{}),
		err: fmt.Errorf("verify ledger: %w", context.DeadlineExceeded)} // as buildLedgerVerify returns it
	close(timedOut.done)
	sharedLedgerVerifyCache.mu.Lock()
	sharedLedgerVerifyCache.ent[key] = &swrEntry{building: timedOut}
	sharedLedgerVerifyCache.mu.Unlock()
	t.Cleanup(func() {
		sharedLedgerVerifyCache.mu.Lock()
		delete(sharedLedgerVerifyCache.ent, key)
		sharedLedgerVerifyCache.mu.Unlock()
	})
	if code, _ := verifyStatus(t, srv); code != http.StatusServiceUnavailable {
		t.Fatalf("a timed-out verify build answered %d, want 503", code)
	}
}

// F (3), the body cache: a disk body that ages out while its refresh is still
// rendering answers warming, and starts no second render of the same key.
func TestPersistedBody_PastTheWindowWithARefreshInFlightAnswersWarming(t *testing.T) {
	shortColdServeWait(t, 200*time.Millisecond)
	file := filepath.Join(t.TempDir(), "apicache", "x.db.b.f1.json")
	persistBody(file, "k", time.Now().Add(-time.Hour), []byte(`{"v":"disk"}`))
	bc := newSWRBodyCache(time.Minute)
	release := make(chan struct{})
	var renders atomic.Int32
	h := func(w http.ResponseWriter, r *http.Request) {
		renders.Add(1)
		<-release
		writeJSON(w, map[string]any{"v": "new"})
	}
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	settle := func() { // let every render finish inside the temp dir
		deadline := time.Now().Add(5 * time.Second)
		for {
			bc.mu.Lock()
			e := bc.ent["k"]
			busy := e != nil && (e.rebuilding || e.building != nil)
			bc.mu.Unlock()
			if !busy || time.Now().After(deadline) {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	defer settle()
	defer close(release)

	rec := httptest.NewRecorder()
	bc.serveAt(file, "k", rec, req, h) // serves the disk body; the refresh blocks in h
	if !strings.Contains(rec.Body.String(), "disk") {
		t.Fatalf("inside the window: %d %s, want the disk body", rec.Code, rec.Body.String())
	}
	for deadline := time.Now().Add(eventuallyWait); renders.Load() == 0; {
		if time.Now().After(deadline) {
			t.Fatal("the disk body started no refresh")
		}
		time.Sleep(5 * time.Millisecond)
	}
	bc.mu.Lock()
	bc.created = time.Now().Add(-fromDiskMaxStale - time.Second)
	bc.mu.Unlock()
	rec = httptest.NewRecorder()
	bc.serveAt(file, "k", rec, req, h)
	if rec.Code != http.StatusServiceUnavailable || strings.Contains(rec.Body.String(), "disk") {
		t.Fatalf("past the window, refresh in flight: %d %s; want 503 warming", rec.Code, rec.Body.String())
	}
	time.Sleep(50 * time.Millisecond)
	if n := renders.Load(); n != 1 {
		t.Fatalf("%d renders of one key: a second build started beside the refresh", n)
	}
}
