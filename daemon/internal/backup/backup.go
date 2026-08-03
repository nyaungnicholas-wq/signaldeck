// Package backup is the nightly SQLite backup agent. It runs VACUUM INTO —
// SQLite's safe online-backup path (a consistent, compacted copy taken while
// the daemon keeps reading and writing under WAL) — into a timestamped file
// under the backup directory, prunes old copies, and then COPIES the freshest
// backup OFF the machine (to iCloud Drive by default) so a total-loss event —
// the single SQLite file on the one Mac dying — is survivable. The offsite copy
// is strictly best-effort: if the offsite directory is unset, missing, or
// unwritable the local backup still happens, a dq_events(offsite_backup_
// unavailable) records why, and the run succeeds — the fleet never fails on a
// backup-destination problem, and the failure stays VISIBLE via /api/quality.
package backup

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/notify"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// meta keys the worker records so a silent backup failure is visible at
// GET /api/quality (ops block). All best-effort — a meta write hiccup never
// fails the run.
const (
	MetaLastBackupTs   = "backup_last_ts"      // unix secs of the last successful local backup
	MetaLastBackupFile = "backup_last_file"    // basename of that backup
	MetaLastOffsiteTs  = "backup_last_offsite" // unix secs of the last successful offsite copy
	MetaOffsiteDir     = "backup_offsite_dir"  // configured offsite dir ("" = not configured)
)

// Worker is the periodic backup agent (implements workers.Worker).
type Worker struct {
	St  *store.Store
	Dir string // local backup directory (required)
	// OffsiteDir is the OFF-MACHINE destination (iCloud Drive by default). The
	// freshest local backup is copied here after every local backup. Empty =
	// offsite disabled (local backup still runs; no dq event — a deliberate
	// opt-out is not a failure).
	OffsiteDir string
	Keep       int // rotated copies to keep in EACH location (default 7)
	// FirstRunDelay defers only the first Run (the fleet runner fires every
	// worker immediately at boot; a fresh backup at every restart is noise).
	// 0 = no delay.
	FirstRunDelay time.Duration
	// MinGap skips a Run when the last successful local backup (meta
	// backup_last_ts) is younger than this (default 20h). The delay above only
	// postpones the boot run — without this gate every daemon restart still
	// produced a full VACUUM INTO copy plus its offsite upload, and a few
	// deploys in one day rotated real nightly history out of the Keep window.
	MinGap time.Duration

	// VerifyBackup is the post-write integrity check applied to every fresh
	// copy (local and offsite) before it is trusted. nil = the real
	// PRAGMA-quick_check-based verifyBackup; overridable in tests to simulate
	// a corrupt result without needing an actually-corrupt multi-GB SQLite
	// file. H8 hostile-review fix: before this field existed, success was
	// `os.Stat(target)` returning without error, which accepts a
	// corrupt-but-nonzero-sized copy — and KEEP=7 rotation would then destroy
	// the last good generation behind it.
	VerifyBackup func(ctx context.Context, path string) error

	// Remote fans backup failures out BEYOND the Mac (H9: dq_events and
	// /api/quality only page a human who is already looking). nil or
	// unconfigured = no-op — dq/meta visibility above is unchanged.
	Remote *notify.Notifier

	ran bool
}

// page delivers one backup-failure message to the remote transports.
// Best-effort by notify's own contract: never blocks, never fails the run.
func (w *Worker) page(ctx context.Context, title, body string) {
	w.Remote.Send(ctx, notify.Message{Title: title, Body: body, Kind: "backup"})
}

// verify runs the configured integrity check (or the real one) against path.
func (w *Worker) verify(ctx context.Context, path string) error {
	if w.VerifyBackup != nil {
		return w.VerifyBackup(ctx, path)
	}
	return verifyBackup(ctx, path)
}

// verifyBackup opens the freshly-written copy at path and runs PRAGMA
// quick_check — SQLite's structural-integrity scan — so a corrupt-but-nonzero
// copy (a torn write, a disk error VACUUM INTO didn't surface as a Go error)
// is caught before it is trusted as a good generation. quick_check, not the
// slower full integrity_check: this runs nightly against a file that can be
// 2GB+, and quick_check still catches the page/btree-level corruption a torn
// write produces while skipping the exhaustive index/foreign-key cross-check.
func verifyBackup(ctx context.Context, path string) error {
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return fmt.Errorf("open for verify: %w", err)
	}
	defer db.Close() //nolint:errcheck
	var result string
	if err := db.QueryRowContext(ctx, `PRAGMA quick_check`).Scan(&result); err != nil {
		return fmt.Errorf("quick_check query: %w", err)
	}
	if result != "ok" {
		return fmt.Errorf("quick_check reported corruption: %s", result)
	}
	return nil
}

// Name implements workers.Worker.
func (w *Worker) Name() string { return "db-backup" }

// Interval implements workers.Worker: nightly.
func (w *Worker) Interval() time.Duration { return 24 * time.Hour }

