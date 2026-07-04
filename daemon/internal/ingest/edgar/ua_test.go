package edgar

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestResolveUA covers the SEC-compliant User-Agent precedence chain:
// full override env > contact-email env > localhost fallback.
func TestResolveUA(t *testing.T) {
	t.Run("full override wins", func(t *testing.T) {
		t.Setenv("SIGNALDECK_EDGAR_UA", "Sample Company Name admin@sample.com")
		t.Setenv("SIGNALDECK_CONTACT_EMAIL", "ignored@example.com")
		if got := ResolveUA(); got != "Sample Company Name admin@sample.com" {
			t.Fatalf("ResolveUA = %q, want the verbatim override", got)
		}
	})
	t.Run("contact email builds declarative UA", func(t *testing.T) {
		t.Setenv("SIGNALDECK_EDGAR_UA", "")
		t.Setenv("SIGNALDECK_CONTACT_EMAIL", "ops@example.com")
		if got := ResolveUA(); got != "SignalDeck/0.1 (ops@example.com)" {
			t.Fatalf("ResolveUA = %q, want SignalDeck/0.1 (ops@example.com)", got)
		}
	})
	t.Run("fallback still identifies the app", func(t *testing.T) {
		t.Setenv("SIGNALDECK_EDGAR_UA", "")
		t.Setenv("SIGNALDECK_CONTACT_EMAIL", "")
		if got := ResolveUA(); got != "SignalDeck/0.1 (signaldeck@localhost)" {
			t.Fatalf("ResolveUA = %q, want the localhost fallback", got)
		}
	})
	t.Run("whitespace-only env is treated as unset", func(t *testing.T) {
		t.Setenv("SIGNALDECK_EDGAR_UA", "   ")
		t.Setenv("SIGNALDECK_CONTACT_EMAIL", "\t")
		if got := ResolveUA(); got != "SignalDeck/0.1 (signaldeck@localhost)" {
			t.Fatalf("ResolveUA = %q, want the localhost fallback", got)
		}
	})
}

// TestGet_SendsResolvedUAAndAccept asserts every EDGAR request carries the
// resolved declarative User-Agent and an Accept header — the two headers
// SEC's WAF keys on (the old anonymous-style UA was 403'd fleet-wide).
func TestGet_SendsResolvedUAAndAccept(t *testing.T) {
	t.Setenv("SIGNALDECK_EDGAR_UA", "")
	t.Setenv("SIGNALDECK_CONTACT_EMAIL", "audit@example.com")

	var gotUA, gotAccept string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		gotAccept = r.Header.Get("Accept")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)

	c := New()
	c.MinInterval = 1
	if _, err := c.get(context.Background(), srv.URL); err != nil {
		t.Fatalf("get: %v", err)
	}
	if gotUA != "SignalDeck/0.1 (audit@example.com)" {
		t.Fatalf("User-Agent = %q, want SignalDeck/0.1 (audit@example.com)", gotUA)
	}
	if gotAccept == "" {
		t.Fatal("Accept header missing — SEC's WAF rejects header-anemic clients")
	}
}
