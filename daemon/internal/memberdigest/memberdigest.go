// Package memberdigest is the members' opt-in daily read (plan step 5): one
// plain-text message per trading day listing the current regime forecasts for
// the stocks a member watches, by email and/or a linked Telegram chat.
//
// PUBLISHER LINE (docs/PUBLISHER_GUARDRAILS.md). The read is a filtered view of
// the same forecast table every member sees: a symbol's block is built from
// the symbol and the shared rows only, worded identically for everyone. It is
// DERIVED ONLY: regime labels, conviction bands and backtest accuracy, never a
// price, close, volume, return, market cap or headline. Crypto is not part of
// the member product and never appears.
package memberdigest

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nyaungnicholas-wq/signaldeck/internal/marketcal"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
	"github.com/nyaungnicholas-wq/signaldeck/internal/workers"
)

// Facts are the shared inputs of one run, read once and used for every member.
type Facts struct {
	Current []store.RegimeForecast
	Calls   []store.RegimeWeekCall // frozen calls of the last 24h, oldest first
	Vol     []store.VolForecast
}

// LoadFacts reads the current forecasts and the calls frozen in the 24h
// before now (the weekly digest's earliest-call logic, over a day).
func LoadFacts(ctx context.Context, st Store, now time.Time) (Facts, error) {
	var f Facts
	var err error
	if f.Current, err = st.RegimeForecasts(ctx); err != nil {
		return f, err
	}
	if f.Calls, err = st.RegimeOutcomeCallsSince(ctx, now.Add(-24*time.Hour).Unix()); err != nil {
		return f, err
	}
	f.Vol, err = st.VolForecasts(ctx)
	return f, err
}

// Digest is one composed message.
type Digest struct {
	Subject, Body    string
	Watched, Changed int
}

// Compose renders the read for one watchlist. ok is false when the list holds
// no stock (nothing to send). unsubURL "" omits the unsubscribe line (Telegram),
// base "" omits the link to the site.
func Compose(f Facts, watched []md.Symbol, base, unsubURL string) (Digest, bool) {
	seen := map[string]bool{}
	var syms []md.Symbol
	for _, s := range watched {
		key := string(s.Market) + "|" + s.Symbol
		if s.Market == md.Crypto || seen[key] {
			continue
		}
		seen[key] = true
		syms = append(syms, s)
	}
	if len(syms) == 0 {
		return Digest{}, false
	}
	sort.Slice(syms, func(i, j int) bool {
		if syms[i].Symbol != syms[j].Symbol {
			return syms[i].Symbol < syms[j].Symbol
		}
		return syms[i].Market < syms[j].Market
	})

	// The earliest frozen call per (symbol, market, kind) in the window; Calls
	// is oldest first, so keep the first seen. Market is in the key: a stock
	// and a futures contract can share a ticker.
	type sk struct{ sym, market, kind string }
	earliest := map[sk]string{}
	for _, c := range f.Calls {
		k := sk{c.Symbol, c.Market, string(c.Kind)}
		if _, ok := earliest[k]; !ok {
			earliest[k] = c.Regime
		}
	}

	d := Digest{Watched: len(syms)}
	blocks := make([]string, 0, len(syms))
	for _, s := range syms {
		var rows []store.RegimeForecast
		for _, r := range f.Current {
			if r.Symbol == s.Symbol && r.Market == string(s.Market) && r.Market != string(md.Crypto) {
				rows = append(rows, r)
			}
		}
		sort.Slice(rows, func(i, j int) bool { return rows[i].Kind < rows[j].Kind })
		var lines []string
		changed := false
		for _, r := range rows {
			line := fmt.Sprintf("  %s: %s (conviction %s, backtest accuracy %.1f%%)",
				r.Kind, r.Regime, r.Tier, r.HistoricalAccuracy*100)
			if from, ok := earliest[sk{s.Symbol, string(s.Market), string(r.Kind)}]; ok && from != r.Regime {
				line += " - changed from " + from
				changed = true
			}
			lines = append(lines, line)
		}
		for _, v := range f.Vol {
			if v.Symbol == s.Symbol && v.Market == string(s.Market) && v.Market != string(md.Crypto) {
				lines = append(lines, fmt.Sprintf("  vol63: %s (conviction %s, backtest accuracy %.1f%%)",
					v.Regime, v.Tier, v.HistoricalAccuracy*100))
				break
			}
		}
		if len(lines) == 0 {
			lines = []string{"  no current forecast"}
		}
		if changed {
			d.Changed++
		}
		head := s.Symbol
		if s.Market != md.Stocks { // a futures contract may share a stock's ticker
			head += " (" + string(s.Market) + ")"
		}
		blocks = append(blocks, head+"\n"+strings.Join(lines, "\n"))
	}
	d.Subject = fmt.Sprintf("SignalDeck daily read: %d watched, %d changed", d.Watched, d.Changed)
	footer := []string{
		"These are statistical classifiers trained on price and volume history, not an adviser.",
		"Same forecasts for every member; not personalised advice; backtest accuracy is hypothetical and not a promise of results.",
	}
	if base != "" {
		footer = append(footer, "Today's read: "+base+"/today")
	}
	if unsubURL != "" {
		footer = append(footer, "Unsubscribe: "+unsubURL)
	}
	d.Body = "Today's regime forecasts for the symbols on your SignalDeck watchlist.\n\n" +
		strings.Join(blocks, "\n\n") + "\n\n" + strings.Join(footer, "\n") + "\n"
	return d, true
}

