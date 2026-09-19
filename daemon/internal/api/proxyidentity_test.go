// Browsers reach this daemon through a server-side proxy on loopback.
// The daemon therefore sees neither the real scheme nor the real client address unless something carries them.
// Guessing wrong is silent in both directions: a session cookie without Secure on an HTTPS site, and
// every anonymous visitor either sharing one rate-limit bucket or each minting their own.
// X-Forwarded-For is append-only, so the trustworthy entry is the one the nearest proxy added.
// That trustworthy entry is the last, not the first, in the header.
// The daemon must ignore client-supplied values in these headers unless explicitly told to trust the proxy.

package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	cfg "github.com/nyaungnicholas-wq/signaldeck/internal/config"
)

func TestSecureCookieIsSetOnAPublishedDeployment(t *testing.T) {
	tests := []struct {
		name       string
		cfg        cfg.Config
		xfp        string
		wantSecure bool
	}{
		{
			name:       "plain local daemon with no headers returns false",
			cfg:        cfg.Config{PublicSurface: false, TrustProxy: false},
			xfp:        "",
			wantSecure: false,
		},
		{
			name:       "PublicSurface true forces Secure cookie even without X-Forwarded-Proto header",
			cfg:        cfg.Config{PublicSurface: true, TrustProxy: false},
			xfp:        "",
			wantSecure: true,
		},
		{
			name:       "TrustProxy true with X-Forwarded-Proto https returns true",
			cfg:        cfg.Config{PublicSurface: false, TrustProxy: true},
			xfp:        "https",
			wantSecure: true,
		},
		{
			name:       "TrustProxy true with X-Forwarded-Proto HTTPS (case-insensitive) returns true",
			cfg:        cfg.Config{PublicSurface: false, TrustProxy: true},
			xfp:        "HTTPS",
			wantSecure: true,
		},
		{
			name:       "TrustProxy true with X-Forwarded-Proto http returns false",
			cfg:        cfg.Config{PublicSurface: false, TrustProxy: true},
			xfp:        "http",
			wantSecure: false,
		},
		{
			name:       "TrustProxy false ignores X-Forwarded-Proto https header",
			cfg:        cfg.Config{PublicSurface: false, TrustProxy: false},
			xfp:        "https",
			wantSecure: false,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/anything", nil)
			if tt.xfp != "" {
				req.Header.Set("X-Forwarded-Proto", tt.xfp)
			}
			deps := Deps{Cfg: tt.cfg}
			if got := deps.secureCookie(req); got != tt.wantSecure {
				t.Errorf("secureCookie() = %v, want %v", got, tt.wantSecure)
			}
		})
	}
}

func TestClientKeyUsesTheProxysHopNotTheClientsClaim(t *testing.T) {
	tests := []struct {
		name string
		cfg  cfg.Config
		xff  string
		auth string
		uid  int64
		want string
	}{
		{
			name: "uid 7 overrides all other identifiers",
			cfg:  cfg.Config{PublicSurface: false, TrustProxy: true, APIToken: "s3cret"},
			xff:  "203.0.113.9, 198.51.100.7",
			auth: "Bearer s3cret",
			uid:  7,
			want: "u:7",
		},
		{
			name: "TrustProxy true uses last hop of X-Forwarded-For with two IPs",
			cfg:  cfg.Config{PublicSurface: false, TrustProxy: true, APIToken: ""},
			xff:  "203.0.113.9, 198.51.100.7",
			auth: "",
			uid:  0,
			want: "ip:198.51.100.7",
		},
		{
			name: "TrustProxy true uses single hop X-Forwarded-For",
			cfg:  cfg.Config{PublicSurface: false, TrustProxy: true, APIToken: ""},
			xff:  "198.51.100.7",
			auth: "",
			uid:  0,
			want: "ip:198.51.100.7",
		},
		{
			name: "TrustProxy true ignores irregular spacing in X-Forwarded-For",
			cfg:  cfg.Config{PublicSurface: false, TrustProxy: true, APIToken: ""},
			xff:  "203.0.113.9,  198.51.100.7  ",
			auth: "",
			uid:  0,
			want: "ip:198.51.100.7",
		},
		{
			name: "TrustProxy false ignores X-Forwarded-For and uses RemoteAddr",
			cfg:  cfg.Config{PublicSurface: false, TrustProxy: false, APIToken: ""},
			xff:  "203.0.113.9",
			auth: "",
			uid:  0,
			want: "ip:192.0.2.1",
		},
		{
			name: "TrustProxy true with empty X-Forwarded-For falls back to RemoteAddr",
			cfg:  cfg.Config{PublicSurface: false, TrustProxy: true, APIToken: ""},
			xff:  "",
			auth: "",
			uid:  0,
			want: "ip:192.0.2.1",
		},
		{
			name: "Bearer token matching APIToken returns tok",
			cfg:  cfg.Config{PublicSurface: false, TrustProxy: false, APIToken: "s3cret"},
			xff:  "",
			auth: "Bearer s3cret",
			uid:  0,
			want: "tok",
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/anything", nil)
			if tt.xff != "" {
				req.Header.Set("X-Forwarded-For", tt.xff)
			}
			if tt.auth != "" {
				req.Header.Set("Authorization", tt.auth)
			}
			deps := Deps{Cfg: tt.cfg}
			if got := deps.clientKey(req, tt.uid); got != tt.want {
				t.Errorf("clientKey() = %q, want %q", got, tt.want)
			}
		})
	}
}
