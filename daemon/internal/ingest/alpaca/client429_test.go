package alpaca

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// These tests exercise the shared 429 back-off (getRetrying) through BOTH bars
// paths. Everything is served by httptest — no live Alpaca call is made.

// barsHandler serves 429 for the first failN requests, then a one-bar page in
// whichever shape the path expects. retryAfter, when non-empty, is sent as the
// Retry-After header on every 429. It records the wall-clock gap between
// successive requests so a test can assert a back-off actually happened.
type barsHandler struct {
	failN      int32
	retryAfter string
	multi      bool

	reqs  atomic.Int32
	mu    chan struct{} // 1-slot mutex guarding last/gaps
	last  time.Time
	gaps  []time.Duration
	start time.Time
}

func newBarsHandler(failN int, retryAfter string, multi bool) *barsHandler {
	h := &barsHandler{failN: int32(failN), retryAfter: retryAfter, multi: multi, mu: make(chan struct{}, 1)}
	h.mu <- struct{}{}
	h.start = time.Now()
	return h
}

func (h *barsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	<-h.mu
	if !h.last.IsZero() {
		h.gaps = append(h.gaps, now.Sub(h.last))
	}
	h.last = now
	h.mu <- struct{}{}

	if h.reqs.Add(1) <= h.failN {
		if h.retryAfter != "" {
			w.Header().Set("Retry-After", h.retryAfter)
		}
		http.Error(w, `{"message":"rate limited"}`, http.StatusTooManyRequests)
		return
	}
	bar := map[string]any{"t": "2026-06-01T04:00:00Z", "o": 1, "h": 1, "l": 1, "c": 1, "v": 1}
	body := map[string]any{"bars": []map[string]any{bar}, "next_page_token": nil}
	if h.multi {
		body["bars"] = map[string]any{"AAA": []map[string]any{bar}}
	}
	_ = json.NewEncoder(w).Encode(body)
}

// maxGap is the longest observed pause between consecutive requests.
func (h *barsHandler) maxGap() time.Duration {
	<-h.mu
	defer func() { h.mu <- struct{}{} }()
	var m time.Duration
	for _, g := range h.gaps {
		if g > m {
			m = g
		}
	}
	return m
}

// fetchPath drives one of the two bars fetchers against the test server.
type fetchPath struct {
	name  string
	multi bool
	call  func(c *Client, ctx context.Context) error
}

func fetchPaths() []fetchPath {
	start := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	return []fetchPath{
		{"single", false, func(c *Client, ctx context.Context) error {
			_, err := c.fetchBarsPage(ctx, "AAA", "1Day", start, "")
			return err
		}},
		{"multi", true, func(c *Client, ctx context.Context) error {
			_, err := c.fetchMultiBarsPage(ctx, []string{"AAA"}, "1Day", start, "")
			return err
		}},
	}
}

// setBackoff pins backoff429 for one test.
func setBackoff(t *testing.T, d time.Duration) {
	t.Helper()
	old := backoff429
	backoff429 = d
	t.Cleanup(func() { backoff429 = old })
}

// TestGetRetrying429 is the table: 429 with and without Retry-After, asserting
// the retry count, that back-off actually elapsed, and that a permanent 429
// gives up with an error rather than looping or returning a silent empty page.
func TestGetRetrying429(t *testing.T) {
	tests := []struct {
		name       string
		failN      int // number of 429s before success (>maxBackfillRetries = permanent)
		retryAfter string
		backoff    time.Duration
		wantReqs   int32
		wantErr    bool
		// minGap asserts a real pause happened between two requests;
		// maxGap asserts the pause was NOT the (much larger) exponential
		// ladder, which is how "Retry-After was honoured" is detected.
		minGap time.Duration
		maxGap time.Duration
	}{
		{
			// No Retry-After: must use the exponential ladder. base=40ms, so
			// the first sleep is jitter(40ms) ∈ [20ms,40ms] and the second is
			// jitter(80ms) ∈ [40ms,80ms].
			name: "no retry-after uses jittered backoff", failN: 2, retryAfter: "",
			backoff: 40 * time.Millisecond, wantReqs: 3, wantErr: false,
			minGap: 20 * time.Millisecond, maxGap: 2 * time.Second,
		},
		{
			// Retry-After: 0 with a deliberately HUGE exponential base. If the
			// header is ignored the first gap is ≥1s; honouring it keeps every
			// gap tiny. This is the discriminating assertion.
			name: "retry-after 0 overrides large backoff", failN: 2, retryAfter: "0",
			backoff: 2 * time.Second, wantReqs: 3, wantErr: false,
			minGap: 0, maxGap: 500 * time.Millisecond,
		},
		{
			// Delay-seconds form is honoured: 1s pause even though the
			// exponential base is 1ms.
			name: "retry-after 1s honoured over tiny backoff", failN: 1, retryAfter: "1",
			backoff: time.Millisecond, wantReqs: 2, wantErr: false,
			minGap: 900 * time.Millisecond, maxGap: 3 * time.Second,
		},
		{
			// Garbage header falls back to the ladder rather than erroring or
			// sleeping forever.
			name: "unparseable retry-after falls back", failN: 1, retryAfter: "soon",
			backoff: 40 * time.Millisecond, wantReqs: 2, wantErr: false,
			minGap: 20 * time.Millisecond, maxGap: 2 * time.Second,
		},
		{
			// Permanent 429: bounded retries, then a real error.
			name: "permanent 429 gives up", failN: 1000, retryAfter: "",
			backoff: time.Millisecond, wantReqs: int32(maxBackfillRetries) + 1, wantErr: true,
			minGap: 0, maxGap: 2 * time.Second,
		},
		{
			// Permanent 429 WITH Retry-After still gives up — an obliging
			// server must not be able to hold a worker forever.
			name: "permanent 429 with retry-after gives up", failN: 1000, retryAfter: "0",
			backoff: time.Millisecond, wantReqs: int32(maxBackfillRetries) + 1, wantErr: true,
			minGap: 0, maxGap: 2 * time.Second,
		},
	}

	for _, tc := range tests {
		for _, p := range fetchPaths() {
			t.Run(tc.name+"/"+p.name, func(t *testing.T) {
				setBackoff(t, tc.backoff)
				h := newBarsHandler(tc.failN, tc.retryAfter, p.multi)
				srv := httptest.NewServer(h)
				defer srv.Close()

				c := New("k", "s")
				c.BaseData = srv.URL
				err := p.call(c, context.Background())

				if tc.wantErr {
					if err == nil {
						t.Fatal("want error on permanent 429, got nil")
					}
					// The failure must be attributed, not swallowed.
					if !strings.Contains(err.Error(), "rate limited") {
						t.Errorf("error %q does not mention rate limiting", err)
					}
				} else if err != nil {
					t.Fatalf("want success after back-off, got %v", err)
				}
				if got := h.reqs.Load(); got != tc.wantReqs {
					t.Errorf("requests = %d, want %d", got, tc.wantReqs)
				}
				if got := h.maxGap(); got < tc.minGap {
					t.Errorf("max inter-request gap = %v, want >= %v (no back-off happened)", got, tc.minGap)
				} else if got > tc.maxGap {
					t.Errorf("max inter-request gap = %v, want <= %v (Retry-After not honoured)", got, tc.maxGap)
				}
			})
		}
	}
}