// codeAlphabet has 32 symbols with no 0/O or 1/I, so byte%32 is unbiased and
// a code survives being read aloud.
const codeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// NewLinkCode draws an 8-character Telegram link code.
func NewLinkCode() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand does not fail on supported platforms
	}
	for i := range b {
		b[i] = codeAlphabet[int(b[i])%len(codeAlphabet)]
	}
	return string(b)
}

// botCommand splits "/cmd@BotName rest" or "/cmd rest" into ("/cmd", rest).
// Telegram appends @BotName to commands typed in groups or picked from the
// menu, so both spellings must mean the same thing.
func botCommand(text string) (cmd, rest string) {
	t := strings.TrimSpace(text)
	if !strings.HasPrefix(t, "/") {
		return "", t
	}
	head, rest, _ := strings.Cut(t, " ")
	head, _, _ = strings.Cut(head, "@")
	return strings.ToLower(head), strings.TrimSpace(rest)
}

// ParseLinkCode extracts a link code from "/start CODE", "/start@Bot CODE" or
// "CODE"; "" if the text is not one.
func ParseLinkCode(text string) string {
	t := strings.TrimSpace(text)
	if cmd, rest := botCommand(t); cmd == "/start" {
		t = rest
	} else if cmd != "" {
		return ""
	}
	t = strings.ToUpper(t)
	if len(t) != 8 {
		return ""
	}
	for i := 0; i < len(t); i++ {
		if strings.IndexByte(codeAlphabet, t[i]) < 0 {
			return ""
		}
	}
	return t
}

// Telegram is a minimal Bot API client. Every error it returns has the token
// removed: the token is in the request URL, which net/http errors quote.
type Telegram struct {
	Token   string
	APIBase string       // "" = https://api.telegram.org
	Client  *http.Client // nil = 5s-timeout client
}

// APIError is a Bot API refusal. Status 403 means the user blocked the bot or
// removed it from the chat: that chat will never accept a message again.
type APIError struct {
	Status int
	msg    string // already redacted
}

func (e *APIError) Error() string { return e.msg }

func (t *Telegram) redact(s string) string {
	if t.Token == "" {
		return s
	}
	return strings.ReplaceAll(s, t.Token, "[redacted]")
}

