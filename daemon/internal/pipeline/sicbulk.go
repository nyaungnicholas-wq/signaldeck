// Stage 4 — SIC/SECTOR COVERAGE worker: sic-bulk-sync.
//
// WHY: the filings-poller enriches companies.sic/sic_desc opportunistically
// (it rides submissions fetches it already makes — zero added requests), but
// it only sweeps UNIVERSE symbols, so after weeks the directory sits at
// 533/10,415 rows classified. This worker closes the gap for the WHOLE
// directory with ONE free download.
//
// SOURCE (researched 2026-07-06; candidates compared):
//	(a) EDGAR bulk submissions.zip — CHOSEN. SEC's official nightly export of
//	    the entire Submissions API, documented on "EDGAR Application
//	    Programming Interfaces" (sec.gov/search-filings/
//	    edgar-application-programming-interfaces): "The submission.zip file
//	    contains the public EDGAR filing history for all filers … recompiled
//	    nightly [~3:00 a.m. ET]". Verified live: HTTP 200, ~1.5 GB
//	    (content-length 1,549,810,197). Every CIK##########.json entry carries
//	    the sic/sicDescription header — full coverage, one request.
//	(b) EDGAR full-text/company facet — no SIC field in responses; rejected.
//	(c) Per-company submissions rotation (status quo) — correct but ~10k paced
//	    requests ≈ days of polite crawling per full pass; kept as the
//	    DEGRADATION path, not the primary.
//
// MECHANICS: stream-download to a temp file under SIGNALDECK_TMP (else
// os.TempDir()), walk the zip's central directory on disk, fully decode ONLY
// the ~10.4k entries whose CIK is in the companies directory, batch-update
// sic/sic_desc by CIK in one transaction, log coverage before/after, delete
// the temp file (also on failure). Memory stays flat: the archive is never
// held in RAM.
//
// GATE (pure function, tested): the worker ticks every 12h but acts at most
//   - at BOOT (first tick) when coverage < 50% — fresh installs get the whole
//     directory classified immediately; at most ONE attempt per UTC day so a
//     failing download can never hammer SEC;
//   - else ONCE PER UTC MONTH (the archive is nightly, but SIC codes change
//     rarely — monthly keeps us a polite bulk consumer).
//
// HONESTY + DEGRADATION: a 403/moved/corrupt archive records a dq event
// (kind sic_bulk_unavailable), returns an honest detail, and NEVER fails the
// fleet — the filings-poller rotation keeps enriching regardless. A blank SIC
// in the archive is honest absence and is never stored. Disk guard: requires
// > 5 GiB free in the temp dir before downloading (dq kind sic_bulk_skipped).
//
// MANUAL RUN: `signaldeckd -sic-bulk-sync` (see cmd/signaldeckd) constructs
// this worker with Force=true, runs it once against the configured store, and
// exits — stop the daemon first to avoid write contention.
package pipeline

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/ingest/edgar"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

const (
	// sicBulkMonthKey is the UTC month ("2006-01") of the last SUCCESSFUL sync.
	sicBulkMonthKey = "sic_bulk_last_month"
	// sicBulkAttemptDayKey is the UTC day of the last boot-mode ATTEMPT
	// (success or failure) — the once-per-day brake on the <50% catch-up path.
	sicBulkAttemptDayKey = "sic_bulk_attempt_day"
	// sicBulkTsKey is the unix time of the last successful sync (freshness).
	sicBulkTsKey = "sic_bulk_ts"
	// sicBulkCoverageGate is the boot-catch-up threshold.
	sicBulkCoverageGate = 0.50
	// sicBulkMinFreeBytes is the disk guard: the compressed archive is ~1.5 GB
	// and unzip reads are on top; 5 GiB headroom keeps the host safe.
	sicBulkMinFreeBytes = 5 << 30
	// sicBulkDownloadTimeout bounds the one streaming download.
	sicBulkDownloadTimeout = 45 * time.Minute
)

