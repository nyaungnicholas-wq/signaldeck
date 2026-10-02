package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// rewriteDayProbs is the probe these tests use to see whether the gate ran. It
// rewrites prob in place, which no production path ever does: the fingerprint
// the cache keys on reads only which rows are in the set, so this leaves the
// key unchanged while the gate itself WOULD see it. The verdict that comes back
// after a rewrite therefore says whether the cache answered or the gate re-ran.
func rewriteDayProbs(t *testing.T, dbPath string, day time.Time, expr string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close() //nolint:errcheck
	ts := day.UTC().Truncate(24 * time.Hour).Unix()
	query := fmt.Sprintf("UPDATE prediction_outcomes SET prob = %s WHERE horizon = '1d' AND ts = ?", expr)
	res, err := db.ExecContext(context.Background(), query, ts)
	if err != nil {
		t.Fatalf("update prob: %v", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		t.Fatalf("rows affected: %v", err)
	}
	if rows == 0 {
		t.Fatalf("probe touched no rows")
	}
}

// A pass is reused while no row has entered or left the gated set, and a new
// resolution re-runs the gate at once, so a newly collapsed day cannot publish
// late.
func TestCollapseCacheServesAPassUntilAResolutionLands(t *testing.T) {
	now := gateNow
	_, st, d := newTestServer(t, nil)
	path := writeRegistry(t, registryFor(map[string]int{"1d": 3}))
	d.RegistryPath = path
	reg, err := loadRegistry(path)
	if err != nil {
		t.Fatalf("load registry: %v", err)
	}
	d.CollapseCache = &CollapseVerdictCache{}
	ctx := context.Background()
	gate := func() (string, bool, error) {
		return d.collapsedGradingWindowCached(ctx, reg, now)
	}

	for i := 1; i <= 3; i++ {
		seedResolvedForecasts(t, st, md.H1d, now.AddDate(0, 0, -i), 300, 180)
	}
	if _, collapsed, err := gate(); err != nil || collapsed {
		t.Fatalf("clean window: collapsed=%v err=%v", collapsed, err)
	}

	rewriteDayProbs(t, d.Cfg.DBPath, now.AddDate(0, 0, -1), "0.5")
	if _, collapsed, err := gate(); err != nil || collapsed {
		t.Fatalf("pass not served from cache: the gate re-ran although no row entered or left (collapsed=%v err=%v)", collapsed, err)
	}

	seedResolvedForecasts(t, st, md.H1d, now, 300, 180)
	reason, collapsed, err := gate()
	if err != nil || !collapsed {
		t.Fatalf("FAIL-OPEN: a cached pass outlived a new resolution (collapsed=%v err=%v)", collapsed, err)
	}
	if !strings.Contains(reason, now.AddDate(0, 0, -1).UTC().Format("2006-01-02")) {
		t.Errorf("reason does not mention the changed day: %q", reason)
	}
}

// A refusal is still cached, and is keyed the same way: it clears only when the
// rows change and the re-run gate passes.
func TestCollapseCacheStillCachesARefusal(t *testing.T) {
	now := gateNow
	_, st, d := newTestServer(t, nil)
	path := writeRegistry(t, registryFor(map[string]int{"1d": 3}))
	d.RegistryPath = path
	reg, err := loadRegistry(path)
	if err != nil {
		t.Fatalf("load registry: %v", err)
	}
	d.CollapseCache = &CollapseVerdictCache{}
	ctx := context.Background()
	gate := func() (string, bool, error) {
		return d.collapsedGradingWindowCached(ctx, reg, now)
	}

	for i := 1; i <= 3; i++ {
		seedResolvedForecasts(t, st, md.H1d, now.AddDate(0, 0, -i), 300, 180)
	}
	seedResolvedForecasts(t, st, md.H1d, now.AddDate(0, 0, -4), 300, 5)
	reason, collapsed, err := gate()
	if err != nil || !collapsed {
		t.Fatalf("collapsed day not refused: collapsed=%v err=%v", collapsed, err)
	}

	rewriteDayProbs(t, d.Cfg.DBPath, now.AddDate(0, 0, -4), "0.40 + (symbol_id % 180) * 0.001")
	reason2, collapsed2, err := gate()
	if err != nil || !collapsed2 || reason2 != reason {
		t.Fatalf("refusal not served from cache (collapsed=%v err=%v)", collapsed2, err)
	}

	seedResolvedForecasts(t, st, md.H1d, now, 300, 180)
	if _, collapsed, err := gate(); err != nil || collapsed {
		t.Fatalf("a cached refusal outlived a new resolution: the gate did not re-run (collapsed=%v err=%v)", collapsed, err)
	}
}

// execRaw runs one statement straight against the store's file.
func execRaw(t *testing.T, dbPath, q string, args ...any) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close() //nolint:errcheck
	if _, err := db.ExecContext(context.Background(), q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

// breakFingerprint makes the fingerprint read FAIL while the gate still reads
// fine: two resolved rows whose symbol ids sum past int64, so SQLite's SUM()
// raises "integer overflow". They join no symbol, so nothing else sees them,
// and two extra rows on a 300-symbol day do not change its verdict.
func breakFingerprint(t *testing.T, dbPath string, day time.Time) {
	t.Helper()
	ts := day.UTC().Truncate(24 * time.Hour).Unix()
	for _, id := range []int64{1 << 62, 1<<62 + 1} {
		execRaw(t, dbPath, `INSERT INTO prediction_outcomes (symbol_id, horizon, ts, prob, up, resolved_at)
			VALUES (?, '1d', ?, 0.5, 1, ?)`, id, ts, ts)
	}
}

// A fingerprint that cannot be read cannot be matched to a cached pass, and a
// pass is the verdict that publishes: it must never be served on one. A cached
// refusal still stands; with neither, the error reaches the caller.
func TestCollapseCacheFailsClosedWhenTheRowsCannotBeIdentified(t *testing.T) {
	now := gateNow
	setup := func(collapsedDay bool) (Deps, func() (string, bool, error)) {
		_, st, d := newTestServer(t, nil)
		reg, err := loadRegistry(writeRegistry(t, registryFor(map[string]int{"1d": 3})))
		if err != nil {
			t.Fatalf("load registry: %v", err)
		}
		d.CollapseCache = &CollapseVerdictCache{}
		for i := 1; i <= 3; i++ {
			seedResolvedForecasts(t, st, md.H1d, now.AddDate(0, 0, -i), 300, 180)
		}
		if collapsedDay {
			seedResolvedForecasts(t, st, md.H1d, now.AddDate(0, 0, -4), 300, 5)
		}
		return d, func() (string, bool, error) {
			return d.collapsedGradingWindowCached(context.Background(), reg, now)
		}
	}

	t.Run("cached pass is not served", func(t *testing.T) {
		d, gate := setup(false)
		if _, c, err := gate(); err != nil || c {
			t.Fatalf("clean window: collapsed=%v err=%v", c, err)
		}
		breakFingerprint(t, d.Cfg.DBPath, now.AddDate(0, 0, -1))
		if _, c, err := gate(); err == nil || c {
			t.Fatalf("FAIL-OPEN: a cached pass was served on unidentifiable rows (collapsed=%v err=%v)", c, err)
		}
	})
	t.Run("cached refusal still stands", func(t *testing.T) {
		d, gate := setup(true)
		reason, c, err := gate()
		if err != nil || !c {
			t.Fatalf("collapsed day not refused: collapsed=%v err=%v", c, err)
		}
		breakFingerprint(t, d.Cfg.DBPath, now.AddDate(0, 0, -1))
		if r2, c2, err := gate(); err != nil || !c2 || r2 != reason {
			t.Fatalf("cached refusal not served on unidentifiable rows (collapsed=%v err=%v)", c2, err)
		}
	})
	t.Run("nothing cached: error", func(t *testing.T) {
		d, gate := setup(false)
		breakFingerprint(t, d.Cfg.DBPath, now.AddDate(0, 0, -1))
		if _, c, err := gate(); err == nil || c {
			t.Fatalf("unidentifiable rows with nothing cached must return the error (collapsed=%v err=%v)", c, err)
		}
	})
}

// A gate that errored never decided, so nothing may be cached from it: the next
// read with the same rows must run the gate again, not serve a stored pass.
func TestCollapseCacheNeverCachesAGateError(t *testing.T) {
	now := gateNow
	_, st, d := newTestServer(t, nil)
	reg, err := loadRegistry(writeRegistry(t, registryFor(map[string]int{"1d": 3})))
	if err != nil {
		t.Fatalf("load registry: %v", err)
	}
	d.CollapseCache = &CollapseVerdictCache{}
	for i := 1; i <= 3; i++ {
		seedResolvedForecasts(t, st, md.H1d, now.AddDate(0, 0, -i), 300, 180)
	}
	// A ts past year 9999 has no date(), so the gate's per-day scan fails, while
	// the fingerprint's sums stay inside int64 and read fine.
	execRaw(t, d.Cfg.DBPath, `INSERT INTO prediction_outcomes (symbol_id, horizon, ts, prob, up, resolved_at)
		VALUES (1, '1d', 9000000000000000000, 0.5, 1, 1)`)
	for i := 1; i <= 2; i++ {
		if _, c, err := d.collapsedGradingWindowCached(context.Background(), reg, now); err == nil || c {
			t.Fatalf("read %d: gate error not returned (collapsed=%v err=%v)", i, c, err)
		}
	}
}

// /api/track-record must refuse on a gate it could not evaluate, as
// /api/accuracy does (REFUSED_UNAVAILABLE), not publish a win rate.
func TestTrackRecordRefusesWhenTheCollapseGateCannotRun(t *testing.T) {
	sd30Off(t) // with SD-30 on, 1d is withheld whatever the gate says
	_, st, d := newTestServer(t, nil)
	d.RegistryPath = writeRegistry(t, registryFor(map[string]int{"1d": 3}))
	d.CollapseCache = &CollapseVerdictCache{}
	sym, err := st.UpsertSymbol(context.Background(), "TRK", md.Stocks, "")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	base := int64(store.GradingEpochTS)
	for di := int64(0); di < 40; di++ {
		prob, fwd := 0.8, 0.02
		if di%2 == 1 {
			prob, fwd = 0.2, -0.02
		}
		seedResolvedPrediction(t, st, sym.ID, md.H1d, base+di*86400, prob, fwd)
	}
	get := func() map[string]any {
		rr := httptest.NewRecorder()
		d.trackRecord(rr, httptest.NewRequest("GET", "/api/track-record?horizon=1d", nil))
		var resp map[string]any
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
			t.Fatalf("status %d, bad json: %v", rr.Code, err)
		}
		return resp
	}
	// Control: the same record publishes while the gate can run, so the refusal
	// below is the gate error's doing and nothing else's.
	if resp := get(); resp["gated"] != false || resp["winRate"] == nil {
		t.Fatalf("control: record should publish (gated=%v winRate=%v note=%v)", resp["gated"], resp["winRate"], resp["note"])
	}
	breakFingerprint(t, d.Cfg.DBPath, time.Unix(base, 0))
	resp := get()
	if resp["gated"] != true || resp["winRate"] != nil || resp["brier"] != nil || resp["ic"] != nil {
		t.Fatalf("FAIL-OPEN: track record published over an unevaluated gate (gated=%v winRate=%v)", resp["gated"], resp["winRate"])
	}
	if note, _ := resp["note"].(string); !strings.Contains(note, "could not be evaluated") {
		t.Errorf("the refusal must say the gate could not be evaluated, got note %q", note)
	}
}
