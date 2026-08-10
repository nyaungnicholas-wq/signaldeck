// Package e2e boots the real signaldeckd binary against a throwaway database
// and drives it over HTTP: health, auth, subscribe/watchlist, agents fleet,
// graceful shutdown, and persistence across a restart. No Alpaca or LLM keys
// reach it and no request can leave the machine — every worker must degrade
// gracefully.
//
// It runs on every platform the daemon builds for, Windows included. See
// TestDaemonGracefulRestart for why the old blanket Windows skip was both
// correct in its premise and too broad in its conclusion.
package e2e

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/testharness"
)

const (
	csrfHeader = "X-Signaldeck"
	// A cold boot applies schema.sql, runs migrate(), verifySchema() and
	// VerifyAuditContract() against an empty file. 20s was tight on a warm Unix
	// box and is not enough for a cold Windows one.
	bootTimeout = 60 * time.Second
	// The daemon's own shutdown budget plus slack.
	shutdownTimeout = 30 * time.Second
)

func postJSON(t *testing.T, c *http.Client, url string, body any) (*http.Response, string) {
	t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", url, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(csrfHeader, "1")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer resp.Body.Close() //nolint:errcheck
	rb, _ := io.ReadAll(resp.Body)
	return resp, string(rb)
}

func getBody(t *testing.T, c *http.Client, url string) (*http.Response, string) {
	t.Helper()
	resp, err := c.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close() //nolint:errcheck
	rb, _ := io.ReadAll(resp.Body)
	return resp, string(rb)
}

// registerAndSubscribe drives the account + watchlist path both tests need.
func registerAndSubscribe(t *testing.T, c *http.Client, base string) {
	t.Helper()
	resp, body := postJSON(t, c, base+"/api/auth/register", map[string]string{
		"username": "e2euser", "password": "correct-horse-9",
	})
	if resp.StatusCode != 200 {
		t.Fatalf("register: %d %s", resp.StatusCode, body)
	}
	resp, body = postJSON(t, c, base+"/api/subscribe", map[string]string{
		"symbol": "ETH/USD", "market": "crypto",
	})
	if resp.StatusCode != 200 {
		t.Fatalf("subscribe ETH/USD: %d %s", resp.StatusCode, body)
	}
}

// TestDaemonEndToEnd is the API contract: it never signals the daemon, so it
// runs identically on every platform.
func TestDaemonEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e: skipped in -short mode")
	}
	bin := testharness.Build(t, "..")
	up := testharness.FakeUpstreams(t)
	addr := testharness.FreeAddr(t)
	dbPath := testharness.DBPath(t, "e2e.db")

	d := testharness.Start(t, bin, testharness.Env(t, dbPath, addr, up.URL), addr)
	d.WaitHealthy(t, bootTimeout)

	// Health reports Alpaca disabled. This is the credential-isolation assertion
	// and it now means something: isolation comes from SIGNALDECK_ROOT pointing
	// at an EMPTY temp dir (internal/config/config.go:33-35 takes it verbatim),
	// so config.Load finds no daemon/.env and no stock-trader/.env. The env var
	// this test used to rely on — HOME — was never consulted, because
	// projectRoot()'s second rung walks the WORKING DIRECTORY upward for a
	// signaldeck/daemon and found the real checkout from daemon/e2e long before
	// the $HOME rung was reached.
	_, health := getBody(t, http.DefaultClient, d.URL+"/api/health")
	if !strings.Contains(health, `"alpaca":false`) {
		t.Fatalf("expected alpaca:false in health — credential isolation is broken, "+
			"this daemon found real keys: %s", health)
	}

	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar, Timeout: 10 * time.Second}

	// Unauthenticated watchlist is 401.
	resp, _ := getBody(t, http.DefaultClient, d.URL+"/api/watchlist")
	if resp.StatusCode != 401 {
		t.Fatalf("anon watchlist: %d want 401", resp.StatusCode)
	}

	// Register + session cookie.
	resp, body := postJSON(t, c, d.URL+"/api/auth/register", map[string]string{
		"username": "e2euser", "password": "correct-horse-9",
	})
	if resp.StatusCode != 200 {
		t.Fatalf("register: %d %s", resp.StatusCode, body)
	}
	resp, body = getBody(t, c, d.URL+"/api/auth/me")
	if resp.StatusCode != 200 || !strings.Contains(body, `"e2euser"`) {
		t.Fatalf("me: %d %s", resp.StatusCode, body)
	}

	// Subscribing a STOCK without Alpaca keys must degrade with a clear client
	// error, not a crash.
	resp, body = postJSON(t, c, d.URL+"/api/subscribe", map[string]string{
		"symbol": "AAPL", "market": "stocks",
	})
	if resp.StatusCode != 422 || !strings.Contains(body, "Alpaca") {
		t.Fatalf("keyless AAPL subscribe: %d %s (want 422 mentioning Alpaca)", resp.StatusCode, body)
	}

	// Crypto subscription needs no keys and must land on the watchlist.
	resp, body = postJSON(t, c, d.URL+"/api/subscribe", map[string]string{
		"symbol": "ETH/USD", "market": "crypto",
	})
	if resp.StatusCode != 200 {
		t.Fatalf("subscribe ETH/USD: %d %s", resp.StatusCode, body)
	}
	resp, body = getBody(t, c, d.URL+"/api/watchlist")
	if resp.StatusCode != 200 || !strings.Contains(body, `"ETH/USD"`) {
		t.Fatalf("watchlist: %d %s (want ETH/USD)", resp.StatusCode, body)
	}

	// The worker fleet reports runs on /api/agents (workers fire immediately on
	// start; poll briefly).
	var workers []map[string]any
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		_, body = getBody(t, c, d.URL+"/api/agents")
		workers = nil
		if json.Unmarshal([]byte(body), &workers) == nil && len(workers) > 0 {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if len(workers) == 0 {
		t.Fatalf("no worker runs on /api/agents: %s\nlogs:\n%s", body, d.Logs())
	}
	names := map[string]bool{}
	for _, w := range workers {
		if n, _ := w["worker"].(string); n != "" {
			names[n] = true
		}
	}
	if len(names) < 3 {
		t.Fatalf("expected a fleet of workers, saw only %v", names)
	}

	// Every upstream the fleet reached for was intercepted by the fake server —
	// SIGNALDECK_HTTP_SANDBOX rewrote it at the transport, so nothing left this
	// machine. Log the set rather than asserting a fixed one: WHICH workers fire
	// inside the window depends on their schedules, so a fixed list would be a
	// flaky test dressed up as a strict one. The guarantee is structural; this
	// line is the evidence a human can read.
	t.Logf("upstream hosts intercepted (0 reached the network): %v", up.Origins())
}

