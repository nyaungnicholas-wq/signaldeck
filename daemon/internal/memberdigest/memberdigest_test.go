package memberdigest

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/marketcal"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
	"github.com/nyaungnicholas-wq/signaldeck/internal/structregime"
	"github.com/nyaungnicholas-wq/signaldeck/internal/volregime"
)

func rf(sym, market string, kind structregime.Kind, regime, tier string, acc float64) store.RegimeForecast {
	return store.RegimeForecast{Symbol: sym, Market: market, Forecast: structregime.Forecast{
		Kind: kind, Regime: regime, Tier: tier, HistoricalAccuracy: acc, HorizonDays: 21}}
}

func testFacts() Facts {
	return Facts{
		Current: []store.RegimeForecast{
			rf("AAA", "stocks", structregime.KindTrend21, "uptrend", "very-high", 0.972),
			rf("AAA", "stocks", structregime.KindLiquidity21, "quiet", "medium", 0.611),
			rf("BBB", "stocks", structregime.KindTrend21, "downtrend", "high", 0.905),
			rf("XBT/USD", "crypto", "trend21-crypto", "uptrend", "high", 0.8),
		},
		Calls: []store.RegimeWeekCall{
			{Symbol: "AAA", Market: "stocks", Kind: structregime.KindTrend21, Ts: 1, Regime: "downtrend"}, // earliest: flipped
			{Symbol: "AAA", Market: "stocks", Kind: structregime.KindTrend21, Ts: 2, Regime: "uptrend"},
			{Symbol: "AAA", Market: "stocks", Kind: structregime.KindLiquidity21, Ts: 1, Regime: "quiet"}, // unchanged
			{Symbol: "BBB", Market: "stocks", Kind: structregime.KindTrend21, Ts: 1, Regime: "downtrend"},
		},
		Vol: []store.VolForecast{{Symbol: "AAA", Market: "stocks",
			Forecast: volregime.Forecast{Regime: "elevated", Tier: "high", HistoricalAccuracy: 0.743}}},
	}
}

func syms(names ...string) []md.Symbol {
	var out []md.Symbol
	for i, n := range names {
		m := md.Stocks
		if strings.Contains(n, "/") {
			m = md.Crypto
		}
		out = append(out, md.Symbol{ID: int64(i + 1), Symbol: n, Market: m})
	}
	return out
}

// symbolBlock returns the lines of body from "sym" up to the next blank line.
func symbolBlock(t *testing.T, body, sym string) string {
	t.Helper()
	for _, b := range strings.Split(body, "\n\n") {
		if strings.HasPrefix(b, sym+"\n") {
			return b
		}
	}
	t.Fatalf("no block for %s in:\n%s", sym, body)
	return ""
}

