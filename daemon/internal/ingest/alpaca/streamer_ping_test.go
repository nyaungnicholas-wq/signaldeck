package alpaca

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// pingServer completes the greet/auth handshake, then runs after(conn).
func pingServer(t *testing.T, after func(ctx context.Context, c *websocket.Conn)) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow() //nolint:errcheck
		ctx := r.Context()
		_ = c.Write(ctx, websocket.MessageText, []byte(`[{"T":"success","msg":"connected"}]`))
		if _, _, err := c.Read(ctx); err != nil { // auth frame
			return
		}
		_ = c.Write(ctx, websocket.MessageText, []byte(`[{"T":"success","msg":"authenticated"}]`))
		after(ctx, c)
	}))
	t.Cleanup(srv.Close)
	return "ws" + strings.TrimPrefix(srv.URL, "http")
}

func shrinkPing(t *testing.T) {
	t.Helper()
	oe, ot := pingEvery, pingTimeout
	pingEvery, pingTimeout = 20*time.Millisecond, 150*time.Millisecond
	t.Cleanup(func() { pingEvery, pingTimeout = oe, ot })
}

// A peer that stops reading never answers a ping: Run must return (nil, so the
// supervisor redials without filing an error) instead of blocking forever on a
// half-open connection.
func TestStreamerPingDetectsDeadPeer(t *testing.T) {
	shrinkPing(t)
	url := pingServer(t, func(ctx context.Context, _ *websocket.Conn) { <-ctx.Done() })
	s := NewStreamer(New("k", "s"), nil, testResolver(nil), url)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	// nil (a routine redial), not ctx's deadline error: the ping ended Run.
	if err := s.Run(ctx); err != nil {
		t.Fatalf("Run = %v; want nil after an unanswered ping", err)
	}
}

// A live peer streaming frames must survive pings. A ping run inside the select
// loop would fail here: the reader blocks handing over a frame and never takes
// the pong.
func TestStreamerPingKeepsBusyLivePeer(t *testing.T) {
	shrinkPing(t)
	url := pingServer(t, func(ctx context.Context, c *websocket.Conn) {
		go func() {
			for {
				if _, _, err := c.Read(ctx); err != nil {
					return
				}
			}
		}()
		for ctx.Err() == nil {
			if c.Write(ctx, websocket.MessageText, []byte(`[{"T":"success","msg":"tick"}]`)) != nil {
				return
			}
			time.Sleep(2 * time.Millisecond)
		}
	})
	s := NewStreamer(New("k", "s"), nil, testResolver(nil), url)
	ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
	defer cancel()
	if err := s.Run(ctx); err != context.DeadlineExceeded {
		t.Fatalf("Run = %v; want it to keep running until the deadline", err)
	}
}
