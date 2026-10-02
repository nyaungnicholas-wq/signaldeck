package api

// Public accounts (2026-09-29): sign-up with email verification, password
// reset, and the MEMBER tier that makes opening sign-up safe.
//
// THE MEMBER TIER IS THE LOAD-BEARING PART. Before it, every authenticated
// account had identical authority: 159 of 161 routes answered any session,
// including the LLM analyst (spends the provider budget), symbol subscription
// (starts global ingestion), backtests and operator diagnostics. Opening
// sign-up on that surface would have handed every stranger the operator's
// console. A member now reaches exactly what an anonymous visitor reaches on a
// published deployment, plus memberRoutes; everything else is admin-only.
//
// Anti-abuse on the unauthenticated writes here, in order of cost to an
// attacker: a honeypot field (free bots), a per-client window limit, per-email
// mail limits, and Cloudflare Turnstile when SIGNALDECK_TURNSTILE_SECRET is set.
// Responses that could reveal whether an email has an account are identical
// either way (resend, forgot, and a sign-up on a taken address).

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

const (
	verifyTTL = 24 * time.Hour
	resetTTL  = time.Hour
)

// memberRoutes are the only non-public routes a non-admin account may call on
// a published deployment. Every entry serves derived analytics or public-domain
// filings, never a vendor price, bar, volume, quote or headline, and none spends
// LLM budget but /api/ask (flag-gated, capped). TestMemberRoutesServeNoLicensedData pins this against
// datalicense.RestrictedRoutes and against the MIXED routes measured
// 2026-09-30 (dashboard, screener, symbol, signal-report ...), whose "derived"
// payloads still carry closes, volumes or price-quoting notes.
//
// Three entries are safe only because their handlers branch on isMember:
// /api/watchlist drops closes, sparks, day change and score notes;
// /api/companies drops price, volume and market cap, and ignores the mcap
// filters that would let a caller bisect a price; /api/company/profile never
// runs its LLM summary for a member.
var memberRoutes = map[string]bool{
	// The member's own list. watch/unwatch touch ONLY that list: /api/subscribe
	// starts global ingestion and /api/unsubscribe deactivates a feed when its
	// last watcher leaves, so neither is here.
	"/api/watchlist": true, "/api/watch": true, "/api/unwatch": true,
	"/api/companies": true,
	// Validated regime and volatility forecasts, each with its measured accuracy.
	"/api/regimes": true, "/api/vol-regime": true, "/api/market-regimes": true,
	"/api/symbol-agent": true,
	// Public-domain intel: SEC EDGAR filings, Form 4, 13F and XBRL fundamentals,
	// FINRA short data, STOCK Act disclosures. The two FINRA routes are listed
	// so every member-surface test scans them, but the gate keeps them
	// operator-only unless SIGNALDECK_MEMBER_FINRA=1 (memberFINRARoutes).
	"/api/company/profile": true, "/api/filings": true, "/api/insiders": true,
	"/api/institutions": true, "/api/dilution": true, "/api/fundamentals": true,
	"/api/short-interest": true, "/api/shorts": true, "/api/congress": true,
	// The member's own alert switches (plan step 5): on/off and a Telegram
	// link code, never an input that could personalise what is sent.
	"/api/alert-prefs": true, "/api/alert-prefs/telegram-link": true,
	"/api/alert-prefs/telegram-unlink": true,
	// The member's own call journal (plan step 8): their calls and their
	// grade, never a price or return, never an input to SignalDeck's forecasts.
	"/api/journal": true, "/api/journal/withdraw": true, "/api/journal/symbols": true,
	// Ask the data (plan step 10): gated INSIDE the handler (403 until
	// SIGNALDECK_MEMBER_COPILOT=1), member catalog entries only, so this one
	// does spend LLM budget, capped per member per day (ask.go).
	"/api/ask": true,
	// The current HAR volatility forecasts (plan step 9): symbol, horizon,
	// date and annualised vol only; no null, coefficient or outcome.
	"/api/vol-forecast/latest": true,
}

// isMember is a signed-in account that is not the operator on a published
// deployment: the caller the three branching handlers above strip fields for.
// An anonymous caller is NOT a member; on a published deployment it never
// reaches those handlers, and a private deployment's anonymous reads keep their
// old shape.
func (d Deps) isMember(r *http.Request) bool {
	return userID(r) != 0 && !d.isOperator(r)
}

