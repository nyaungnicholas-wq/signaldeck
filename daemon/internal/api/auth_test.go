package api

import (
	"database/sql"
	"net/http"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// storedSessionTokens reads the sessions table straight off disk, bypassing the
// store's accessors — this is the attacker's view of the file (world-readable,
// copied to iCloud), which is the whole point of C8.
func storedSessionTokens(t *testing.T, dbPath string) []string {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close() //nolint:errcheck
	rows, err := db.Query(`SELECT token FROM sessions`)
	if err != nil {
		t.Fatalf("query sessions: %v", err)
	}
	defer rows.Close() //nolint:errcheck
	var out []string
	for rows.Next() {
		var tok string
		if err := rows.Scan(&tok); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, tok)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	return out
}

// TestSessionCookieIsNeverPersisted is the end-to-end form of C8: log in for
// real, then read the database the way anyone with the file can. The value in
// the Set-Cookie header must appear nowhere in the sessions table, and the
// cookie must still work — a session store that is safe to read but no longer
// authenticates is not a fix.
func TestSessionCookieIsNeverPersisted(t *testing.T) {
	srv, _, d := newTestServer(t, nil)
	c := newClient(t)

	resp := postJSON(t, c, srv.URL+"/api/auth/register", map[string]string{
		"username": "c8user", "password": "correct-horse-battery",
	})
	if resp.StatusCode != 200 {
		t.Fatalf("register: %d (%s)", resp.StatusCode, drain(t, resp))
	}
	var cookie string
	for _, ck := range resp.Cookies() {
		if ck.Name == sessionCookie {
			cookie = ck.Value
		}
	}
	drain(t, resp)
	if cookie == "" {
		t.Fatalf("register issued no %s cookie", sessionCookie)
	}

	stored := storedSessionTokens(t, d.Cfg.DBPath)
	if len(stored) != 1 {
		t.Fatalf("sessions rows = %d, want 1", len(stored))
	}
	if stored[0] == cookie {
		t.Fatalf("the cookie value is stored verbatim — any DB read is account takeover")
	}

	// The cookie authenticates.
	if got := getMeStatus(t, c, srv.URL); got != 200 {
		t.Fatalf("session cookie rejected: /api/auth/me = %d", got)
	}

	// The STORED value must not authenticate when replayed as the cookie.
	replay := newClient(t)
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/auth/me", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: stored[0]})
	res, err := replay.Do(req)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if res.StatusCode != 401 {
		t.Fatalf("stored session value replayed successfully: /api/auth/me = %d (%s)",
			res.StatusCode, drain(t, res))
	}
	drain(t, res)

	// Logout still resolves the row from the plaintext cookie.
	resp = postJSON(t, c, srv.URL+"/api/auth/logout", map[string]string{})
	drain(t, resp)
	if n := len(storedSessionTokens(t, d.Cfg.DBPath)); n != 0 {
		t.Fatalf("logout left %d session row(s)", n)
	}
}

func getMeStatus(t *testing.T, c *http.Client, base string) int {
	t.Helper()
	resp, err := c.Get(base + "/api/auth/me")
	if err != nil {
		t.Fatalf("me: %v", err)
	}
	drain(t, resp)
	return resp.StatusCode
}

// Login had no lockout: the request limiter keys on client, so rotating source
// addresses bought a fresh budget each time, and one address still had ~170k
// write-tier attempts a day. Nothing counted failures anywhere.
func TestLoginLockoutBacksOffAndClearsOnSuccess(t *testing.T) {
	f := &failCounter{fails: map[string]*failState{}}
	now := time.Now()

	// Under the threshold a person mistyping their password is never delayed.
	for i := 0; i < loginLockoutAfter-1; i++ {
		f.fail("alice", now)
		if w := f.retryAfter("alice", now); w != 0 {
			t.Fatalf("locked out after %d failures (threshold is %d): wait=%v", i+1, loginLockoutAfter, w)
		}
	}

	// The threshold failure starts the backoff, and the FIRST wait is already
	// long enough to matter. At the old 1s base the gate cleared between two
	// ordinary requests and the message rounded down to "try again in 0s".
	f.fail("alice", now)
	first := f.retryAfter("alice", now)
	if first < loginLockoutBase {
		t.Fatalf("first lockout was %v, want at least %v — too short to slow guessing", first, loginLockoutBase)
	}

	// Each further failure at least doubles the wait, up to the cap.
	f.fail("alice", now)
	if second := f.retryAfter("alice", now); second < first*2 {
		t.Errorf("backoff did not grow: %v then %v", first, second)
	}

	// The cap holds, so an attacker cannot lock a real user out indefinitely.
	for i := 0; i < 40; i++ {
		f.fail("alice", now)
	}
	if w := f.retryAfter("alice", now); w > loginLockoutMax {
		t.Errorf("backoff %v exceeded the %v cap", w, loginLockoutMax)
	}

	// Waiting it out clears the gate.
	if w := f.retryAfter("alice", now.Add(loginLockoutMax+time.Second)); w != 0 {
		t.Errorf("still locked out past the cap: %v", w)
	}

	// A success wipes the counter rather than leaving it primed.
	f.succeed("alice")
	if w := f.retryAfter("alice", now); w != 0 {
		t.Errorf("still locked out after a successful sign-in: %v", w)
	}

	// An unknown username locks out exactly like a real one, so the lockout is
	// not an account-enumeration oracle.
	for i := 0; i <= loginLockoutAfter; i++ {
		f.fail("nosuchuser", now)
	}
	if f.retryAfter("nosuchuser", now) <= 0 {
		t.Error("unknown username did not lock out — the response now distinguishes real accounts from fake ones")
	}
}
