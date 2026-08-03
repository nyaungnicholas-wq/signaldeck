package backup

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// verifyContent checks that a freshly-written backup still CONTAINS the
// accountability record, which PRAGMA quick_check cannot see.
//
// quick_check verifies pages and btrees — structure. On 2026-08-01 a backup
// exited clean from VACUUM INTO and passed quick_check while carrying 14
// prediction_ledger rows against a live 261,164, with no anchors at all: a
// structurally perfect file with the audit trail gone. A restore from it would
// have silently lost the entire accountability record.
//
// tools/verify_backup.py has caught that shape since, but it runs only from
// ops/signaldeck-backup-offline.sh — macOS-only, and last run 2026-07-29. On a
// Windows host this worker is the ONLY backup path, so the check has to live
// here or it does not run at all.
//
// Every problem is collected and reported in one verdict rather than returning
// at the first fault, so an operator repairs the backup once instead of
// discovering the next fault on the next nightly run.
func verifyContent(ctx context.Context, backupPath string, liveLedgerCount int64) error {
	db, err := sql.Open("sqlite", "file:"+backupPath+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return fmt.Errorf("open backup for content verify: %w", err)
	}
	defer db.Close() //nolint:errcheck

	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("open backup for content verify: %w", err)
	}

	var problems []string
	var ledgerCount int64

	// Check prediction_ledger
	var exists int
	if err := db.QueryRowContext(ctx, "SELECT 1 FROM sqlite_master WHERE type='table' AND name='prediction_ledger'").Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			problems = append(problems, "table prediction_ledger does not exist")
		} else {
			problems = append(problems, fmt.Sprintf("prediction_ledger check: %v", err))
		}
	} else {
		if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM prediction_ledger").Scan(&ledgerCount); err != nil {
			problems = append(problems, fmt.Sprintf("prediction_ledger count: %v", err))
		} else if ledgerCount < 1 && liveLedgerCount > 0 {
			// Empty is only a fault RELATIVE TO THE LIVE DATABASE. A system
			// that has genuinely made no predictions yet has an empty ledger,
			// and refusing to back it up would be a false alarm on a correct
			// backup. When live has rows and the copy has none, that is the
			// 2026-08-01 failure in its most extreme form.
			problems = append(problems, fmt.Sprintf("table prediction_ledger is empty while live holds %d rows", liveLedgerCount))
		}
	}

	// Check ledger_anchors
	var anchoredCount int64
	if err := db.QueryRowContext(ctx, "SELECT 1 FROM sqlite_master WHERE type='table' AND name='ledger_anchors'").Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			problems = append(problems, "missing ledger_anchors table (anchor)")
		} else {
			problems = append(problems, fmt.Sprintf("ledger_anchors check: %v", err))
		}
	} else {
		if err := db.QueryRowContext(ctx, "SELECT ledger_count FROM ledger_anchors ORDER BY seq DESC LIMIT 1").Scan(&anchoredCount); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				// Same relative test: no predictions means no anchors, which is
				// correct rather than broken. Anchors missing while the live
				// ledger holds rows is the fault.
				if liveLedgerCount > 0 {
					problems = append(problems, "ledger_anchors has no rows (anchor)")
				}
			} else {
				problems = append(problems, fmt.Sprintf("ledger_anchors query: %v", err))
			}
		} else if ledgerCount < anchoredCount {
			problems = append(problems, fmt.Sprintf("prediction_ledger count %d is behind anchored count %d (anchor)", ledgerCount, anchoredCount))
		}
	}

	// Staleness vs live
	if liveLedgerCount > 0 {
		if float64(ledgerCount) < float64(liveLedgerCount)*0.5 {
			problems = append(problems, fmt.Sprintf("backup prediction_ledger count %d is stale against live count %d", ledgerCount, liveLedgerCount))
		}
	}

	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("backup content verification failed: %s", strings.Join(problems, "; "))
}