package api

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/config"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// The waitlist is the ONLY unauthenticated write on a published deployment,
// which makes normaliseEmail a trust boundary. The cases that matter are not
// the pretty-address ones: they are the header-injection payload and the
// length bound.
func TestNormaliseEmail(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want string
		ok   bool
	}{
		{"a@b.co", "a@b.co", true},
		{"  A@B.Co  ", "a@b.co", true}, // trimmed and lowercased
		{"first.last+tag@sub.example.com", "first.last+tag@sub.example.com", true},
		{"", "", false},
		{"   ", "", false},
		{"nope", "", false},                             // no @
		{"a@@b.co", "", false},                          // two @
		{"a@b@c.co", "", false},                         // two @, separated
		{"@b.co", "", false},                            // empty local part
		{"a@", "", false},                               // empty domain
		{"a@b", "", false},                              // domain with no dot
		{"a@b.", "", false},                             // trailing dot
		{"a b@c.co", "", false},                         // space
		{"a@b.co\r\nBcc: everyone@evil.com", "", false}, // HEADER INJECTION
		// A TRAILING newline is whitespace and is trimmed, which is correct --
		// people paste addresses with one. The dangerous case is an EMBEDDED
		// CRLF, covered above, which survives the trim and is refused.
		{"a@b.co\n", "a@b.co", true},
		{"a@b\r\n.co", "", false}, // embedded, mid-address
		{"a\t@b.co", "", false},
		{strings.Repeat("x", 250) + "@b.co", "", false}, // over 254
	} {
		got, ok := normaliseEmail(tc.in)
		if ok != tc.ok || got != tc.want {
			t.Errorf("normaliseEmail(%q) = (%q, %v), want (%q, %v)", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func newWaitlistDeps(t *testing.T) Deps {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "wl.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return Deps{St: st, Cfg: config.Config{}}
}

func post(t *testing.T, d Deps, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/waitlist", strings.NewReader(body))
	d.waitlistAdd(rec, req)
	return rec
}

// A duplicate must look EXACTLY like a new signup from the outside. Anything
// else lets a stranger test whether a given person is on the list.
func TestWaitlistDuplicateIsIndistinguishable(t *testing.T) {
	d := newWaitlistDeps(t)

	first := post(t, d, `{"email":"a@b.co"}`)
	second := post(t, d, `{"email":"A@B.CO"}`) // same address, different case

	if first.Code != 200 || second.Code != 200 {
		t.Fatalf("codes %d and %d, want 200 and 200", first.Code, second.Code)
	}
	if first.Body.String() != second.Body.String() {
		t.Errorf("a duplicate is distinguishable from a new signup:\n first  %q\n second %q",
			first.Body.String(), second.Body.String())
	}
	n, err := d.St.CountWaitlist(context.Background())
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("stored %d rows for the same address, want 1", n)
	}
}

// A filled honeypot is a bot. Answer 200, store nothing: telling a scraper it
// was detected only teaches it to stop filling the field.
func TestWaitlistHoneypotStoresNothing(t *testing.T) {
	d := newWaitlistDeps(t)
	rec := post(t, d, `{"email":"bot@b.co","hp":"http://spam"}`)
	if rec.Code != 200 {
		t.Errorf("honeypot got %d, want a bland 200", rec.Code)
	}
	n, _ := d.St.CountWaitlist(context.Background())
	if n != 0 {
		t.Errorf("honeypot submission was stored (%d rows)", n)
	}
}

func TestWaitlistRejectsBadInput(t *testing.T) {
	d := newWaitlistDeps(t)
	for _, body := range []string{
		`{"email":"nope"}`,
		`{"email":""}`,
		`{"email":"a@b.co\r\nBcc: x@y.co"}`,
		`not json at all`,
	} {
		if rec := post(t, d, body); rec.Code != 400 {
			t.Errorf("body %q got %d, want 400", body, rec.Code)
		}
	}
	if n, _ := d.St.CountWaitlist(context.Background()); n != 0 {
		t.Errorf("a rejected submission was stored (%d rows)", n)
	}
}

// GET must not write. The mux registers "POST /api/waitlist" so Go routes
// only POST here, but the handler guards anyway -- the mux pattern and the
// handler are edited by different people at different times.
func TestWaitlistRefusesNonPost(t *testing.T) {
	d := newWaitlistDeps(t)
	rec := httptest.NewRecorder()
	d.waitlistAdd(rec, httptest.NewRequest("GET", "/api/waitlist", nil))
	if rec.Code != 405 {
		t.Errorf("GET got %d, want 405", rec.Code)
	}
}
