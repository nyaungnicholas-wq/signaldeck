package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
	"github.com/nyaungnicholas-wq/signaldeck/internal/tombstone"
)

// TestReplayDeletesOnlyWhenIDEmailAndUsernameAllMatch: users.id is reused, so a
// tombstone for an id that now belongs to a different account (a different
// address, or the same empty address under another name) must leave that
// account alone, while the restored account it names is erased; a dry run
// erases nothing.
func TestReplayDeletesOnlyWhenIDEmailAndUsernameAllMatch(t *testing.T) {
	dir := t.TempDir()
	db, logp := filepath.Join(dir, "restored.db"), filepath.Join(dir, "deletions.jsonl")
	t.Setenv("SIGNALDECK_TOMBSTONE_LOG", logp)
	ctx := context.Background()
	st, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	mk := func(name, email string) (id int64) {
		t.Helper()
		if email == "" { // the username-only accounts registerPrivate still makes
			id, err = st.CreateUser(ctx, name, "x", false)
		} else {
			id, err = st.CreateUserWithEmail(ctx, name, email, "x")
		}
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	alice, bob, dan, fay := mk("alice", "Alice@Example.com"), mk("bob", "bob@example.com"), mk("dan", ""), mk("fay", "")
	st.Close() //nolint:errcheck
	// alice and fay deleted their accounts after this backup was taken; bob's
	// id once belonged to carol, and dan's to erin (no address either), who
	// deleted theirs before bob and dan signed up.
	for _, ts := range []struct {
		uid         int64
		name, email string
	}{{alice, "alice", "alice@example.com"}, {bob, "carol", "carol@example.com"}, {dan, "erin", ""}, {fay, "fay", ""}} {
		if err := tombstone.Append(ts.uid, ts.name, ts.email); err != nil {
			t.Fatal(err)
		}
	}

	present := func() string {
		t.Helper()
		st, err := store.Open(db)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close() //nolint:errcheck
		var got []string
		for _, n := range []string{"alice", "bob", "dan", "fay"} {
			if _, ok, _ := st.GetUserByName(ctx, n); ok {
				got = append(got, n)
			}
		}
		return strings.Join(got, " ")
	}
	if err := replayTombstones([]string{"-db", db, "-log", logp}); err != nil {
		t.Fatal(err)
	}
	if got := present(); got != "alice bob dan fay" {
		t.Fatalf("a dry run deleted accounts: left %q", got)
	}
	if err := replayTombstones([]string{"-db", db, "-log", logp, "-apply"}); err != nil {
		t.Fatal(err)
	}
	// alice and fay erased; bob and dan, who reuse a deleted account's id
	// (dan with no address to tell them apart), kept.
	if got := present(); got != "bob dan" {
		t.Fatalf("after the replay %q are left, want \"bob dan\"", got)
	}
}

// TestReplayRefusesAMissingDatabase: store.Open creates a missing file, so a
// mistyped -db once replayed onto a fresh empty database and reported "0
// deleted accounts present", which reads as nothing to replay.
func TestReplayRefusesAMissingDatabase(t *testing.T) {
	dir := t.TempDir()
	logp, typo := filepath.Join(dir, "deletions.jsonl"), filepath.Join(dir, "typo.db")
	t.Setenv("SIGNALDECK_TOMBSTONE_LOG", logp)
	if err := tombstone.Append(1, "a", "a@b.c"); err != nil {
		t.Fatal(err)
	}
	if err := replayTombstones([]string{"-db", typo, "-log", logp, "-apply"}); err == nil {
		t.Fatal("a replay onto a missing database succeeded")
	}
	if _, err := os.Stat(typo); err == nil {
		t.Fatal("the replay created the mistyped database")
	}
}
