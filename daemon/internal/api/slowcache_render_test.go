package api

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// A failed background rebuild must carry a real error: before 2026-10-11 the
// refresh logged only its (still live) context error, so every failure read
// err=<nil> and hid the 500 the handler had answered.
func TestSWRRenderReportsHandlerStatus(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/screener", nil)
	body, status := swrRender(req, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	if body != nil || status != http.StatusInternalServerError {
		t.Fatalf("500 handler: body=%q status=%d, want nil/500", body, status)
	}
	body, status = swrRender(req, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok")) })
	if string(body) != "ok" || status != http.StatusOK {
		t.Fatalf("200 handler: body=%q status=%d", body, status)
	}
	_, status = swrRender(req, func(w http.ResponseWriter, r *http.Request) { panic("x") })
	if status != http.StatusInternalServerError {
		t.Fatalf("panicking handler: status=%d, want 500", status)
	}
}

func TestRenderFailureNeverNil(t *testing.T) {
	err := renderFailure(nil, http.StatusInternalServerError)
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("live context + 500: got %v, want an error naming 500", err)
	}
	if err := renderFailure(context.DeadlineExceeded, 500); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expired context must stay DeadlineExceeded for the out-of-time message, got %v", err)
	}
}

// The refresh call site itself: a background rebuild whose handler answers 500
// must log a real error, never err=<nil> (a helper-only test passed with the
// call site reverted, 2026-10-11 review).
func TestSWRBodyRefreshLogsHandlerFailure(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(prev)

	c := newSWRBodyCache(time.Minute)
	e := &swrBodyEntry{body: []byte("old"), rebuilding: true}
	bg := httptest.NewRequest(http.MethodGet, "/api/screener", nil)
	c.refresh(context.Background(), bg, e, "", "st2|screener", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	out := buf.String()
	if !strings.Contains(out, "cache rebuild failed") {
		t.Fatalf("no rebuild-failure line logged: %q", out)
	}
	if strings.Contains(out, "err=<nil>") || !strings.Contains(out, "HTTP 500") {
		t.Fatalf("rebuild failure logged without its cause: %q", out)
	}
	if string(e.body) != "old" || e.rebuilding {
		t.Fatalf("stale copy must keep serving and rebuilding must clear: body=%q rebuilding=%v", e.body, e.rebuilding)
	}
}