// TestDaemonGracefulRestart covers the two things the suite exists for and the
// old code skipped away on the deployment platform: a graceful shutdown, and
// state surviving a restart.
//
// WHY THE OLD SKIP EXISTED, AND WHY IT IS GONE. e2e_test.go:218-225 skipped the
// whole suite on Windows because it sent syscall.SIGTERM, and os.Process.Signal
// on Windows accepts only os.Kill — anything else returns
// syscall.EWINDOWS ("not supported by windows") without touching the child. The
// author was right that downgrading to a Kill would keep the test green while no
// longer testing the thing it exists to test. But "Go cannot deliver SIGTERM on
// Windows" is not "Windows has no graceful shutdown": it has console control
// events, the Go runtime in the child turns CTRL_BREAK_EVENT into os.Interrupt,
// and cmd/signaldeckd/main.go:67 already listens for os.Interrupt beside
// syscall.SIGTERM. testharness.GracefulStop sends the platform's real mechanism
// into that same listener, so the code path under test on Windows is the code
// path production takes on Unix. Nothing is faked and nothing is downgraded.
func TestDaemonGracefulRestart(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e: skipped in -short mode")
	}
	bin := testharness.Build(t, "..")
	up := testharness.FakeUpstreams(t)
	addr := testharness.FreeAddr(t)
	dbPath := testharness.DBPath(t, "restart.db")

	d := testharness.Start(t, bin, testharness.Env(t, dbPath, addr, up.URL), addr)
	d.WaitHealthy(t, bootTimeout)

	jar, _ := cookiejar.New(nil)
	registerAndSubscribe(t, &http.Client{Jar: jar, Timeout: 10 * time.Second}, d.URL)

	if err := d.GracefulStop(t, shutdownTimeout); err != nil {
		if errors.Is(err, testharness.ErrSignalUnavailable) {
			// The ONLY skip left, and it names a measured environmental cause
			// rather than a whole platform: this process has no console to
			// generate the control event on. Run `go test` from a terminal or a
			// CI runner and this test executes.
			t.Skipf("e2e: cannot deliver %s here: %v", testharness.SignalName(), err)
		}
		t.Fatalf("e2e: delivering %s failed: %v\nlogs:\n%s", testharness.SignalName(), err, d.Logs())
	}

	// Restart on the SAME database and the SAME port: the account and the
	// watchlist must survive.
	d2 := testharness.Start(t, bin, testharness.Env(t, dbPath, addr, up.URL), addr)
	d2.WaitHealthy(t, bootTimeout)

	jar2, _ := cookiejar.New(nil)
	c2 := &http.Client{Jar: jar2, Timeout: 10 * time.Second}
	resp, body := postJSON(t, c2, d2.URL+"/api/auth/login", map[string]string{
		"username": "e2euser", "password": "correct-horse-9",
	})
	if resp.StatusCode != 200 {
		t.Fatalf("login after restart: %d %s (persistence broken?)", resp.StatusCode, body)
	}
	resp, body = getBody(t, c2, d2.URL+"/api/watchlist")
	if resp.StatusCode != 200 || !strings.Contains(body, `"ETH/USD"`) {
		t.Fatalf("watchlist after restart: %d %s", resp.StatusCode, body)
	}

	if err := d2.GracefulStop(t, shutdownTimeout); err != nil {
		t.Fatalf("e2e: second graceful stop: %v\nlogs:\n%s", err, d2.Logs())
	}
}
