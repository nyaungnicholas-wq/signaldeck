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
	pruned, err := w.prune(dir)
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

// TestOffsiteCopyLandsAndPrunes runs a real backup with an offsite dir set and
// verifies the freshest backup is copied off-machine, the offsite meta cursor is
// recorded, and both locations prune to Keep.
func TestOffsiteCopyLands(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "src.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close() //nolint:errcheck
	ctx := context.Background()
	if err := st.SetMeta(ctx, "k", "v"); err != nil {
		t.Fatal(err)
	}
	local := filepath.Join(dir, "backups")
	offsite := filepath.Join(dir, "offsite")
	w := &Worker{St: st, Dir: local, OffsiteDir: offsite, Keep: 7}
	detail, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(detail, "offsite copied to") {
		t.Errorf("detail = %q; want offsite success", detail)
	}
	// The offsite dir must hold exactly the one backup, and it must be openable.
	entries, err := os.ReadDir(offsite)
	if err != nil || len(entries) != 1 {
		t.Fatalf("want 1 offsite backup, got %d (err %v)", len(entries), err)
	}
	bst, err := store.Open(filepath.Join(offsite, entries[0].Name()))
	if err != nil {
		t.Fatalf("open offsite backup: %v", err)
	}
	defer bst.Close() //nolint:errcheck
	if v, _ := bst.GetMeta(ctx, "k"); v != "v" {
		t.Errorf("offsite copy = %q; want v", v)
	}
	if ts, _ := st.GetMeta(ctx, MetaLastOffsiteTs); ts == "" {
		t.Error("last offsite meta not recorded")
	}
}

// TestOffsiteUnavailableStillBacksUpLocal points the offsite dir at an
// unwritable path: the local backup must still succeed, a dq event must be
// recorded, and the run must NOT fail the fleet.
func TestOffsiteUnavailableStillBacksUpLocal(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "src.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close() //nolint:errcheck
	ctx := context.Background()

	// A file (not a dir) where the offsite dir should be — MkdirAll fails.
	blocker := filepath.Join(dir, "blocked")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	local := filepath.Join(dir, "backups")
	w := &Worker{St: st, Dir: local, OffsiteDir: filepath.Join(blocker, "sub"), Keep: 7}
	detail, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("Run must not fail the fleet on offsite trouble: %v", err)
	}
	if !strings.Contains(detail, "local backup kept") {
		t.Errorf("detail = %q; want honest offsite-failure fragment", detail)
	}
	// Local backup still landed.
	if entries, err := os.ReadDir(local); err != nil || len(entries) != 1 {
		t.Fatalf("local backup missing: %d (err %v)", len(entries), err)
	}
	// A dq event records why.
	events, err := st.RecentDQ(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range events {
		if e.Kind == "offsite_backup_unavailable" {
			found = true
		}
	}
	if !found {
		t.Errorf("want an offsite_backup_unavailable dq event, got %+v", events)
	}
}

// TestOffsiteNotConfigured: an empty offsite dir is a deliberate opt-out — local
// backup runs, the detail says so, and NO dq event is recorded.
func TestOffsiteNotConfigured(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "src.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close() //nolint:errcheck
	ctx := context.Background()
	w := &Worker{St: st, Dir: filepath.Join(dir, "backups"), Keep: 7}
	detail, err := w.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(detail, "offsite not configured") {
		t.Errorf("detail = %q; want offsite-not-configured", detail)
	}
	events, _ := st.RecentDQ(ctx, 10)
	for _, e := range events {
		if e.Kind == "offsite_backup_unavailable" {
			t.Error("no dq event should be recorded when offsite is a deliberate opt-out")
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
