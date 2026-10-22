package pipeline

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// A listener that accepts but answers too slowly is still an error (the run
// must count as failing) but must not be called "unreachable": on 2026-10-11
// that wording sent the reader looking for a dead listener while the health
// handler was simply stalled on its reads.
func TestAPIProbe_SlowAnswerIsAnErrorButNotUnreachable(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)

	p := &APIProbe{Addr: strings.TrimPrefix(srv.URL, "http://"), Client: &http.Client{Timeout: 100 * time.Millisecond}}
	_, err := p.Run(context.Background())
	if err == nil {
		t.Fatal("slow listener reported success")
	}
	if strings.Contains(err.Error(), "unreachable") || !strings.Contains(err.Error(), "gave no response") {
		t.Fatalf("err = %v, want the slow-answer wording, not 'unreachable'", err)
	}
}