// Run takes one backup and prunes old ones.
func (w *Worker) Run(ctx context.Context) (string, error) {
	if w.St != nil {
		gap := w.MinGap
		if gap <= 0 {
			// 30h (was 20h, 2026-07-24): the primary backup is now the OFFLINE
			// post-market-close run (ops/signaldeck-backup-offline.sh) — with the
			// daemon gated to 06:20-13:10 PT, a 20h gate made this worker fire its
			// full VACUUM INTO at boot every morning during market hours, wedging
			// the app. At 30h this worker is a pure failsafe: it only runs when
			// the offline backup has actually missed a day.
			gap = 30 * time.Hour
		}
		if v, err := w.St.GetMeta(ctx, MetaLastBackupTs); err == nil && v != "" {
			if last, perr := strconv.ParseInt(v, 10, 64); perr == nil {
				if age := time.Since(time.Unix(last, 0)); age >= 0 && age < gap {
					return fmt.Sprintf("skipped — last backup %s ago (restart gate, min gap %s)",
						age.Round(time.Minute), gap), nil
				}
			}
		}
	}
	if !w.ran {
		w.ran = true
		if w.FirstRunDelay > 0 {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(w.FirstRunDelay):
			}
		}
	}
	if w.Dir == "" {
		return "", fmt.Errorf("backup dir not configured")
	}
	if err := os.MkdirAll(w.Dir, 0o755); err != nil {
		return "", fmt.Errorf("create backup dir: %w", err)
	}
	target := filepath.Join(w.Dir, fmt.Sprintf("signaldeck-%s.db", time.Now().Format("20060102-150405")))
	// VACUUM INTO refuses to overwrite; a leftover from a crashed run would
	// block forever, so clear the exact target first.
	os.Remove(target) //nolint:errcheck
	// Run on the READ pool: VACUUM INTO only reads the source database, and
	// keeping it off the single write connection means writers aren't queued
	// behind a potentially long copy.
	if _, err := w.St.DB().ExecContext(ctx, `VACUUM INTO ?`, target); err != nil {
		w.page(ctx, "SignalDeck backup FAILED", fmt.Sprintf("VACUUM INTO %s: %v", filepath.Base(target), err))
		return "", fmt.Errorf("vacuum into %s: %w", target, err)
	}
	st, err := os.Stat(target)
	if err != nil {
		return "", fmt.Errorf("backup written but unstattable: %w", err)
	}
	// H8: verify BEFORE pruning or recording success. A failed check removes
	// the corrupt copy, records why, and returns an error — which means
	// prune() below is never reached, so the existing (good) generations in
	// w.Dir are never rotated away behind a bad one, and meta's
	// backup_last_ts is never advanced past the last KNOWN-GOOD backup.
	if verr := w.verify(ctx, target); verr != nil {
		os.Remove(target) //nolint:errcheck
		_ = w.St.InsertDQ(ctx, md.DQEvent{
			Ts:     time.Now().Unix(),
			Kind:   "backup_corrupt",
			Detail: fmt.Sprintf("%s failed integrity check (removed, previous generations NOT rotated): %v", filepath.Base(target), verr),
		})
		w.page(ctx, "SignalDeck backup FAILED — corrupt copy",
			fmt.Sprintf("%s failed integrity check (removed, previous generations kept): %v", filepath.Base(target), verr))
		return "", fmt.Errorf("backup integrity check failed, corrupt copy removed, no rotation: %w", verr)
	}
	// quick_check above proves STRUCTURE. It does not prove the copy still has
	// the accountability record in it — see verifyContent. Same H8 ordering as
	// the structural check: fail before prune() and before meta advances, so a
	// gutted copy never rotates away a good generation or moves
	// backup_last_ts past the last KNOWN-GOOD backup.
	//
	// A failed live count reads as 0, which verifyContent treats as "unknown"
	// and skips the staleness comparison — not knowing how many rows there
	// should be is not evidence that the backup is short.
	var liveLedger int64
	_ = w.St.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM prediction_ledger`).Scan(&liveLedger)
	if cerr := verifyContent(ctx, target, liveLedger); cerr != nil {
		// Quarantine rather than delete: a backup that failed verification is
		// the evidence for WHY it failed, and it is the only artifact of that
		// run. Kept outside w.Dir so prune() never sees it.
		qdir := filepath.Join(filepath.Dir(w.Dir), "quarantine", "backups-"+time.Now().Format("20060102"))
		moved := "removed"
		if mkErr := os.MkdirAll(qdir, 0o755); mkErr == nil {
			dest := filepath.Join(qdir, filepath.Base(target))
			if os.Rename(target, dest) == nil {
				moved = "quarantined in " + qdir
			} else {
				os.Remove(target) //nolint:errcheck
			}
		} else {
			os.Remove(target) //nolint:errcheck
		}
		_ = w.St.InsertDQ(ctx, md.DQEvent{
			Ts:     time.Now().Unix(),
			Kind:   "backup_gutted",
			Detail: fmt.Sprintf("%s passed quick_check but failed CONTENT verification (%s, previous generations NOT rotated): %v", filepath.Base(target), moved, cerr),
		})
		w.page(ctx, "SignalDeck backup FAILED — accountability record missing",
			fmt.Sprintf("%s is structurally valid but substantively empty (%s): %v", filepath.Base(target), moved, cerr))
		return "", fmt.Errorf("backup content verification failed, copy %s, no rotation: %w", moved, cerr)
	}
	pruned, perr := w.prune(w.Dir)
	detail := fmt.Sprintf("backup %s (%.1f MB), pruned %d old", filepath.Base(target), float64(st.Size())/(1024*1024), pruned)
	if perr != nil {
		detail += fmt.Sprintf(" (prune error: %v)", perr)
	}
	// Record the local success for /api/quality before attempting offsite, so a
	// failed offsite copy never hides that the local backup DID happen.
	_ = w.St.SetMeta(ctx, MetaLastBackupTs, fmt.Sprintf("%d", time.Now().Unix()))
	_ = w.St.SetMeta(ctx, MetaLastBackupFile, filepath.Base(target))
	_ = w.St.SetMeta(ctx, MetaOffsiteDir, w.OffsiteDir)

	detail += "; " + w.offsite(ctx, target)
	return detail, nil
}

// offsite copies the freshest backup off the machine and prunes the offsite
// location. It is ALWAYS best-effort: every failure (unset/missing/unwritable
// dir, copy error) is turned into an honest detail fragment + a dq_events
// record, and returns without erroring so the fleet run still succeeds.
func (w *Worker) offsite(ctx context.Context, src string) string {
	if w.OffsiteDir == "" {
		return "offsite not configured"
	}
	if err := os.MkdirAll(w.OffsiteDir, 0o755); err != nil {
		return w.offsiteFail(ctx, fmt.Sprintf("offsite dir unusable (%s): %v", w.OffsiteDir, err))
	}
	dst := filepath.Join(w.OffsiteDir, filepath.Base(src))
	if err := copyFile(src, dst); err != nil {
		return w.offsiteFail(ctx, fmt.Sprintf("offsite copy to %s failed: %v", w.OffsiteDir, err))
	}
	// H8: the offsite copy is the actual disaster-recovery artifact (the one
	// meant to survive the single Mac dying), so it gets the same integrity
	// check as the local copy — a bit-flip introduced by the raw file copy
	// (network drive hiccup, interrupted iCloud sync) must not silently pass
	// as a good offsite generation either.
	if verr := w.verify(ctx, dst); verr != nil {
		os.Remove(dst) //nolint:errcheck
		return w.offsiteFail(ctx, fmt.Sprintf("offsite copy to %s failed integrity check (removed, not rotated): %v", w.OffsiteDir, verr))
	}
	pruned, perr := w.prune(w.OffsiteDir)
	_ = w.St.SetMeta(ctx, MetaLastOffsiteTs, fmt.Sprintf("%d", time.Now().Unix()))
	out := fmt.Sprintf("offsite copied to %s (pruned %d old)", w.OffsiteDir, pruned)
	if perr != nil {
		out += fmt.Sprintf(" (offsite prune error: %v)", perr)
	}
	return out
}

// offsiteFail records the honest dq event and returns the detail fragment.
// It also pages: the offsite copy is the disaster-recovery artifact, so its
// failure is critical even though the local run still succeeds.
func (w *Worker) offsiteFail(ctx context.Context, reason string) string {
	_ = w.St.InsertDQ(ctx, md.DQEvent{
		Ts:     time.Now().Unix(),
		Kind:   "offsite_backup_unavailable",
		Detail: reason,
	})
	w.page(ctx, "SignalDeck offsite backup unavailable", reason+" (local backup kept)")
	return reason + " (local backup kept; dq recorded)"
}

// copyFile copies src to dst atomically (temp + rename in the destination dir,
// so a reader in the offsite location never sees a half-written file). fsync is
// deliberately skipped: iCloud/Finder sync the file on its own, and a torn copy
// is superseded by the next nightly run.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close() //nolint:errcheck
	tmp := dst + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()    //nolint:errcheck
		os.Remove(tmp) //nolint:errcheck
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp) //nolint:errcheck
		return err
	}
	return os.Rename(tmp, dst)
}

// prune deletes all but the newest Keep (default 7) backups in dir.
func (w *Worker) prune(dir string) (int, error) {
	keep := w.Keep
	if keep <= 0 {
		keep = 7
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), "signaldeck-") && strings.HasSuffix(e.Name(), ".db") {
			names = append(names, e.Name())
		}
	}
	// Timestamped names sort chronologically; newest last.
	sort.Strings(names)
	pruned := 0
	for len(names) > keep {
		if err := os.Remove(filepath.Join(dir, names[0])); err != nil {
			return pruned, err
		}
		names = names[1:]
		pruned++
	}
	return pruned, nil
}
