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

// A reset redeems the link and changes the password atomically: success spends
// the link, replaces the hash, ends every session and verifies the address; a
// dead link changes nothing at all.
func TestResetPasswordIsAtomic(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "acct.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	uid, err := st.CreateUserWithEmail(ctx, "rita", "rita@example.com", "old-hash")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreateSession(ctx, "sess-token-1", uid, time.Now().Add(time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	tok, err := st.CreateAuthToken(ctx, uid, TokenReset, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	hashOf := func() string {
		var h string
		if err := st.db.QueryRow(`SELECT pass_hash FROM users WHERE id=?`, uid).Scan(&h); err != nil {
			t.Fatal(err)
		}
		return h
	}

	// A wrong token of the right shape changes nothing.
	wrong := tok[:63] + "0"
	if tok[63] == '0' {
		wrong = tok[:63] + "1"
	}
	if _, err := st.ResetPassword(ctx, wrong, "evil"); err != ErrTokenInvalid {
		t.Fatalf("bad token: err=%v, want ErrTokenInvalid", err)
	}
	if hashOf() != "old-hash" {
		t.Fatal("a failed reset changed the password")
	}

	got, err := st.ResetPassword(ctx, tok, "new-hash")
	if err != nil || got != uid {
		t.Fatalf("ResetPassword = %d, %v", got, err)
	}
	if hashOf() != "new-hash" {
		t.Fatal("password not replaced")
	}
	if _, ok, _ := st.SessionUser(ctx, "sess-token-1"); ok {
		t.Fatal("old session survived the reset")
	}
	if _, verified, _, _ := st.AccountEmail(ctx, uid); !verified {
		t.Fatal("the reset link proved the inbox but the address is unverified")
	}
	if _, err := st.ResetPassword(ctx, tok, "again"); err != ErrTokenInvalid {
		t.Fatalf("second use of the link: err=%v, want ErrTokenInvalid", err)
	}
}