// TestGetRetrying429ContextCancel: a cancelled context must abort the sleep
// promptly and surface ctx.Err(), not run the full retry ladder.
func TestGetRetrying429ContextCancel(t *testing.T) {
	for _, p := range fetchPaths() {
		t.Run(p.name, func(t *testing.T) {
			setBackoff(t, 30*time.Second) // long enough that only cancel can end it
			h := newBarsHandler(1000, "", p.multi)
			srv := httptest.NewServer(h)
			defer srv.Close()

			ctx, cancel := context.WithCancel(context.Background())
			go func() {
				time.Sleep(50 * time.Millisecond)
				cancel()
			}()

			c := New("k", "s")
			c.BaseData = srv.URL
			started := time.Now()
			err := p.call(c, ctx)
			elapsed := time.Since(started)

			if err == nil {
				t.Fatal("want error after cancel, got nil")
			}
			if elapsed > 5*time.Second {
				t.Errorf("took %v — back-off sleep did not respect ctx cancellation", elapsed)
			}
			if h.reqs.Load() != 1 {
				t.Errorf("requests = %d, want 1 (cancel during the first back-off)", h.reqs.Load())
			}
		})
	}
}

// TestParseRetryAfter covers both wire forms plus the reject cases that must
// fall back to exponential back-off.
func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		header string
		want   time.Duration
		wantOK bool
	}{
		{"absent", "", 0, false},
		{"whitespace", "   ", 0, false},
		{"zero seconds", "0", 0, true},
		{"delay seconds", "30", 30 * time.Second, true},
		{"padded delay seconds", " 5 ", 5 * time.Second, true},
		{"negative seconds rejected", "-5", 0, false},
		{"absurd delay capped", "86400", maxRetryAfter, true},
		{"unparseable", "soon", 0, false},
		{"http-date future", now.Add(20 * time.Second).UTC().Format(http.TimeFormat), 20 * time.Second, true},
		{"http-date past rejected", now.Add(-20 * time.Second).UTC().Format(http.TimeFormat), 0, false},
		{"http-date absurd capped", now.Add(4 * time.Hour).UTC().Format(http.TimeFormat), maxRetryAfter, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseRetryAfter(tc.header, now)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if ok && got != tc.want {
				t.Errorf("duration = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestJitterBounds: jitter stays within [d/2, d] and never panics on the
// degenerate inputs the back-off ladder can produce in tests.
func TestJitterBounds(t *testing.T) {
	if got := jitter(0); got != 0 {
		t.Errorf("jitter(0) = %v, want 0", got)
	}
	if got := jitter(-time.Second); got != 0 {
		t.Errorf("jitter(negative) = %v, want 0", got)
	}
	if got := jitter(time.Millisecond); got < 0 || got > time.Millisecond {
		t.Errorf("jitter(1ms) = %v, out of [0,1ms]", got)
	}
	d := 100 * time.Millisecond
	for i := 0; i < 200; i++ {
		got := jitter(d)
		if got < d/2 || got > d {
			t.Fatalf("jitter(%v) = %v, out of [%v,%v]", d, got, d/2, d)
		}
	}
}
