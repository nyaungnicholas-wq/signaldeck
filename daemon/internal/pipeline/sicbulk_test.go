// Stage 4 tests — the sic-bulk-sync worker: end-to-end fixture-zip run
// (coverage before/after, temp-file cleanup, meta cursors), the pure
// boot-vs-monthly gate table, download-failure degradation (dq event, fleet
// never fails, once-per-day attempt brake), the disk guard, and the no-op
// paths. t.TempDir stores + httptest only — the LIVE store is never touched.
package pipeline

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/ingest/edgar"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// newBulkServer serves the edgar testdata fixture zip (3 CIK entries: 320193
// sic 3571, 1045810 sic 3674, 19617 blank sic + decoys) and counts hits.
func newBulkServer(t *testing.T) (*edgar.Client, *int) {
	t.Helper()
	body := edgarFixture(t, "bulk_submissions.zip")
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	c := edgar.New()
	c.UA = "test t@e.c"
	c.BulkURL = srv.URL + "/submissions.zip"
	c.MinInterval = time.Millisecond
	return c, &hits
}

// seedDirectory upserts a 3-row directory: AAPL + NVDA unclassified, plus a
// GOOG share-class pair on ONE CIK to prove by-CIK fan-out. Coverage 0/4.
func seedDirectory(t *testing.T, st *store.Store) {
	t.Helper()
	if err := st.UpsertCompanies(context.Background(), []store.CompanyRow{
		{CIK: 320193, Ticker: "AAPL", Name: "Apple Inc.", Exchange: "Nasdaq", UpdatedTs: 1},
		{CIK: 1045810, Ticker: "NVDA", Name: "NVIDIA CORP", Exchange: "Nasdaq", UpdatedTs: 1},
		{CIK: 1045810, Ticker: "NVDA2", Name: "NVIDIA CORP CL2 (fixture)", Exchange: "Nasdaq", UpdatedTs: 1},
		{CIK: 999999, Ticker: "ZZZZ", Name: "Not In Archive Corp", Exchange: "NYSE", UpdatedTs: 1},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
}

func TestSICBulkSync_BootCatchUpEndToEnd(t *testing.T) {
	st := openS8Store(t)
	ctx := context.Background()
	seedDirectory(t, st)
	client, hits := newBulkServer(t)
	tmp := t.TempDir()
	now := time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)

	w := &SICBulkSync{St: st, Client: client, Now: func() time.Time { return now }, TmpDir: tmp, MinFree: 1}
	detail, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("run: %v (%s)", err, detail)
	}
	if !strings.Contains(detail, "boot catch-up") {
		t.Fatalf("detail = %q, want the boot catch-up reason (coverage 0%% < 50%%)", detail)
	}
	// Both NVDA share classes updated from ONE CIK; AAPL updated; ZZZZ honest ''.
	rows, _ := st.ListCompanies(ctx, "", "", "")
	got := map[string]string{}
	for _, r := range rows {
		got[r.Ticker] = r.SIC
	}
	if got["AAPL"] != "3571" || got["NVDA"] != "3674" || got["NVDA2"] != "3674" {
		t.Fatalf("sic after sync = %v", got)
	}
	if got["ZZZZ"] != "" {
		t.Fatalf("ZZZZ = %q — a CIK absent from the archive must stay honestly unclassified", got["ZZZZ"])
	}
	total, with, _ := st.CompanySICCoverage(ctx)
	if total != 4 || with != 3 {
		t.Fatalf("coverage after = %d/%d, want 3/4", with, total)
	}
	// Cursors recorded.
	if v, _ := st.GetMeta(ctx, "sic_bulk_last_month"); v != "2026-07" {
		t.Fatalf("sic_bulk_last_month = %q", v)
	}
	if v, _ := st.GetMeta(ctx, "sic_bulk_ts"); v == "" {
		t.Fatalf("sic_bulk_ts not recorded")
	}
	// Temp zip deleted.
	ents, _ := os.ReadDir(tmp)
	if len(ents) != 0 {
		t.Fatalf("temp dir not cleaned: %v", ents)
	}
	if *hits != 1 {
		t.Fatalf("downloads = %d, want exactly one per run", *hits)
	}

	// Second run in the SAME month: coverage now 75% ≥ 50% ⇒ monthly gate holds.
	detail2, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("re-run: %v", err)
	}
	if !strings.Contains(detail2, "waiting") || *hits != 1 {
		t.Fatalf("same-month re-run must wait without downloading: detail=%q hits=%d", detail2, *hits)
	}

	// A month later: the monthly refresh fires again.
	w.Now = func() time.Time { return time.Date(2026, 8, 1, 4, 0, 0, 0, time.UTC) }
	detail3, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("next-month run: %v", err)
	}
	if !strings.Contains(detail3, "monthly refresh") || *hits != 2 {
		t.Fatalf("next-month run: detail=%q hits=%d", detail3, *hits)
	}
}

