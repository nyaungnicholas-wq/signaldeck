package httpretry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestDo(t *testing.T) {
	old := Wait
	Wait = time.Millisecond
	t.Cleanup(func() { Wait = old })

	cases := []struct {
		name     string
		statuses []int // per request; the last repeats
		cancel   bool
		wantReqs int32
		wantCode int
		wantErr  bool
	}{
		{"two 503 then 200", []int{503, 503, 200}, false, 3, 200, false},
		{"permanent 500 returned as-is", []int{500}, false, 3, 500, false},
		{"404 not retried", []int{404}, false, 1, 404, false},
		{"cancelled ctx", []int{503}, true, 1, 0, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var n atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				i := int(n.Add(1)) - 1
				if i >= len(c.statuses) {
					i = len(c.statuses) - 1
				}
				w.WriteHeader(c.statuses[i])
			}))
			defer srv.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if c.cancel {
				cancel()
			}
			resp, err := Do(ctx, srv.Client(), func() (*http.Request, error) {
				return http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
			})
			if (err != nil) != c.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, c.wantErr)
			}
			if err == nil {
				defer resp.Body.Close() //nolint:errcheck
				if resp.StatusCode != c.wantCode {
					t.Fatalf("status = %d, want %d", resp.StatusCode, c.wantCode)
				}
			}
			if got := n.Load(); got > c.wantReqs || (!c.cancel && got != c.wantReqs) {
				t.Fatalf("requests = %d, want %d", got, c.wantReqs)
			}
		})
	}

	// A network error (server gone) is retried, then reported.
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()
	if _, err := Do(context.Background(), http.DefaultClient, func() (*http.Request, error) {
		return http.NewRequest(http.MethodGet, url, nil)
	}); err == nil {
		t.Fatal("want an error from a closed server")
	}
}
