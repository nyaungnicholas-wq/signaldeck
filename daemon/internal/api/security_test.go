package api

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/config"
)

// Probes must stay reachable without a credential. /api/ready was omitted from
// the exemption list and began 401-ing the moment PublicReads closed — a
// readiness endpoint nothing can probe, which is the same as not having one.
func TestProbesStayReachableWhenReadsAreClosed(t *testing.T) {
	d := Deps{Cfg: config.Config{PublicReads: false}}
	for _, p := range []string{"/api/health", "/api/ready", "/api/auth/login", "/api/auth/register"} {
		if d.requiresAuth(p) {
			t.Errorf("%s requires auth with PublicReads=false — a monitor or the login page cannot reach it", p)
		}
	}
	// Everything else must still be closed, or the exemption is a hole.
	// /api/track-record used to stand in for "everything else" here. It is now
	// a DELIBERATE exemption (see TestProofReceiptsArePublicButNarrowly), so it
	// moved out rather than being deleted — the closed set still needs
	// representatives, and /api/readyish still guards against prefix matching.
	for _, p := range []string{"/api/dashboard", "/api/companies", "/api/readyish"} {
		if !d.requiresAuth(p) {
			t.Errorf("%s is anonymously readable with PublicReads=false", p)
		}
	}
}

// The /proof page is the one surface built to be shown to someone with no
// account, and it reads exactly two endpoints. They must be public even with
// PublicReads=false, because that flag defaults closed whenever a tunnel is
// configured (A9) and the receipts are meant to survive that.
//
// Both directions are asserted. A test that only checked the two paths were
// open would pass just as happily if the exemption had been written as a
// prefix and quietly published the whole ledger surface.
func TestProofReceiptsArePublicButNarrowly(t *testing.T) {
	d := Deps{Cfg: config.Config{PublicReads: false}}
	// /api/prereg is on this list since 2026-09-13. It was already in
	// publicRoutes, so a PUBLIC deployment served it, but not this posture --
	// and /proof now renders the registration chain, which three other pages
	// send readers here to read. Without the exemption that section shows
	// "not readable without a session" to exactly the anonymous visitor the
	// page exists for.
	for _, p := range []string{"/api/track-record", "/api/ledger/verify", "/api/accuracy", "/api/prereg"} {
		if d.requiresAuth(p) {
			t.Errorf("%s requires auth with PublicReads=false — /proof renders its "+
				"error state to every anonymous visitor it exists for", p)
		}
	}
	// The exemption is a list of EXACT paths, NOT a prefix and NOT the ledger
	// surface.
	// /api/ledger/anchors is the neighbour that must not ride along: it is the
	// signed-anchor history, and publishing it was never the decision made here.
	for _, p := range []string{
		"/api/ledger/anchors", "/api/ledger", "/api/track-record/raw",
		"/api/portfolio", "/api/watchlist", "/api/ai/ask", "/api/notify/test",
	} {
		if !d.requiresAuth(p) {
			t.Errorf("%s became anonymously readable — the /proof exemption widened "+
				"beyond the two endpoints it was scoped to", p)
		}
	}
}

// stalledClient is a client that stopped reading: every body write fails the
// way the server's write deadline makes it fail.
type stalledClient struct{ *httptest.ResponseRecorder }

func (stalledClient) Write([]byte) (int, error) {
	return 0, errors.New("write tcp 127.0.0.1:8322->127.0.0.1:50123: i/o timeout")
}

// writeJSON's encode error ("api: encode ... i/o timeout") was logged while the
// access log line for the same request said status=200, so a monitor counting
// statuses scored an undelivered response as a success.
func TestAccessLogRecordsAFailedResponseWrite(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	h := Deps{}.withAccessLog(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]int{"n": 1})
	}))
	for _, c := range []struct {
		w    http.ResponseWriter
		want []string
		not  string
	}{
		{httptest.NewRecorder(), []string{"status=200"}, "write_err"},
		{stalledClient{httptest.NewRecorder()}, []string{"status=499", "handler_status=200", "i/o timeout"}, " status=200"},
	} {
		buf.Reset()
		h.ServeHTTP(c.w, httptest.NewRequest(http.MethodGet, "/api/accesslog-probe", nil))
		var line string
		for _, l := range strings.Split(buf.String(), "\n") {
			if strings.Contains(l, "msg=request") && strings.Contains(l, "/api/accesslog-probe") {
				line = l
			}
		}
		for _, w := range c.want {
			if !strings.Contains(line, w) {
				t.Errorf("access log %q lacks %q", line, w)
			}
		}
		if strings.Contains(line, c.not) {
			t.Errorf("access log %q contains %q", line, c.not)
		}
	}
}
