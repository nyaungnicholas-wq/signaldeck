package api

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"sync/atomic"
	"time"
)

// serveUntil serves srv on ln until ctx is done, then drains.
// Shutdown waits up to drain for in-flight requests; if they outlive it,
// Close cuts their connections so their request contexts cancel, then it
// waits up to drain again for the handlers to return; it returns only after
// that, so a caller may close the store afterwards. Returns nil on a graceful
// stop, the Serve error otherwise.
func serveUntil(ctx context.Context, srv *http.Server, ln net.Listener, drain time.Duration) error {
	var active atomic.Int64
	next := srv.Handler
	if next == nil {
		next = http.DefaultServeMux
	}
	srv.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		active.Add(1)
		defer active.Add(-1)
		next.ServeHTTP(w, r)
	})

	served := make(chan error, 1)
	go func() {
		served <- srv.Serve(ln)
	}()

	select {
	case err := <-served:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
	}

	shutCtx, cancel := context.WithTimeout(context.Background(), drain)
	defer cancel()

	if err := srv.Shutdown(shutCtx); err != nil {
		_ = srv.Close()
		deadline := time.Now().Add(drain)
		for active.Load() > 0 && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		if n := active.Load(); n > 0 {
			slog.Warn("api: handlers still running after the shutdown drain; the store may close under them", "inflight", n, "drain", drain)
		}
	}

	if err := <-served; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