// SICBulkSync is the sic-bulk-sync worker (implements workers.Worker).
type SICBulkSync struct {
	St *store.Store
	// Client is the daemon-wide SHARED EDGAR client (one limiter + one UA for
	// the whole process). nil ⇒ clean no-op.
	Client *edgar.Client
	// Now is a test hook; nil = time.Now.
	Now func() time.Time
	// TmpDir overrides the scratch dir; "" = SIGNALDECK_TMP else os.TempDir().
	TmpDir string
	// MinFree overrides the disk guard in bytes; 0 = 5 GiB.
	MinFree uint64
	// FreeDisk is a test hook returning free bytes for a dir; nil = statfs.
	FreeDisk func(dir string) (uint64, error)
	// Force bypasses the cadence gate (NOT the disk guard) — the manual
	// one-shot path (`signaldeckd -sic-bulk-sync`).
	Force bool
}

func (w *SICBulkSync) Name() string            { return "sic-bulk-sync" }
func (w *SICBulkSync) Interval() time.Duration { return 12 * time.Hour }

// SICBulkShouldRun is the pure cadence gate. Inputs are the directory's
// coverage counts, the UTC month/day keys for now, and the persisted cursors.
// It returns whether to act and a one-line human reason either way.
func SICBulkShouldRun(total, withSIC int, monthKey, lastMonth, dayKey, lastAttemptDay string, force bool) (bool, string) {
	if force {
		return true, "forced (manual run)"
	}
	if total == 0 {
		return false, "waiting: companies directory is empty (companies-sync hasn't completed a run)"
	}
	cov := float64(withSIC) / float64(total)
	if cov < sicBulkCoverageGate {
		if lastAttemptDay == dayKey {
			return false, fmt.Sprintf("waiting: coverage %.1f%% < 50%% but already attempted today (%s) — retrying tomorrow", cov*100, dayKey)
		}
		return true, fmt.Sprintf("boot catch-up: coverage %.1f%% < 50%%", cov*100)
	}
	if monthKey != lastMonth {
		return true, fmt.Sprintf("monthly refresh for %s (coverage %.1f%%)", monthKey, cov*100)
	}
	return false, fmt.Sprintf("waiting: coverage %.1f%%, already synced in %s (next: first tick of the next UTC month)", cov*100, monthKey)
}

func (w *SICBulkSync) tmpDir() string {
	if w.TmpDir != "" {
		return w.TmpDir
	}
	if d := strings.TrimSpace(os.Getenv("SIGNALDECK_TMP")); d != "" {
		return d
	}
	return os.TempDir()
}

func (w *SICBulkSync) minFree() uint64 {
	if w.MinFree > 0 {
		return w.MinFree
	}
	return sicBulkMinFreeBytes
}

func (w *SICBulkSync) freeDisk(dir string) (uint64, error) {
	if w.FreeDisk != nil {
		return w.FreeDisk(dir)
	}
	var fs syscall.Statfs_t
	if err := syscall.Statfs(dir, &fs); err != nil {
		return 0, err
	}
	return uint64(fs.Bavail) * uint64(fs.Bsize), nil
}

// degrade records a dq event and returns an HONEST ok-status detail — a bulk
// outage must never fail the fleet (the filings-poller rotation still runs).
func (w *SICBulkSync) degrade(ctx context.Context, kind, detail string, ts int64) (string, error) {
	_ = w.St.InsertDQ(ctx, md.DQEvent{Ts: ts, Kind: kind, Detail: detail})
	return detail, nil
}