// Crypto is not part of the member product: the owner chose US stocks/ETFs and
// futures, and Kraken's terms restrict derived works (datalicense.go,
// cryptohist). So memberRoutes handlers drop market=crypto rows from lists and
// refuse single-symbol crypto lookups for a member. The operator and the
// public proof record (publicRoutes) are unchanged.
const cryptoNotForMembers = "crypto is not covered for members"

// refuseMemberCrypto answers 404 and returns true when a member asks a member
// route about a crypto symbol.
func (d Deps) refuseMemberCrypto(w http.ResponseWriter, r *http.Request, m md.Market) bool {
	if m != md.Crypto || !d.isMember(r) {
		return false
	}
	httpErr(w, http.StatusNotFound, cryptoNotForMembers)
	return true
}

// Window limiters for the unauthenticated account writes. Package-level so all
// requests share them; per-process is enough for a single-instance daemon.
var (
	signupLimiter = newWindowLimiter(5, time.Hour)  // accounts created per client
	acctIPLimiter = newWindowLimiter(20, time.Hour) // resend/forgot per client
	mailLimiter   = newWindowLimiter(3, time.Hour)  // confirmation emails per address
	resetLimiter  = newWindowLimiter(3, time.Hour)  // reset emails per address: resend spam cannot block a reset
	adminCache    = &cachedAdmin{}
	publicURLMemo = &memoString{ttl: 15 * time.Second}
)

// Seams for tests: mail and the human check are external services.
var (
	sendAccountEmail = func(d Deps, ctx context.Context, to, subject, body string) error {
		return d.Notifier.SendEmail(ctx, to, subject, body)
	}
	mailReady          = func(d Deps) bool { return d.Notifier.MailReady() }
	turnstileVerifyURL = "https://challenges.cloudflare.com/turnstile/v0/siteverify"
)

// ── limiter ────────────────────────────────────────────────────────────────

type windowLimiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	hits   map[string][]time.Time
}

func newWindowLimiter(max int, window time.Duration) *windowLimiter {
	return &windowLimiter{max: max, window: window, hits: map[string][]time.Time{}}
}

