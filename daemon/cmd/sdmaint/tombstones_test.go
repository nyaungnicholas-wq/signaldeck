package main

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
	"github.com/nyaungnicholas-wq/signaldeck/internal/tombstone"
)

// TestReplayDeletesOnlyWhenIDAndAccountHashMatch: users.id is reused, so a
// tombstone for an id that now belongs to a different account (a different
// address, or the same empty address under another name) must leave that
// account alone, while the restored account it names is erased; a dry run
// erases nothing.
func TestReplayDeletesOnlyWhenIDAndAccountHashMatch(t *testing.T) {
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
	created := func(id int64) int64 { u, _, _ := st.GetUserByID(ctx, id); return u.CreatedTs }
	aliceTs, danTs, fayTs := created(alice), created(dan), created(fay)
	st.Close() //nolint:errcheck
	// alice and fay deleted their accounts after this backup was taken; bob's
	// id once belonged to carol, and dan's to erin (no address either), who
	// deleted theirs before bob and dan signed up.
	for _, ts := range []struct {
		uid         int64
		name, email string
		created     int64
	}{{alice, "alice", "alice@example.com", aliceTs}, {bob, "carol", "carol@example.com", aliceTs}, {dan, "erin", "", danTs}, {fay, "fay", "", fayTs}} {
		if err := tombstone.Append(ts.uid, ts.name, ts.email, ts.created); err != nil {
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
	if err := tombstone.Append(1, "a", "a@b.c", 1); err != nil {
		t.Fatal(err)
	}
	if err := replayTombstones([]string{"-db", typo, "-log", logp, "-apply"}); err == nil {
		t.Fatal("a replay onto a missing database succeeded")
	}
	if _, err := os.Stat(typo); err == nil {
		t.Fatal("the replay created the mistyped database")
	}
}

// TestReplaySparesALaterSignUpUnderTheSameIDNameAndAddress: users.id has no
// AUTOINCREMENT, so when the newest account is deleted and its owner signs up
// again with the same name and address, the new account gets the same id and
// only created_ts tells the two apart (2026-10-22 review). A replay onto the
// live copy must keep the new account; one onto a backup taken before the
// delete must still erase the old one.
func TestReplaySparesALaterSignUpUnderTheSameIDNameAndAddress(t *testing.T) {
	dir := t.TempDir()
	live, backup, logp := filepath.Join(dir, "live.db"), filepath.Join(dir, "backup.db"), filepath.Join(dir, "deletions.jsonl")
	t.Setenv("SIGNALDECK_TOMBSTONE_LOG", logp)
	ctx := context.Background()
	st, err := store.Open(live)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateUserWithEmail(ctx, "hal", "hal@example.com", "x"); err != nil {
		t.Fatal(err)
	}
	gus, err := st.CreateUserWithEmail(ctx, "gus", "gus@example.com", "x")
	if err != nil {
		t.Fatal(err)
	}
	st.Close() //nolint:errcheck
	for _, sfx := range []string{"", "-wal"} { // the backup, taken before gus deletes
		if img, err := os.ReadFile(live + sfx); err == nil {
			if err := os.WriteFile(backup+sfx, img, 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	if st, err = store.Open(live); err != nil {
		t.Fatal(err)
	}
	// gus deletes his account the way accountDelete does: tombstone first.
	email, created, _, err := st.AccountEmailFor(ctx, gus, "gus")
	if err != nil {
		t.Fatal(err)
	}
	if err := tombstone.Append(gus, "gus", email, created); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteAccount(ctx, gus, "gus"); err != nil {
		t.Fatal(err)
	}
	if again, err := st.CreateUserWithEmail(ctx, "gus", "gus@example.com", "x"); err != nil || again != gus {
		t.Fatalf("the new sign-up got id %d (%v), want the reused id %d", again, err, gus)
	}
	st.Close() //nolint:errcheck
	// It signed up after the delete; the test runs inside one second, so move
	// its created_ts on by a minute.
	raw, err := sql.Open("sqlite", live)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`UPDATE users SET created_ts=? WHERE id=?`, created+60, gus); err != nil {
		t.Fatal(err)
	}
	raw.Close() //nolint:errcheck
	has := func(db string) bool {
		t.Helper()
		st, err := store.Open(db)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close() //nolint:errcheck
		_, ok, _ := st.GetUserByName(ctx, "gus")
		return ok
	}
	if !has(backup) || !has(live) {
		t.Fatal("setup: gus is missing from the backup or from the live copy before any replay")
	}
	for _, db := range []string{live, backup} {
		if err := replayTombstones([]string{"-db", db, "-log", logp, "-apply"}); err != nil {
			t.Fatal(err)
		}
	}
	if !has(live) {
		t.Fatal("the replay erased the new account that reused the deleted one's id, name and address")
	}
	if has(backup) {
		t.Fatal("the replay left the deleted account in the backup taken before its deletion")
	}
}