func (w *SICBulkSync) Run(ctx context.Context) (string, error) {
	if w.Client == nil {
		return "skipped: no EDGAR client", nil
	}
	now := time.Now
	if w.Now != nil {
		now = w.Now
	}
	t := now()
	monthKey := t.UTC().Format("2006-01")
	dayKey := t.UTC().Format("2006-01-02")

	total, withSIC, err := w.St.CompanySICCoverage(ctx)
	if err != nil {
		return "", err
	}
	lastMonth, _ := w.St.GetMeta(ctx, sicBulkMonthKey)
	lastAttemptDay, _ := w.St.GetMeta(ctx, sicBulkAttemptDayKey)
	run, why := SICBulkShouldRun(total, withSIC, monthKey, lastMonth, dayKey, lastAttemptDay, w.Force)
	if !run {
		return why, nil
	}
	// Record the attempt BEFORE downloading: if this run fails (or the process
	// dies mid-download) the boot-catch-up path waits until tomorrow instead of
	// re-pulling 1.5 GB every 12h tick.
	_ = w.St.SetMeta(ctx, sicBulkAttemptDayKey, dayKey)

	// Disk guard: > 5 GiB free in the scratch dir before touching the network.
	dir := w.tmpDir()
	free, ferr := w.freeDisk(dir)
	if ferr != nil {
		return w.degrade(ctx, "sic_bulk_skipped",
			fmt.Sprintf("sic-bulk-sync skipped: cannot stat free space in %s (%v) — refusing a ~1.5 GB download blind", dir, ferr), t.Unix())
	}
	if free < w.minFree() {
		return w.degrade(ctx, "sic_bulk_skipped",
			fmt.Sprintf("sic-bulk-sync skipped: %.1f GiB free in %s < %.1f GiB guard — free disk space or set SIGNALDECK_TMP",
				float64(free)/(1<<30), dir, float64(w.minFree())/(1<<30)), t.Unix())
	}

	// ONE streaming download per run, straight to disk; always cleaned up.
	dst := filepath.Join(dir, fmt.Sprintf("signaldeck-sic-bulk-%d.zip", t.Unix()))
	defer os.Remove(dst) //nolint:errcheck
	dlCtx, cancel := context.WithTimeout(ctx, sicBulkDownloadTimeout)
	defer cancel()
	nBytes, err := w.Client.DownloadBulkSubmissions(dlCtx, dst)
	if err != nil {
		return w.degrade(ctx, "sic_bulk_unavailable",
			fmt.Sprintf("bulk submissions.zip unavailable (%v) — degraded to the filings-poller SIC rotation; will retry per gate", err), t.Unix())
	}

	// Only the directory's CIKs are worth decompressing (~10.4k of ~1M entries).
	rows, err := w.St.ListCompanies(ctx, "", "", "")
	if err != nil {
		return "", err
	}
	want := make(map[int64]bool, len(rows))
	for _, r := range rows {
		want[r.CIK] = true
	}
	recs, matched, err := edgar.ExtractBulkSIC(dst, want)
	if err != nil {
		return w.degrade(ctx, "sic_bulk_unavailable",
			fmt.Sprintf("bulk submissions.zip unreadable (%v) — corrupt/partial download discarded; degraded to the filings-poller SIC rotation", err), t.Unix())
	}
	updates := make([]store.SICUpdate, 0, len(recs))
	for _, r := range recs {
		updates = append(updates, store.SICUpdate{CIK: r.CIK, SIC: r.SIC, SICDesc: r.SICDesc})
	}
	rowsUpdated, err := w.St.BatchUpdateCompanySICByCIK(ctx, updates, t.Unix())
	if err != nil {
		return "", err // a store failure is a REAL error — visible in worker_runs
	}

	total2, with2, _ := w.St.CompanySICCoverage(ctx)
	_ = w.St.SetMeta(ctx, sicBulkMonthKey, monthKey)
	_ = w.St.SetMeta(ctx, sicBulkTsKey, fmt.Sprint(t.Unix()))
	pct := func(with, tot int) float64 {
		if tot == 0 {
			return 0
		}
		return float64(with) / float64(tot) * 100
	}
	slog.Info("sic-bulk-sync", "reason", why,
		"coverageBefore", fmt.Sprintf("%d/%d (%.1f%%)", withSIC, total, pct(withSIC, total)),
		"coverageAfter", fmt.Sprintf("%d/%d (%.1f%%)", with2, total2, pct(with2, total2)),
		"cikMatched", matched, "cikWithSIC", len(recs), "rowsUpdated", rowsUpdated,
		"downloadedMB", nBytes>>20)
	return fmt.Sprintf(
		"SIC bulk sync (%s): coverage %d/%d (%.1f%%) → %d/%d (%.1f%%); %d directory CIKs found in archive, %d carried a SIC, %d rows updated; one %d MB download (temp deleted). Unclassified rows are honest absence (EDGAR has no SIC for them).",
		why, withSIC, total, pct(withSIC, total), with2, total2, pct(with2, total2),
		matched, len(recs), rowsUpdated, nBytes>>20), nil
}
