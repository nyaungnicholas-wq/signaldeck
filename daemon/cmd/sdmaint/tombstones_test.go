package main

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
	"github.com/nyaungnicholas-wq/signaldeck/internal/tombstone"
)

// TestReplayDeletesOnlyWhenIDAndEmailBothMatch: users.id is reused, so a
// tombstone for an id that now belongs to a different address must leave that
// account alone, while the restored account it names is erased; a dry run
// erases nothing.
func TestReplayDeletesOnlyWhenIDAndEmailBothMatch(t *testing.T) {
	dir := t.TempDir()
	db, logp := filepath.Join(dir, "restored.db"), filepath.Join(dir, "deletions.jsonl")
	t.Setenv("SIGNALDECK_TOMBSTONE_LOG", logp)
	ctx := context.Background()
	st, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	alice, err := st.CreateUserWithEmail(ctx, "alice", "Alice@Example.com", "x")
	if err != nil {
		t.Fatal(err)
	}
	bob, err := st.CreateUserWithEmail(ctx, "bob", "bob@example.com", "x")
	if err != nil {
		t.Fatal(err)
	}
	st.Close() //nolint:errcheck
	// alice deleted her account after this backup was taken; bob's id once
	// belonged to carol, who deleted hers before bob signed up.
	if err := tombstone.Append(alice, "alice@example.com"); err != nil {
		t.Fatal(err)
	}
	if err := tombstone.Append(bob, "carol@example.com"); err != nil {
		t.Fatal(err)
	}

	present := func() (a, b bool) {
		t.Helper()
		st, err := store.Open(db)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close() //nolint:errcheck
		_, a, _ = st.GetUserByName(ctx, "alice")
		_, b, _ = st.GetUserByName(ctx, "bob")
		return a, b
	}
	if err := replayTombstones([]string{"-db", db, "-log", logp}); err != nil {
		t.Fatal(err)
	}
	if a, b := present(); !a || !b {
		t.Fatalf("a dry run deleted accounts: alice=%v bob=%v", a, b)
	}
	if err := replayTombstones([]string{"-db", db, "-log", logp, "-apply"}); err != nil {
		t.Fatal(err)
	}
	a, b := present()
	if a {
		t.Error("the tombstoned account survived the replay")
	}
	if !b {
		t.Error("the replay deleted a different account that reuses the id")
	}
}