// TestSICBulkShouldRun is the pure gate table: boot if coverage < 50%
// (≤1 attempt/day), else monthly; force bypasses; empty directory waits.
func TestSICBulkShouldRun(t *testing.T) {
	cases := []struct {
		name                       string
		total, withSIC             int
		month, lastMonth, day, att string
		force, want                bool
	}{
		{"empty directory waits", 0, 0, "2026-07", "", "2026-07-06", "", false, false},
		{"boot: low coverage runs", 10415, 533, "2026-07", "", "2026-07-06", "", false, true},
		{"boot: already attempted today waits", 10415, 533, "2026-07", "", "2026-07-06", "2026-07-06", false, false},
		{"boot: attempted yesterday retries", 10415, 533, "2026-07", "", "2026-07-06", "2026-07-05", false, true},
		{"covered: same month waits", 10415, 10000, "2026-07", "2026-07", "2026-07-06", "", false, false},
		{"covered: new month runs", 10415, 10000, "2026-08", "2026-07", "2026-08-01", "", false, true},
		{"covered: never ran runs", 10415, 10000, "2026-07", "", "2026-07-06", "", false, true},
		{"exactly 50% is covered (monthly path)", 100, 50, "2026-07", "2026-07", "2026-07-06", "", false, false},
		{"force bypasses everything", 0, 0, "2026-07", "2026-07", "2026-07-06", "2026-07-06", true, true},
	}
	for _, c := range cases {
		got, why := SICBulkShouldRun(c.total, c.withSIC, c.month, c.lastMonth, c.day, c.att, c.force)
		if got != c.want {
			t.Errorf("%s: run=%v want %v (%s)", c.name, got, c.want, why)
		}
		if why == "" {
			t.Errorf("%s: the gate must always explain itself", c.name)
		}
	}
}

func TestSICBulkSync_DownloadFailureDegrades(t *testing.T) {
	st := openS8Store(t)
	ctx := context.Background()
	seedDirectory(t, st)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Forbidden", http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)
	client := edgar.New()
	client.UA = "test t@e.c"
	client.BulkURL = srv.URL + "/submissions.zip"
	client.MinInterval = time.Millisecond
	now := time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)

	w := &SICBulkSync{St: st, Client: client, Now: func() time.Time { return now }, TmpDir: t.TempDir(), MinFree: 1}
	detail, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("a bulk outage must NEVER fail the fleet: %v", err)
	}
	if !strings.Contains(detail, "degraded to the filings-poller SIC rotation") {
		t.Fatalf("detail = %q, want the honest degradation note", detail)
	}
	// dq event recorded.
	evs, _ := st.RecentDQ(ctx, 10)
	found := false
	for _, e := range evs {
		if e.Kind == "sic_bulk_unavailable" {
			found = true
		}
	}
	if !found {
		t.Fatalf("dq events = %+v, want kind sic_bulk_unavailable", evs)
	}
	// The attempt-day brake: a same-day re-run must NOT re-download 1.5 GB.
	detail2, err := w.Run(ctx)
	if err != nil || !strings.Contains(detail2, "already attempted today") {
		t.Fatalf("same-day retry after failure: detail=%q err=%v", detail2, err)
	}
	// Success month never recorded on failure.
	if v, _ := st.GetMeta(ctx, "sic_bulk_last_month"); v != "" {
		t.Fatalf("sic_bulk_last_month = %q after a FAILED run", v)
	}
}

func TestSICBulkSync_DiskGuard(t *testing.T) {
	st := openS8Store(t)
	ctx := context.Background()
	seedDirectory(t, st)
	client, hits := newBulkServer(t)
	now := time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)

	w := &SICBulkSync{
		St: st, Client: client, Now: func() time.Time { return now }, TmpDir: t.TempDir(),
		// Default 5 GiB guard with a hook reporting only 1 GiB free.
		FreeDisk: func(string) (uint64, error) { return 1 << 30, nil },
	}
	detail, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("disk guard must skip, not fail: %v", err)
	}
	if !strings.Contains(detail, "skipped") || !strings.Contains(detail, "guard") {
		t.Fatalf("detail = %q, want the disk-guard skip", detail)
	}
	if *hits != 0 {
		t.Fatalf("downloads = %d — the guard must fire BEFORE touching the network", *hits)
	}
	evs, _ := st.RecentDQ(ctx, 10)
	found := false
	for _, e := range evs {
		if e.Kind == "sic_bulk_skipped" {
			found = true
		}
	}
	if !found {
		t.Fatalf("dq events = %+v, want kind sic_bulk_skipped", evs)
	}
}

func TestSICBulkSync_NoopPaths(t *testing.T) {
	ctx := context.Background()

	// nil client (no EDGAR wired) — clean skip.
	st := openS8Store(t)
	w := &SICBulkSync{St: st}
	if detail, err := w.Run(ctx); err != nil || !strings.Contains(detail, "skipped") {
		t.Fatalf("nil client: detail=%q err=%v", detail, err)
	}

	// Empty directory — waits for companies-sync, no download.
	st2 := openS8Store(t)
	client, hits := newBulkServer(t)
	w2 := &SICBulkSync{St: st2, Client: client, TmpDir: t.TempDir(), MinFree: 1}
	detail, err := w2.Run(ctx)
	if err != nil || !strings.Contains(detail, "directory is empty") || *hits != 0 {
		t.Fatalf("empty directory: detail=%q err=%v hits=%d", detail, err, *hits)
	}
}

// TestSICBulkSync_ForceBypassesGate covers the manual one-shot path
// (`signaldeckd -sic-bulk-sync`): gate ignored, disk guard still honored.
func TestSICBulkSync_ForceBypassesGate(t *testing.T) {
	st := openS8Store(t)
	ctx := context.Background()
	seedDirectory(t, st)
	client, hits := newBulkServer(t)
	now := time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)
	// Same-month cursor already set — the periodic gate would wait.
	_ = st.SetMeta(ctx, "sic_bulk_last_month", "2026-07")
	_ = st.SetMeta(ctx, "sic_bulk_attempt_day", "2026-07-06")

	w := &SICBulkSync{St: st, Client: client, Now: func() time.Time { return now }, TmpDir: t.TempDir(), MinFree: 1, Force: true}
	detail, err := w.Run(ctx)
	if err != nil || !strings.Contains(detail, "forced") || *hits != 1 {
		t.Fatalf("forced run: detail=%q err=%v hits=%d", detail, err, *hits)
	}
	if _, with, _ := st.CompanySICCoverage(ctx); with != 3 {
		t.Fatalf("forced run coverage = %d, want 3", with)
	}
}
