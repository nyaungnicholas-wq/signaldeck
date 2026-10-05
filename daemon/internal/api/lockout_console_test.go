package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"
)

// TestTunnelFailuresCannotLockTheConsole is the regression proof for the
// 2026-10-05 security review: the sign-in lockout was keyed on the username
// alone, so five wrong passwords sent through the public tunnel locked the
// owner out everywhere, and about 96 requests a day kept them out. Failures
// that arrive through a tunnel (X-Forwarded-For present) must still lock that
// ladder, and a sign-in typed on the machine itself must still go through.
func TestTunnelFailuresCannotLockTheConsole(t *testing.T) {
	old := loginFailures
	loginFailures = &failCounter{fails: map[string]*failState{}}
	t.Cleanup(func() { loginFailures = old })
	srv, _, _ := newPublishedServer(t) // seeds admin "owner" / "adminpass123"

	login := func(pass, xff string) int {
		b, _ := json.Marshal(map[string]string{"username": "owner", "password": pass})
		req, _ := http.NewRequest("POST", srv.URL+"/api/auth/login", bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(csrfHeader, "1")
		if xff != "" {
			req.Header.Set("X-Forwarded-For", xff)
		}
		resp, err := newClient(t).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		drain(t, resp)
		return resp.StatusCode
	}
	for i := 0; i < loginLockoutAfter+1; i++ {
		login("wrong-password", fmt.Sprintf("203.0.113.%d", i+1))
	}
	if code := login("adminpass123", "203.0.113.99"); code != http.StatusTooManyRequests {
		t.Fatalf("a tunnel sign-in after %d tunnel failures: %d, want 429", loginLockoutAfter+1, code)
	}
	// A spoofed loopback hop in front of the real client is still the tunnel.
	if code := login("adminpass123", "127.0.0.1, 203.0.113.98"); code != http.StatusTooManyRequests {
		t.Fatalf("a tunnel sign-in carrying a spoofed loopback hop: %d, want 429", code)
	}
	// What the web proxy sends for a sign-in typed at 127.0.0.1:8323: Next
	// fills X-Forwarded-For from its socket when the header is missing.
	if code := login("adminpass123", "127.0.0.1"); code != http.StatusOK {
		t.Fatalf("the console sign-in (via the web proxy) was locked out by tunnel failures: %d, want 200", code)
	}
	if code := login("adminpass123", ""); code != http.StatusOK {
		t.Fatalf("the console sign-in (direct) was locked out by tunnel failures: %d, want 200", code)
	}
}

// TestWindowLimiterSprayKeepsExhaustedBudgets: filling the limiter with fresh
// keys used to drop the whole table, which also forgot every exhausted budget
// (a victim address's mail limit among them). The spray must be shed instead.
func TestWindowLimiterSprayKeepsExhaustedBudgets(t *testing.T) {
	l := newWindowLimiter(3, time.Hour)
	for i := 0; i < 3; i++ {
		l.allow("victim@gmail.com")
	}
	if l.allow("victim@gmail.com") {
		t.Fatal("the victim budget should be exhausted before the spray")
	}
	for i := 0; i < 50001; i++ {
		l.allow(fmt.Sprintf("spray-%d", i))
	}
	if l.allow("victim@gmail.com") {
		t.Fatal("a spray of fresh keys reset an exhausted budget")
	}
	if n := len(l.hits); n > 1000 {
		t.Fatalf("the spray was not shed: %d keys still held", n)
	}
}
