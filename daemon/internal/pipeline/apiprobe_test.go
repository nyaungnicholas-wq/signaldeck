package pipeline

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A listening API must report ok; a dead one must report ERROR, because
// status="error" is the only status health.FailingWorkers counts. Both
// directions are pinned so this cannot be "fixed" by always returning nil.
func TestAPIProbe_LiveListenerPasses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/health" {
			t.Errorf("probed %s, want /api/health", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := &APIProbe{Addr: strings.TrimPrefix(srv.URL, "http://")}
	detail, err := p.Run(context.Background())
	if err != nil {
		t.Fatalf("live listener reported an error: %v", err)
	}
	if !strings.Contains(detail, "answering") {
		t.Fatalf("detail = %q, want it to say the listener is answering", detail)
	}
}

func TestAPIProbe_DeadListenerIsAnError(t *testing.T) {
	// Bind then immediately release, so the port is almost certainly closed —
	// the state this worker exists to notice.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()

	p := &APIProbe{Addr: addr}
	if _, err := p.Run(context.Background()); err == nil {
		t.Fatal("a dead listener returned nil — the run would be filed status=ok " +
			"and FailingWorkers, /api/ready and health.json would all stay green " +
			"with the API unreachable, which is the entire defect this closes")
	}
}

func TestAPIProbe_A500IsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	p := &APIProbe{Addr: strings.TrimPrefix(srv.URL, "http://")}
	if _, err := p.Run(context.Background()); err == nil {
		t.Fatal("HTTP 500 from the health route returned nil")
	}
}

// An unconfigured address is NOT a failure — reporting one would be a
// fabricated measurement about a surface that does not exist.
func TestAPIProbe_NoAddressIsNotAFailure(t *testing.T) {
	p := &APIProbe{}
	detail, err := p.Run(context.Background())
	if err != nil {
		t.Fatalf("empty Addr reported an error: %v", err)
	}
	if !strings.Contains(detail, "nothing to probe") {
		t.Fatalf("detail = %q, want it to say there is nothing to probe", detail)
	}
}

// A wildcard bind is reachable at loopback; dialing the bind string verbatim
// fails on the empty host, which would make the probe cry wolf on every run.
func TestProbeHost_RewritesWildcardBinds(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{":8322", "127.0.0.1:8322"},
		{"0.0.0.0:8322", "127.0.0.1:8322"},
		{"[::]:8322", "127.0.0.1:8322"},
		{"127.0.0.1:8322", "127.0.0.1:8322"},
	} {
		if got := probeHost(tc.in); got != tc.want {
			t.Errorf("probeHost(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
