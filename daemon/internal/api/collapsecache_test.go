package api

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
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
