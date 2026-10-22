package maintain

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/archive"
)

func TestWaitlistRetentionReportOnlyDeletesNothing(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	now := time.Now()

	oldTs := now.Add(-400 * 24 * time.Hour)
	if _, err := st.AddWaitlist(ctx, "old@example.com", "web", oldTs); err != nil {
		t.Fatalf("add old: %v", err)
	}

	newTs := now.Add(-10 * 24 * time.Hour)
	if _, err := st.AddWaitlist(ctx, "new@example.com", "web", newTs); err != nil {
		t.Fatalf("add new: %v", err)
	}

	w := &DerivedRetention{St: st, Arc: archive.New(t.TempDir()), KeepWaitlist: 365 * 24 * time.Hour}

	t.Setenv("SIGNALDECK_RETENTION_ENFORCE", "")
	detail, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("Run error: %v, detail: %s", err, detail)
	}
	if count(t, st, `SELECT COUNT(*) FROM waitlist`) != 2 {
		t.Errorf("expected 2 rows, got %d, detail: %s", count(t, st, `SELECT COUNT(*) FROM waitlist`), detail)
	}
	if !strings.Contains(detail, "waitlist retention: would remove 1") {
		t.Errorf("detail missing 'would remove 1': %s", detail)
	}
	if strings.Contains(detail, "@") {
		t.Errorf("detail contains email address: %s", detail)
	}

	t.Setenv("SIGNALDECK_RETENTION_ENFORCE", "0")
	detail, err = w.Run(ctx)
	if err != nil {
		t.Fatalf("Run error: %v, detail: %s", err, detail)
	}
	if count(t, st, `SELECT COUNT(*) FROM waitlist`) != 2 {
		t.Errorf("expected 2 rows after enforce=0, got %d, detail: %s", count(t, st, `SELECT COUNT(*) FROM waitlist`), detail)
	}
}

func TestWaitlistRetentionEnforceDeletesOnlyOlderThanCutoff(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	now := time.Now()

	oldTs := now.Add(-400 * 24 * time.Hour)
	if _, err := st.AddWaitlist(ctx, "old@example.com", "web", oldTs); err != nil {
		t.Fatalf("add old: %v", err)
	}

	edgeTs := now.Add(-364 * 24 * time.Hour)
	if _, err := st.AddWaitlist(ctx, "edge@example.com", "web", edgeTs); err != nil {
		t.Fatalf("add edge: %v", err)
	}

	newTs := now.Add(-1 * 24 * time.Hour)
	if _, err := st.AddWaitlist(ctx, "new@example.com", "web", newTs); err != nil {
		t.Fatalf("add new: %v", err)
	}

	w := &DerivedRetention{St: st, Arc: archive.New(t.TempDir()), KeepWaitlist: 365 * 24 * time.Hour}

	t.Setenv("SIGNALDECK_RETENTION_ENFORCE", "1")
	detail, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("Run error: %v, detail: %s", err, detail)
	}
	if !strings.Contains(detail, "waitlist retention: removed 1") {
		t.Errorf("detail missing 'removed 1': %s", detail)
	}
	if count(t, st, `SELECT COUNT(*) FROM waitlist`) != 2 {
		t.Errorf("expected 2 rows, got %d, detail: %s", count(t, st, `SELECT COUNT(*) FROM waitlist`), detail)
	}
	if count(t, st, `SELECT COUNT(*) FROM waitlist WHERE email = ?`, "old@example.com") != 0 {
		t.Errorf("old@example.com should be removed, detail: %s", detail)
	}
	if count(t, st, `SELECT COUNT(*) FROM waitlist WHERE email = ?`, "edge@example.com") != 1 {
		t.Errorf("edge@example.com should remain, detail: %s", detail)
	}
	if count(t, st, `SELECT COUNT(*) FROM waitlist WHERE email = ?`, "new@example.com") != 1 {
		t.Errorf("new@example.com should remain, detail: %s", detail)
	}
	if strings.Contains(detail, "@") {
		t.Errorf("detail contains email address: %s", detail)
	}
}

func TestWaitlistRetentionWindowFromEnv(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	now := time.Now()

	aTs := now.Add(-40 * 24 * time.Hour)
	if _, err := st.AddWaitlist(ctx, "a@example.com", "web", aTs); err != nil {
		t.Fatalf("add a: %v", err)
	}

	bTs := now.Add(-20 * 24 * time.Hour)
	if _, err := st.AddWaitlist(ctx, "b@example.com", "web", bTs); err != nil {
		t.Fatalf("add b: %v", err)
	}

	w := &DerivedRetention{St: st, Arc: archive.New(t.TempDir())}

	t.Setenv("SIGNALDECK_WAITLIST_RETENTION_D", "30")
	t.Setenv("SIGNALDECK_RETENTION_ENFORCE", "1")
	detail, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("Run error: %v, detail: %s", err, detail)
	}
	if !strings.Contains(detail, "waitlist retention: removed 1") {
		t.Errorf("detail missing 'removed 1': %s", detail)
	}
	if count(t, st, `SELECT COUNT(*) FROM waitlist`) != 1 {
		t.Errorf("expected 1 row, got %d, detail: %s", count(t, st, `SELECT COUNT(*) FROM waitlist`), detail)
	}
	if count(t, st, `SELECT COUNT(*) FROM waitlist WHERE email = ?`, "a@example.com") != 0 {
		t.Errorf("a@example.com should be removed, detail: %s", detail)
	}
	if count(t, st, `SELECT COUNT(*) FROM waitlist WHERE email = ?`, "b@example.com") != 1 {
		t.Errorf("b@example.com should remain, detail: %s", detail)
	}
	if strings.Contains(detail, "@") {
		t.Errorf("detail contains email address: %s", detail)
	}
}

// The waitlist step must not depend on the archive tiers: SymbolNameMap failing
// used to return before it ran, so expired sign-ups silently stayed.
func TestWaitlistRetentionRunsWhenArchiveTiersCannot(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	if _, err := st.AddWaitlist(ctx, "old@example.com", "web", time.Now().Add(-400*24*time.Hour)); err != nil {
		t.Fatalf("add old: %v", err)
	}
	if _, err := st.DB().ExecContext(ctx, `ALTER TABLE symbols RENAME TO symbols_gone`); err != nil {
		t.Fatalf("break SymbolNameMap: %v", err)
	}
	w := &DerivedRetention{St: st, Arc: archive.New(t.TempDir()), KeepWaitlist: 365 * 24 * time.Hour}
	t.Setenv("SIGNALDECK_RETENTION_ENFORCE", "1")
	detail, err := w.Run(ctx)
	if err == nil {
		t.Fatalf("expected the SymbolNameMap error, got nil; detail: %s", detail)
	}
	if n := count(t, st, `SELECT COUNT(*) FROM waitlist`); n != 0 {
		t.Errorf("waitlist step skipped: %d expired row(s) left; detail: %s", n, detail)
	}
	if !strings.Contains(detail, "waitlist retention: removed 1") {
		t.Errorf("detail missing 'removed 1': %q", detail)
	}
}
