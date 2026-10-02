package memberdigest

// Tests for the 2026-10-01 review round (items C, D, E, F, H and the RFC 8058
// headers of A).

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
	"github.com/nyaungnicholas-wq/signaldeck/internal/structregime"
)

// faultyStore fails chosen calls for chosen users.
type faultyStore struct {
	*store.Store
	failMark  map[int64]bool
	failWatch map[int64]bool
}

func (s *faultyStore) MarkDigestSent(ctx context.Context, uid int64, day string) error {
	if s.failMark[uid] {
		return errors.New("database is locked")
	}
	return s.Store.MarkDigestSent(ctx, uid, day)
}

func (s *faultyStore) ListMemberSymbols(ctx context.Context, uid int64) ([]md.Symbol, error) {
	if s.failWatch[uid] {
		return nil, errors.New("database is locked")
	}
	return s.Store.ListMemberSymbols(ctx, uid)
}

func (f *fixture) mailsTo(addr string) int {
	f.mail.mu.Lock()
	defer f.mail.mu.Unlock()
	n := 0
	for _, m := range f.mail.msgs {
		if m.to == addr {
			n++
		}
	}
	return n
}

// C: a failed MarkDigestSent after a delivered email never leads to a second
// email the same day, even across a capped (deferred) follow-up pass.
func TestWorkerNeverResendsWhenMarkFails(t *testing.T) {
	f := newFixture(t)
	f.w.Telegram = nil
	f.w.Cap = 1
	f.w.St = &faultyStore{Store: f.st, failMark: map[int64]bool{f.ann: true}}
	if d, err := f.w.Run(context.Background()); err != nil || d != "sent=1 failed=0 skipped=0 deferred=1" {
		t.Fatalf("first pass: %q %v", d, err)
	}
	if d, err := f.w.Run(context.Background()); err != nil || d != "sent=1 failed=0 skipped=1 deferred=0" {
		t.Fatalf("follow-up pass: %q %v", d, err)
	}
	if d, _ := f.w.Run(context.Background()); d != "sent=0 failed=0 skipped=2 deferred=0" {
		t.Fatalf("third pass: %q", d)
	}
	if n := f.mailsTo("ann@gmail.com"); n != 1 {
		t.Errorf("ann mailed %d times today", n)
	}
	if next := f.w.NextFire(f.tradingDay, f.tradingDay); next.Sub(f.tradingDay) < time.Hour {
		t.Errorf("nothing left today but the next fire is %v", next)
	}
}

// C: one member's store error is that member's failure, not the end of the
// pass for everyone after them.
func TestWorkerStoreErrorDoesNotDropTheRest(t *testing.T) {
	f := newFixture(t)
	f.w.Telegram = nil
	f.w.St = &faultyStore{Store: f.st, failWatch: map[int64]bool{f.ann: true}}
	d, err := f.w.Run(context.Background())
	if err != nil || d != "sent=1 failed=1 skipped=0 deferred=0" {
		t.Fatalf("pass with one bad member: %q %v", d, err)
	}
	if f.mailsTo("bob@gmail.com") != 1 {
		t.Error("bob was not mailed after ann's error")
	}
	if next := f.w.NextFire(f.tradingDay, f.tradingDay); next.Sub(f.tradingDay) != retryAfter {
		t.Errorf("ann has a try left but the next fire is %v", next)
	}
}

// D: no sends after 16:00 ET, even on a catch-up fire.
func TestWorkerNoEveningSends(t *testing.T) {
	f := newFixture(t)
	late := tradingDayAt(16, 30)
	f.w.Now = func() time.Time { return late }
	if d, _ := f.w.Run(context.Background()); !strings.HasPrefix(d, "waiting") || f.mail.count() != 0 {
		t.Errorf("16:30 ET: %q, %d mails", d, f.mail.count())
	}
	edge := tradingDayAt(15, 59)
	f.w.Now = func() time.Time { return edge }
	if d, _ := f.w.Run(context.Background()); !strings.HasPrefix(d, "sent=2") {
		t.Errorf("15:59 ET: %q", d)
	}
}

// E: earliest calls are keyed by market too.
func TestComposeFlipKeysByMarket(t *testing.T) {
	f := Facts{
		Current: []store.RegimeForecast{
			rf("AAA", "stocks", structregime.KindTrend21, "uptrend", "high", 0.9),
			rf("AAA", "futures", structregime.KindTrend21, "uptrend", "high", 0.9),
		},
		Calls: []store.RegimeWeekCall{{Symbol: "AAA", Market: "futures", Kind: structregime.KindTrend21, Ts: 1, Regime: "downtrend"}},
	}
	d, _ := Compose(f, []md.Symbol{{Symbol: "AAA", Market: md.Stocks}, {Symbol: "AAA", Market: md.Market("futures")}}, "", "")
	if d.Changed != 1 || strings.Count(d.Body, "changed from downtrend") != 1 ||
		!strings.Contains(d.Body, "AAA (futures)\n  trend21: uptrend (conviction high, backtest accuracy 90.0%) - changed from downtrend") {
		t.Errorf("a futures flip leaked onto the stock (or vanished):\n%s", d.Body)
	}
}

