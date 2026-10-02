package store

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
)

// TestDigestTriesSurviveReopen: the member-digest worker's per-channel tries
// and deliveries must outlive a daemon restart, or a restart re-sends a
// delivered read or grants fresh tries past the cap.
func TestDigestTriesSurviveReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "digest.db")
	st, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	ctx := context.Background()
	annID, err := st.CreateVerifiedUser(ctx, "ann", "ann@gmail.com", "x")
	if err != nil {
		t.Fatalf("create ann: %v", err)
	}
	bobID, err := st.CreateVerifiedUser(ctx, "bob", "bob@gmail.com", "x")
	if err != nil {
		t.Fatalf("create bob: %v", err)
	}

	mustOK(t, st.SaveDigestTry(ctx, DigestTry{UserID: annID, Day: "2026-10-01", Channel: "email", Tries: 2, Delivered: false}))
	mustOK(t, st.SaveDigestTry(ctx, DigestTry{UserID: annID, Day: "2026-10-02", Channel: "email", Tries: 1, Delivered: false}))
	mustOK(t, st.SaveDigestTry(ctx, DigestTry{UserID: annID, Day: "2026-10-02", Channel: "telegram", Tries: 1, Delivered: true}))
	mustOK(t, st.SaveDigestTry(ctx, DigestTry{UserID: bobID, Day: "2026-10-02", Channel: "email", Tries: 1, Delivered: false}))

	mustOK(t, st.SaveDigestTry(ctx, DigestTry{UserID: bobID, Day: "2026-10-02", Channel: "email", Tries: 2, Delivered: false}))

	if err := st.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	st2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { mustOK(t, st2.Close()) })

	wantDay2 := []DigestTry{
		{UserID: annID, Day: "2026-10-02", Channel: "email", Tries: 1, Delivered: false},
		{UserID: annID, Day: "2026-10-02", Channel: "telegram", Tries: 1, Delivered: true},
		{UserID: bobID, Day: "2026-10-02", Channel: "email", Tries: 2, Delivered: false},
	}
	gotDay2, err := st2.DigestTries(ctx, "2026-10-02")
	if err != nil {
		t.Fatalf("DigestTries 2026-10-02: %v", err)
	}
	if !reflect.DeepEqual(gotDay2, wantDay2) {
		t.Fatalf("DigestTries 2026-10-02 mismatch:\n got: %+v\nwant: %+v", gotDay2, wantDay2)
	}

	wantDay1 := []DigestTry{{UserID: annID, Day: "2026-10-01", Channel: "email", Tries: 2, Delivered: false}}
	gotDay1, err := st2.DigestTries(ctx, "2026-10-01")
	if err != nil {
		t.Fatalf("DigestTries 2026-10-01: %v", err)
	}
	if !reflect.DeepEqual(gotDay1, wantDay1) {
		t.Fatalf("DigestTries 2026-10-01 mismatch:\n got: %+v\nwant: %+v", gotDay1, wantDay1)
	}

	if err := st2.PruneDigestTries(ctx, "2026-10-02"); err != nil {
		t.Fatalf("PruneDigestTries: %v", err)
	}

	if got, err := st2.DigestTries(ctx, "2026-10-01"); err != nil {
		t.Fatalf("DigestTries 2026-10-01 after prune: %v", err)
	} else if len(got) != 0 {
		t.Fatalf("DigestTries 2026-10-01 after prune: want 0 rows, got %v", got)
	}

	if got, err := st2.DigestTries(ctx, "2026-10-02"); err != nil {
		t.Fatalf("DigestTries 2026-10-02 after prune: %v", err)
	} else if !reflect.DeepEqual(got, wantDay2) {
		t.Fatalf("DigestTries 2026-10-02 after prune mismatch:\n got: %+v\nwant: %+v", got, wantDay2)
	}
}

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