func (l *windowLimiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	cut := now.Add(-l.window)
	kept := l.hits[key][:0]
	for _, t := range l.hits[key] {
		if t.After(cut) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= l.max {
		l.hits[key] = kept
		return false
	}
	l.hits[key] = append(kept, now)
	if len(l.hits) > 50000 { // bound memory under a spray of distinct keys
		for k, v := range l.hits {
			if len(v) == 0 || v[len(v)-1].Before(cut) {
				delete(l.hits, k)
			}
		}
		// Still over after the sweep means a live spray of fresh keys. Drop
		// the table rather than grow without bound: briefly forgetting budgets
		// is the lesser harm, and the other limiters still apply.
		if len(l.hits) > 50000 {
			l.hits = map[string][]time.Time{key: {now}}
		}
	}
	return true
}

// ── admin + published ──────────────────────────────────────────────────────

type cachedAdmin struct {
	mu sync.Mutex
	st *store.Store // the cache is only valid for the store it was read from
	id int64
	at time.Time
}

func (d Deps) isAdminUID(ctx context.Context, uid int64) bool {
	adminCache.mu.Lock()
	defer adminCache.mu.Unlock()
	if adminCache.st != d.St || time.Since(adminCache.at) > time.Minute || adminCache.id == 0 {
		id, err := d.St.AdminUserID(ctx)
		if err != nil {
			return false // fail closed: a member gate that opens on a DB error is no gate
		}
		adminCache.st, adminCache.id, adminCache.at = d.St, id, time.Now()
	}
	return adminCache.id != 0 && uid == adminCache.id
}

// published is true whenever strangers can reach the daemon: an explicit public
// surface, or a tunnel/allowlisted host (reachablePrivately false).
func (d Deps) published() bool {
	// A configured public URL or quick-tunnel log is an explicit statement that
	// strangers reach this daemon. Relying on ReachablePrivately() alone made
	// the whole member tier hinge on a stale hostname left in ALLOWED_HOSTS:
	// deleting it would have silently opened operator authority to sign-ups.
	return d.Cfg.PublicSurface || d.Cfg.PublicURL != "" || d.Cfg.TunnelLog != "" ||
		!d.Cfg.ReachablePrivately()
}

// isOperator is true for the admin (or API token) on a published deployment,
// and for any signed-in user on a private one: the pre-accounts meaning of
// "authenticated". Use it wherever a handler treated any session as the
// operator. Members are authenticated, but they are not the operator.
func (d Deps) isOperator(r *http.Request) bool {
	uid := userID(r)
	if uid == 0 {
		return false
	}
	return !d.published() || d.isAdminUID(r.Context(), uid)
}

// acctKey is the limiter key for the account endpoints: the client key, with an
// IPv6 address folded to its /64 so one allocation cannot mint unlimited buckets.
func acctKey(key string) string {
	ip := net.ParseIP(clientIP(key))
	if ip == nil || ip.To4() != nil {
		return key
	}
	return "ip6:" + ip.Mask(net.CIDRMask(64, 128)).String()
}

func memberAllowed(path string) bool {
	return alwaysOpen(path) || publicRoutes[path] || memberRoutes[path] ||
		strings.HasPrefix(path, "/api/evidence/")
}

// memberFINRARoutes serve FINRA short data, whose terms may not permit passing
// it on to members (owner's call, 2026-10-02: close to members). They stay in
// memberRoutes, so the member-surface tests scan them, and the member gate
// (security.go 6b) refuses them unless SIGNALDECK_MEMBER_FINRA=1.
var memberFINRARoutes = map[string]bool{"/api/shorts": true, "/api/short-interest": true}

// memberMay is the member gate's answer for path on this deployment.
func (d Deps) memberMay(path string) bool {
	return memberAllowed(path) && (d.Cfg.MemberFINRA || !memberFINRARoutes[path])
}

// ── public URL (email links + dynamic origin) ──────────────────────────────

type memoString struct {
	mu  sync.Mutex
	val string
	at  time.Time
	ttl time.Duration
}

// Only cloudflared's own banner line (|  https://x.trycloudflare.com  |),
// never a URL that merely appears in the log, such as a requested path.
var trycloudflareRe = regexp.MustCompile(`\|\s+(https://[a-z0-9-]+\.trycloudflare\.com)\s+\|`)

// publicBase is the origin emailed links point at: SIGNALDECK_PUBLIC_URL when
// set, else the newest quick-tunnel URL in SIGNALDECK_TUNNEL_LOG ("" = unknown).
func (d Deps) publicBase() string {
	if d.Cfg.PublicURL != "" {
		return d.Cfg.PublicURL
	}
	if d.Cfg.TunnelLog == "" {
		return ""
	}
	publicURLMemo.mu.Lock()
	defer publicURLMemo.mu.Unlock()
	if time.Since(publicURLMemo.at) < publicURLMemo.ttl {
		return publicURLMemo.val
	}
	publicURLMemo.val, publicURLMemo.at = lastTunnelURL(d.Cfg.TunnelLog), time.Now()
	return publicURLMemo.val
}

func lastTunnelURL(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close() //nolint:errcheck
	st, err := f.Stat()
	if err != nil {
		return ""
	}
	// The newest banner ANYWHERE, not only in the last 256 KB: an unrotated log
	// on a box up 24/7 scrolls the banner out of the tail in ~8 days, and with
	// no public origin every login POST fails the origin check. Walk back one
	// chunk at a time, searching whole lines only; the partial line at a
	// chunk's start is carried into the next (earlier) chunk.
	const chunk = 256 << 10
	var carry []byte
	for end := st.Size(); end > 0; {
		start := max(0, end-chunk)
		buf := make([]byte, end-start, end-start+int64(len(carry)))
		if _, err := f.ReadAt(buf, start); err != nil {
			return ""
		}
		buf = append(buf, carry...)
		cut := 0 // buf[:cut] is the tail of a line that began in an earlier chunk
		if start > 0 {
			if cut = bytes.IndexByte(buf, '\n') + 1; cut == 0 {
				carry, end = buf, start // one line spans this whole chunk
				continue
			}
		}
		if all := trycloudflareRe.FindAllSubmatch(buf[cut:], -1); len(all) > 0 {
			return string(all[len(all)-1][1])
		}
		carry, end = buf[:cut], start
	}
	return ""
}

// originsNow is the CORS/CSRF origin allowlist for this request: the configured
// origins plus the current public URL, which for a quick tunnel changes on
// every restart and so cannot be written into the environment ahead of time.
func (d Deps) originsNow() []string {
	base := d.publicBase()
	if base == "" {
		return d.Cfg.WebOrigins
	}
	out := make([]string, 0, len(d.Cfg.WebOrigins)+1)
	out = append(out, d.Cfg.WebOrigins...)
	return append(out, base)
}

// ── turnstile ──────────────────────────────────────────────────────────────

var turnstileOffOnce sync.Once

// turnstileOK verifies a Turnstile token. With no secret configured the check
// is skipped (logged once per process), so the site works before the operator
// has created the widget; the honeypot and limiters still apply.
func (d Deps) turnstileOK(ctx context.Context, token, remoteIP string) bool {
	if d.Cfg.TurnstileSecret == "" {
		// The comment above promised a log; until 2026-09-30 there was none,
		// so a public daemon ran sign-up and reset with no bot check silently.
		turnstileOffOnce.Do(func() {
			slog.Warn("turnstile: no secret configured — sign-up and reset are guarded only by the honeypot and rate limiters")
		})
		return true
	}
	if token == "" || len(token) > 2048 {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	form := url.Values{"secret": {d.Cfg.TurnstileSecret}, "response": {token}}
	if remoteIP != "" {
		form.Set("remoteip", remoteIP)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, turnstileVerifyURL, strings.NewReader(form.Encode()))
	if err != nil {
		return false
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		slog.Warn("turnstile: verify unreachable, refusing", "err", err)
		return false // fail closed: an unverifiable human check is a failed one
	}
	defer res.Body.Close() //nolint:errcheck
	var out struct {
		Success bool `json:"success"`
	}
	if json.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(&out) != nil {
		return false
	}
	return out.Success
}

func clientIP(key string) string {
	if strings.HasPrefix(key, "ip:") {
		return strings.TrimPrefix(key, "ip:")
	}
	return ""
}

// ── handlers ───────────────────────────────────────────────────────────────

type signupBody struct {
	Username       string `json:"username"`
	Password       string `json:"password"`
	Email          string `json:"email"`
	TurnstileToken string `json:"turnstileToken"`
	Website        string `json:"website"` // honeypot
}

const verifySent = "verify-sent"

// authRegister creates an account. The FIRST account ever created becomes the
// admin with no email step (bootstrapping a fresh install). Every account after
// that is a member: it needs a verified email before it can sign in.
func (d Deps) authRegister(w http.ResponseWriter, r *http.Request) {
	if !d.Cfg.OpenSignup {
		httpErr(w, 403, "registration is closed")
		return
	}
	var body signupBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpErr(w, 400, "bad json")
		return
	}
	// Honeypot: humans never see the field. Answer exactly like success so a
	// bot learns nothing, and create nothing.
	if body.Website != "" {
		writeJSON(w, map[string]string{"status": verifySent})
		return
	}
	// A private (loopback, untunnelled) daemon keeps the original behaviour:
	// immediate account + session, no email step. Nobody but the operator can
	// reach it, and the test suite and local dev depend on it.
	if !d.published() {
		d.registerPrivate(w, r, credsBody{Username: body.Username, Password: body.Password})
		return
	}
	key := d.clientKey(r, 0)
	if !signupLimiter.allow(acctKey(key)) {
		w.Header().Set("Retry-After", "3600")
		httpErr(w, http.StatusTooManyRequests, "too many sign-ups from this network — try again later")
		return
	}
	creds := credsBody{Username: body.Username, Password: body.Password}
	if msg := creds.validate(); msg != "" {
		httpErr(w, 400, msg)
		return
	}
	ctx := r.Context()
	n, err := d.St.CountUsers(ctx)
	if err != nil {
		httpInternal(w, err)
		return
	}
	if n == 0 {
		// Never bootstrap the admin over the internet: on an empty or restored
		// database the first stranger to POST would become the operator.
		httpErr(w, 403, "no admin account exists; create it on the server itself, not through the public site")
		return
	}

	email, ok := normaliseEmail(body.Email)
	if !ok {
		httpErr(w, 400, "enter a valid email address")
		return
	}
	// Gmail only (2026-09-30, Nicholas: "anyone with a gmail"): a Gmail address
	// costs a bot a Google account, where a throwaway domain costs nothing.
	// Folded to one spelling per inbox, so dots and +tags cannot multiply it.
	email, ok = canonicalEmail(email)
	if !ok {
		httpErr(w, 400, "sign-up takes Gmail addresses only (…@gmail.com)")
		return
	}
	base := d.publicBase()
	if !mailReady(d) || base == "" {
		httpErr(w, http.StatusServiceUnavailable, "sign-ups are temporarily unavailable — email is not set up yet")
		return
	}
	if !d.turnstileOK(ctx, body.TurnstileToken, clientIP(key)) {
		httpErr(w, 403, "the human check failed — reload the page and try again")
		return
	}
	// Unverified sign-ups older than their link are dead weight and a squatting
	// vector: without this, anyone could park an address forever.
	if err := d.St.PurgeStaleUnverified(ctx, time.Now().Add(-verifyTTL)); err != nil {
		slog.Warn("signup: stale unverified purge failed", "err", err)
	}
	if _, exists, err := d.St.GetUserByName(ctx, creds.Username); err != nil {
		httpInternal(w, err)
		return
	} else if exists {
		httpErr(w, 409, "that username is taken")
		return
	}
	// bcrypt runs on EVERY path below, before the taken/new split, so the two
	// answers cost the same time.
	hash, err := bcrypt.GenerateFromPassword([]byte(creds.Password), bcrypt.DefaultCost)
	if err != nil {
		httpInternal(w, err)
		return
	}
	if taken, err := d.St.EmailTaken(ctx, email); err != nil {
		httpInternal(w, err)
		return
	} else if taken {
		// Same answer as success; tell the real owner instead of the requester.
		if mailLimiter.allow(email) {
			d.sendAsync(email, "Your SignalDeck account",
				"Someone (hopefully you) tried to create a SignalDeck account with this email, but one already exists.\n\n"+
					"Sign in: "+base+"/login\nForgot your password? "+base+"/forgot\n\n"+
					"If this wasn't you, you can ignore this email.")
		}
		writeJSON(w, map[string]string{"status": verifySent})
		return
	}
	release := d.St.Priority() // a person is waiting: go ahead of the worker fleet
	defer release()
	uid, err := d.St.CreateUserWithEmail(ctx, creds.Username, email, string(hash))
	if err != nil {
		// A unique-index race on username or email; report it as taken.
		httpErr(w, 409, "that username or email is already registered")
		return
	}
	// Mail after answering, exactly like the taken path, so SMTP latency cannot
	// tell the two apart. A failed send is recovered with Resend.
	mailLimiter.allow(email)
	mailInBackground(func(ctx context.Context) {
		if err := d.sendVerify(ctx, uid, email, base); err != nil {
			slog.Warn("signup: verification email failed", "uid", uid, "err", err)
		}
	})
	writeJSON(w, map[string]string{"status": verifySent})
}

// registerPrivate is the pre-2026-09-29 register flow, used only when the
// daemon is not published: the first account is admin, every account gets a
// session immediately.
func (d Deps) registerPrivate(w http.ResponseWriter, r *http.Request, creds credsBody) {
	if msg := creds.validate(); msg != "" {
		httpErr(w, 400, msg)
		return
	}
	if _, exists, err := d.St.GetUserByName(r.Context(), creds.Username); err != nil {
		httpInternal(w, err)
		return
	} else if exists {
		httpErr(w, 409, "username taken")
		return
	}
	n, err := d.St.CountUsers(r.Context())
	if err != nil {
		httpInternal(w, err)
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(creds.Password), bcrypt.DefaultCost)
	if err != nil {
		httpInternal(w, err)
		return
	}
	uid, err := d.St.CreateUser(r.Context(), creds.Username, string(hash), n == 0)
	if err != nil {
		httpInternal(w, err)
		return
	}
	d.startSession(w, r, uid, creds.Username, n == 0)
}

func (d Deps) sendVerify(ctx context.Context, uid int64, email, base string) error {
	tok, err := d.St.CreateAuthToken(ctx, uid, store.TokenVerify, verifyTTL)
	if err != nil {
		return err
	}
	return sendAccountEmail(d, ctx, email, "Confirm your SignalDeck account",
		"Welcome to SignalDeck.\n\nConfirm your email to activate your account:\n"+
			base+"/verify?token="+tok+"\n\n"+
			"This link works once and expires in 24 hours. If you didn't sign up, ignore this email — "+
			"no account is activated without the click.")
}

// mailWG tracks the account-mail goroutines. They outlive the request by
// design (timing must not reveal whether an account exists), so a test that
// swaps sendAccountEmail or closes the store must Wait first.
var mailWG sync.WaitGroup

// mailInBackground runs send after the response, on its own 45s budget.
func mailInBackground(send func(ctx context.Context)) {
	mailWG.Add(1)
	go func() {
		defer mailWG.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		send(ctx)
	}()
}

// sendAsync mails without holding the request open, for the responses whose
// timing must not reveal whether an account exists.
func (d Deps) sendAsync(to, subject, body string) {
	mailInBackground(func(ctx context.Context) {
		if err := sendAccountEmail(d, ctx, to, subject, body); err != nil {
			slog.Warn("account email failed", "err", err)
		}
	})
}

type tokenBody struct {
	Token    string `json:"token"`
	Password string `json:"password"`
}

// authVerify redeems an emailed confirmation link and signs the user in.
func (d Deps) authVerify(w http.ResponseWriter, r *http.Request) {
	var body tokenBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpErr(w, 400, "bad json")
		return
	}
	if !acctIPLimiter.allow(acctKey(d.clientKey(r, 0))) {
		httpErr(w, http.StatusTooManyRequests, "too many attempts — try again later")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	r = r.WithContext(ctx)
	release := d.St.Priority()
	defer release()
	uid, err := d.St.VerifyEmail(ctx, strings.TrimSpace(body.Token)) // redeem + verify, one transaction
	if errors.Is(err, store.ErrTokenInvalid) {
		httpErr(w, 400, err.Error()+" — sign up again or request a new link")
		return
	} else if err != nil {
		// The token is only spent when the UPDATE commits, so a busy database
		// leaves the link usable: say so instead of a bare 500.
		slog.Warn("verify: token redeem failed", "err", err)
		httpErr(w, http.StatusServiceUnavailable, "the server is busy — open the link again in a minute; it still works")
		return
	}
	// The account is confirmed from here on, so signing in gets a fresh budget,
	// not what the redeem left of the first: a timeout here would show an error
	// for an account that IS confirmed (2026-09-30).
	sctx, scancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer scancel()
	r = r.WithContext(sctx)
	u, ok, err := d.St.GetUserByID(r.Context(), uid)
	if err != nil || !ok {
		httpInternal(w, errors.New("verified account vanished"))
		return
	}
	d.startSession(w, r, u.ID, u.Username, u.IsAdmin)
}

type emailBody struct {
	Email          string `json:"email"`
	TurnstileToken string `json:"turnstileToken"`
}

const sentIfExists = "sent-if-exists"

// authResend re-sends the confirmation link. Same answer whether or not the
// address has an account.
func (d Deps) authResend(w http.ResponseWriter, r *http.Request) {
	var body emailBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpErr(w, 400, "bad json")
		return
	}
	if !acctIPLimiter.allow(acctKey(d.clientKey(r, 0))) {
		httpErr(w, http.StatusTooManyRequests, "too many requests — try again later")
		return
	}
	email, ok := normaliseEmail(body.Email)
	email, _ = canonicalEmail(email) // the spelling sign-up stored
	base := d.publicBase()
	if ok && base != "" && mailReady(d) && mailLimiter.allow(email) {
		if uid, _, verified, found, err := d.St.AccountByEmail(r.Context(), email); err == nil && found && !verified {
			mailInBackground(func(ctx context.Context) {
				if err := d.sendVerify(ctx, uid, email, base); err != nil {
					slog.Warn("resend: verification email failed", "err", err)
				}
			})
		}
	}
	writeJSON(w, map[string]string{"status": sentIfExists})
}

