package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/config"
	"github.com/nyaungnicholas-wq/signaldeck/internal/notify"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

func newAlertPrefsServer(t *testing.T, mutate func(*config.Config), telegram bool) (srvURL string, st *store.Store, member, owner *http.Client) {
	t.Helper()
	srv, st, mb := startPublished(t, mutate, true, func(d Deps) http.Handler {
		if telegram {
			d.Notifier = &notify.Notifier{TelegramToken: "123:SECRET"}
		}
		lim := newRateLimiter(d.Cfg.RateRPS, d.Cfg.RateBurst)
		return d.httpServerWith(d.routes(lim), lim).Handler
	})
	member = signupVerified(t, srv, mb, "mira", "mira@gmail.com")
	owner = newClient(t)
	if code, body := acctPost(t, owner, srv.URL+"/api/auth/login",
		map[string]string{"username": "owner", "password": "adminpass123"}); code != 200 {
		t.Fatalf("owner login: %d %s", code, body)
	}
	return srv.URL, st, member, owner
}

func prefsOf(t *testing.T, c *http.Client, base string) alertPrefsView {
	t.Helper()
	code, body := getAs(t, c, base+"/api/alert-prefs")
	if code != 200 {
		t.Fatalf("GET alert-prefs: %d %s", code, body)
	}
	var v alertPrefsView
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestAlertPrefsMemberFlow(t *testing.T) {
	base, st, member, owner := newAlertPrefsServer(t, nil, true)
	ctx := context.Background()

	if v := prefsOf(t, member, base); v.EmailDigest || !v.EmailVerified || v.TelegramLinked || !v.TelegramAvailable || !v.MailAvailable {
		t.Fatalf("defaults: %+v (opt-in: everything off; verified member; transports up)", v)
	}
	resp := postJSON(t, member, base+"/api/alert-prefs", map[string]bool{"emailDigest": true})
	if body := drain(t, resp); resp.StatusCode != 200 || !strings.Contains(body, `"emailDigest":true`) {
		t.Fatalf("opt in: %d %s", resp.StatusCode, body)
	}
	// Bad bodies are refused, not read as "off".
	for _, b := range []any{map[string]string{}, map[string]string{"emailDigest": "yes"}} {
		if resp := postJSON(t, member, base+"/api/alert-prefs", b); resp.StatusCode != 400 {
			t.Errorf("body %v: %d, want 400", b, resp.StatusCode)
		}
	}
	// No verified address (the bootstrap admin has none): no email digest.
	if resp := postJSON(t, owner, base+"/api/alert-prefs", map[string]bool{"emailDigest": true}); resp.StatusCode != http.StatusConflict {
		t.Errorf("owner without a verified email opted in: %d", resp.StatusCode)
	}

	resp = postJSON(t, member, base+"/api/alert-prefs/telegram-link", map[string]string{})
	body := drain(t, resp)
	var link struct {
		Code      string `json:"code"`
		ExpiresAt int64  `json:"expiresAt"`
	}
	if err := json.Unmarshal([]byte(body), &link); resp.StatusCode != 200 || err != nil || len(link.Code) != 8 ||
		link.ExpiresAt < time.Now().Add(14*time.Minute).Unix() {
		t.Fatalf("telegram-link: %d %s", resp.StatusCode, body)
	}
	if _, ok, err := st.LinkTelegramByCode(ctx, link.Code, "777", time.Now()); err != nil || !ok {
		t.Fatalf("issued code does not link: %v %v", ok, err)
	}
	if v := prefsOf(t, member, base); !v.TelegramLinked || !v.EmailDigest {
		t.Fatalf("after link: %+v", v)
	}
	resp = postJSON(t, member, base+"/api/alert-prefs/telegram-unlink", map[string]string{})
	if body := drain(t, resp); resp.StatusCode != 200 || !strings.Contains(body, `"telegramLinked":false`) {
		t.Fatalf("unlink: %d %s", resp.StatusCode, body)
	}

	// CSRF: a POST without the custom header never reaches the handler.
	req, _ := http.NewRequest("POST", base+"/api/alert-prefs", strings.NewReader(`{"emailDigest":false}`))
	if resp, err := member.Do(req); err != nil || resp.StatusCode != 403 {
		t.Fatalf("POST without CSRF header: %v %v", resp.StatusCode, err)
	}
	if v := prefsOf(t, member, base); !v.EmailDigest {
		t.Fatal("a header-less POST changed the prefs")
	}
}

func TestAlertPrefsTelegramUnavailable(t *testing.T) {
	base, _, member, _ := newAlertPrefsServer(t, nil, false)
	if v := prefsOf(t, member, base); v.TelegramAvailable {
		t.Fatalf("no bot token but telegramAvailable: %+v", v)
	}
	if resp := postJSON(t, member, base+"/api/alert-prefs/telegram-link", map[string]string{}); resp.StatusCode != 503 {
		t.Fatalf("telegram-link with no bot: %d, want 503", resp.StatusCode)
	}
}

// The unsubscribe link works with no session on both published postures and
// turns only the token's user off; alert-prefs itself is never anonymous.
func TestUnsubscribeLinkIsAnonymousAndTokened(t *testing.T) {
	// Even a private daemon that opens general reads keeps the switches closed.
	open := Deps{Cfg: config.Config{PublicReads: true}}
	for _, p := range []string{"/api/alert-prefs", "/api/alert-prefs/telegram-link", "/api/alert-prefs/telegram-unlink"} {
		if !open.requiresAuth(p) {
			t.Errorf("%s is anonymous under PublicReads", p)
		}
	}
	if open.requiresAuth("/api/alerts/unsubscribe") {
		t.Error("the unsubscribe link needs a session under PublicReads")
	}
	for _, posture := range []struct {
		name   string
		mutate func(*config.Config)
	}{
		{"public-surface", nil},
		{"tunnel", func(c *config.Config) { c.PublicSurface, c.PublicReads = false, false }},
	} {
		t.Run(posture.name, func(t *testing.T) {
			base, st, member, _ := newAlertPrefsServer(t, posture.mutate, false)
			ctx := context.Background()
			if resp := postJSON(t, member, base+"/api/alert-prefs", map[string]bool{"emailDigest": true}); resp.StatusCode != 200 {
				t.Fatalf("opt in: %d", resp.StatusCode)
			}
			anon := newClient(t)
			for _, p := range []string{"/api/alert-prefs"} {
				if code, _ := getAs(t, anon, base+p); code != 401 {
					t.Errorf("anonymous GET %s: %d, want 401", p, code)
				}
			}
			if resp := postJSON(t, anon, base+"/api/alert-prefs/telegram-unlink", map[string]string{}); resp.StatusCode != 401 {
				t.Errorf("anonymous unlink: %d, want 401", resp.StatusCode)
			}
			mira, _, err := st.GetUserByName(ctx, "mira")
			if err != nil {
				t.Fatal(err)
			}
			tok, err := st.CreateAuthToken(ctx, mira.ID, store.TokenUnsubscribe, time.Hour)
			if err != nil {
				t.Fatal(err)
			}
			// A plain form POST, as a browser or mail client sends it: no CSRF
			// header, no session.
			post := func(url string, form url.Values) (int, string) {
				t.Helper()
				resp, err := anon.PostForm(url, form)
				if err != nil {
					t.Fatal(err)
				}
				return resp.StatusCode, drain(t, resp)
			}
			on := func() bool { t.Helper(); return prefsOf(t, member, base).EmailDigest }

			// GET (what a link scanner does) only shows the confirm form.
			code, body := getAs(t, anon, base+"/api/alerts/unsubscribe?token="+tok)
			if code != 200 || !strings.Contains(body, `<form method="post" action="/api/alerts/unsubscribe">`) ||
				!strings.Contains(body, `name="token" value="`+tok+`"`) {
				t.Fatalf("confirm page: %d %s", code, body)
			}
			if !on() {
				t.Fatal("a GET of the unsubscribe link turned the digest off")
			}
			// The token is escaped into the page, never echoed raw.
			if _, body := getAs(t, anon, base+"/api/alerts/unsubscribe?token=%22%3E%3Cscript%3E"); strings.Contains(body, "<script>") {
				t.Errorf("token reflected unescaped: %s", body)
			}
			// POST without a token, or with a bad one, changes nothing.
			if code, body := post(base+"/api/alerts/unsubscribe", url.Values{}); code != 400 || !strings.Contains(body, "invalid") || !on() {
				t.Errorf("POST without token: %d %s", code, body)
			}
			if code, _ := post(base+"/api/alerts/unsubscribe", url.Values{"token": {"nope"}}); code != 400 || !on() {
				t.Errorf("POST bad token: %d", code)
			}
			// The confirm form's POST redeems.
			if code, body := post(base+"/api/alerts/unsubscribe", url.Values{"token": {tok}}); code != 200 || !strings.Contains(body, "unsubscribed") {
				t.Fatalf("form POST: %d %s", code, body)
			}
			if on() {
				t.Fatal("the confirmed POST left the digest on")
			}
			// RFC 8058 one-click: token in the URL, body List-Unsubscribe=One-Click.
			if resp := postJSON(t, member, base+"/api/alert-prefs", map[string]bool{"emailDigest": true}); resp.StatusCode != 200 {
				t.Fatalf("opt back in: %d", resp.StatusCode)
			}
			if code, _ := post(base+"/api/alerts/unsubscribe?token="+tok,
				url.Values{"List-Unsubscribe": {"One-Click"}}); code != 200 || on() {
				t.Errorf("one-click POST: %d, on=%v", code, on())
			}
			// The CSRF exemption is that exact path: a header-less form POST to a
			// neighbour is still refused.
			for _, p := range []string{"/api/alerts/unsubscribe/x", "/api/alerts/seen", "/api/alert-prefs"} {
				if code, body := post(base+p, url.Values{"token": {tok}}); code != 403 || !strings.Contains(body, csrfHeader) {
					t.Errorf("header-less POST %s: %d %s, want the CSRF refusal", p, code, body)
				}
			}
		})
	}
}

func TestCSRFExemptionIsExactPathAndMethod(t *testing.T) {
	for _, c := range []struct {
		method, path string
		want         bool
	}{
		{"POST", "/api/alerts/unsubscribe", true},
		{"GET", "/api/alerts/unsubscribe", false},
		{"PUT", "/api/alerts/unsubscribe", false},
		{"POST", "/api/alerts/unsubscribe/", false},
		{"POST", "/api/alerts/seen", false},
		{"POST", "/api/alert-prefs", false},
	} {
		if got := csrfExemptUnsubscribe(httptest.NewRequest(c.method, c.path, nil)); got != c.want {
			t.Errorf("%s %s exempt=%v, want %v", c.method, c.path, got, c.want)
		}
	}
}

// B: the daily email needs SIGNALDECK_PUBLIC_URL. A quick-tunnel origin
// (publicBase's fallback) rotates on restart, so links mailed with it die.
func TestDigestMailNeedsConfiguredPublicURL(t *testing.T) {
	oldReady := mailReady
	mailReady = func(Deps) bool { return true }
	t.Cleanup(func() { mailReady = oldReady })
	log := filepath.Join(t.TempDir(), "tunnel.log")
	if err := os.WriteFile(log, []byte("|  https://abc-def.trycloudflare.com  |\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	publicURLMemo.mu.Lock()
	publicURLMemo.at = time.Time{}
	publicURLMemo.mu.Unlock()
	tunnel := Deps{Cfg: config.Config{TunnelLog: log}}
	if tunnel.publicBase() == "" {
		t.Fatal("fixture: the tunnel log yields no public base")
	}
	if tunnel.digestMailAvailable() {
		t.Error("daily email offered on a quick-tunnel origin")
	}
	if !(Deps{Cfg: config.Config{PublicURL: "https://sd.example"}}).digestMailAvailable() {
		t.Error("daily email unavailable with a configured public URL")
	}
	publicURLMemo.mu.Lock()
	publicURLMemo.at = time.Time{}
	publicURLMemo.mu.Unlock()
}
