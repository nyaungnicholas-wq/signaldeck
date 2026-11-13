package api

import (
	"context"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

// TestServeUntilWaitsForASlowHandlerThatHonoursItsContext prevents the store
// from closing while a handler is still running after its context is cancelled.
func TestServeUntilWaitsForASlowHandlerThatHonoursItsContext(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	var finished atomic.Bool
	entered := make(chan struct{})

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-r.Context().Done()
		time.Sleep(50 * time.Millisecond)
		finished.Store(true)
	})

	srv := &http.Server{Handler: handler}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- serveUntil(ctx, srv, ln, 200*time.Millisecond)
	}()

	go func() {
		resp, err := http.Get("http://" + ln.Addr().String() + "/")
		if err == nil {
			resp.Body.Close()
		}
	}()

	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("handler never entered")
	}

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serveUntil: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serveUntil did not return")
	}

	if !finished.Load() {
		t.Fatal("serveUntil returned while a handler was still running: the store would close under it")
	}
}

// TestServeUntilReturnsTheServeError prevents silently swallowing a Serve error.
func TestServeUntilReturnsTheServeError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ln.Close()

	srv := &http.Server{Handler: http.NotFoundHandler()}
	err = serveUntil(context.Background(), srv, ln, time.Second)
	if err == nil {
		t.Fatal("want the Serve error for a closed listener, got nil")
	}
}