// F: email fails, Telegram succeeds: the email is retried, Telegram is not
// resent, and the day closes once both are done.
func TestWorkerRetriesOnlyTheFailedChannel(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if err := f.st.SetEmailDigest(ctx, f.bob, false); err != nil {
		t.Fatal(err)
	}
	if err := f.st.SetTelegramLinkCode(ctx, f.ann, "ABCD2345", f.tradingDay.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := f.st.LinkTelegramByCode(ctx, "ABCD2345", "777", f.tradingDay); !ok {
		t.Fatal("link")
	}
	f.mail.fail = true
	if d, _ := f.w.Run(ctx); d != "sent=1 failed=0 skipped=0 deferred=0" {
		t.Fatalf("email down, telegram up: %q", d)
	}
	if next := f.w.NextFire(f.tradingDay, f.tradingDay); next.Sub(f.tradingDay) != retryAfter {
		t.Errorf("the email has a try left but the next fire is %v", next)
	}
	if p, _ := f.st.AlertPrefs(ctx, f.ann); p.LastDigestDay != "" {
		t.Error("day marked done with the email still owed")
	}
	f.mail.fail = false
	if d, _ := f.w.Run(ctx); d != "sent=1 failed=0 skipped=0 deferred=0" {
		t.Fatalf("email retry: %q", d)
	}
	if f.mailsTo("ann@gmail.com") != 1 || len(f.tg.sends) != 1 {
		t.Errorf("mails=%d telegram=%d, want 1 and 1", f.mailsTo("ann@gmail.com"), len(f.tg.sends))
	}
	if p, _ := f.st.AlertPrefs(ctx, f.ann); p.LastDigestDay == "" {
		t.Error("both channels delivered but the day is not marked")
	}
}

// A: the email carries RFC 8058 one-click headers pointing at its own link.
func TestWorkerSendsListUnsubscribeHeaders(t *testing.T) {
	f := newFixture(t)
	f.w.Telegram = nil
	if _, err := f.w.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	m := f.mail.msgs[0]
	lu := m.headers["List-Unsubscribe"]
	if !strings.HasPrefix(lu, "<https://sd.example/api/alerts/unsubscribe?token=") || !strings.HasSuffix(lu, ">") ||
		!strings.Contains(m.body, strings.Trim(lu, "<>")) {
		t.Errorf("List-Unsubscribe %q does not match the body link", lu)
	}
	if m.headers["List-Unsubscribe-Post"] != "List-Unsubscribe=One-Click" {
		t.Errorf("List-Unsubscribe-Post %q", m.headers["List-Unsubscribe-Post"])
	}
}

// H: a 403 from Telegram (bot blocked) unlinks the chat and is not retried.
func TestWorkerUnlinksBlockedChat(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if err := f.st.SetTelegramLinkCode(ctx, f.bob, "ABCD2345", f.tradingDay.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := f.st.LinkTelegramByCode(ctx, "ABCD2345", "777", f.tradingDay); !ok {
		t.Fatal("link")
	}
	f.tg.forbid = true
	if d, _ := f.w.Run(ctx); d != "sent=2 failed=0 skipped=0 deferred=0" {
		t.Fatalf("run: %q", d)
	}
	if p, _ := f.st.AlertPrefs(ctx, f.bob); p.TelegramChatID != "" || p.LastDigestDay == "" {
		t.Errorf("after a 403: %+v (want unlinked, day done)", p)
	}
	if f.w.retry != 0 {
		t.Error("a blocked chat is queued for a retry")
	}
}

func TestParseLinkCodeBotSuffix(t *testing.T) {
	for in, want := range map[string]string{
		"/start@SignalDeckBot ABCD2345": "ABCD2345", "/START@x abcd2345": "ABCD2345",
		"/stop": "", "/help ABCD2345": "", "/start@SignalDeckBot": "",
	} {
		if got := ParseLinkCode(in); got != want {
			t.Errorf("ParseLinkCode(%q) = %q, want %q", in, got, want)
		}
	}
}

// H: "/stop" from a linked chat unlinks it and says so; from an unknown chat
// it does nothing.
func TestLinkWorkerStop(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if err := f.st.SetTelegramLinkCode(ctx, f.bob, "ABCD2345", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := f.st.LinkTelegramByCode(ctx, "ABCD2345", "777", time.Now()); !ok {
		t.Fatal("link")
	}
	f.tg.updates = `[{"update_id":5,"message":{"chat":{"id":777},"text":"/stop@SignalDeckBot"}},` +
		`{"update_id":6,"message":{"chat":{"id":999},"text":"/stop"}}]`
	lw := &LinkWorker{St: f.st, Telegram: f.w.Telegram}
	if d, err := lw.Run(ctx); err != nil || d != "updates=2 linked=0 unmatched=0 stopped=1" {
		t.Fatalf("stop run: %q %v", d, err)
	}
	if p, _ := f.st.AlertPrefs(ctx, f.bob); p.TelegramChatID != "" {
		t.Error("/stop left the chat linked")
	}
	if len(f.tg.sends) != 1 || f.tg.sends[0]["chat_id"] != "777" {
		t.Errorf("replies: %+v", f.tg.sends)
	}
}