func (t *Telegram) call(ctx context.Context, method string, payload, out any) error {
	err := t.do(ctx, method, payload, out)
	var api *APIError
	if errors.As(err, &api) {
		return &APIError{Status: api.Status, msg: t.redact(api.msg)}
	}
	if err != nil {
		return errors.New(t.redact(err.Error()))
	}
	return nil
}

func (t *Telegram) do(ctx context.Context, method string, payload, out any) error {
	base := t.APIBase
	if base == "" {
		base = "https://api.telegram.org"
	}
	client := t.Client
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimSuffix(base, "/")+"/bot"+t.Token+"/"+method, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close() //nolint:errcheck
	var env struct {
		OK          bool            `json:"ok"`
		Result      json.RawMessage `json:"result"`
		Description string          `json:"description"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(nil, res.Body, 4<<20)).Decode(&env); err != nil {
		return fmt.Errorf("telegram %s: HTTP %d: %w", method, res.StatusCode, err)
	}
	if res.StatusCode >= 300 || !env.OK {
		return &APIError{Status: res.StatusCode,
			msg: fmt.Sprintf("telegram %s: HTTP %d: %s", method, res.StatusCode, env.Description)}
	}
	if out != nil {
		return json.Unmarshal(env.Result, out)
	}
	return nil
}

// Send posts text to one chat.
func (t *Telegram) Send(ctx context.Context, chatID, text string) error {
	if len(text) > 3900 { // Telegram's cap is 4096; cut on a rune boundary
		cut := 3900
		for cut > 0 && !utf8.RuneStart(text[cut]) {
			cut--
		}
		text = text[:cut]
	}
	return t.call(ctx, "sendMessage", map[string]any{"chat_id": chatID, "text": text}, nil)
}

// chatGone reports a send refused because the user blocked or left the bot.
func chatGone(err error) bool {
	var api *APIError
	return errors.As(err, &api) && api.Status == http.StatusForbidden
}

// Sender delivers one email with extra headers (List-Unsubscribe).
type Sender func(ctx context.Context, to, subject, body string, headers map[string]string) error

// Store is what the digest worker reads and writes; *store.Store satisfies it.
type Store interface {
	DigestRecipients(ctx context.Context) ([]store.DigestRecipient, error)
	ListMemberSymbols(ctx context.Context, uid int64) ([]md.Symbol, error)
	ListUserSymbols(ctx context.Context, uid int64) ([]md.Symbol, error)
	CreateAuthToken(ctx context.Context, uid int64, kind string, ttl time.Duration) (string, error)
	MarkDigestSent(ctx context.Context, uid int64, day string) error
	PurgeExpiredTokens(ctx context.Context, kind string, now time.Time) error
	UnlinkTelegram(ctx context.Context, uid int64) error
	RegimeForecasts(ctx context.Context) ([]store.RegimeForecast, error)
	RegimeOutcomeCallsSince(ctx context.Context, since int64) ([]store.RegimeWeekCall, error)
	VolForecasts(ctx context.Context) ([]store.VolForecast, error)
	DigestTries(ctx context.Context, day string) ([]store.DigestTry, error)
	SaveDigestTry(ctx context.Context, t store.DigestTry) error
	PruneDigestTries(ctx context.Context, day string) error
}

const (
	defaultCap     = 500 // members per pass; the rest follow 5 minutes later
	unsubscribeTTL = 60 * 24 * time.Hour
	fireHourET     = 8
	lastHourET     = 16 // no sends from 16:00 ET on: a catch-up never mails at night
	maxTries       = 2  // attempts per member per channel per day
	retryAfter     = 30 * time.Minute
	chEmail        = "email"
	chTelegram     = "telegram"
)

type chanKey struct {
	uid int64
	ch  string
}

// Worker is "member-digest": trading days 08:00-16:00 ET, one read per member
// per day on each channel they enabled.
type Worker struct {
	St        Store
	Base      func() string // the CONFIGURED public URL; "" = no email, no links
	MailReady func() bool
	Mail      Sender
	Telegram  *Telegram // nil = no Telegram channel
	Now       func() time.Time
	Cap       int // 0 = defaultCap

	deferred int // members the last pass left for the next one
	retry    int // members with a channel that failed and has a try left

	// Per-day state (reloaded from the store when the ET date changes). ok
	// holds each channel delivered today, so it is never sent twice in a day
	// even when MarkDigestSent fails. Every change is mirrored into
	// member_digest_tries (save), so a restart neither re-sends a delivered
	// channel nor grants fresh tries past maxTries.
	day   string
	tries map[chanKey]int
	ok    map[chanKey]bool
}

func (w *Worker) Name() string            { return "member-digest" }
func (w *Worker) Interval() time.Duration { return 24 * time.Hour }

// NextFire is the next trading day 08:00 ET; at once when the daemon was down
// across a slot (the scheduler never runs a worker at boot by itself); five
// minutes out while a capped pass left members unsent; 30 minutes out while a
// failed channel has a try left.
func (w *Worker) NextFire(last, now time.Time) time.Time {
	switch {
	case w.deferred > 0:
		return now.Add(5 * time.Minute)
	case w.retry > 0:
		return now.Add(retryAfter)
	}
	return workers.TradingDayAtETCatchUp(last, now, fireHourET, 0)
}

func (w *Worker) Run(ctx context.Context) (detail string, err error) {
	now := time.Now()
	if w.Now != nil {
		now = w.Now()
	}
	var sent, failed, skipped, deferred, retry int
	// Whatever path this run leaves by, the follow-up schedule matches what it
	// actually left undone (a stale deferred count re-fired a finished pass).
	defer func() {
		if err != nil {
			retry = 1 // a store error is usually transient: try again in 30 min
		}
		w.deferred, w.retry = deferred, retry
	}()
	et := now.In(marketcal.Loc())
	// A catch-up fire can land on any day or hour; the gate, not NextFire, is
	// what keeps a weekend restart or a late-evening boot from mailing anyone.
	if !marketcal.IsTradingDay(et) || et.Hour() < fireHourET || et.Hour() >= lastHourET {
		return "waiting (sends trading days 08:00-16:00 ET)", nil
	}
	day := et.Format("2006-01-02")
	if w.day != day {
		rows, err := w.St.DigestTries(ctx, day)
		if err != nil {
			return "", err
		}
		w.day, w.tries, w.ok = day, map[chanKey]int{}, map[chanKey]bool{}
		for _, r := range rows {
			k := chanKey{r.UserID, r.Channel}
			w.tries[k], w.ok[k] = r.Tries, r.Delivered
		}
		if err := w.St.PruneDigestTries(ctx, day); err != nil {
			slog.Warn("member-digest: earlier days' try rows not pruned", "err", err)
		}
	}
	base := ""
	if w.Base != nil {
		base = w.Base()
	}
	emailOK := base != "" && w.MailReady != nil && w.MailReady() && w.Mail != nil
	tgOK := w.Telegram != nil
	if !emailOK && !tgOK {
		return "skipped: no transport", nil
	}
	if err := w.St.PurgeExpiredTokens(ctx, store.TokenUnsubscribe, now); err != nil {
		slog.Warn("member-digest: expired unsubscribe token purge failed", "err", err)
	}
	recipients, err := w.St.DigestRecipients(ctx)
	if err != nil {
		return "", err
	}
	facts, err := LoadFacts(ctx, w.St, now)
	if err != nil {
		return "", err
	}
	capN := w.Cap
	if capN <= 0 {
		capN = defaultCap
	}
	for _, r := range recipients {
		if r.LastDigestDay == day {
			skipped++
			continue
		}
		var pending []string
		for _, ch := range []string{chEmail, chTelegram} {
			on := ch == chEmail && emailOK && r.Email != "" || ch == chTelegram && tgOK && r.ChatID != ""
			k := chanKey{r.UserID, ch}
			if on && !w.ok[k] && w.tries[k] < maxTries {
				pending = append(pending, ch)
			}
		}
		if len(pending) == 0 {
			skipped++
			continue
		}
		if sent+failed >= capN {
			deferred++
			continue
		}
		for _, ch := range pending {
			k := chanKey{r.UserID, ch}
			w.tries[k]++ // spent in memory even if the save fails: bounded in this process
			// Recorded BEFORE the send: an unrecorded try would come back after
			// a restart. A failed save sends nothing and retries the pass.
			if err := w.save(ctx, k); err != nil {
				return fmt.Sprintf("sent=%d failed=%d skipped=%d deferred=%d", sent, failed, skipped, deferred), err
			}
		}
		delivered := w.deliver(ctx, r, pending, facts, base)
		if delivered < 0 { // nothing watched: no read (the spent try bounds the rechecks)
			skipped++
			continue
		}
		if delivered > 0 {
			sent++
		} else {
			failed++
		}
		left := false
		for _, ch := range pending {
			k := chanKey{r.UserID, ch}
			if !w.ok[k] && w.tries[k] < maxTries {
				left = true
			}
		}
		if left {
			retry++
			continue
		}
		// Every enabled channel succeeded or spent its tries: done for today.
		if err := w.St.MarkDigestSent(ctx, r.UserID, day); err != nil {
			slog.Warn("member-digest: could not record today's send; held in memory", "uid", r.UserID, "err", err)
		}
	}
	if deferred > 0 {
		slog.Warn("member-digest: per-run send cap reached; the rest go out on the next pass",
			"cap", capN, "deferred", deferred)
	}
	return fmt.Sprintf("sent=%d failed=%d skipped=%d deferred=%d", sent, failed, skipped, deferred), nil
}

// deliver sends today's read on each pending channel and returns how many
// succeeded, or -1 when the member watches no stock. Errors are logged and
// leave the channel unsent; they never abort the pass.
func (w *Worker) deliver(ctx context.Context, r store.DigestRecipient, pending []string, facts Facts, base string) int {
	watched, err := w.St.ListMemberSymbols(ctx, r.UserID)
	if err != nil {
		slog.Warn("member-digest: watchlist read failed", "uid", r.UserID, "err", err)
		return 0
	}
	own, err := w.St.ListUserSymbols(ctx, r.UserID) // an operator's list
	if err != nil {
		slog.Warn("member-digest: watchlist read failed", "uid", r.UserID, "err", err)
		return 0
	}
	watched = append(watched, own...)
	if _, ok := Compose(facts, watched, base, ""); !ok {
		return -1
	}
	n := 0
	for _, ch := range pending {
		k := chanKey{r.UserID, ch}
		switch ch {
		case chEmail:
			tok, err := w.St.CreateAuthToken(ctx, r.UserID, store.TokenUnsubscribe, unsubscribeTTL)
			if err != nil {
				slog.Warn("member-digest: unsubscribe token failed", "uid", r.UserID, "err", err)
				continue
			}
			unsub := base + "/api/alerts/unsubscribe?token=" + tok
			d, _ := Compose(facts, watched, base, unsub)
			// RFC 8058 one-click: the mail client POSTs List-Unsubscribe=One-Click
			// to this URL, which the daemon redeems without a session.
			hdr := map[string]string{"List-Unsubscribe": "<" + unsub + ">",
				"List-Unsubscribe-Post": "List-Unsubscribe=One-Click"}
			if err := w.Mail(ctx, r.Email, d.Subject, d.Body, hdr); err != nil {
				slog.Warn("member-digest: email failed", "uid", r.UserID, "err", err)
				continue
			}
		case chTelegram:
			d, _ := Compose(facts, watched, base, "")
			if err := w.Telegram.Send(ctx, r.ChatID, d.Subject+"\n\n"+d.Body); err != nil {
				slog.Warn("member-digest: telegram failed", "uid", r.UserID, "err", err)
				if chatGone(err) {
					// Blocked or kicked: this chat will refuse forever.
					w.tries[k] = maxTries
					if err := w.save(ctx, k); err != nil {
						slog.Warn("member-digest: spent tries not recorded; held in memory", "uid", r.UserID, "err", err)
					}
					if err := w.St.UnlinkTelegram(ctx, r.UserID); err != nil {
						slog.Warn("member-digest: unlink after 403 failed", "uid", r.UserID, "err", err)
					}
				}
				continue
			}
		}
		w.ok[k] = true
		if err := w.save(ctx, k); err != nil {
			slog.Warn("member-digest: delivery not recorded; held in memory", "uid", r.UserID, "channel", ch, "err", err)
		}
		n++
	}
	return n
}

// save mirrors one channel's state for today into member_digest_tries, which
// Run reloads after a restart.
func (w *Worker) save(ctx context.Context, k chanKey) error {
	return w.St.SaveDigestTry(ctx, store.DigestTry{UserID: k.uid, Day: w.day, Channel: k.ch,
		Tries: w.tries[k], Delivered: w.ok[k]})
}

const metaTelegramOffset = "telegram_link_offset"

// LinkWorker is "telegram-link": it polls the bot for "/start CODE" messages
// and binds each matching chat to the member who drew the code; "/stop" from
// a linked chat unlinks it.
type LinkWorker struct {
	St       *store.Store
	Telegram *Telegram
	Now      func() time.Time
}

func (lw *LinkWorker) Name() string            { return "telegram-link" }
func (lw *LinkWorker) Interval() time.Duration { return time.Minute }

func (lw *LinkWorker) Run(ctx context.Context) (string, error) {
	now := time.Now()
	if lw.Now != nil {
		now = lw.Now()
	}
	raw, err := lw.St.GetMeta(ctx, metaTelegramOffset)
	if err != nil {
		return "", err
	}
	offset, _ := strconv.ParseInt(raw, 10, 64)
	var updates []struct {
		UpdateID int64 `json:"update_id"`
		Message  *struct {
			Chat struct {
				ID int64 `json:"id"`
			} `json:"chat"`
			Text string `json:"text"`
		} `json:"message"`
	}
	if err := lw.Telegram.call(ctx, "getUpdates", map[string]any{
		"offset": offset, "timeout": 0, "allowed_updates": []string{"message"},
	}, &updates); err != nil {
		return "", err
	}
	next := offset
	linked, unmatched, stopped := 0, 0, 0
	for _, u := range updates {
		if u.UpdateID+1 > next {
			next = u.UpdateID + 1
		}
		if u.Message == nil {
			continue
		}
		// Message text is never logged: only what it matched.
		chat := strconv.FormatInt(u.Message.Chat.ID, 10)
		if cmd, _ := botCommand(u.Message.Text); cmd == "/stop" {
			ok, err := lw.St.UnlinkTelegramChat(ctx, chat)
			if err != nil {
				return "", err
			}
			if ok {
				stopped++
				if err := lw.Telegram.Send(ctx, chat, "Unlinked. No more SignalDeck daily reads here."); err != nil {
					slog.Warn("telegram-link: reply failed", "err", err)
				}
			}
			continue
		}
		code := ParseLinkCode(u.Message.Text)
		if code == "" {
			continue
		}
		_, ok, err := lw.St.LinkTelegramByCode(ctx, code, chat, now)
		if err != nil {
			return "", err
		}
		if !ok {
			unmatched++
			continue
		}
		linked++
		if err := lw.Telegram.Send(ctx, chat, "Linked to SignalDeck daily read."); err != nil {
			slog.Warn("telegram-link: reply failed", "err", err)
		}
	}
	if next != offset {
		if err := lw.St.SetMeta(ctx, metaTelegramOffset, strconv.FormatInt(next, 10)); err != nil {
			return "", err
		}
	}
	return fmt.Sprintf("updates=%d linked=%d unmatched=%d stopped=%d", len(updates), linked, unmatched, stopped), nil
}
