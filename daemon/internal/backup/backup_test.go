package backup

import (
	"context"
	"fmt"
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

// TestRestartGateSkipsFreshBackup: a Run while the backup_last_ts cursor is
// younger than MinGap must skip (a daemon restart is not a new night), and an
// aged cursor must back up again.
func TestRestartGateSkipsFreshBackup(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "src.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close() //nolint:errcheck
	ctx := context.Background()
	bdir := filepath.Join(dir, "backups")
	w := &Worker{St: st, Dir: bdir, Keep: 7}
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	// Simulated restart: the fleet fires Run again right away.
	detail, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("gated Run: %v", err)
	}
	if !strings.Contains(detail, "skipped") {
		t.Errorf("detail = %q; want restart-gate skip", detail)
	}
	if entries, err := os.ReadDir(bdir); err != nil || len(entries) != 1 {
		t.Fatalf("restart must not add a backup: got %d (err %v)", len(entries), err)
	}
	// Age the cursor past the gate → the nightly run proceeds again.
	if err := st.SetMeta(ctx, MetaLastBackupTs, fmt.Sprintf("%d", time.Now().Add(-31*time.Hour).Unix())); err != nil {
		t.Fatal(err)
	}
	detail, err = w.Run(ctx)
	if err != nil {
		t.Fatalf("aged Run: %v", err)
	}
	if !strings.Contains(detail, "backup signaldeck-") {
		t.Errorf("detail = %q; want a fresh backup past the gate", detail)
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

// TestVerifyBackupDetectsCorruption is the H8 regression proof for the real
// (non-injected) check: before this existed, success was `os.Stat(target)`
// returning without error, which a corrupt-but-nonzero file always satisfies.
// This flips bytes deep inside a real VACUUM INTO'd file (well past the
// 100-byte header, into btree page data) and requires verifyBackup to catch
// it, alongside a clean pass on the unmodified file.
func TestVerifyBackupDetectsCorruption(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "src.db"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	// Pad the source so VACUUM INTO produces more than a bare 2-page file —
	// otherwise the corrupted range below could land past EOF.
	for i := 0; i < 200; i++ {
		if err := st.SetMeta(ctx, fmt.Sprintf("pad%d", i), strings.Repeat("x", 64)); err != nil {
			t.Fatal(err)
		}
	}
	target := filepath.Join(dir, "backup.db")
	if _, err := st.DB().ExecContext(ctx, `VACUUM INTO ?`, target); err != nil {
		t.Fatal(err)
	}
	st.Close() //nolint:errcheck

	if err := verifyBackup(ctx, target); err != nil {
		t.Fatalf("unmodified backup should verify clean: %v", err)
	}

	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) < 8192 {
		t.Fatalf("test fixture too small to corrupt safely: %d bytes", len(data))
	}
	corrupt := append([]byte(nil), data...)
	for i := 4096; i < 8192; i++ {
		corrupt[i] ^= 0xFF
	}
	corruptPath := filepath.Join(dir, "corrupt.db")
	if err := os.WriteFile(corruptPath, corrupt, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := verifyBackup(ctx, corruptPath); err == nil {
		t.Fatal("corrupted backup must fail verify, got nil error")
	}
}

// TestCorruptLocalBackupRefusesRotation is the H8 regression proof for the
// wiring: a backup that fails its integrity check must not overwrite the
// last-known-good generation, must not advance backup_last_ts, and must
// raise a dq event — so a corrupt-but-nonzero copy can never rotate a good
// one out from under KEEP=7. VerifyBackup is injected here (rather than
// corrupting a real multi-hundred-KB file every test run) because the wiring
// under test is "what Run() does when verify fails", not the check itself —
// that is TestVerifyBackupDetectsCorruption's job.
func TestCorruptLocalBackupRefusesRotation(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "src.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close() //nolint:errcheck
	ctx := context.Background()

	bdir := filepath.Join(dir, "backups")
	w := &Worker{St: st, Dir: bdir, Keep: 7}
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("first (good) Run: %v", err)
	}
	goodTs, err := st.GetMeta(ctx, MetaLastBackupTs)
	if err != nil || goodTs == "" {
		t.Fatalf("expected backup_last_ts after a good run: %q, %v", goodTs, err)
	}

	// Age the restart gate so the second Run actually attempts a backup
	// instead of skipping (mirrors TestRestartGateSkipsFreshBackup).
	if err := st.SetMeta(ctx, MetaLastBackupTs, fmt.Sprintf("%d", time.Now().Add(-31*time.Hour).Unix())); err != nil {
		t.Fatal(err)
	}
	// Target filenames are second-granularity (time.Now().Format
	// "20060102-150405"); real backups are a day apart so this never
	// collides in production, but two Run() calls back-to-back in a test can
	// land in the same wall-clock second — and Run()'s own "clear a leftover
	// target" cleanup would then delete the FIRST (good) backup merely
	// because its filename matches, before verify ever runs. Sleep past the
	// second boundary so this test exercises the intended failure path.
	time.Sleep(1100 * time.Millisecond)
	w.VerifyBackup = func(ctx context.Context, path string) error {
		return fmt.Errorf("simulated corruption")
	}
	if _, err := w.Run(ctx); err == nil {
		t.Fatal("Run must fail when the fresh backup fails its integrity check")
	}

	// The corrupt copy must not be left on disk, and the earlier good
	// generation must be the ONLY file present — nothing rotated it away.
	entries, err := os.ReadDir(bdir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("want exactly 1 (the earlier good) backup file, got %d (err %v)", len(entries), err)
	}

	// backup_last_ts must still be the aged cursor from the good run, not
	// advanced by the failed one.
	if got, _ := st.GetMeta(ctx, MetaLastBackupTs); got == goodTs {
		t.Errorf("backup_last_ts should be the aged test cursor, not the original %q — got %q", goodTs, got)
	}

	events, err := st.RecentDQ(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range events {
		if e.Kind == "backup_corrupt" {
			found = true
		}
	}
	if !found {
		t.Errorf("want a backup_corrupt dq event, got %+v", events)
	}
}

// TestCorruptOffsiteRefusesRotation mirrors the local case for the offsite
// copy: a failed integrity check on the OFFSITE artifact — the one actually
// meant to survive the Mac dying — must not rotate away the previous good
// offsite generation either, and must still leave the local backup intact
// (offsite failures are always best-effort w.r.t. the fleet run).
func TestCorruptOffsiteRefusesRotation(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "src.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close() //nolint:errcheck
	ctx := context.Background()

	local := filepath.Join(dir, "backups")
	offsite := filepath.Join(dir, "offsite")
	w := &Worker{St: st, Dir: local, OffsiteDir: offsite, Keep: 7}
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("first (good) Run: %v", err)
	}
	entries, err := os.ReadDir(offsite)
	if err != nil || len(entries) != 1 {
		t.Fatalf("want 1 good offsite backup after the first run, got %d (err %v)", len(entries), err)
	}
	goodOffsiteName := entries[0].Name()

	if err := st.SetMeta(ctx, MetaLastBackupTs, fmt.Sprintf("%d", time.Now().Add(-31*time.Hour).Unix())); err != nil {
		t.Fatal(err)
	}
	// See TestCorruptLocalBackupRefusesRotation: sleep past the second
	// boundary so the second Run()'s target filename doesn't collide with
	// (and delete) the first good local backup before verify ever runs.
	time.Sleep(1100 * time.Millisecond)
	// Fail verify ONLY for paths under the offsite dir, so the local copy
	// (and its own verify) still succeeds — isolating the offsite failure
	// path.
	w.VerifyBackup = func(ctx context.Context, path string) error {
		if strings.HasPrefix(path, offsite) {
			return fmt.Errorf("simulated offsite corruption")
		}
		return nil
	}
	detail, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("Run must not fail the fleet on an offsite integrity failure: %v", err)
	}
	if !strings.Contains(detail, "failed integrity check") {
		t.Errorf("detail = %q; want an honest offsite-integrity-failure fragment", detail)
	}

	// The local backup must have rotated normally (2 generations now).
	if entries, err := os.ReadDir(local); err != nil || len(entries) != 2 {
		t.Fatalf("local backups: got %d, want 2 (err %v)", len(entries), err)
	}
	// The offsite dir must still hold ONLY the earlier good generation — the
	// corrupt second copy must not have been left behind or rotated in.
	entries, err = os.ReadDir(offsite)
	if err != nil || len(entries) != 1 {
		t.Fatalf("want exactly 1 (the earlier good) offsite backup, got %d (err %v)", len(entries), err)
	}
	if entries[0].Name() != goodOffsiteName {
		t.Errorf("offsite backup = %q, want the earlier good generation %q", entries[0].Name(), goodOffsiteName)
	}

	events, err := st.RecentDQ(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range events {
		if e.Kind == "offsite_backup_unavailable" && strings.Contains(e.Detail, "integrity check") {
			found = true
		}
	}
	if !found {
		t.Errorf("want an offsite_backup_unavailable dq event citing the integrity failure, got %+v", events)
	}
}
