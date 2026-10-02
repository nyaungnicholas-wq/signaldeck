package store

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// An account looping sign-ins must not grow the sessions table without bound:
// beyond maxSessionsPerUser the oldest end, the newest keep working, and other
// accounts are untouched.
func TestCreateSessionCapsSessionsPerUser(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()
	loop, err := st.CreateUser(ctx, "looper", "h", false)
	if err != nil {
		t.Fatal(err)
	}
	other, err := st.CreateUser(ctx, "other", "h", false)
	if err != nil {
		t.Fatal(err)
	}
	exp := time.Now().Add(time.Hour).Unix()
	if err := st.CreateSession(ctx, "other-tok", other, exp); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < maxSessionsPerUser+15; i++ {
		if _, err := st.w.ExecContext(ctx, `UPDATE sessions SET created_ts = created_ts - 1`); err != nil {
			t.Fatal(err) // age every existing row so insertion order is unambiguous
		}
		if err := st.CreateSession(ctx, fmt.Sprintf("tok-%02d", i), loop, exp); err != nil {
			t.Fatal(err)
		}
	}
	var n int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE user_id=?`, loop).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != maxSessionsPerUser {
		t.Fatalf("looper holds %d sessions, want the cap %d", n, maxSessionsPerUser)
	}
	if _, ok, _ := st.SessionUser(ctx, fmt.Sprintf("tok-%02d", maxSessionsPerUser+14)); !ok {
		t.Fatal("the newest session was the one ended")
	}
	if _, ok, _ := st.SessionUser(ctx, "tok-00"); ok {
		t.Fatal("the oldest session survived past the cap")
	}
	if _, ok, _ := st.SessionUser(ctx, "other-tok"); !ok {
		t.Fatal("another account's session was ended")
	}
}
