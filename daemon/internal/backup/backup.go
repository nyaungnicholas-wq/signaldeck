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
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
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

	ran bool
}

// Name implements workers.Worker.
func (w *Worker) Name() string { return "db-backup" }

// Interval implements workers.Worker: nightly.
func (w *Worker) Interval() time.Duration { return 24 * time.Hour }

// Run takes one backup and prunes old ones.
func (w *Worker) Run(ctx context.Context) (string, error) {
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
		return "", fmt.Errorf("vacuum into %s: %w", target, err)
	}
	st, err := os.Stat(target)
	if err != nil {
		return "", fmt.Errorf("backup written but unstattable: %w", err)
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
	pruned, perr := w.prune(w.OffsiteDir)
	_ = w.St.SetMeta(ctx, MetaLastOffsiteTs, fmt.Sprintf("%d", time.Now().Unix()))
	out := fmt.Sprintf("offsite copied to %s (pruned %d old)", w.OffsiteDir, pruned)
	if perr != nil {
		out += fmt.Sprintf(" (offsite prune error: %v)", perr)
	}
	return out
}

// offsiteFail records the honest dq event and returns the detail fragment.
func (w *Worker) offsiteFail(ctx context.Context, reason string) string {
	_ = w.St.InsertDQ(ctx, md.DQEvent{
		Ts:     time.Now().Unix(),
		Kind:   "offsite_backup_unavailable",
		Detail: reason,
	})
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
		out.Close()      //nolint:errcheck
		os.Remove(tmp)   //nolint:errcheck
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
