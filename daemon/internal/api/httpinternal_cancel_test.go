package api

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"testing"
)

// A request the client abandoned is 499, not a 500 that counts as a server
// failure; a genuine error is still an opaque 500.
func TestHTTPInternalCancelledIsNot500(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want int
	}{
		{context.Canceled, 499},
		{fmt.Errorf("track record: %w", context.Canceled), 499},
		{errors.New("sqlite: disk I/O error"), 500},
		{context.DeadlineExceeded, 500},
	} {
		rec := httptest.NewRecorder()
		httpInternal(rec, tc.err)
		if rec.Code != tc.want {
			t.Errorf("httpInternal(%v) = %d, want %d", tc.err, rec.Code, tc.want)
		}
	}
}