func TestComposeFlipsCryptoAndFooter(t *testing.T) {
	d, ok := Compose(testFacts(), syms("BBB", "AAA", "XBT/USD"), "https://sd.example", "https://sd.example/api/alerts/unsubscribe?token=t1")
	if !ok {
		t.Fatal("compose declined a non-empty watchlist")
	}
	if d.Subject != "SignalDeck daily read: 2 watched, 1 changed" {
		t.Errorf("subject %q", d.Subject)
	}
	a := symbolBlock(t, d.Body, "AAA")
	for _, want := range []string{
		"trend21: uptrend (conviction very-high, backtest accuracy 97.2%) - changed from downtrend",
		"liquidity21: quiet (conviction medium, backtest accuracy 61.1%)",
		"vol63: elevated (conviction high, backtest accuracy 74.3%)",
	} {
		if !strings.Contains(a, want) {
			t.Errorf("AAA block missing %q:\n%s", want, a)
		}
	}
	if strings.Contains(a, "liquidity21: quiet (conviction medium, backtest accuracy 61.1%) - changed") {
		t.Error("an unchanged call reported as changed")
	}
	if strings.Contains(symbolBlock(t, d.Body, "BBB"), "changed") {
		t.Error("BBB did not flip")
	}
	if strings.Index(d.Body, "AAA\n") > strings.Index(d.Body, "BBB\n") {
		t.Error("symbols not sorted")
	}
	if strings.Contains(d.Body, "XBT") || strings.Contains(d.Body, "crypto") {
		t.Errorf("crypto in a member digest:\n%s", d.Body)
	}
	for _, want := range []string{
		"Same forecasts for every member; not personalised advice; backtest accuracy is hypothetical and not a promise of results.",
		"https://sd.example/today",
		"https://sd.example/api/alerts/unsubscribe?token=t1",
	} {
		if !strings.Contains(d.Body, want) {
			t.Errorf("footer missing %q", want)
		}
	}
	// No links when there is no origin or no unsubscribe URL (Telegram).
	tg, _ := Compose(testFacts(), syms("AAA"), "", "")
	if strings.Contains(tg.Body, "http") || strings.Contains(tg.Body, "Unsubscribe") {
		t.Errorf("link with no base/unsub:\n%s", tg.Body)
	}
	// The same ticker in two markets is two symbols, not one deduped away.
	if d, _ := Compose(testFacts(), []md.Symbol{{Symbol: "AAA", Market: md.Stocks},
		{Symbol: "AAA", Market: md.Market("futures")}, {Symbol: "AAA", Market: md.Stocks}}, "", ""); d.Watched != 2 {
		t.Errorf("AAA stocks+futures watched=%d, want 2", d.Watched)
	}
	if _, ok := Compose(testFacts(), nil, "https://x", "u"); ok {
		t.Error("an empty watchlist composed a digest")
	}
	if _, ok := Compose(testFacts(), syms("XBT/USD"), "https://x", "u"); ok {
		t.Error("a crypto-only watchlist composed a digest")
	}
	// A watched stock with no forecast is said plainly, not dropped silently.
	if d, _ := Compose(testFacts(), syms("ZZZ"), "", ""); !strings.Contains(symbolBlock(t, d.Body, "ZZZ"), "no current forecast") {
		t.Errorf("no-forecast symbol:\n%s", d.Body)
	}
}

// Impersonal: what a member reads about a symbol does not depend on who they
// are or what else they watch.
func TestComposeIsImpersonal(t *testing.T) {
	one, _ := Compose(testFacts(), syms("AAA"), "https://sd.example", "https://sd.example/u?token=one")
	two, _ := Compose(testFacts(), syms("BBB", "AAA"), "https://sd.example", "https://sd.example/u?token=two")
	if a, b := symbolBlock(t, one.Body, "AAA"), symbolBlock(t, two.Body, "AAA"); a != b {
		t.Errorf("AAA reads differently for two members:\n%s\n---\n%s", a, b)
	}
}

func TestParseLinkCode(t *testing.T) {
	for in, want := range map[string]string{
		"/start ABCD2345": "ABCD2345", "abcd2345": "ABCD2345", "  ABCD2345 ": "ABCD2345",
		"/start": "", "ABCD234": "", "ABCD23450": "", "ABCD234O": "", "hello there": "", "": "",
	} {
		if got := ParseLinkCode(in); got != want {
			t.Errorf("ParseLinkCode(%q) = %q, want %q", in, got, want)
		}
	}
	c := NewLinkCode()
	if ParseLinkCode(c) != c {
		t.Errorf("NewLinkCode %q does not parse", c)
	}
}

// ── worker ──────────────────────────────────────────────────────────────────

type sent struct {
	to, subject, body string
	headers           map[string]string
}

type fakeMail struct {
	mu   sync.Mutex
	msgs []sent
	fail bool
}

func (f *fakeMail) send(_ context.Context, to, subject, body string, headers map[string]string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return errors.New("smtp down")
	}
	f.msgs = append(f.msgs, sent{to, subject, body, headers})
	return nil
}

func (f *fakeMail) count() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.msgs) }

// fakeTelegram serves the Bot API methods the package calls.
type fakeTelegram struct {
	mu      sync.Mutex
	forbid  bool // sendMessage answers 403 (bot blocked)
	sends   []map[string]any
	updates string // JSON array for getUpdates
	offsets []float64
}

