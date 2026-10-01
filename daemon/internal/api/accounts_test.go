package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/nyaungnicholas-wq/signaldeck/internal/config"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// These pin the public-account security: email verification before sign-in,
// the member tier, the honeypot, the limiters, and the no-enumeration answers.
// Every server here is PUBLISHED (PublicSurface) — the only mode in which the
// email flow and the member gate apply.

type sentMail struct{ to, subject, body string }

type mailbox struct {
	mu   sync.Mutex
	msgs []sentMail
}

func (m *mailbox) add(to, subject, body string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.msgs = append(m.msgs, sentMail{to, subject, body})
}

func (m *mailbox) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.msgs)
}

func (m *mailbox) last() sentMail {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.msgs[len(m.msgs)-1]
}

func (m *mailbox) linkToken(t *testing.T, path string) string {
	t.Helper()
	re := regexp.MustCompile(regexp.QuoteMeta(path) + `\?token=([0-9a-f]{64})`)
	got := re.FindStringSubmatch(m.last().body)
	if got == nil {
		t.Fatalf("no %s link in the last mail: %q", path, m.last().body)
	}
	return got[1]
}

func waitMail(t *testing.T, mb *mailbox, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for mb.count() < n {
		if time.Now().After(deadline) {
			t.Fatalf("waited for %d mail(s), have %d", n, mb.count())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func newPublishedServer(t *testing.T) (*httptest.Server, *store.Store, *mailbox) {
	t.Helper()
	return newPublishedServerWith(t, nil, true)
}

// newPublishedServerWith lets a test change the posture (mutate) and choose
// whether an admin already exists.
func newPublishedServerWith(t *testing.T, mutate func(*config.Config), seedAdmin bool) (*httptest.Server, *store.Store, *mailbox) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "acct.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	cfg := baseCfg()
	cfg.PublicSurface = true
	cfg.OpenSignup = true
	cfg.PublicURL = "https://sd.example"
	// Raise the per-client request limiter so these tests exercise the ACCOUNT
	// limiters, not the generic 2 writes/sec tier every POST shares.
	cfg.RateRPS, cfg.RateBurst = 1000, 6000
	if mutate != nil {
		mutate(&cfg)
	}
	d := Deps{St: st, Cfg: cfg, Version: "test", Started: time.Now()}
	srv := httptest.NewUnstartedServer(nil)
	t.Cleanup(srv.Close)
	d.Cfg.AllowedHosts = []string{srv.Listener.Addr().String()}
	mux := http.NewServeMux()
	d.registerAuth(mux)
	mux.HandleFunc("GET /api/watchlist", d.watchlist)
	mux.HandleFunc("GET /api/trends", d.trends)
	mux.HandleFunc("POST /api/subscribe", d.subscribe)
	srv.Config.Handler = d.secure(mux)
	srv.Start()

	// An admin already exists, so every sign-up below is a MEMBER.
	if seedAdmin {
		hash, _ := bcrypt.GenerateFromPassword([]byte("adminpass123"), bcrypt.MinCost)
		if _, err := st.CreateUser(context.Background(), "owner", string(hash), true); err != nil {
			t.Fatal(err)
		}
	}

	mb := &mailbox{}
	oldSend, oldReady := sendAccountEmail, mailReady
	sendAccountEmail = func(_ Deps, _ context.Context, to, subject, body string) error {
		mb.add(to, subject, body)
		return nil
	}
	mailReady = func(Deps) bool { return true }
	oldS, oldA, oldM := signupLimiter, acctIPLimiter, mailLimiter
	signupLimiter = newWindowLimiter(5, time.Hour)
	acctIPLimiter = newWindowLimiter(20, time.Hour)
	mailLimiter = newWindowLimiter(3, time.Hour)
	t.Cleanup(func() {
		mailWG.Wait() // in-flight mail reads sendAccountEmail and the store
		sendAccountEmail, mailReady = oldSend, oldReady
		signupLimiter, acctIPLimiter, mailLimiter = oldS, oldA, oldM
	})
	return srv, st, mb
}

func signup(t *testing.T, c *http.Client, base, user, email string) (int, string) {
	t.Helper()
	resp := postJSON(t, c, base+"/api/auth/register", map[string]string{
		"username": user, "email": email, "password": "correcthorse1",
		"turnstileToken": "", "website": "",
	})
	return resp.StatusCode, drain(t, resp)
}

func acctPost(t *testing.T, c *http.Client, url string, body map[string]string) (int, string) {
	t.Helper()
	resp := postJSON(t, c, url, body)
	return resp.StatusCode, drain(t, resp)
}

// signupVerified creates a member and redeems its confirmation link, returning
// the signed-in client.
func signupVerified(t *testing.T, srv *httptest.Server, mb *mailbox, user, email string) *http.Client {
	t.Helper()
	before := mb.count()
	if code, body := signup(t, newClient(t), srv.URL, user, email); code != 200 {
		t.Fatalf("signup %s: %d %s", user, code, body)
	}
	waitMail(t, mb, before+1)
	c := newClient(t)
	if code, body := acctPost(t, c, srv.URL+"/api/auth/verify", map[string]string{"token": mb.linkToken(t, "/verify")}); code != 200 {
		t.Fatalf("verify %s: %d %s", user, code, body)
	}
	return c
}

func TestSignupRequiresEmailVerification(t *testing.T) {
	srv, _, mb := newPublishedServer(t)
	code, body := signup(t, newClient(t), srv.URL, "alice", "alice@example.com")
	if code != 200 || !strings.Contains(body, verifySent) {
		t.Fatalf("signup: %d %s", code, body)
	}
	waitMail(t, mb, 1)
	if m := mb.last(); m.to != "alice@example.com" || !strings.Contains(m.body, "https://sd.example/verify?token=") {
		t.Fatalf("verification mail wrong: %+v", m)
	}
	creds := map[string]string{"username": "alice", "password": "correcthorse1"}
	if code, body := acctPost(t, newClient(t), srv.URL+"/api/auth/login", creds); code != 403 || !strings.Contains(body, "unverified") {
		t.Fatalf("login before verify: %d %s, want 403 unverified", code, body)
	}
	tok := mb.linkToken(t, "/verify")
	c2 := newClient(t)
	if code, body := acctPost(t, c2, srv.URL+"/api/auth/verify", map[string]string{"token": tok}); code != 200 {
		t.Fatalf("verify: %d %s", code, body)
	}
	if code, body := getAs(t, c2, srv.URL+"/api/auth/me"); code != 200 || !strings.Contains(body, "alice") {
		t.Fatalf("me after verify: %d %s", code, body)
	}
	if code, body := acctPost(t, newClient(t), srv.URL+"/api/auth/login", creds); code != 200 {
		t.Fatalf("login after verify: %d %s", code, body)
	}
	if code, body := acctPost(t, newClient(t), srv.URL+"/api/auth/verify", map[string]string{"token": tok}); code != 400 {
		t.Fatalf("second use of a verify token: %d %s, want 400", code, body)
	}
}

func TestSignupHoneypotCreatesNothing(t *testing.T) {
	srv, _, mb := newPublishedServer(t)
	code, body := acctPost(t, newClient(t), srv.URL+"/api/auth/register", map[string]string{
		"username": "botty", "email": "bot@example.com", "password": "correcthorse1",
		"website": "http://spam.example",
	})
	if code != 200 || !strings.Contains(body, verifySent) {
		t.Fatalf("honeypot answer must look like success: %d %s", code, body)
	}
	time.Sleep(100 * time.Millisecond)
	if mb.count() != 0 {
		t.Fatalf("honeypot sent %d mail(s)", mb.count())
	}
	if code, _ := acctPost(t, newClient(t), srv.URL+"/api/auth/login",
		map[string]string{"username": "botty", "password": "correcthorse1"}); code != 401 {
		t.Fatalf("honeypot created an account: login %d", code)
	}
}

func TestSignupRequiresValidEmail(t *testing.T) {
	srv, _, _ := newPublishedServer(t)
	if code, body := signup(t, newClient(t), srv.URL, "erin", "not-an-email"); code != 400 {
		t.Fatalf("bad email: %d %s, want 400", code, body)
	}
}

func TestTakenEmailLooksLikeSuccess(t *testing.T) {
	srv, _, mb := newPublishedServer(t)
	if code, body := signup(t, newClient(t), srv.URL, "bob", "bob@example.com"); code != 200 {
		t.Fatalf("first signup: %d %s", code, body)
	}
	waitMail(t, mb, 1)
	code, body := signup(t, newClient(t), srv.URL, "bob2", "BOB@example.com")
	if code != 200 || !strings.Contains(body, verifySent) {
		t.Fatalf("taken email must answer like success: %d %s", code, body)
	}
	waitMail(t, mb, 2)
	if m := mb.last(); strings.Contains(m.subject, "Confirm") || !strings.Contains(m.body, "/forgot") {
		t.Fatalf("taken email should get the already-exists notice: %+v", m)
	}
}

func TestSignupRateLimited(t *testing.T) {
	srv, _, _ := newPublishedServer(t)
	for i := 0; i < 5; i++ {
		u := "user" + string(rune('0'+i))
		if code, body := signup(t, newClient(t), srv.URL, u, u+"@example.com"); code != 200 {
			t.Fatalf("signup %d: %d %s", i, code, body)
		}
	}
	if code, body := signup(t, newClient(t), srv.URL, "user9", "user9@example.com"); code != 429 {
		t.Fatalf("6th signup: %d %s, want 429", code, body)
	}
}

func TestMemberCannotReachOperatorRoutes(t *testing.T) {
	srv, _, mb := newPublishedServer(t)
	carol := signupVerified(t, srv, mb, "carol", "carol@example.com")
	if code, body := getAs(t, carol, srv.URL+"/api/trends"); code != 403 || !strings.Contains(body, "member") {
		t.Fatalf("member reached an operator route: %d %s", code, body)
	}
	if code, body := getAs(t, carol, srv.URL+"/api/watchlist"); code != 200 {
		t.Fatalf("member watchlist: %d %s", code, body)
	}
	owner := newClient(t)
	if code, body := acctPost(t, owner, srv.URL+"/api/auth/login",
		map[string]string{"username": "owner", "password": "adminpass123"}); code != 200 {
		t.Fatalf("owner login: %d %s", code, body)
	}
	if code, body := getAs(t, owner, srv.URL+"/api/trends"); code == 403 {
		t.Fatalf("admin blocked by the member gate: %s", body)
	}
}

func TestForgotAndResetPassword(t *testing.T) {
	srv, _, mb := newPublishedServer(t)
	signupVerified(t, srv, mb, "dave", "dave@example.com")
	before := mb.count()
	if code, body := acctPost(t, newClient(t), srv.URL+"/api/auth/forgot",
		map[string]string{"email": "dave@example.com"}); code != 200 || !strings.Contains(body, sentIfExists) {
		t.Fatalf("forgot: %d %s", code, body)
	}
	waitMail(t, mb, before+1)
	tok := mb.linkToken(t, "/reset")
	if code, body := acctPost(t, newClient(t), srv.URL+"/api/auth/reset",
		map[string]string{"token": tok, "password": "newpassword9"}); code != 200 {
		t.Fatalf("reset: %d %s", code, body)
	}
	if code, _ := acctPost(t, newClient(t), srv.URL+"/api/auth/login",
		map[string]string{"username": "dave", "password": "correcthorse1"}); code != 401 {
		t.Fatalf("old password still works: %d", code)
	}
	if code, body := acctPost(t, newClient(t), srv.URL+"/api/auth/login",
		map[string]string{"username": "dave", "password": "newpassword9"}); code != 200 {
		t.Fatalf("new password: %d %s", code, body)
	}
	if code, _ := acctPost(t, newClient(t), srv.URL+"/api/auth/reset",
		map[string]string{"token": tok, "password": "another99999"}); code != 400 {
		t.Fatalf("reset token reused: %d, want 400", code)
	}
}

func TestForgotUnknownEmailLooksIdentical(t *testing.T) {
	srv, _, mb := newPublishedServer(t)
	code, body := acctPost(t, newClient(t), srv.URL+"/api/auth/forgot", map[string]string{"email": "nobody@example.com"})
	if code != 200 || !strings.Contains(body, sentIfExists) {
		t.Fatalf("unknown email: %d %s", code, body)
	}
	time.Sleep(150 * time.Millisecond)
	if mb.count() != 0 {
		t.Fatalf("mail sent for an unknown address")
	}
}

func TestResetRejectsShortPassword(t *testing.T) {
	srv, _, _ := newPublishedServer(t)
	if code, body := acctPost(t, newClient(t), srv.URL+"/api/auth/reset",
		map[string]string{"token": strings.Repeat("a", 64), "password": "short"}); code != 400 {
		t.Fatalf("short password: %d %s, want 400", code, body)
	}
}

// The LIVE posture: published only because a public URL / tunnel log is set,
// PublicSurface off. Before the fix the gate hinged on a stale ALLOWED_HOSTS
// entry; here there is none, and the member tier must still hold.
func TestMemberGateHoldsOnTunnelPosture(t *testing.T) {
	srv, _, mb := newPublishedServerWith(t, func(c *config.Config) { c.PublicSurface = false }, true)
	if code, body := signup(t, newClient(t), srv.URL, "erin", "not-an-email"); code != 400 {
		t.Fatalf("tunnel posture fell back to the private register: %d %s", code, body)
	}
	m := signupVerified(t, srv, mb, "frank", "frank@example.com")
	if code, body := getAs(t, m, srv.URL+"/api/trends"); code != 403 {
		t.Fatalf("member reached an operator route on the tunnel posture: %d %s", code, body)
	}
	if code, body := acctPost(t, m, srv.URL+"/api/subscribe", map[string]string{"symbol": "AAPL", "market": "stocks"}); code != 403 {
		t.Fatalf("member started global ingestion: %d %s", code, body)
	}
}

func TestPublishedRefusesAdminBootstrap(t *testing.T) {
	srv, _, _ := newPublishedServerWith(t, nil, false)
	if code, body := signup(t, newClient(t), srv.URL, "grace", "grace@example.com"); code != 403 {
		t.Fatalf("first account over the internet: %d %s, want 403", code, body)
	}
}

// A squatter signs up with someone else's address. The real owner must be able
// to take it back with a reset, which also verifies the address.
func TestUnverifiedAccountCanBeReset(t *testing.T) {
	srv, _, mb := newPublishedServer(t)
	if code, body := signup(t, newClient(t), srv.URL, "squatter", "owner2@example.com"); code != 200 {
		t.Fatalf("signup: %d %s", code, body)
	}
	waitMail(t, mb, 1)
	if code, body := acctPost(t, newClient(t), srv.URL+"/api/auth/forgot", map[string]string{"email": "owner2@example.com"}); code != 200 {
		t.Fatalf("forgot: %d %s", code, body)
	}
	waitMail(t, mb, 2)
	tok := mb.linkToken(t, "/reset")
	if code, body := acctPost(t, newClient(t), srv.URL+"/api/auth/reset", map[string]string{"token": tok, "password": "ownerspass9"}); code != 200 {
		t.Fatalf("reset: %d %s", code, body)
	}
	if code, _ := acctPost(t, newClient(t), srv.URL+"/api/auth/login", map[string]string{"username": "squatter", "password": "correcthorse1"}); code != 401 {
		t.Fatalf("squatter's password still works: %d", code)
	}
	if code, body := acctPost(t, newClient(t), srv.URL+"/api/auth/login", map[string]string{"username": "squatter", "password": "ownerspass9"}); code != 200 {
		t.Fatalf("owner cannot sign in after reset: %d %s", code, body)
	}
}

func TestEmailRejectsAddressListSyntax(t *testing.T) {
	for _, bad := range []string{"a>@x.com", "a,b@x.com", "a;b@x.com", `"a"@x.com`, "a@[1.2.3.4]"} {
		if _, ok := normaliseEmail(bad); ok {
			t.Errorf("accepted %q", bad)
		}
	}
	if _, ok := normaliseEmail("real.person+tag@example.co.uk"); !ok {
		t.Error("rejected a normal address")
	}
}

func TestTunnelURLReadsOnlyTheBanner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tunnel.log")
	log := `{"message":"|  https://good-one.trycloudflare.com                    |"}
{"message":"GET /https://evil-one.trycloudflare.com/x 404"}
`
	if err := os.WriteFile(path, []byte(log), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := lastTunnelURL(path); got != "https://good-one.trycloudflare.com" {
		t.Fatalf("lastTunnelURL = %q", got)
	}
}
