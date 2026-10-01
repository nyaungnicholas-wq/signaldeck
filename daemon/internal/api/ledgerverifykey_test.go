package api

// Step 3-4 review round 4: what the cached default verify may be filed under
// (T1), which key failures are load and which are faults (T2), and what the
// payload's headSeq and computedAt pin (T3).

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/ledgeranchor"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// execAll runs setup SQL straight against the database, as a writer without
// the signing key could.
func execAll(t *testing.T, st *store.Store, qs ...string) {
	t.Helper()
	for _, q := range qs {
		if _, err := st.DB().ExecContext(context.Background(), q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
}

// T1, the verifier's probe PGe. The key is read BEFORE the build, so the chain
// can move in between. An honest 12-row chain carries two anchors; a database
// writer without the signing key fabricates rows at the same seqs; a reader's
// key read sees the fabrication while every cold build slot is held; the honest
// rows are put back for the build; the fabrication returns once it has run.
// Round 3 filed the honest verdict under the fabricated key, so the default
// verify read clean (failing 0) while a fresh verify said TAMPER EVIDENCE
// (failing 2). A result is filed only under the key of the state its build read.
func TestLedgerVerify_AResultIsFiledOnlyUnderTheKeyItsBuildRead(t *testing.T) {
	t.Setenv(anchorEnvInterval, "0s")
	srv, st := newLedgerServer(t, nil)
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	if err != nil {
		t.Fatal(err)
	}
	// Two honest anchors, at 6 rows and at 12 (the second from the TTL refresh).
	appendLedgerRows(t, st, sym.ID, 6, 0.5)
	getLedgerVerify(t, srv, "")
	appendLedgerRows(t, st, sym.ID, 6, 0.5)
	e := ageVerify(t, st, 3*time.Minute)
	sharedLedgerVerifyCache.mu.Lock()
	before := e.payload
	sharedLedgerVerifyCache.mu.Unlock()
	getLedgerVerify(t, srv, "")
	waitNotRebuilding(t, e)
	// That refresh's build read the new anchor's key, not this one: the entry
	// keeps its own payload, neither evicted nor replaced by that build's.
	sharedLedgerVerifyCache.mu.Lock()
	kept := e.payload != nil && reflect.ValueOf(e.payload).Pointer() == reflect.ValueOf(before).Pointer()
	sharedLedgerVerifyCache.mu.Unlock()
	if !kept {
		t.Fatal("a refresh whose build read another key evicted or replaced the entry it was refreshing")
	}
	t.Setenv(anchorEnvDisable, "1")
	if v := getLedgerVerify(t, srv, ""); v.Tamper.AnchorCount != 2 || v.Tamper.FailingAnchors != 0 {
		t.Fatalf("setup: honest chain anchors=%d failing=%d, want 2/0", v.Tamper.AnchorCount, v.Tamper.FailingAnchors)
	}
	honestKey := verifyKey(t, Deps{St: st})

	execAll(t, st, `CREATE TABLE zz_honest AS SELECT * FROM prediction_ledger`,
		`DELETE FROM prediction_ledger`, `DELETE FROM sqlite_sequence WHERE name='prediction_ledger'`)
	appendLedgerRows(t, st, sym.ID, 12, 0.77)
	execAll(t, st, `CREATE TABLE zz_fab AS SELECT * FROM prediction_ledger`)
	fabKey := verifyKey(t, Deps{St: st})
	if fabKey == honestKey {
		t.Fatal("setup: the fabrication did not move the key")
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
	for i := 0; i < maxConcurrentColdBuilds; i++ {
		coldBuildSlots <- struct{}{}
	}
	t.Cleanup(free)
	type reply struct {
		code int
		body []byte
		err  error
	}
	got := make(chan reply, 1)
	client := newClient(t)
	go func() {
		res, err := client.Get(srv.URL + "/api/ledger/verify")
		if err != nil {
			got <- reply{err: err}
			return
		}
		var buf bytes.Buffer
		_, err = buf.ReadFrom(res.Body)
		res.Body.Close() //nolint:errcheck
		got <- reply{res.StatusCode, buf.Bytes(), err}
	}()
	// The reader has read the fabricated key once its cold build is queued.
	for deadline := time.Now().Add(5 * time.Second); ; {
		sharedLedgerVerifyCache.mu.Lock()
		fe := sharedLedgerVerifyCache.ent[fabKey]
		queued := fe != nil && fe.building != nil
		sharedLedgerVerifyCache.mu.Unlock()
		if queued {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("setup: the reader never queued a build under the fabricated key")
		}
		time.Sleep(5 * time.Millisecond)
	}
	execAll(t, st, `DELETE FROM prediction_ledger`, `INSERT INTO prediction_ledger SELECT * FROM zz_honest`)
	restored := time.Now()
	free()
	first := <-got
	if first.err != nil || first.code != http.StatusOK {
		t.Fatalf("the reader whose build ran on the honest rows: %d %s %v; want its 200", first.code, first.body, first.err)
	}
	execAll(t, st, `DELETE FROM prediction_ledger`, `INSERT INTO prediction_ledger SELECT * FROM zz_fab`)
	if k := verifyKey(t, Deps{St: st}); k != fabKey {
		t.Fatal("setup: the fabrication's key did not come back")
	}

	for i := 0; i < 2; i++ {
		if v := getLedgerVerify(t, srv, ""); v.Intact || v.Tamper.FailingAnchors == 0 || !strings.Contains(v.Tamper.Claim, "TAMPER EVIDENCE") {
			t.Fatalf("read %d of the fabricated chain: intact=%v failing=%d; want tamper evidence — "+
				"the honest chain's verdict was filed under the fabricated chain's key", i, v.Intact, v.Tamper.FailingAnchors)
		}
	}
	// The fabrication's own verdict IS cached under its key (served, not rebuilt
	// per reader), and the honest verdict went under the key its build read.
	if p, ok := sharedLedgerVerifyCache.peek(fabKey); !ok || p["intact"] != false {
		t.Fatalf("fabricated key's entry: ok=%v intact=%v; want its own tamper verdict cached", ok, p["intact"])
	}
	sharedLedgerVerifyCache.mu.Lock()
	he := sharedLedgerVerifyCache.ent[honestKey]
	filed := he != nil && he.payload != nil && !he.builtAt.Before(restored)
	sharedLedgerVerifyCache.mu.Unlock()
	if !filed {
		t.Fatal("the build that read the honest rows was not filed under the honest key")
	}
}

// T2, the verifier's probe PGg. A key that fails to read for any reason but
// time (an anchor row whose created_at will not scan, insertable without the
// signing key) is a fault, not load. Round 3 served the last build for 10
// minutes and then "warming" for ever, logged nothing, and the warmer skipped
// it at INFO, while a fresh verify said the chain was broken. Now: 500 with no
// internals, never the last build; one WARN a minute, not one log per read; and
// the warmer counts the step as failed.
func TestLedgerVerify_AnUnreadableKeyIsAFaultNotWarming(t *testing.T) {
	t.Setenv(ledgeranchor.EnvKeyPath, filepath.Join(t.TempDir(), "anchor.key"))
	t.Setenv(anchorEnvDisable, "1")
	d, st := newWave2Deps(t)
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	if err != nil {
		t.Fatal(err)
	}
	appendLedgerRows(t, st, sym.ID, 5, 0.5)
	if _, err := d.cachedLedgerVerify(ctx); err != nil { // a last build the timeout path would serve
		t.Fatalf("setup: the first verify: %v", err)
	}
	execAll(t, st, `DELETE FROM prediction_ledger WHERE seq=5`) // a fresh verify now reads intact=false
	unreadableLedgerKey(t, st)

	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	ledgerKeyWarnedAt.Store(0)
	for i := 0; i < 3; i++ {
		rec := httptest.NewRecorder()
		d.ledgerVerify(rec, httptest.NewRequest(http.MethodGet, "/api/ledger/verify", nil))
		body := rec.Body.String()
		if rec.Code != http.StatusInternalServerError || !strings.Contains(body, `"internal error"`) || strings.Contains(body, "created_at") {
			t.Fatalf("read %d: %d %s; want 500 internal error and nothing about the row", i, rec.Code, body)
		}
	}
	slog.SetDefault(prev)
	out := logs.String()
	if n := strings.Count(out, "level=WARN"); n != 1 || !strings.Contains(out, "created_at") {
		t.Fatalf("logged %d WARN line(s) over 3 reads; want exactly 1 naming the scan error:\n%s", n, out)
	}
	if strings.Contains(out, "level=ERROR") {
		t.Fatalf("logged per read at ERROR; want only the rate-limited WARN:\n%s", out)
	}
	// A caller that left is not the fault, and must not use up the minute's WARN.
	logs.Reset()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	ledgerKeyWarnedAt.Store(0)
	gone, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := d.cachedLedgerVerify(gone); err == nil {
		t.Fatal("a cancelled read answered")
	}
	slog.SetDefault(prev)
	if strings.Contains(logs.String(), "level=WARN") {
		t.Fatalf("a cancelled read was logged as the fault:\n%s", logs.String())
	}

	if _, err := d.cachedLedgerVerify(waitForBuild(ctx)); err == nil || errors.Is(err, errWarming) {
		t.Fatalf("the warmer's ledger step: err %v; want the fault, not a skip", err)
	}
	if err := d.WarmCaches(ctx); err == nil || !strings.Contains(err.Error(), "ledger-verify") {
		t.Fatalf("WarmCaches err = %v; want the ledger-verify step counted as failed", err)
	}
}

// T3 (round-3 mutant V5): a failed read of the hash stored at the newest
// anchor's seq is an error, never the "no row at the anchored seq" key a
// deleted row reads as.
func TestLedgerVerifyKey_AFailedHashReadIsAnErrorNotTheNoRowKey(t *testing.T) {
	srv, st := newLedgerServer(t, nil) // anchoring on, key in a temp dir
	sym, err := st.UpsertSymbol(context.Background(), "AAPL", md.Stocks, "Apple")
	if err != nil {
		t.Fatal(err)
	}
	appendLedgerRows(t, st, sym.ID, 5, 0.5)
	if v := getLedgerVerify(t, srv, ""); !v.Tamper.Anchoring.Wrote {
		t.Fatalf("setup: no anchor written (%q)", v.Tamper.Anchoring.Reason)
	}
	// The anchor still reads; the ledger row lookup cannot.
	execAll(t, st, `ALTER TABLE prediction_ledger RENAME TO zz_prediction_ledger`)
	if k, err := (Deps{St: st}).ledgerVerifyKey(context.Background()); err == nil {
		t.Fatalf("key %q with a nil error; want the lookup's error", k)
	}
}

// T3, the other half: NO row at the newest anchor's seq is a key of its own, not
// a failure. The anchored history is gone, and the default verify must say so
// (200, tamper evidence), not 500 or the last clean build.
func TestLedgerVerify_ADeletedAnchoredRowReadsAsTamper(t *testing.T) {
	srv, st := newLedgerServer(t, nil) // anchoring on, key in a temp dir
	sym, err := st.UpsertSymbol(context.Background(), "AAPL", md.Stocks, "Apple")
	if err != nil {
		t.Fatal(err)
	}
	appendLedgerRows(t, st, sym.ID, 5, 0.5)
	if v := getLedgerVerify(t, srv, ""); !v.Tamper.Anchoring.Wrote {
		t.Fatalf("setup: no anchor written (%q)", v.Tamper.Anchoring.Reason)
	}
	execAll(t, st, `DELETE FROM prediction_ledger WHERE seq=5`) // the anchored head
	if v := getLedgerVerify(t, srv, ""); v.Intact || v.Tamper.FailingAnchors != 1 || !strings.Contains(v.Tamper.Claim, "TAMPER EVIDENCE") {
		t.Fatalf("after the anchored row was deleted: intact=%v failing=%d; want tamper evidence", v.Intact, v.Tamper.FailingAnchors)
	}
}

// T3: computedAt is taken BEFORE the build reads the rows, so the payload
// covers every row there at computedAt. The clock appends a row as it is read:
// a stamp taken first is followed by reads that include it; a stamp taken after
// the reads (or after anchoring, as round 3 did) is not. Both paths.
func TestLedgerVerify_ComputedAtIsStampedBeforeTheRowsAreRead(t *testing.T) {
	t.Setenv(anchorEnvDisable, "1")
	srv, st := newLedgerServer(t, nil)
	sym, err := st.UpsertSymbol(context.Background(), "AAPL", md.Stocks, "Apple")
	if err != nil {
		t.Fatal(err)
	}
	appendLedgerRows(t, st, sym.ID, 5, 0.5)
	stamp := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	orig := ledgerVerifyNow
	t.Cleanup(func() { ledgerVerifyNow = orig })
	for i, path := range []string{"/api/ledger/verify", "/api/ledger/verify?full=1"} {
		var appended store.LedgerEntry
		var appendErr error
		ledgerVerifyNow = func() time.Time {
			appended, appendErr = st.AppendLedger(context.Background(), store.LedgerEntry{
				PredictedAt: int64(5000 + i), SymbolID: sym.ID, Horizon: md.H1d, BarTs: int64(5000 + i),
				RawProb: 0.6, CalProb: 0.6, FeatureHash: "fh", ModelVersion: 1,
			})
			return stamp
		}
		res := ledgerGet(t, srv, path)
		var got struct {
			Count      int64  `json:"count"`
			HeadSeq    int64  `json:"headSeq"`
			ComputedAt string `json:"computedAt"`
		}
		err := json.NewDecoder(res.Body).Decode(&got)
		res.Body.Close() //nolint:errcheck
		ledgerVerifyNow = orig
		if err != nil || res.StatusCode != http.StatusOK || appendErr != nil || appended.Seq == 0 {
			t.Fatalf("%s: %d %v (append at stamp: seq %d, %v)", path, res.StatusCode, err, appended.Seq, appendErr)
		}
		if got.ComputedAt != "2030-01-02T03:04:05Z" || got.HeadSeq != appended.Seq || got.Count != int64(6+i) {
			t.Fatalf("%s: %+v; want computedAt 2030-01-02T03:04:05Z covering the row appended at the stamp (seq %d, count %d)",
				path, got, appended.Seq, 6+i)
		}
	}
}

// T3 (round-3 mutant V16, headSeq = count, passed every contiguous fixture):
// headSeq is the head row's seq. AUTOINCREMENT moved on between appends, so
// the chain is seqs 1-3 and 11-12: 5 rows, head seq 12. Both paths.
func TestLedgerVerify_HeadSeqIsTheHeadRowsSeqNotTheCount(t *testing.T) {
	t.Setenv(anchorEnvDisable, "1")
	srv, st := newLedgerServer(t, nil)
	sym, err := st.UpsertSymbol(context.Background(), "AAPL", md.Stocks, "Apple")
	if err != nil {
		t.Fatal(err)
	}
	appendLedgerRows(t, st, sym.ID, 3, 0.5)
	execAll(t, st, `UPDATE sqlite_sequence SET seq=10 WHERE name='prediction_ledger'`)
	appendLedgerRows(t, st, sym.ID, 2, 0.6)
	for _, path := range []string{"/api/ledger/verify", "/api/ledger/verify?full=1"} {
		res := ledgerGet(t, srv, path)
		var got struct {
			Intact  bool  `json:"intact"`
			Count   int64 `json:"count"`
			HeadSeq int64 `json:"headSeq"`
		}
		err := json.NewDecoder(res.Body).Decode(&got)
		res.Body.Close() //nolint:errcheck
		if err != nil || !got.Intact || got.Count != 5 || got.HeadSeq != 12 {
			t.Fatalf("%s: %+v (err %v); want intact, count 5, headSeq 12", path, got, err)
		}
	}
}
