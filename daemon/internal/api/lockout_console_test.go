package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
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

// TestKnownDeviceSurvivesAnInternetLockout: a browser that signed in before
// keeps signing in while an attacker holds the name's shared ladder locked
// from elsewhere, and a forged device cookie gets the attacker nothing.
func TestKnownDeviceSurvivesAnInternetLockout(t *testing.T) {
	old := loginFailures
	loginFailures = &failCounter{fails: map[string]*failState{}}
	t.Cleanup(func() { loginFailures = old })
	srv, _, _ := newPublishedServer(t) // seeds admin "owner" / "adminpass123"

	login := func(pass, xff string, cookies []*http.Cookie) (int, []*http.Cookie) {
		b, _ := json.Marshal(map[string]string{"username": "owner", "password": pass})
		req, _ := http.NewRequest("POST", srv.URL+"/api/auth/login", bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(csrfHeader, "1")
		req.Header.Set("X-Forwarded-For", xff)
		for _, c := range cookies {
			req.AddCookie(c)
		}
		resp, err := newClient(t).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		drain(t, resp)
		return resp.StatusCode, resp.Cookies()
	}
	// The owner's browser signs in through the tunnel once.
	code, set := login("adminpass123", "198.51.100.7", nil)
	if code != http.StatusOK {
		t.Fatalf("first sign-in: %d", code)
	}
	var device *http.Cookie
	for _, c := range set {
		if c.Name == deviceCookie {
			device = c
		}
	}
	if device == nil || device.Value == "" || !device.HttpOnly {
		t.Fatalf("no HttpOnly known-device cookie after a sign-in: %+v", set)
	}
	// An attacker locks the shared ladder from other addresses.
	for i := 0; i < loginLockoutAfter+1; i++ {
		login("wrong-password", fmt.Sprintf("203.0.113.%d", i+1), nil)
	}
	if code, _ := login("adminpass123", "203.0.113.50", nil); code != http.StatusTooManyRequests {
		t.Fatalf("a cookie-less sign-in after the attack: %d, want 429", code)
	}
	forged := &http.Cookie{Name: deviceCookie, Value: strings.Repeat("ab", 32)}
	if code, _ := login("adminpass123", "203.0.113.51", []*http.Cookie{forged}); code != http.StatusTooManyRequests {
		t.Fatalf("a forged device cookie: %d, want 429", code)
	}
	if code, _ := login("adminpass123", "198.51.100.7", []*http.Cookie{device}); code != http.StatusOK {
		t.Fatalf("the known browser was locked out by the attack: %d, want 200", code)
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
