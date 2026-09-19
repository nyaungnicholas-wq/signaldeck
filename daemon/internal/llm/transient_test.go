package llm

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestCompleteWith_ExhaustedRetriesWrapErrTransient(t *testing.T) {
	var hits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"detail":"ResourceExhausted: Worker local total request limit reached (4100/16)"}`))
	}))
	defer srv.Close()
	c := New([]string{"testkey"}, srv.URL, "m", "m", "m", 100).(*httpClient)
	_, err := c.CompleteWith(context.Background(), "m", "sys", []Message{{Role: "user", Content: "hi"}}, 16)
	if err == nil {
		t.Fatal("want an error from a server that always answers 503")
	}
	if !errors.Is(err, ErrTransient) {
		t.Fatalf("err = %v, want it to wrap ErrTransient", err)
	}
	if n := atomic.LoadInt64(&hits); n < 2 {
		t.Fatalf("server saw %d request(s), want more than 1 (retries must actually happen)", n)
	}
	// this is the real production failure - the provider's shared free-tier pool answers 503 ResourceExhausted under load - and the sentinel is what lets the sentiment tagger tell it apart from a real bug WITHOUT matching vendor error text.
}

func TestCompleteWith_PermanentErrorIsNotTransient(t *testing.T) {
	var hits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"detail":"bad request"}`))
	}))
	defer srv.Close()
	c := New([]string{"testkey"}, srv.URL, "m", "m", "m", 100).(*httpClient)
	_, err := c.CompleteWith(context.Background(), "m", "sys", []Message{{Role: "user", Content: "hi"}}, 16)
	if err == nil {
		t.Fatal("want an error from a server that always answers 400")
	}
	if errors.Is(err, ErrTransient) {
		t.Fatalf("permanent error wrongly wrapped as transient: %v", err)
	}
	if n := atomic.LoadInt64(&hits); n != 1 {
		t.Fatalf("server saw %d request(s), want exactly 1 (a permanent error must not be retried)", n)
	}
	// this is the guard that stops ErrTransient from swallowing real bugs - without it, every permanent failure would be reported as "provider busy, resuming next pass" and the worker would look healthy forever.
}