// authForgot emails a password-reset link to a verified account.
func (d Deps) authForgot(w http.ResponseWriter, r *http.Request) {
	var body emailBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpErr(w, 400, "bad json")
		return
	}
	key := d.clientKey(r, 0)
	if !acctIPLimiter.allow(acctKey(key)) {
		httpErr(w, http.StatusTooManyRequests, "too many requests — try again later")
		return
	}
	if !d.turnstileOK(r.Context(), body.TurnstileToken, clientIP(key)) {
		httpErr(w, 403, "the human check failed — reload the page and try again")
		return
	}
	email, ok := normaliseEmail(body.Email)
	email, _ = canonicalEmail(email) // the spelling sign-up stored
	base := d.publicBase()
	// Unverified accounts may reset too: the reset link proves control of the
	// inbox and verifies the address, which defeats a squatter who signed up
	// with someone else's email and a password of their own choosing.
	if ok && base != "" && mailReady(d) && resetLimiter.allow(email) {
		if uid, username, _, found, err := d.St.AccountByEmail(r.Context(), email); err == nil && found {
			mailInBackground(func(ctx context.Context) {
				tok, err := d.St.CreateAuthToken(ctx, uid, store.TokenReset, resetTTL)
				if err != nil {
					slog.Warn("forgot: token failed", "err", err)
					return
				}
				if err := sendAccountEmail(d, ctx, email, "Reset your SignalDeck password",
					"Hi "+username+",\n\nReset your password here:\n"+base+"/reset?token="+tok+"\n\n"+
						"This link works once and expires in 1 hour. If you didn't ask for this, ignore it — "+
						"your password has not changed."); err != nil {
					slog.Warn("forgot: email failed", "err", err)
				}
			})
		}
	}
	writeJSON(w, map[string]string{"status": sentIfExists})
}

