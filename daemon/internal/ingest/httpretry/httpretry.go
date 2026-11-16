// Package httpretry retries a GET through transient failures (network errors
// and 5xx). The news and CFTC fetchers made one attempt, so one 502 or reset
// lost a whole poll (audit 11-10).
package httpretry

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// Attempts is the total number of tries; Wait is the first pause, doubling.
// Vars so tests shrink them.
var (
	Attempts = 3
	Wait     = time.Second
)

// Do sends newReq() up to Attempts times, building a fresh request each time.
// It retries a network error (unless ctx is done) and a 5xx status. Any other
// response returns at once. After the last attempt a 5xx response is returned
// as-is, body open, so the caller's own status check reports it; the caller
// owns resp.Body whenever err is nil.
func Do(ctx context.Context, hc *http.Client, newReq func() (*http.Request, error)) (*http.Response, error) {
	for attempt := 1; ; attempt++ {
		req, err := newReq()
		if err != nil {
			return nil, err
		}
		resp, err := hc.Do(req)
		retryable := (err != nil && ctx.Err() == nil) || (err == nil && resp.StatusCode >= 500)
		if !retryable || attempt >= Attempts {
			if err != nil {
				return nil, fmt.Errorf("after %d attempt(s): %w", attempt, err)
			}
			return resp, nil
		}
		if resp != nil {
			resp.Body.Close() //nolint:errcheck
		}
		t := time.NewTimer(Wait << (attempt - 1))
		select {
		case <-ctx.Done():
			t.Stop()
			return nil, ctx.Err()
		case <-t.C:
		}
	}
}
