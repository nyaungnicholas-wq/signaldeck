package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestAddWaitlist_EmptyEmailIsRefused guards the primary key against an upstream validation bug.
func TestAddWaitlist_EmptyEmailIsRefused(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	now := time.Unix(1_700_000_000, 0).UTC()

	added, err := st.AddWaitlist(ctx, "", "web", now)
	if added {
		t.Fatalf("got added = true, want false")
	}
	if !errors.Is(err, ErrEmptyEmail) {
		t.Fatalf("got err = %v, want errors.Is(err, ErrEmptyEmail)", err)
	}

	count, err := st.CountWaitlist(ctx)
	if err != nil {
		t.Fatalf("CountWaitlist failed: %v", err)
	}
	if count != 0 {
		t.Fatalf("got count = %v, want 0", count)
	}
}

// TestAddWaitlist_IsIdempotent ensures INSERT OR IGNORE does not overwrite the original signup time with a later re-submit.
func TestAddWaitlist_IsIdempotent(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	email := "test@example.com"
	now1 := time.Unix(1_700_000_000, 0).UTC()
	now2 := time.Unix(1_700_000_001, 0).UTC() // Different time
	source1 := "web"
	source2 := "api" // Different source

	// First call
	added, err := st.AddWaitlist(ctx, email, source1, now1)
	if err != nil {
		t.Fatalf("AddWaitlist (first call) failed: %v", err)
	}
	if !added {
		t.Fatalf("AddWaitlist (first call): got added = false, want true")
	}

	// Second call with same email, different source and time
	added, err = st.AddWaitlist(ctx, email, source2, now2)
	if err != nil {
		t.Fatalf("AddWaitlist (second call) failed: %v", err)
	}
	if added {
		t.Fatalf("AddWaitlist (second call): got added = true, want false")
	}

	// Check count
	count, err := st.CountWaitlist(ctx)
	if err != nil {
		t.Fatalf("CountWaitlist failed: %v", err)
	}
	if count != 1 {
		t.Fatalf("got count = %v, want 1", count)
	}

	// Query the row directly to ensure original data is preserved
	// created_ts is an INTEGER column holding unix seconds. Scanned as int64,
	// not time.Time: the driver WILL convert an integer into a time.Time, so a
	// time.Time assertion here would be testing that conversion rather than the
	// value AddWaitlist actually stored.
	var createdTs int64
	var storedSource string
	row := st.db.QueryRowContext(ctx, `SELECT created_ts, source FROM waitlist WHERE email=?`, email)
	if err := row.Scan(&createdTs, &storedSource); err != nil {
		t.Fatalf("QueryRowContext failed: %v", err)
	}

	if createdTs != now1.Unix() {
		t.Fatalf("got created_ts = %v, want %v", createdTs, now1.Unix())
	}
	if storedSource != source1 {
		t.Fatalf("got source = %v, want %v", storedSource, source1)
	}
}

// TestAddWaitlist_DistinctAddressesBothLand ensures that multiple unique signups are correctly recorded.
func TestAddWaitlist_DistinctAddressesBothLand(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	now := time.Unix(1_700_000_000, 0).UTC()

	email1 := "test1@example.com"
	email2 := "test2@example.com"
	source := "web"

	// Add first email
	added1, err1 := st.AddWaitlist(ctx, email1, source, now)
	if err1 != nil {
		t.Fatalf("AddWaitlist for %s failed: %v", email1, err1)
	}
	if !added1 {
		t.Fatalf("AddWaitlist for %s: got added = false, want true", email1)
	}

	// Add second email
	added2, err2 := st.AddWaitlist(ctx, email2, source, now)
	if err2 != nil {
		t.Fatalf("AddWaitlist for %s failed: %v", email2, err2)
	}
	if !added2 {
		t.Fatalf("AddWaitlist for %s: got added = false, want true", email2)
	}

	// Check count
	count, err := st.CountWaitlist(ctx)
	if err != nil {
		t.Fatalf("CountWaitlist failed: %v", err)
	}
	if count != 2 {
		t.Fatalf("got count = %v, want 2", count)
	}
}

// TestCountWaitlist_EmptyTableIsZeroNotError pins that the operator's only visibility into the list does not need a no-rows special case.
func TestCountWaitlist_EmptyTableIsZeroNotError(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	count, err := st.CountWaitlist(ctx)
	if err != nil {
		t.Fatalf("CountWaitlist failed: %v", err)
	}
	if count != 0 {
		t.Fatalf("got count = %v, want 0", count)
	}
}
