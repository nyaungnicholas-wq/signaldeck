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
	if _, err := st.VerifyEmail(ctx, tok); err != ErrTokenInvalid {
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

// Confirming an address redeems the link and verifies in one transaction; a dead
// link verifies nothing.
func TestVerifyEmailIsAtomic(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "acct.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	uid, err := st.CreateUserWithEmail(ctx, "vera", "vera@example.com", "h")
	if err != nil {
		t.Fatal(err)
	}
	tok, err := st.CreateAuthToken(ctx, uid, TokenVerify, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.VerifyEmail(ctx, "short"); err != ErrTokenInvalid {
		t.Fatalf("malformed link: err=%v", err)
	}
	if _, verified, _, _ := st.AccountEmail(ctx, uid); verified {
		t.Fatal("a dead link verified the address")
	}
	if got, err := st.VerifyEmail(ctx, tok); err != nil || got != uid {
		t.Fatalf("VerifyEmail = %d, %v", got, err)
	}
	if _, verified, _, _ := st.AccountEmail(ctx, uid); !verified {
		t.Fatal("address not verified")
	}
	if _, err := st.VerifyEmail(ctx, tok); err != ErrTokenInvalid {
		t.Fatalf("second use: err=%v, want ErrTokenInvalid", err)
	}
}

// ClaimUnverified must claim only the row it was handed: same address, still
// unconfirmed. An owner who confirmed by email a moment before keeps the
// password they chose, and a row whose address differs (a deleted id reused by
// a stranger's sign-up) is left alone.
func TestClaimUnverifiedRechecksTheRow(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "claim.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	hashOf := func(uid int64) string {
		var h string
		if err := st.db.QueryRow(`SELECT pass_hash FROM users WHERE id=?`, uid).Scan(&h); err != nil {
			t.Fatal(err)
		}
		return h
	}

	confirmed, err := st.CreateVerifiedUser(ctx, "owner", "owner@gmail.com", "owners-own-hash")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.ClaimUnverified(ctx, confirmed, "owner@gmail.com", "claim-hash"); err != ErrClaimRaced {
		t.Fatalf("claiming a confirmed account: err=%v, want ErrClaimRaced", err)
	}
	if h := hashOf(confirmed); h != "owners-own-hash" {
		t.Fatalf("a confirmed owner's password was replaced (now %q)", h)
	}

	stranger, err := st.CreateUserWithEmail(ctx, "stranger", "stranger@gmail.com", "strangers-hash")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.ClaimUnverified(ctx, stranger, "victim@gmail.com", "claim-hash"); err != ErrClaimRaced {
		t.Fatalf("claiming a row with another address: err=%v, want ErrClaimRaced", err)
	}
	if h := hashOf(stranger); h != "strangers-hash" {
		t.Fatalf("a row with another address was claimed (hash now %q)", h)
	}

	squat, err := st.CreateUserWithEmail(ctx, "squatter", "victim@gmail.com", "squatters-hash")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.ClaimUnverified(ctx, squat, "victim@gmail.com", "claim-hash"); err != nil {
		t.Fatalf("claiming an unconfirmed squat: %v", err)
	}
	if _, _, verified, _, _ := st.AccountByEmail(ctx, "victim@gmail.com"); !verified || hashOf(squat) != "claim-hash" {
		t.Fatalf("the squat was not claimed: verified=%v hash=%q", verified, hashOf(squat))
	}
}
