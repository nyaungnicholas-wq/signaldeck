package backup

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// The 2026-08-01 backup exited clean from VACUUM INTO and PASSED PRAGMA
// quick_check, yet carried 14 prediction_ledger rows against a live 261,164
// with no anchors at all. Structure was perfect and the accountability record
// was gone. quick_check cannot see that — it checks pages and btrees, not
// whether the rows that make the system auditable are still in the file.
//
// tools/verify_backup.py has caught this since, but it runs only from
// ops/signaldeck-backup-offline.sh, which is macOS-only and last ran
// 2026-07-29. On this Windows host the in-daemon worker is the ONLY backup
// path, so until these checks exist in Go they do not run at all.
//
// These tests are the check for that: each builds a real SQLite file and
// asserts verifyContent's verdict on it.

// writeLedgerDB builds a backup-shaped SQLite file. ledgerRows < 0 omits the
// prediction_ledger table entirely; anchorCount < 0 omits ledger_anchors.
func writeLedgerDB(t *testing.T, name string, ledgerRows, anchorCount int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("open %s: %v", name, err)
	}
	defer db.Close() //nolint:errcheck

	if ledgerRows >= 0 {
		if _, err := db.Exec(`CREATE TABLE prediction_ledger (id INTEGER PRIMARY KEY)`); err != nil {
			t.Fatalf("create prediction_ledger: %v", err)
		}
		for i := 0; i < ledgerRows; i++ {
			if _, err := db.Exec(`INSERT INTO prediction_ledger DEFAULT VALUES`); err != nil {
				t.Fatalf("insert ledger row: %v", err)
			}
		}
	}
	if anchorCount >= 0 {
		if _, err := db.Exec(`CREATE TABLE ledger_anchors (seq INTEGER PRIMARY KEY, ledger_count INTEGER)`); err != nil {
			t.Fatalf("create ledger_anchors: %v", err)
		}
		if _, err := db.Exec(`INSERT INTO ledger_anchors (seq, ledger_count) VALUES (1, ?)`, anchorCount); err != nil {
			t.Fatalf("insert anchor: %v", err)
		}
	}
	return path
}

func TestVerifyContentAcceptsAHealthyBackup(t *testing.T) {
	path := writeLedgerDB(t, "good.db", 100, 100)
	if err := verifyContent(context.Background(), path, 100); err != nil {
		t.Fatalf("healthy backup rejected: %v", err)
	}
}

// The exact 2026-08-01 shape: structurally perfect, accountability record gone.
func TestVerifyContentRejectsTheAugust1Shape(t *testing.T) {
	path := writeLedgerDB(t, "aug1.db", 14, -1) // 14 rows, no anchor table
	err := verifyContent(context.Background(), path, 261164)
	if err == nil {
		t.Fatal("the 2026-08-01 backup shape (14 rows vs live 261,164, no anchors) was accepted")
	}
	// It must fail for the reasons that are actually wrong, not incidentally.
	for _, want := range []string{"anchor", "stale"} {
		if !strings.Contains(strings.ToLower(err.Error()), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestVerifyContentRejectsMissingOrEmptyLedger(t *testing.T) {
	for _, tc := range []struct {
		name       string
		ledgerRows int
	}{
		{"table absent", -1},
		{"table empty", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := writeLedgerDB(t, "bad.db", tc.ledgerRows, 0)
			if err := verifyContent(context.Background(), path, 100); err == nil {
				t.Fatalf("backup with prediction_ledger %s was accepted", tc.name)
			}
		})
	}
}

// A copy that lost rows relative to its own anchor is unusable as an audit
// record even if it is a large file.
func TestVerifyContentRejectsLedgerBehindItsAnchor(t *testing.T) {
	path := writeLedgerDB(t, "behind.db", 50, 400)
	err := verifyContent(context.Background(), path, 50)
	if err == nil {
		t.Fatal("ledger of 50 behind an anchored 400 was accepted")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "anchor") {
		t.Errorf("error %q does not name the anchor mismatch", err)
	}
}

// Staleness is judged against the LIVE database, which is the only thing that
// knows how many rows there are supposed to be.
func TestVerifyContentRejectsAStaleCopyAndAcceptsANearCurrentOne(t *testing.T) {
	stale := writeLedgerDB(t, "stale.db", 100, 100)
	if err := verifyContent(context.Background(), stale, 1000); err == nil {
		t.Fatal("backup holding 10% of the live ledger was accepted")
	}
	fresh := writeLedgerDB(t, "fresh.db", 900, 900)
	if err := verifyContent(context.Background(), fresh, 1000); err != nil {
		t.Fatalf("backup holding 90%% of the live ledger was rejected: %v", err)
	}
}

// liveLedgerCount <= 0 means the live count could not be read. That is not
// evidence the backup is stale, so the staleness comparison must be skipped
// rather than failing a good backup on a missing input.
func TestVerifyContentSkipsStalenessWhenLiveCountUnknown(t *testing.T) {
	path := writeLedgerDB(t, "unknownlive.db", 10, 10)
	if err := verifyContent(context.Background(), path, 0); err != nil {
		t.Fatalf("unknown live count must not fail a structurally sound backup: %v", err)
	}
}

func TestVerifyContentRejectsAnUnopenableFile(t *testing.T) {
	if err := verifyContent(context.Background(), filepath.Join(t.TempDir(), "nope.db"), 10); err == nil {
		t.Fatal("a backup file that does not exist was accepted")
	}
}

// Every problem should be reported in one verdict, so an operator fixes the
// backup once instead of discovering faults one run at a time.
func TestVerifyContentReportsEveryProblemAtOnce(t *testing.T) {
	path := writeLedgerDB(t, "multi.db", 0, -1) // empty ledger AND no anchors
	err := verifyContent(context.Background(), path, 5000)
	if err == nil {
		t.Fatal("a backup with multiple faults was accepted")
	}
	msg := strings.ToLower(err.Error())
	if !strings.Contains(msg, "prediction_ledger") || !strings.Contains(msg, "anchor") {
		t.Errorf("verdict %q does not report both faults", err)
	}
}
