package store

import (
	"context"
	"strconv"
	"testing"
	"time"
)

// TestDeleteAccountWaitsForTheWriteLock pins the 2026-10-05 review's M1: with
// a worker holding the write lock (the fleet does, all day), a delete that
// read before it wrote failed at once with SQLITE_BUSY instead of waiting.
func TestDeleteAccountWaitsForTheWriteLock(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	uid, err := st.CreateUser(ctx, "member", "h", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"copilot_ask:2026-10-05:" + strconv.FormatInt(uid, 10), "copilot_ask:2026-10-05:1" + strconv.FormatInt(uid, 10)} {
		if err := st.SetMeta(ctx, k, "3"); err != nil {
			t.Fatal(err)
		}
	}
	tx, err := st.w.BeginTx(ctx, nil) // _txlock=immediate: the lock is held from here
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO meta (k, v) VALUES ('lock-holder','1')`); err != nil {
		t.Fatal(err)
	}
	go func() { time.Sleep(300 * time.Millisecond); tx.Commit() }() //nolint:errcheck
	if err := st.DeleteAccount(ctx, uid); err != nil {
		t.Fatalf("delete while a worker held the write lock: %v; want it to wait for the lock", err)
	}
	if _, ok, err := st.GetUserByID(ctx, uid); err != nil || ok {
		t.Fatalf("after delete: found=%v err=%v; want the account gone", ok, err)
	}
	if v, _ := st.GetMeta(ctx, "copilot_ask:2026-10-05:"+strconv.FormatInt(uid, 10)); v != "" {
		t.Fatalf("the deleted account's copilot quota survived (%q); a reused id would inherit it", v)
	}
	if v, _ := st.GetMeta(ctx, "copilot_ask:2026-10-05:1"+strconv.FormatInt(uid, 10)); v != "3" {
		t.Fatalf("another account's quota was deleted (%q)", v)
	}
	admin, err := st.CreateUser(ctx, "owner", "h", true)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteAccount(ctx, admin); err != ErrAdminAccount {
		t.Fatalf("deleting the admin: %v; want ErrAdminAccount", err)
	}
}
