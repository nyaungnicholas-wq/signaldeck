// This test file replaces the old "delete X-Forwarded-For" local-proxy exception
// which was a redistribution hole on any wildcard bind. The new local‑proxy
// assertion requires a configured key, a matching header, and a loopback source
// address, ignoring forwarding headers for the keyed check.
package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/config"
)

func TestLocalProxyAssertedRequiresKeyHeaderAndLoopback(t *testing.T) {
	key := "test-local-key"
	tests := []struct {
		name        string
		cfg         config.Config
		remoteAddr  string
		setHeader   bool // whether to set X-Signaldeck-Local header
		headerValue string
		setXFF      bool // whether to set X-Forwarded-For header
		expected    bool
	}{
		{
			name:        "key header present, loopback IPv4, X-Forwarded-For present",
			cfg:         config.Config{LocalProxyKey: key},
			remoteAddr:  "127.0.0.1:5000",
			setHeader:   true,
			headerValue: key,
			setXFF:      true,
			expected:    true,
		},
		{
			name:        "key header present, loopback IPv6",
			cfg:         config.Config{LocalProxyKey: key},
			remoteAddr:  "[::1]:5000",
			setHeader:   true,
			headerValue: key,
			setXFF:      false,
			expected:    true,
		},
		{
			name:        "key header present, non-loopback remote",
			cfg:         config.Config{LocalProxyKey: key},
			remoteAddr:  "10.0.0.5:5000",
			setHeader:   true,
			headerValue: key,
			setXFF:      false,
			expected:    false,
		},
		{
			name:        "wrong header value, loopback remote",
			cfg:         config.Config{LocalProxyKey: key},
			remoteAddr:  "127.0.0.1:5000",
			setHeader:   true,
			headerValue: "nope",
			setXFF:      false,
			expected:    false,
		},
		{
			name:        "header absent, loopback remote",
			cfg:         config.Config{LocalProxyKey: key},
			remoteAddr:  "127.0.0.1:5000",
			setHeader:   false,
			headerValue: "",
			setXFF:      false,
			expected:    false,
		},
		{
			name:        "empty configured key never matches empty header",
			cfg:         config.Config{LocalProxyKey: ""},
			remoteAddr:  "127.0.0.1:5000",
			setHeader:   false,
			headerValue: "",
			setXFF:      false,
			expected:    false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deps := Deps{Cfg: tt.cfg}
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = tt.remoteAddr
			if tt.setHeader {
				req.Header.Set("X-Signaldeck-Local", tt.headerValue)
			}
			if tt.setXFF {
				req.Header.Set("X-Forwarded-For", "203.0.113.7")
			}
			if got := deps.localProxyAsserted(req); got != tt.expected {
				t.Fatalf("localProxyAsserted() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestRawDataRefusedHonoursKeyedLocalProxy(t *testing.T) {
	d, _ := newExportDeps(t)
	d.Cfg.LocalProxyKey = "test-local-key"

	// Helper to make request
	makeReq := func(headerVal string, remoteAddr string) *http.Request {
		req := httptest.NewRequest(http.MethodGet, "/api/export/bars.csv?symbol=AAPL&market=stocks", nil)
		req.RemoteAddr = remoteAddr
		req.Header.Set("X-Forwarded-For", "203.0.113.7")
		if headerVal != "" {
			req.Header.Set("X-Signaldeck-Local", headerVal)
		}
		return req
	}

	// Case 1: correct key, loopback -> 200
	req := makeReq("test-local-key", "127.0.0.1:51234")
	rec := httptest.NewRecorder()
	d.exportBars(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with correct key and loopback, got %d", rec.Code)
	}

	// Case 2: wrong key value -> 451
	req = makeReq("wrong", "127.0.0.1:51234")
	rec = httptest.NewRecorder()
	d.exportBars(rec, req)
	if rec.Code != http.StatusUnavailableForLegalReasons { // 451
		t.Fatalf("expected 451 with wrong key, got %d", rec.Code)
	}

	// Case 3: correct key but non-loopback remote -> 451
	req = makeReq("test-local-key", "10.0.0.5:51234")
	rec = httptest.NewRecorder()
	d.exportBars(rec, req)
	if rec.Code != http.StatusUnavailableForLegalReasons {
		t.Fatalf("expected 451 with correct key but non-loopback remote, got %d", rec.Code)
	}
}

func TestRawDataRefusedIgnoresKeyWhenUnconfigured(t *testing.T) {
	d, _ := newExportDeps(t) // LocalProxyKey remains empty
	req := httptest.NewRequest(http.MethodGet, "/api/export/bars.csv?symbol=AAPL&market=stocks", nil)
	req.RemoteAddr = "127.0.0.1:51234"
	req.Header.Set("X-Forwarded-For", "203.0.113.7")
	req.Header.Set("X-Signaldeck-Local", "anything")
	rec := httptest.NewRecorder()
	d.exportBars(rec, req)
	if rec.Code != http.StatusUnavailableForLegalReasons {
		t.Fatalf("expected 451 when LocalProxyKey unconfigured, got %d", rec.Code)
	}
}
