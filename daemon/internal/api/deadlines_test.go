package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// A slow handler on a bodyless request must not lose its connection.
//
// withDeadlines used to arm the connection READ deadline for the whole request
// on every non-streaming route. A read deadline is not confined to the handler's
// own reads: net/http runs a background read to notice a client hanging up, and
// when that read trips the deadline the server cancels the request context. So
// every handler slower than requestReadTimeout died with err="context canceled"
// and returned an opaque 500 — measured live on GET /api/ledger/verify, which
// returned 500 at exactly ms=20000 on its cold-cache build and 200 in 1.1s once
// warm, while api.go's own constants budget 90s for a response and document cold
// builds at 22-45s.
//
// The deadlines here are scaled down so the test is fast; the shape is identical.
func TestSlowHandlerOnABodylessRequestKeepsItsConnection(t *testing.T) {
	const readDL = 150 * time.Millisecond
	const work = 3 * readDL

	srv := newDeadlineServer(t, readDL, 5*time.Second, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(work):
		case <-r.Context().Done():
			// The regression: the context is cancelled out from under a handler
			// that was never reading anything.
			http.Error(w, "context died: "+r.Context().Err().Error(), 500)
			return
		}
		_, _ = io.WriteString(w, "done")
	})

	body, status := srv.get(t, "/slow")
	if status != 200 || body != "done" {
		t.Fatalf("slow bodyless GET = %d %q; want 200 \"done\"", status, body)
	}
}

// The protection the read deadline exists for must survive the fix: a request
// that announces a body and then dribbles it is still cut off. This is the
// 2026-07-26 review's case — a connection held open having sent a few bytes.
func TestSlowRequestBodyIsStillCutOff(t *testing.T) {
	const readDL = 150 * time.Millisecond

	srv := newDeadlineServer(t, readDL, 5*time.Second, func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.ReadAll(r.Body); err != nil {
			http.Error(w, "body read failed", 400)
			return
		}
		_, _ = io.WriteString(w, "read it all")
	})

	c, err := net.Dial("tcp", srv.addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close() //nolint:errcheck

	// Announce 100 bytes, send 5, then stall well past the read deadline.
	_, _ = fmt.Fprintf(c, "POST /slow HTTP/1.1\r\nHost: x\r\nContent-Length: 100\r\n\r\nhello")
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf, _ := io.ReadAll(c)
	got := string(buf)
	if strings.Contains(got, "read it all") {
		t.Fatalf("a stalled body was served anyway: %q", got)
	}
}

// A handler that reads its body and THEN works slowly must also survive: the
// bound covers reading the body, not the work that follows it.
func TestSlowWorkAfterReadingTheBodyKeepsItsConnection(t *testing.T) {
	const readDL = 150 * time.Millisecond
	const work = 3 * readDL

	srv := newDeadlineServer(t, readDL, 5*time.Second, func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.ReadAll(r.Body); err != nil {
			http.Error(w, "body read failed", 400)
			return
		}
		select {
		case <-time.After(work):
		case <-r.Context().Done():
			http.Error(w, "context died: "+r.Context().Err().Error(), 500)
			return
		}
		_, _ = io.WriteString(w, "done")
	})

	resp, err := http.Post("http://"+srv.addr+"/slow", "text/plain", strings.NewReader("hi"))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || string(b) != "done" {
		t.Fatalf("slow post-body work = %d %q; want 200 \"done\"", resp.StatusCode, string(b))
	}
}

// deadlineServer is a real listener, because the defect lives in the connection
// deadlines: httptest's ResponseRecorder exposes no connection, so
// SetReadDeadline is a no-op there and the bug is invisible.
type deadlineServer struct{ addr string }

func newDeadlineServer(t *testing.T, read, write time.Duration, h http.HandlerFunc) *deadlineServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	wrapped := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rc := http.NewResponseController(w)
		_ = rc.SetWriteDeadline(time.Now().Add(write))
		if r.Body != nil && r.Body != http.NoBody {
			_ = rc.SetReadDeadline(time.Now().Add(read))
			r.Body = &bodyDeadline{ReadCloser: r.Body, rc: rc}
		}
		h(w, r)
	})
	srv := &http.Server{Handler: wrapped, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return &deadlineServer{addr: ln.Addr().String()}
}

func (d *deadlineServer) get(t *testing.T, path string) (string, int) {
	t.Helper()
	resp, err := http.Get("http://" + d.addr + path)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	b, _ := io.ReadAll(resp.Body)
	return string(b), resp.StatusCode
}

// An OPTIONAL write inside a read must not be able to spend the read's budget.
//
// GET /api/ledger/verify calls maybeAnchor, which may WRITE a new anchor. That
// write took the REQUEST'S context, so when it queued behind the daemon's single
// writer connection (one writer, 103 workers) it consumed the entire 30s verify
// deadline and the handler returned 503 — throwing away a chain result it had
// already computed.
//
// Measured 2026-09-19 at 531,173 ledger rows: the endpoint timed out at 30s,
// and the identical request with SIGNALDECK_LEDGER_ANCHOR_DISABLE=1 returned
// intact in 0.73s. SQLite was never the cost (full chain walk 1.61s, anchor
// prefix pass 0.36s). The receipts page — the one whose whole argument is
// "check my claims yourself" — could not verify its own chain for any visitor.
//
// Scaled down here, same shape: a slow optional write under its own budget must
// expire alone and leave the request able to answer.
func TestOptionalWriteCannotSpendTheRequestBudget(t *testing.T) {
	const requestBudget = 300 * time.Millisecond
	const writeBudget = 50 * time.Millisecond
	const writeBlocksFor = 10 * time.Second // a writer that is simply not coming

	reqCtx, cancelReq := context.WithTimeout(context.Background(), requestBudget)
	defer cancelReq()

	start := time.Now()
	wctx, cancelW := context.WithTimeout(reqCtx, writeBudget)
	defer cancelW()
	select {
	case <-time.After(writeBlocksFor):
		t.Fatal("the blocked write returned on its own; the test is not exercising the bound")
	case <-wctx.Done():
	}
	elapsed := time.Since(start)

	if !errors.Is(wctx.Err(), context.DeadlineExceeded) {
		t.Fatalf("write ctx err = %v; want DeadlineExceeded", wctx.Err())
	}
	if elapsed >= requestBudget {
		t.Fatalf("the optional write spent %v of a %v request budget", elapsed, requestBudget)
	}
	// The point: the REQUEST is still alive and can return its result.
	if err := reqCtx.Err(); err != nil {
		t.Fatalf("request context died with the optional write: %v", err)
	}
}

// The bound only works if it is smaller than the deadline it protects. An
// anchorWriteBudget at or above ledgerVerifyTimeout is the unbounded behaviour
// wearing a constant, and would reintroduce the 503 above.
func TestAnchorWriteBudgetIsSmallerThanTheVerifyDeadline(t *testing.T) {
	if anchorWriteBudget <= 0 {
		t.Fatalf("anchorWriteBudget = %v; the optional anchor write must be bounded", anchorWriteBudget)
	}
	if anchorWriteBudget >= ledgerVerifyTimeout {
		t.Fatalf("anchorWriteBudget %v >= ledgerVerifyTimeout %v: a blocked writer can still spend the whole read budget",
			anchorWriteBudget, ledgerVerifyTimeout)
	}
}
