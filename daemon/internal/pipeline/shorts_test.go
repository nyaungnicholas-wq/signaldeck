package pipeline

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/ingest/finra"
	"github.com/nyaungnicholas-wq/signaldeck/internal/marketcal"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

func openShortsStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "shorts_p.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func nyTime(t *testing.T, y int, m time.Month, d, hh, mm int) time.Time {
	t.Helper()
	return time.Date(y, m, d, hh, mm, 0, 0, marketcal.Loc())
}

// TestTargetShortVolDay: the PURE publish gate — same trading day only past
// ~18:30 ET, else the previous TRADING day, with weekends and full NYSE
// holidays skipped via marketcal. 2026-07-03 (Fri) is the observed July-4th
// closure (July 4 is a Saturday), so the Sat/Mon cases must land on Thu
// 2026-07-02 — exactly what FINRA's live file listing shows for that week.
func TestTargetShortVolDay(t *testing.T) {
	cases := []struct {
		name string
		now  time.Time
		want string
	}{
		{"same trading day after deadline", nyTime(t, 2026, 7, 2, 19, 0), "2026-07-02"},
		{"same trading day before deadline", nyTime(t, 2026, 7, 2, 17, 0), "2026-07-01"},
		{"deadline boundary fires", nyTime(t, 2026, 7, 6, 18, 30), "2026-07-06"},
		{"saturday skips holiday friday (marketcal)", nyTime(t, 2026, 7, 4, 12, 0), "2026-07-02"},
		{"monday morning skips weekend + holiday", nyTime(t, 2026, 7, 6, 10, 0), "2026-07-02"},
	}
	for _, c := range cases {
		if got := TargetShortVolDay(c.now).Format("2006-01-02"); got != c.want {
			t.Errorf("%s: target = %s, want %s", c.name, got, c.want)
		}
	}
}

// shortsFileFor fabricates a file in the verified live format for one day
// (YYYYMMDD), with one tracked (AAPL) and one untracked (ZZZZ) row.
func shortsFileFor(day string) string {
	return "Date|Symbol|ShortVolume|ShortExemptVolume|TotalVolume|Market\n" +
		day + "|AAPL|100.5|1|402|B,Q,N\n" +
		day + "|ZZZZ|5|0|10|Q\n"
}

// newShortsServer serves CNMSshvolYYYYMMDD.txt for the given days and answers
// everything else 403 — exactly how FINRA's CDN answers absent days.
func newShortsServer(t *testing.T, served map[string]bool, hits *int64) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(hits, 1)
		p := strings.TrimPrefix(r.URL.Path, "/CNMSshvol")
		day := strings.TrimSuffix(p, ".txt")
		if served[day] {
			fmt.Fprint(w, shortsFileFor(day)) //nolint:errcheck
			return
		}
		w.WriteHeader(403)
	}))
}

func newShortsWorker(st *store.Store, url string, now time.Time, backfillDays int) *ShortVolPoller {
	return &ShortVolPoller{
		St:           st,
		Client:       &finra.Client{BaseURL: url, UA: "test", MinInterval: time.Millisecond},
		BackfillDays: backfillDays,
		Now:          func() time.Time { return now },
	}
}

// TestShortVolPoller_BackfillThenDedup: first run backfills the last N
// TRADING days (holiday files honestly counted unavailable), stores only
// TRACKED symbols with the correct ratio, sets both meta keys; the second
// run is a pure day-key dedup no-op (zero extra requests).
func TestShortVolPoller_BackfillThenDedup(t *testing.T) {
	st := openShortsStore(t)
	ctx := context.Background()
	aapl, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	if err != nil {
		t.Fatalf("seed AAPL: %v", err)
	}
	// Crypto symbol must never be sent to FINRA matching.
	if _, err := st.UpsertSymbol(ctx, "BTC/USD", md.Crypto, "Bitcoin"); err != nil {
		t.Fatalf("seed BTC: %v", err)
	}

	// Mon 2026-07-06 20:00 ET → target 2026-07-06; 3 trading days back:
	// 07-06, 07-02, 07-01 (Fri 07-03 = observed July-4th holiday, weekend skipped).
	now := nyTime(t, 2026, 7, 6, 20, 0)
	var hits int64
	srv := newShortsServer(t, map[string]bool{
		"20260706": true, "20260702": true, "20260701": true,
	}, &hits)
	defer srv.Close()

	w := newShortsWorker(st, srv.URL, now, 3)
	detail, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(detail, "backfilled 3 trading day(s)") {
		t.Fatalf("detail = %q, want 3-day backfill", detail)
	}

	series, err := st.ShortVolumeSeries(ctx, aapl.ID, 30)
	if err != nil {
		t.Fatalf("series: %v", err)
	}
	if len(series) != 3 {
		t.Fatalf("stored days = %d, want 3 (%+v)", len(series), series)
	}
	if series[0].Day != "2026-07-01" || series[2].Day != "2026-07-06" {
		t.Fatalf("trading-day window wrong: %+v", series)
	}
	if series[2].ShortPct != 100.5/402 {
		t.Fatalf("ratio math: got %v, want %v", series[2].ShortPct, 100.5/402)
	}
	// Untracked ZZZZ never stored (universe-scoped).
	var latestDay string
	if latestDay, err = st.LatestShortVolumeDay(ctx); err != nil || latestDay != "2026-07-06" {
		t.Fatalf("latest day = %q err=%v", latestDay, err)
	}
	ext, err := st.ShortVolumeExtremes(ctx, "2026-07-06", 0, 10)
	if err != nil || len(ext) != 1 || ext[0].Symbol != "AAPL" {
		t.Fatalf("extremes = %+v err=%v, want only tracked AAPL", ext, err)
	}

	if lastDay, _ := st.GetMeta(ctx, finraShortsLastDayKey); lastDay != "2026-07-06" {
		t.Fatalf("last-day meta = %q, want 2026-07-06", lastDay)
	}
	if done, _ := st.GetMeta(ctx, finraShortsBackfillKey); done == "" {
		t.Fatal("backfill meta not set")
	}

	// Second run at the same instant: dedup — no requests, honest detail.
	before := atomic.LoadInt64(&hits)
	detail, err = w.Run(ctx)
	if err != nil {
		t.Fatalf("run2: %v", err)
	}
	if !strings.Contains(detail, "up to date (2026-07-06)") {
		t.Fatalf("detail2 = %q, want up-to-date dedup", detail)
	}
	if atomic.LoadInt64(&hits) != before {
		t.Fatal("dedup run still hit the network")
	}
}

