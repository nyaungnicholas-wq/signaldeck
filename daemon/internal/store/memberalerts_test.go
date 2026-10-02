package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestMemberAlertPrefs(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "alerts.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	ann, err := st.CreateVerifiedUser(ctx, "ann", "ann@gmail.com", "x")
	must(err)
	bob, err := st.CreateUserWithEmail(ctx, "bob", "bob@gmail.com", "x") // unverified
	must(err)

	// Opt-in only: a user who never touched the settings gets nothing.
	if p, err := st.AlertPrefs(ctx, ann); err != nil || p.EmailDigest || p.TelegramChatID != "" {
		t.Fatalf("default prefs: %+v %v", p, err)
	}
	if rs, err := st.DigestRecipients(ctx); err != nil || len(rs) != 0 {
		t.Fatalf("recipients before any opt-in: %+v %v", rs, err)
	}
	must(st.SetEmailDigest(ctx, ann, true))
	must(st.SetEmailDigest(ctx, bob, true))
	rs, err := st.DigestRecipients(ctx)
	must(err)
	if len(rs) != 1 || rs[0].UserID != ann || rs[0].Email != "ann@gmail.com" {
		t.Fatalf("an unverified address must never be mailed: %+v", rs)
	}

	// A row created by drawing a Telegram code must not switch email on.
	cy, err := st.CreateVerifiedUser(ctx, "cy", "cy@gmail.com", "x")
	must(err)
	must(st.SetTelegramLinkCode(ctx, cy, "CYCODE23", time.Now().Add(time.Minute)))
	if p, _ := st.AlertPrefs(ctx, cy); p.EmailDigest {
		t.Fatal("drawing a telegram code opted the user into email")
	}
	must(st.UnlinkTelegram(ctx, cy))

	// Telegram linking: an expired code never links; a live one links once.
	must(st.SetTelegramLinkCode(ctx, bob, "OLDCODE1", time.Now().Add(-time.Minute)))
	if _, ok, err := st.LinkTelegramByCode(ctx, "OLDCODE1", "999", time.Now()); err != nil || ok {
		t.Fatalf("expired code linked: ok=%v err=%v", ok, err)
	}
	must(st.SetTelegramLinkCode(ctx, bob, "ABCD2345", time.Now().Add(15*time.Minute)))
	if p, _ := st.AlertPrefs(ctx, bob); !p.TelegramPending {
		t.Fatal("live code not reported pending")
	}
	if err := st.SetTelegramLinkCode(ctx, ann, "ABCD2345", time.Now().Add(15*time.Minute)); err == nil {
		t.Fatal("two users hold the same live code")
	}
	uid, ok, err := st.LinkTelegramByCode(ctx, "abcd2345", "4242", time.Now())
	if err != nil || !ok || uid != bob {
		t.Fatalf("live code: uid=%d ok=%v err=%v", uid, ok, err)
	}
	if _, ok, _ := st.LinkTelegramByCode(ctx, "ABCD2345", "5555", time.Now()); ok {
		t.Fatal("a spent code linked a second chat")
	}
	rs, err = st.DigestRecipients(ctx)
	must(err)
	if len(rs) != 2 || rs[1].UserID != bob || rs[1].ChatID != "4242" || rs[1].Email != "" {
		t.Fatalf("telegram-only recipient: %+v", rs)
	}
	must(st.UnlinkTelegram(ctx, bob))
	if p, _ := st.AlertPrefs(ctx, bob); p.TelegramChatID != "" {
		t.Fatal("unlink kept the chat")
	}

	// Per-day mark.
	must(st.MarkDigestSent(ctx, ann, "2026-10-01"))
	if p, _ := st.AlertPrefs(ctx, ann); p.LastDigestDay != "2026-10-01" {
		t.Fatalf("last day: %+v", p)
	}

	// Unsubscribe: right kind only; an older link still works after a newer
	// email voided it; repeating is harmless.
	verifyTok, err := st.CreateAuthToken(ctx, ann, TokenVerify, time.Hour)
	must(err)
	if _, err := st.RedeemUnsubscribe(ctx, verifyTok); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("a verify token unsubscribed: %v", err)
	}
	old, err := st.CreateAuthToken(ctx, ann, TokenUnsubscribe, time.Hour)
	must(err)
	_, err = st.CreateAuthToken(ctx, ann, TokenUnsubscribe, time.Hour)
	must(err)
	for i := 0; i < 2; i++ {
		if uid, err := st.RedeemUnsubscribe(ctx, old); err != nil || uid != ann {
			t.Fatalf("unsubscribe #%d: uid=%d err=%v", i, uid, err)
		}
	}
	if p, _ := st.AlertPrefs(ctx, ann); p.EmailDigest {
		t.Fatal("unsubscribe left the digest on")
	}
	expired, err := st.CreateAuthToken(ctx, ann, TokenUnsubscribe, -time.Second)
	must(err)
	if _, err := st.RedeemUnsubscribe(ctx, expired); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("expired unsubscribe token: %v", err)
	}
	must(st.PurgeExpiredTokens(ctx, TokenUnsubscribe, time.Now()))
	var left int
	must(st.db.QueryRow(`SELECT COUNT(*) FROM auth_tokens WHERE kind=? AND user_id=?`, TokenUnsubscribe, ann).Scan(&left))
	if left != 2 {
		t.Fatalf("purge left %d unsubscribe tokens, want the 2 live ones", left)
	}
	if _, err := st.RedeemUnsubscribe(ctx, old); err != nil {
		t.Fatalf("purge removed a live token: %v", err)
	}

	// Purge removes the prefs row with the stale account.
	if _, err := st.w.Exec(`UPDATE users SET created_ts=? WHERE id=?`, time.Now().Add(-48*time.Hour).Unix(), bob); err != nil {
		t.Fatal(err)
	}
	must(st.PurgeStaleUnverified(ctx, time.Now().Add(-24*time.Hour)))
	var n int
	must(st.db.QueryRow(`SELECT COUNT(*) FROM member_alert_prefs WHERE user_id=?`, bob).Scan(&n))
	if n != 0 {
		t.Fatal("purge left the stale account's alert prefs")
	}
}
