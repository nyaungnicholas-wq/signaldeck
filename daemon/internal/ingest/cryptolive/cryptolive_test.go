package cryptolive

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// serveSnapshot stands in for tickstreamd, publishing at a chosen time.
func serveSnapshot(t *testing.T, publish time.Time) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		var nanos int64
		if !publish.IsZero() {
			nanos = publish.UnixNano()
		}
		_ = json.NewEncoder(w).Encode(tickstreamDTO{
			Valid:            true,
			BidPrice:         "63100.50",
			AskPrice:         "63101.00",
			Spread:           "0.50",
			Mid:              63100.75,
			WeightedMid:      63100.80,
			ImbSigned:        0.12,
			PublishUnixNanos: nanos,
		})
	}))
}

func newIngestor(t *testing.T, url string) (*Ingestor, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "cryptolive.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return New(st, url, 1), st
}

// A snapshot published moments ago is accepted.
func TestPollAcceptsFreshSnapshot(t *testing.T) {
	srv := serveSnapshot(t, time.Now().Add(-200*time.Millisecond))
	defer srv.Close()
	g, _ := newIngestor(t, srv.URL)
	if err := g.poll(context.Background()); err != nil {
		t.Fatalf("fresh snapshot must be accepted, got %v", err)
	}
}

// The row carries the PUBLISHER's timestamp, not local receipt time. Stamping
// receipt time makes every row inherit this machine's clock error.
func TestPollStoresPublishTimeNotReceiptTime(t *testing.T) {
	publish := time.Now().Add(-3 * time.Second)
	srv := serveSnapshot(t, publish)
	defer srv.Close()
	g, st := newIngestor(t, srv.URL)
	if err := g.poll(context.Background()); err != nil {
		t.Fatalf("poll: %v", err)
	}
	var got int64
	row := st.DB().QueryRow(`SELECT ts FROM snapshots_1s WHERE symbol_id = 1`)
	if err := row.Scan(&got); err != nil {
		t.Fatalf("reading back snapshot: %v", err)
	}
	if got != publish.Unix() {
		t.Errorf("stored ts = %d, want publish time %d (delta %ds) — "+
			"receipt time was used instead of the publisher's",
			got, publish.Unix(), got-publish.Unix())
	}
}

// A stalled feed is caught: tickstream is up but its publish time is old.
func TestPollRejectsStaleSnapshot(t *testing.T) {
	srv := serveSnapshot(t, time.Now().Add(-30*time.Second))
	defer srv.Close()
	g, _ := newIngestor(t, srv.URL)
	err := g.poll(context.Background())
	if err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("stale snapshot must be refused, got %v", err)
	}
}

// THE FAILS-OPEN BUG. A publish timestamp in the future makes
// time.Since() negative, which is never > 10s, so the original guard passed
// every such snapshot — a dead feed would read as fresh forever.
func TestPollRejectsFutureSnapshot(t *testing.T) {
	srv := serveSnapshot(t, time.Now().Add(77*time.Second))
	defer srv.Close()
	g, _ := newIngestor(t, srv.URL)
	err := g.poll(context.Background())
	if err == nil {
		t.Fatal("a snapshot published 77s in the future must be refused; " +
			"accepting it lets clock skew disable stale detection entirely")
	}
	if !strings.Contains(err.Error(), "clock skew") {
		t.Errorf("want a clock-skew error, got %v", err)
	}
}

// Sub-second disagreement is ordinary scheduling, not skew, and must not flap.
func TestPollToleratesSubSecondFutureSnapshot(t *testing.T) {
	srv := serveSnapshot(t, time.Now().Add(200*time.Millisecond))
	defer srv.Close()
	g, _ := newIngestor(t, srv.URL)
	if err := g.poll(context.Background()); err != nil {
		t.Fatalf("200ms of skew is normal and must be accepted, got %v", err)
	}
}

// A snapshot with no publish timestamp cannot be placed in time at all.
func TestPollRejectsMissingPublishTime(t *testing.T) {
	srv := serveSnapshot(t, time.Time{})
	defer srv.Close()
	g, _ := newIngestor(t, srv.URL)
	if err := g.poll(context.Background()); err == nil {
		t.Fatal("snapshot without a publish timestamp must be refused")
	}
}
