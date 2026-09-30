package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestPurgeStaleUnverifiedAndResetVoidsTokens(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "acct.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	ctx := context.Background()

	stale, err := st.CreateUserWithEmail(ctx, "stale", "stale@example.com", "x")
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := st.CreateUserWithEmail(ctx, "fresh", "fresh@example.com", "x")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.w.Exec(`UPDATE users SET created_ts=? WHERE id=?`, time.Now().Add(-48*time.Hour).Unix(), stale); err != nil {
		t.Fatal(err)
	}
	if err := st.PurgeStaleUnverified(ctx, time.Now().Add(-24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if taken, _ := st.EmailTaken(ctx, "stale@example.com"); taken {
		t.Fatal("stale unverified account survived the purge")
	}
	if taken, _ := st.EmailTaken(ctx, "fresh@example.com"); !taken {
		t.Fatal("purge removed an account still inside its window")
	}

	tok, err := st.CreateAuthToken(ctx, fresh, TokenVerify, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetPassword(ctx, fresh, "y"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ConsumeAuthToken(ctx, tok, TokenVerify); err != ErrTokenInvalid {
		t.Fatalf("verify link survived a password reset: err=%v", err)
	}
}