func (f *fakeTelegram) server(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var p map[string]any
		_ = json.Unmarshal(b, &p)
		f.mu.Lock()
		defer f.mu.Unlock()
		switch {
		case strings.HasSuffix(r.URL.Path, "/sendMessage") && f.forbid:
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"ok":false,"description":"Forbidden: bot was blocked by the user"}`))
		case strings.HasSuffix(r.URL.Path, "/sendMessage"):
			f.sends = append(f.sends, p)
			_, _ = w.Write([]byte(`{"ok":true,"result":{}}`))
		case strings.HasSuffix(r.URL.Path, "/getUpdates"):
			off, _ := p["offset"].(float64)
			f.offsets = append(f.offsets, off)
			u := f.updates
			if u == "" {
				u = "[]"
			}
			f.updates = "[]"
			_, _ = w.Write([]byte(`{"ok":true,"result":` + u + `}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

type fixture struct {
	st         *store.Store
	ann, bob   int64
	aaa, xbt   md.Symbol
	mail       *fakeMail
	tg         *fakeTelegram
	w          *Worker
	tradingDay time.Time
}

// tradingDayAt returns a recent trading day at hour:min ET.
func tradingDayAt(hour, min int) time.Time {
	d := time.Date(2026, 9, 30, hour, min, 0, 0, marketcal.Loc()) // a Wednesday
	for !marketcal.IsTradingDay(d) {
		d = d.AddDate(0, 0, -1)
	}
	return d
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "md.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	f := &fixture{st: st, mail: &fakeMail{}, tg: &fakeTelegram{}, tradingDay: tradingDayAt(8, 30)}
	f.ann, err = st.CreateVerifiedUser(ctx, "ann", "ann@gmail.com", "x")
	must(err)
	f.bob, err = st.CreateVerifiedUser(ctx, "bob", "bob@gmail.com", "x")
	must(err)
	f.aaa, err = st.UpsertSymbol(ctx, "AAA", md.Stocks, "Aaa Inc")
	must(err)
	f.xbt, err = st.UpsertSymbol(ctx, "XBT/USD", md.Crypto, "Bitcoin")
	must(err)
	must(st.UpsertRegimeForecast(ctx, f.aaa.ID, f.tradingDay.Unix(), structregime.Forecast{Kind: structregime.KindTrend21,
		HorizonDays: 21, Regime: "uptrend", Conviction: 0.9, HistoricalAccuracy: 0.95, Tier: "high", Rank: 0.9, N: 500}))
	for _, u := range []int64{f.ann, f.bob} {
		must(st.AddMemberSymbol(ctx, u, f.aaa.ID))
		must(st.AddMemberSymbol(ctx, u, f.xbt.ID))
		must(st.SetEmailDigest(ctx, u, true))
	}
	srv := f.tg.server(t)
	now := f.tradingDay
	f.w = &Worker{
		St:        st,
		Base:      func() string { return "https://sd.example" },
		MailReady: func() bool { return true },
		Mail:      f.mail.send,
		Telegram:  &Telegram{Token: "123:SECRET", APIBase: srv.URL},
		Now:       func() time.Time { return now },
	}
	return f
}

func TestWorkerSendsOncePerDayAndHonoursUnsubscribe(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if err := f.st.SetTelegramLinkCode(ctx, f.bob, "ABCD2345", f.tradingDay.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := f.st.LinkTelegramByCode(ctx, "ABCD2345", "777", f.tradingDay); err != nil || !ok {
		t.Fatalf("link: %v %v", ok, err)
	}
	detail, err := f.w.Run(ctx)
	if err != nil || detail != "sent=2 failed=0 skipped=0 deferred=0" {
		t.Fatalf("first run: %q %v", detail, err)
	}
	if f.mail.count() != 2 || len(f.tg.sends) != 1 {
		t.Fatalf("mails=%d telegram=%d, want 2 and 1", f.mail.count(), len(f.tg.sends))
	}
	if f.tg.sends[0]["chat_id"] != "777" || strings.Contains(f.tg.sends[0]["text"].(string), "token=") {
		t.Errorf("telegram send: %+v", f.tg.sends[0])
	}
	for _, m := range f.mail.msgs {
		if strings.Contains(m.body, "XBT") {
			t.Errorf("crypto in the email to %s", m.to)
		}
		if !strings.Contains(m.body, "trend21: uptrend") {
			t.Errorf("no forecast line in the email to %s:\n%s", m.to, m.body)
		}
	}
	// Per-user cap: a second run the same day sends nothing.
	if detail, err := f.w.Run(ctx); err != nil || detail != "sent=0 failed=0 skipped=2 deferred=0" {
		t.Fatalf("second run same day: %q %v", detail, err)
	}
	if f.mail.count() != 2 {
		t.Fatal("a second digest went out the same day")
	}

	// Unsubscribe with ann's link; next trading day only bob is mailed.
	var annBody string
	for _, m := range f.mail.msgs {
		if m.to == "ann@gmail.com" {
			annBody = m.body
		}
	}
	i := strings.Index(annBody, "https://sd.example/api/alerts/unsubscribe?token=")
	if i < 0 {
		t.Fatalf("no unsubscribe link:\n%s", annBody)
	}
	link := strings.Fields(annBody[i:])[0]
	u, _ := url.Parse(link)
	if uid, err := f.st.RedeemUnsubscribe(ctx, u.Query().Get("token")); err != nil || uid != f.ann {
		t.Fatalf("redeem: %d %v", uid, err)
	}
	next := f.tradingDay.AddDate(0, 0, 1)
	for !marketcal.IsTradingDay(next) {
		next = next.AddDate(0, 0, 1)
	}
	f.w.Now = func() time.Time { return next }
	if detail, err := f.w.Run(ctx); err != nil || detail != "sent=1 failed=0 skipped=0 deferred=0" {
		t.Fatalf("after unsubscribe: %q %v", detail, err)
	}
	if last := f.mail.msgs[len(f.mail.msgs)-1]; last.to != "bob@gmail.com" {
		t.Errorf("mailed %s after ann unsubscribed", last.to)
	}
}

func TestWorkerGlobalCapDefers(t *testing.T) {
	f := newFixture(t)
	f.w.Cap = 1
	detail, err := f.w.Run(context.Background())
	if err != nil || detail != "sent=1 failed=0 skipped=0 deferred=1" {
		t.Fatalf("capped run: %q %v", detail, err)
	}
	// The deferred member is picked up soon, not on the next trading day.
	if next := f.w.NextFire(f.tradingDay, f.tradingDay); next.Sub(f.tradingDay) > 10*time.Minute {
		t.Errorf("deferred sends wait until %v", next)
	}
	if detail, _ := f.w.Run(context.Background()); detail != "sent=1 failed=0 skipped=1 deferred=0" {
		t.Fatalf("catch-up run: %q", detail)
	}
	if next := f.w.NextFire(f.tradingDay, f.tradingDay); next.Sub(f.tradingDay) < time.Hour {
		t.Errorf("no deferred sends left but next fire is %v", next)
	}
}

func TestWorkerFailureLeavesDayUnmarked(t *testing.T) {
	f := newFixture(t)
	f.w.Telegram = nil
	f.mail.fail = true
	if detail, _ := f.w.Run(context.Background()); detail != "sent=0 failed=2 skipped=0 deferred=0" {
		t.Fatalf("failing run: %q", detail)
	}
	// A failed member gets one more try, 30 minutes on, the same day.
	if next := f.w.NextFire(f.tradingDay, f.tradingDay); next.Sub(f.tradingDay) != 30*time.Minute {
		t.Errorf("after a failed pass the next fire is %v", next)
	}
	if detail, _ := f.w.Run(context.Background()); detail != "sent=0 failed=2 skipped=0 deferred=0" {
		t.Fatalf("second failing run: %q", detail)
	}
	// Two tries spent: no hot loop on a dead mail host, back to the calendar.
	if next := f.w.NextFire(f.tradingDay, f.tradingDay); next.Sub(f.tradingDay) < time.Hour {
		t.Errorf("tries exhausted but the next fire is %v", next)
	}
	f.mail.fail = false
	if detail, _ := f.w.Run(context.Background()); detail != "sent=0 failed=0 skipped=2 deferred=0" {
		t.Fatalf("third run the same day: %q", detail)
	}
	next := f.tradingDay.AddDate(0, 0, 1)
	for !marketcal.IsTradingDay(next) {
		next = next.AddDate(0, 0, 1)
	}
	f.w.Now = func() time.Time { return next }
	if detail, _ := f.w.Run(context.Background()); detail != "sent=2 failed=0 skipped=0 deferred=0" {
		t.Fatalf("next trading day after failures: %q", detail)
	}
}

func TestWorkerSucceedsOnRetry(t *testing.T) {
	f := newFixture(t)
	f.w.Telegram = nil
	f.mail.fail = true
	if detail, _ := f.w.Run(context.Background()); detail != "sent=0 failed=2 skipped=0 deferred=0" {
		t.Fatalf("failing run: %q", detail)
	}
	f.mail.fail = false
	if detail, _ := f.w.Run(context.Background()); detail != "sent=2 failed=0 skipped=0 deferred=0" {
		t.Fatalf("retry after failure: %q", detail)
	}
}

func TestTelegramClipsOnRuneBoundary(t *testing.T) {
	ft := &fakeTelegram{}
	srv := ft.server(t)
	tg := &Telegram{Token: "123:SECRET", APIBase: srv.URL}
	if err := tg.Send(context.Background(), "1", "a"+strings.Repeat("é", 3000)); err != nil {
		t.Fatal(err)
	}
	got := ft.sends[0]["text"].(string)
	if len(got) > 3900 || strings.ContainsRune(got, '�') {
		t.Errorf("clipped text: %d bytes, valid=%v", len(got), !strings.ContainsRune(got, '�'))
	}
}

func TestWorkerGates(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	early := tradingDayAt(7, 0)
	f.w.Now = func() time.Time { return early }
	if detail, _ := f.w.Run(ctx); !strings.HasPrefix(detail, "waiting") || f.mail.count() != 0 {
		t.Errorf("before 08:00 ET: %q", detail)
	}
	sat := time.Date(2026, 10, 3, 9, 0, 0, 0, marketcal.Loc())
	f.w.Now = func() time.Time { return sat }
	if detail, _ := f.w.Run(ctx); !strings.HasPrefix(detail, "waiting") || f.mail.count() != 0 {
		t.Errorf("on a Saturday: %q", detail)
	}
	f.w.Now = func() time.Time { return f.tradingDay }
	f.w.Base = func() string { return "" }
	f.w.Telegram = nil
	if detail, _ := f.w.Run(ctx); detail != "skipped: no transport" || f.mail.count() != 0 {
		t.Errorf("no origin and no telegram: %q", detail)
	}
	f.w.Base = func() string { return "https://sd.example" }
	f.w.MailReady = func() bool { return false }
	if detail, _ := f.w.Run(ctx); detail != "skipped: no transport" {
		t.Errorf("mail not ready and no telegram: %q", detail)
	}
	if f.w.Name() != "member-digest" {
		t.Errorf("name %q", f.w.Name())
	}
	if n := f.w.NextFire(time.Time{}, sat); n.In(marketcal.Loc()).Hour() != 8 || !marketcal.IsTradingDay(n) {
		t.Errorf("next fire %v", n)
	}
	// Down across a slot: fire at once (the scheduler never runs at boot).
	if n := f.w.NextFire(f.tradingDay.AddDate(0, 0, -7), f.tradingDay); !n.Equal(f.tradingDay) {
		t.Errorf("missed slot: next fire %v, want now", n)
	}
}

func TestTelegramErrorsAreRedacted(t *testing.T) {
	tg := &Telegram{Token: "123:SECRET", APIBase: "http://127.0.0.1:1"}
	err := tg.Send(context.Background(), "1", "hi")
	if err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("error leaks the token or did not fail: %v", err)
	}
}

func TestLinkWorker(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if err := f.st.SetTelegramLinkCode(ctx, f.bob, "ABCD2345", time.Now().Add(15*time.Minute)); err != nil {
		t.Fatal(err)
	}
	f.tg.updates = `[{"update_id":41,"message":{"chat":{"id":777},"text":"/start abcd2345"}},` +
		`{"update_id":42,"message":{"chat":{"id":888},"text":"WRNG2345"}},` +
		`{"update_id":43,"message":{"chat":{"id":999},"text":"what is this"}}]`
	lw := &LinkWorker{St: f.st, Telegram: f.w.Telegram}
	if lw.Name() != "telegram-link" || lw.Interval() != time.Minute {
		t.Errorf("link worker %q %v", lw.Name(), lw.Interval())
	}
	detail, err := lw.Run(ctx)
	if err != nil || detail != "updates=3 linked=1 unmatched=1 stopped=0" {
		t.Fatalf("link run: %q %v", detail, err)
	}
	if p, _ := f.st.AlertPrefs(ctx, f.bob); p.TelegramChatID != "777" {
		t.Errorf("bob not linked: %+v", p)
	}
	if len(f.tg.sends) != 1 || f.tg.sends[0]["text"] != "Linked to SignalDeck daily read." {
		t.Errorf("reply: %+v", f.tg.sends)
	}
	if _, err := lw.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if got := f.tg.offsets; len(got) != 2 || got[0] != 0 || got[1] != 44 {
		t.Errorf("offsets %v, want [0 44]", got)
	}
}