// TestShortVolPoller_UnavailableIsHonestSkip: a trading day whose file is
// missing after the publish deadline degrades to a dq event + retry (day key
// NOT set) — never a fleet failure, never fabricated rows.
func TestShortVolPoller_UnavailableIsHonestSkip(t *testing.T) {
	st := openShortsStore(t)
	ctx := context.Background()
	if _, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Skip the backfill path: pretend it already ran.
	if err := st.SetMeta(ctx, finraShortsBackfillKey, "done"); err != nil {
		t.Fatalf("meta: %v", err)
	}

	var hits int64
	srv := newShortsServer(t, map[string]bool{}, &hits) // 403 everything
	defer srv.Close()
	now := nyTime(t, 2026, 7, 6, 20, 0)
	w := newShortsWorker(st, srv.URL, now, 3)

	detail, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(detail, "unavailable") {
		t.Fatalf("detail = %q, want honest not-available skip", detail)
	}
	// The message must not promise a retry it cannot make. It used to say
	// "will retry" unconditionally, which stopped being true when this worker
	// moved to a once-per-trading-day schedule with no catch-up: the next fire
	// targeted a NEW day and the missed one was never requested again.
	if !strings.Contains(detail, "catch-up window") {
		t.Fatalf("detail = %q, want the retry claim bounded by the catch-up window", detail)
	}
	if lastDay, _ := st.GetMeta(ctx, finraShortsLastDayKey); lastDay != "" {
		t.Fatalf("day key set to %q on an unavailable day — retry would be lost", lastDay)
	}
}

// THE REGRESSION THAT MATTERS FOR O11: a day that was unavailable when it was
// due must actually be INGESTED once it appears, not skipped forever.
//
// Before the catch-up sweep, the cursor simply advanced to whatever day was
// current on the next run and the missed day was never requested again — the
// one-time backfill is gated shut, and srchealth tracks this source by MAX(day)
// so the hole was invisible the moment the NEXT day landed.
func TestShortVolPoller_MissedDayIsBackfilledOnALaterRun(t *testing.T) {
	st := openShortsStore(t)
	ctx := context.Background()
	if _, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := st.SetMeta(ctx, finraShortsBackfillKey, "done"); err != nil {
		t.Fatalf("meta: %v", err)
	}

	// Day one (2026-07-06) is NOT published when it is due.
	avail := map[string]bool{}
	var hits int64
	srv := newShortsServer(t, avail, &hits)
	defer srv.Close()

	day1 := nyTime(t, 2026, 7, 6, 20, 0)
	w := newShortsWorker(st, srv.URL, day1, 3)
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("run day1: %v", err)
	}
	if has, _ := st.HasShortVolumeDay(ctx, "2026-07-06"); has {
		t.Fatal("fixture wrong: 2026-07-06 should not be stored yet")
	}

	// FINRA posts it late — by the time day two runs, BOTH files exist.
	// Keys are the wire form (YYYYMMDD), matching newShortsServer's path parse.
	avail["20260706"] = true
	avail["20260707"] = true
	day2 := nyTime(t, 2026, 7, 7, 20, 0)
	w2 := newShortsWorker(st, srv.URL, day2, 3)
	detail, err := w2.Run(ctx)
	if err != nil {
		t.Fatalf("run day2: %v", err)
	}

	if has, _ := st.HasShortVolumeDay(ctx, "2026-07-06"); !has {
		t.Fatalf("the missed day was never re-requested — it is lost forever; detail=%q", detail)
	}
	if has, _ := st.HasShortVolumeDay(ctx, "2026-07-07"); !has {
		t.Fatalf("the current day was not ingested; detail=%q", detail)
	}
	if lastDay, _ := st.GetMeta(ctx, finraShortsLastDayKey); lastDay != "2026-07-07" {
		t.Fatalf("cursor = %q, want it advanced to the target day 2026-07-07", lastDay)
	}
}

// TestShortVolPoller_NoStocksSkips: with no tracked stocks there is nothing
// to scope to — the worker skips honestly without touching the network.
func TestShortVolPoller_NoStocksSkips(t *testing.T) {
	st := openShortsStore(t)
	ctx := context.Background()
	var hits int64
	srv := newShortsServer(t, map[string]bool{}, &hits)
	defer srv.Close()
	w := newShortsWorker(st, srv.URL, nyTime(t, 2026, 7, 6, 20, 0), 3)
	detail, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(detail, "no tracked stocks") {
		t.Fatalf("detail = %q", detail)
	}
	if atomic.LoadInt64(&hits) != 0 {
		t.Fatal("no-universe run still hit the network")
	}
}
