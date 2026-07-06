package finra

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestParseDaily_Fixture: the real file shape (verified live 2026-07-06) —
// header row, pipe-delimited, FRACTIONAL volumes — parses correctly, and the
// two deliberately malformed lines are skipped AND counted.
func TestParseDaily_Fixture(t *testing.T) {
	f, err := os.Open(filepath.Join("testdata", "CNMSshvol20260702.txt"))
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer f.Close() //nolint:errcheck
	rows, skipped, err := ParseDaily(f)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if skipped != 2 {
		t.Fatalf("skipped = %d, want 2 (BROKEN float + SHORTLINE)", skipped)
	}
	if len(rows) != 5 {
		t.Fatalf("rows = %d, want 5", len(rows))
	}
	a := rows[0]
	if a.Symbol != "A" || a.Day != "2026-07-02" {
		t.Fatalf("row0 = %+v, want symbol A day 2026-07-02", a)
	}
	// Fractional volumes must survive exactly.
	if a.ShortVol != 343899.973577 || a.TotalVol != 541886.441727 || a.ShortExempt != 319 {
		t.Fatalf("row0 volumes = %+v", a)
	}
	var aapl *Row
	for i := range rows {
		if rows[i].Symbol == "AAPL" {
			aapl = &rows[i]
		}
	}
	if aapl == nil {
		t.Fatal("AAPL row missing")
	}
	if got := Ratio(aapl.ShortVol, aapl.TotalVol); got != 0.4 {
		t.Fatalf("AAPL ratio = %v, want 0.4", got)
	}
}

// TestParseDaily_BadHeader: an upstream format change must fail loudly,
// never parse garbage quietly.
func TestParseDaily_BadHeader(t *testing.T) {
	_, _, err := ParseDaily(strings.NewReader("Sym|Short|Total\nAAPL|1|2\n"))
	if err == nil || !strings.Contains(err.Error(), "unexpected header") {
		t.Fatalf("err = %v, want unexpected-header error", err)
	}
}

// TestRatio: the derived metric's edge cases — zero/negative totals yield 0
// (never NaN/Inf), normal division otherwise.
func TestRatio(t *testing.T) {
	cases := []struct{ short, total, want float64 }{
		{0, 0, 0},
		{5, 0, 0},
		{5, -1, 0},
		{1, 4, 0.25},
		{3, 3, 1},
	}
	for _, c := range cases {
		if got := Ratio(c.short, c.total); got != c.want {
			t.Fatalf("Ratio(%v,%v) = %v, want %v", c.short, c.total, got, c.want)
		}
	}
}

// TestFetchDaily: happy path against httptest — declarative UA stamped, the
// CNMSshvolYYYYMMDD.txt path requested, body parsed.
func TestFetchDaily(t *testing.T) {
	fixture, err := os.ReadFile(filepath.Join("testdata", "CNMSshvol20260702.txt"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var gotUA, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		gotPath = r.URL.Path
		w.Write(fixture) //nolint:errcheck
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, UA: "SignalDeck-test/0.1 (test@example.com)", MinInterval: time.Millisecond}
	day := time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC)
	rows, skipped, err := c.FetchDaily(context.Background(), day)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(rows) != 5 || skipped != 2 {
		t.Fatalf("rows=%d skipped=%d, want 5/2", len(rows), skipped)
	}
	if gotUA != "SignalDeck-test/0.1 (test@example.com)" {
		t.Fatalf("UA = %q — declarative User-Agent is mandatory", gotUA)
	}
	if gotPath != "/CNMSshvol20260702.txt" {
		t.Fatalf("path = %q, want /CNMSshvol20260702.txt", gotPath)
	}
}

// TestFetchDaily_NotAvailable: FINRA's CDN answers absent days with 403
// (verified live 2026-07-06) — both 403 and 404 must map to ErrNotAvailable
// so holidays/weekends degrade to an honest skip.
func TestFetchDaily_NotAvailable(t *testing.T) {
	for _, code := range []int{403, 404} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(code)
		}))
		c := &Client{BaseURL: srv.URL, MinInterval: time.Millisecond}
		_, _, err := c.FetchDaily(context.Background(), time.Date(2026, 7, 4, 0, 0, 0, 0, time.UTC))
		srv.Close()
		if !errors.Is(err, ErrNotAvailable) {
			t.Fatalf("status %d: err = %v, want ErrNotAvailable", code, err)
		}
	}
}

// TestFetchDaily_ServerError: a 5xx is a real error (dq-worthy), not a quiet
// not-available.
func TestFetchDaily_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, MinInterval: time.Millisecond}
	_, _, err := c.FetchDaily(context.Background(), time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC))
	if err == nil || errors.Is(err, ErrNotAvailable) {
		t.Fatalf("err = %v, want a hard error", err)
	}
}

// TestPace: two consecutive requests are spaced by at least MinInterval —
// the same politeness discipline as the shared edgar limiter.
func TestPace(t *testing.T) {
	var times []time.Time
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		times = append(times, time.Now())
		w.WriteHeader(403)
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, MinInterval: 60 * time.Millisecond}
	ctx := context.Background()
	day := time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC)
	c.FetchDaily(ctx, day) //nolint:errcheck
	c.FetchDaily(ctx, day) //nolint:errcheck
	if len(times) != 2 {
		t.Fatalf("requests = %d, want 2", len(times))
	}
	if gap := times[1].Sub(times[0]); gap < 55*time.Millisecond {
		t.Fatalf("gap = %v, want >= ~60ms pacing", gap)
	}
}
