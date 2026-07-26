package api

// A9 (2026-07-26 re-audit): an ngrok reverse tunnel connects to the daemon
// from 127.0.0.1, so every tunneled PUBLIC request presents a loopback
// RemoteAddr. requestIsLoopback must treat any request carrying a forwarding
// header as remote — the header can only ever revoke loopback status, never
// grant it, so forging one cannot widen access.

import (
	"net/http/httptest"
	"testing"
)

func TestRequestIsLoopbackDeniesTunneledRequests(t *testing.T) {
	// The exact A9 shape: tunnel connects locally, real client is public.
	r := httptest.NewRequest("GET", "/api/export/bars", nil)
	r.RemoteAddr = "127.0.0.1:53211"
	r.Header.Set("X-Forwarded-For", "203.0.113.7")
	if requestIsLoopback(r) {
		t.Fatal("tunneled request (loopback RemoteAddr + public X-Forwarded-For) " +
			"was treated as loopback — this reopens the A9 hole")
	}

	for _, h := range []string{"X-Real-Ip", "Forwarded", "Ngrok-Skip-Browser-Warning"} {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = "127.0.0.1:53211"
		r.Header.Set(h, "203.0.113.7")
		if requestIsLoopback(r) {
			t.Errorf("forwarding header %s did not revoke loopback status", h)
		}
	}
}

func TestRequestIsLoopbackStillTrueForPlainLocalCalls(t *testing.T) {
	for _, addr := range []string{"127.0.0.1:9000", "[::1]:9000", "127.0.0.5:9000"} {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = addr
		if !requestIsLoopback(r) {
			t.Errorf("plain local call from %s no longer counts as loopback", addr)
		}
	}
}

func TestRequestIsLoopbackNeverGrantedByHeaders(t *testing.T) {
	// A remote caller claiming a loopback XFF must stay remote.
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "198.51.100.4:44321"
	r.Header.Set("X-Forwarded-For", "127.0.0.1")
	if requestIsLoopback(r) {
		t.Fatal("a forged loopback X-Forwarded-For granted loopback status to a remote caller")
	}
}
