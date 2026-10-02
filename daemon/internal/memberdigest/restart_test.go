package memberdigest

// The member-digest worker's tries and deliveries are mirrored into
// member_digest_tries (store.SaveDigestTry) and reloaded on its first run of a
// day. Before that they lived only in memory, so a restart granted fresh tries
// past maxTries and re-sent channels already delivered.

import (
	"context"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// restart closes f's store, reopens the same file and gives f a fresh Worker
// with the same transports and clock, so nothing survives but the database.
func (f *fixture) restart(t *testing.T) {
	t.Helper()
	if err := f.st.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	st2, err := store.Open(f.st.Path())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st2.Close() })
	f.w = &Worker{
		St:        st2,
		Base:      f.w.Base,
		MailReady: f.w.MailReady,
		Mail:      f.w.Mail,
		Telegram:  f.w.Telegram,
		Now:       f.w.Now,
		Cap:       f.w.Cap,
	}
	f.st = st2
}

// TestWorkerTryCapSurvivesRestart: two failed tries, each followed by a
// restart, spend the day's cap; the third pass after another restart mails
// nobody even though the mail host is back.
func TestWorkerTryCapSurvivesRestart(t *testing.T) {
	f := newFixture(t)
	f.w.Telegram = nil
	f.mail.fail = true

	detail, err := f.w.Run(context.Background())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if detail != "sent=0 failed=2 skipped=0 deferred=0" {
		t.Fatalf("want 'sent=0 failed=2 skipped=0 deferred=0', got %q", detail)
	}

	f.restart(t)

	detail, err = f.w.Run(context.Background())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if detail != "sent=0 failed=2 skipped=0 deferred=0" {
		t.Fatalf("want 'sent=0 failed=2 skipped=0 deferred=0', got %q", detail)
	}

	f.restart(t)
	f.mail.fail = false

	detail, err = f.w.Run(context.Background())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if detail != "sent=0 failed=0 skipped=2 deferred=0" {
		t.Fatalf("want 'sent=0 failed=0 skipped=2 deferred=0', got %q", detail)
	}
	if f.mail.count() != 0 {
		t.Fatalf("want mail count 0, got %d", f.mail.count())
	}
}

// TestWorkerNeverResendsDeliveredChannelAfterRestart: Telegram delivered and
// email failed; after a restart only the email is retried.
func TestWorkerNeverResendsDeliveredChannelAfterRestart(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	if err := f.st.SetEmailDigest(ctx, f.bob, false); err != nil {
		t.Fatalf("set email digest: %v", err)
	}

	if err := f.st.SetTelegramLinkCode(ctx, f.ann, "ABCD2345", f.tradingDay.Add(time.Hour)); err != nil {
		t.Fatalf("link code: %v", err)
	}
	_, ok, err := f.st.LinkTelegramByCode(ctx, "ABCD2345", "777", f.tradingDay)
	if err != nil {
		t.Fatalf("link telegram: %v", err)
	}
	if !ok {
		t.Fatalf("link telegram: not ok")
	}

	f.mail.fail = true

	detail, err := f.w.Run(ctx)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if detail != "sent=1 failed=0 skipped=0 deferred=0" {
		t.Fatalf("want 'sent=1 failed=0 skipped=0 deferred=0', got %q", detail)
	}
	if len(f.tg.sends) != 1 {
		t.Fatalf("want 1 telegram send, got %d", len(f.tg.sends))
	}

	f.restart(t)
	f.mail.fail = false

	detail, err = f.w.Run(ctx)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if detail != "sent=1 failed=0 skipped=0 deferred=0" {
		t.Fatalf("want 'sent=1 failed=0 skipped=0 deferred=0', got %q", detail)
	}
	if f.mailsTo("ann@gmail.com") != 1 {
		t.Fatalf("want 1 mail to ann, got %d", f.mailsTo("ann@gmail.com"))
	}
	if len(f.tg.sends) != 1 {
		t.Fatalf("want 1 telegram send after restart (no re-send), got %d", len(f.tg.sends))
	}
	prefs, err := f.st.AlertPrefs(ctx, f.ann)
	if err != nil {
		t.Fatalf("alert prefs: %v", err)
	}
	if prefs.LastDigestDay == "" {
		t.Fatalf("want non-empty LastDigestDay")
	}
}