// authReset sets a new password from an emailed link, ends every existing
// session for the account, and signs the user in.
func (d Deps) authReset(w http.ResponseWriter, r *http.Request) {
	var body tokenBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpErr(w, 400, "bad json")
		return
	}
	if !acctIPLimiter.allow(acctKey(d.clientKey(r, 0))) {
		httpErr(w, http.StatusTooManyRequests, "too many attempts — try again later")
		return
	}
	if len(body.Password) < 8 || len(body.Password) > 72 {
		httpErr(w, 400, "password must be 8-72 characters")
		return
	}
	// Hash before taking priority: bcrypt is CPU, not a write anyone waits on.
	hash, err := bcrypt.GenerateFromPassword([]byte(body.Password), bcrypt.DefaultCost)
	if err != nil {
		httpInternal(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	release := d.St.Priority()
	defer release()
	// Redeem + new password + verified in ONE transaction, so a busy database
	// rolls everything back and the "it still works" below is true.
	uid, err := d.St.ResetPassword(ctx, strings.TrimSpace(body.Token), string(hash))
	if errors.Is(err, store.ErrTokenInvalid) {
		httpErr(w, 400, err.Error()+" — request a new reset link")
		return
	} else if err != nil {
		slog.Warn("reset: token redeem failed", "err", err)
		httpErr(w, http.StatusServiceUnavailable, "the server is busy — open the link again in a minute; it still works")
		return
	}
	// The password has changed from here on; signing in gets a fresh budget.
	sctx, scancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer scancel()
	r = r.WithContext(sctx)
	u, ok, err := d.St.GetUserByID(r.Context(), uid)
	if err != nil || !ok {
		httpInternal(w, errors.New("account vanished during reset"))
		return
	}
	loginFailures.succeed(u.Username)
	d.startSession(w, r, u.ID, u.Username, u.IsAdmin)
}

func (d Deps) registerAccounts(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/auth/verify", d.authVerify)
	mux.HandleFunc("POST /api/auth/resend", d.authResend)
	mux.HandleFunc("POST /api/auth/forgot", d.authForgot)
	mux.HandleFunc("POST /api/auth/reset", d.authReset)
	mux.HandleFunc("POST /api/auth/google", d.authGoogle)
}
