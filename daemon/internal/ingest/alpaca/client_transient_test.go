package alpaca

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// 5xx is retried like 429; other 4xx are returned at once (audit 11-10).
func TestGetRetryingTransient(t *testing.T) {
	cases := []struct {
		name     string
		statuses []int // per request; the last repeats
		wantReqs int32
		wantCode int
		wantErr  bool
	}{
		{"two 503 then 200", []int{503, 503, 200}, 3, 200, false},
		{"two 502 then 200", []int{502, 502, 200}, 3, 200, false},
		{"permanent 500 gives up", []int{500}, int32(maxBackfillRetries) + 1, 0, true},
		{"404 not retried", []int{404}, 1, 404, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			setBackoff(t, time.Millisecond)
			var n atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				i := int(n.Add(1)) - 1
				if i >= len(c.statuses) {
					i = len(c.statuses) - 1
				}
				w.WriteHeader(c.statuses[i])
			}))
			defer srv.Close()
			resp, err := New("k", "s").getRetrying(context.Background(), srv.URL, "test")
			if (err != nil) != c.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, c.wantErr)
			}
			if err == nil {
				defer resp.Body.Close() //nolint:errcheck
				if resp.StatusCode != c.wantCode {
					t.Fatalf("status = %d, want %d", resp.StatusCode, c.wantCode)
				}
			}
			if got := n.Load(); got != c.wantReqs {
				t.Fatalf("requests = %d, want %d", got, c.wantReqs)
			}
		})
	}

	// A network error (server gone) is retried, then reported.
	setBackoff(t, time.Millisecond)
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()
	if _, err := New("k", "s").getRetrying(context.Background(), url, "test"); err == nil {
		t.Fatal("want an error from a closed server")
	}
}
