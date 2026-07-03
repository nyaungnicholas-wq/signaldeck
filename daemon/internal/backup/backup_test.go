package backup

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// TestBackupSmoke opens a throwaway store in a temp dir, runs the worker
// directly, and verifies a valid backup file lands in the backup dir.
func TestBackupSmoke(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "src.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close() //nolint:errcheck
	ctx := context.Background()
	if err := st.SetMeta(ctx, "backup_test", "hello"); err != nil {
		t.Fatal(err)
	}

	bdir := filepath.Join(dir, "backups")
	w := &Worker{St: st, Dir: bdir, Keep: 7}
	detail, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(detail, "backup signaldeck-") {
		t.Errorf("detail = %q", detail)
	}
	entries, err := os.ReadDir(bdir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("want 1 backup file, got %d (err %v)", len(entries), err)
	}
	// The copy must be an openable SQLite db containing our row.
	bst, err := store.Open(filepath.Join(bdir, entries[0].Name()))
	if err != nil {
		t.Fatalf("open backup: %v", err)
	}
	defer bst.Close() //nolint:errcheck
	if v, err := bst.GetMeta(ctx, "backup_test"); err != nil || v != "hello" {
		t.Errorf("backup meta = %q, %v; want hello", v, err)
	}
}

func TestPruneKeepsNewestSeven(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{
		"signaldeck-20260601-000000.db", "signaldeck-20260602-000000.db",
		"signaldeck-20260603-000000.db", "signaldeck-20260604-000000.db",
		"signaldeck-20260605-000000.db", "signaldeck-20260606-000000.db",
		"signaldeck-20260607-000000.db", "signaldeck-20260608-000000.db",
		"signaldeck-20260609-000000.db",
		"unrelated.txt", // must be ignored
	} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	w := &Worker{Dir: dir, Keep: 7}
	pruned, err := w.prune()
	if err != nil {
		t.Fatal(err)
	}
	if pruned != 2 {
		t.Errorf("pruned = %d, want 2", pruned)
	}
	// Oldest two gone, newest seven + unrelated file remain.
	for _, gone := range []string{"signaldeck-20260601-000000.db", "signaldeck-20260602-000000.db"} {
		if _, err := os.Stat(filepath.Join(dir, gone)); err == nil {
			t.Errorf("%s should have been pruned", gone)
		}
	}
	for _, keep := range []string{"signaldeck-20260603-000000.db", "signaldeck-20260609-000000.db", "unrelated.txt"} {
		if _, err := os.Stat(filepath.Join(dir, keep)); err != nil {
			t.Errorf("%s should remain: %v", keep, err)
		}
	}
}

func TestFirstRunDelayHonorsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w := &Worker{Dir: t.TempDir(), FirstRunDelay: time.Hour}
	if _, err := w.Run(ctx); err == nil {
		t.Error("want ctx error from delayed first run")
	}
}
