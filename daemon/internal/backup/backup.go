// Package backup is the nightly SQLite backup agent. It runs VACUUM INTO —
// SQLite's safe online-backup path (a consistent, compacted copy taken while
// the daemon keeps reading and writing under WAL) — into a timestamped file
// under the backup directory, and prunes old copies.
package backup

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// Worker is the periodic backup agent (implements workers.Worker).
type Worker struct {
	St   *store.Store
	Dir  string        // backup directory (required)
	Keep int           // rotated copies to keep (default 7)
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
	pruned, perr := w.prune()
	detail := fmt.Sprintf("backup %s (%.1f MB), pruned %d old", filepath.Base(target), float64(st.Size())/(1024*1024), pruned)
	if perr != nil {
		detail += fmt.Sprintf(" (prune error: %v)", perr)
	}
	return detail, nil
}

// prune deletes all but the newest Keep (default 7) backups.
func (w *Worker) prune() (int, error) {
	keep := w.Keep
	if keep <= 0 {
		keep = 7
	}
	entries, err := os.ReadDir(w.Dir)
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
		if err := os.Remove(filepath.Join(w.Dir, names[0])); err != nil {
			return pruned, err
		}
		names = names[1:]
		pruned++
	}
	return pruned, nil
}
