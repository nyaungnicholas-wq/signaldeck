// Package e2e boots the real signaldeckd binary against a throwaway database
// and drives it over HTTP: health, auth, subscribe/watchlist, agents fleet,
// graceful SIGTERM shutdown, and persistence across a restart. No Alpaca or
// LLM keys are provided — every worker must degrade gracefully.
package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

const csrfHeader = "X-Signaldeck"

// buildDaemon compiles the daemon once into a temp dir.
func buildDaemon(t *testing.T) string {
	t.Helper()
	// Windows will not exec a file without the .exe extension, and `go build -o`
	// writes exactly the name it is given.
	name := "signaldeckd"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	bin := filepath.Join(t.TempDir(), name)
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/signaldeckd")
	cmd.Dir = ".." // daemon module root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	return bin
}

// freePort asks the kernel for a free localhost port.
func freePort(t *testing.T) int {
	t.Helper()
	for i := 0; i < 5; i++ {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			continue
		}
		port := l.Addr().(*net.TCPAddr).Port
		_ = l.Close()
		return port
	}
	t.Fatal("no free port")
	return 0
}

// safeBuffer guards a bytes.Buffer with a mutex. cmd.Stdout/cmd.Stderr are
// written by exec's internal copy goroutines for as long as the child is
// alive, while the test goroutine reads d.logs.String() on every failure path
// (including ones that fire before the child exits, e.g. waitHealthy timing
// out) — a bare bytes.Buffer is not safe for that concurrent access and
// go test -race catches it reliably.
type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *safeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type daemon struct {
	cmd *exec.Cmd
	url string
	// done receives cmd.Wait()'s result exactly once; exited flips true in the
	// same goroutine right after, so other goroutines can check "has this
	// process been reaped" without touching cmd.ProcessState directly. Reading
	// ProcessState from Cleanup while cmd.Wait() (running in its own goroutine)
	// writes it is the second race go test -race reports on this file.
	done   chan error
	exited atomic.Bool
	logs   *safeBuffer
}

// startDaemon launches the binary with an isolated project root (so no real
// .env, Alpaca, or LLM keys leak in), the given DB path, and a fixed port.
//
// HOME alone never isolated anything. config.projectRoot() walks the WORKING
// DIRECTORY upward for an ancestor holding signaldeck/daemon, and this suite's
// cwd is daemon/e2e inside the real checkout — so the walk found the real root
// and the daemon read the operator's real daemon/.env (LLM key) and
// stock-trader/.env (Alpaca keys). The os.UserHomeDir() rung that HOME
// controls is the LAST resort and was never reached. SIGNALDECK_ROOT is the
// FIRST rung and stops the walk outright; it is what actually isolates this.
func startDaemon(t *testing.T, bin, home, dbPath string, port int) *daemon {
	t.Helper()
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	cmd := exec.Command(bin)
	logs := &safeBuffer{}
	cmd.Stdout = logs
	cmd.Stderr = logs
	cmd.Env = []string{
		// The real isolation: an empty temp dir stated as the project root.
		// config.projectRoot() takes it verbatim and never walks the tree, so
		// neither <root>/signaldeck/daemon/.env nor <root>/stock-trader/.env
		// exists and the daemon runs with NO Alpaca and NO LLM credentials.
		// Must be non-blank — the loader refuses a blank value rather than
		// falling back to the probe. See the comment on startDaemon.
		"SIGNALDECK_ROOT=" + home,
		"HOME=" + home, // still isolated, but it is NOT what stops the .env lookups
		"PATH=" + os.Getenv("PATH"),
		"TMPDIR=" + os.TempDir(),
		// The daemon refuses to start from an unattributable build, because rows
		// it freezes into the real DB could not then be graded (cmd/signaldeckd
		// main.go). That guard is right in production and wrong here: this
		// daemon writes to a throwaway temp DB whose rows are never graded by
		// anything, and a working tree is dirty by definition while it is being
		// worked on. Without this the entire e2e suite fails on every developer
		// machine mid-change — which is how an e2e suite quietly stops being run.
		"SIGNALDECK_ALLOW_DIRTY_BUILD=1",
		"SIGNALDECK_DB=" + dbPath,
		"SIGNALDECK_HTTP=" + addr,
		"SIGNALDECK_ALLOWED_HOSTS=" + addr,
		"SIGNALDECK_OPEN_SIGNUP=true",
		"SIGNALDECK_PUBLIC_READS=true",
		// Point companion-service URLs at a dead port so the test never
		// touches a real hud/tickstream instance on this machine.
		"SIGNALDECK_HUD_URL=http://127.0.0.1:1/api/summary",
		"SIGNALDECK_TICKSTREAM_URL=http://127.0.0.1:1/api/snapshot",
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start daemon: %v", err)
	}
	d := &daemon{cmd: cmd, url: "http://" + addr, done: make(chan error, 1), logs: logs}
	go func() {
		err := cmd.Wait()
		d.exited.Store(true)
		d.done <- err
	}()
	t.Cleanup(func() {
		if !d.exited.Load() {
			_ = cmd.Process.Kill()
			<-d.done
		}
	})
	return d
}

// waitHealthy polls /api/health until the API answers.
func (d *daemon) waitHealthy(t *testing.T, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		resp, err := http.Get(d.url + "/api/health")
		if err == nil {
			io.Copy(io.Discard, resp.Body) //nolint:errcheck
			resp.Body.Close()              //nolint:errcheck
			if resp.StatusCode == 200 {
				return
			}
		}
		select {
		case err := <-d.done:
			t.Fatalf("daemon exited before healthy: %v\nlogs:\n%s", err, d.logs.String())
		case <-time.After(100 * time.Millisecond):
		}
	}
	t.Fatalf("daemon never became healthy\nlogs:\n%s", d.logs.String())
}

// stopForRestart ends the daemon so the restart-persistence steps can run, using
// the strongest shutdown the platform can express.
//
// On Unix that is a graceful SIGTERM. Go cannot deliver SIGTERM on Windows at
// all (os.Process.Signal returns "not supported by windows"), so Windows gets a
// hard Kill instead. These are NOT equivalent and the difference is deliberately
// not papered over: on Windows the restart steps prove CRASH-restart durability
// (SQLite WAL recovery after abrupt termination) and prove nothing whatsoever
// about graceful shutdown. The graceful contract is asserted separately, on Unix
// only, by TestGracefulShutdownSIGTERM.
func (d *daemon) stopForRestart(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		d.stopHard(t)
		return
	}
	d.stopGraceful(t)
}

// stopHard kills the daemon and requires only that it actually exits. A killed
// process reports a non-nil wait error BY DEFINITION, so asserting a clean exit
// here would be asserting something untrue; the exit itself is the whole claim.
func (d *daemon) stopHard(t *testing.T) {
	t.Helper()
	if err := d.cmd.Process.Kill(); err != nil {
		t.Fatalf("kill: %v", err)
	}
	select {
	case <-d.done:
	case <-time.After(10 * time.Second):
		t.Fatalf("daemon did not exit within 10s of Kill\nlogs:\n%s", d.logs.String())
	}
}

// stopGraceful sends SIGTERM and requires a clean exit within 10s.
func (d *daemon) stopGraceful(t *testing.T) {
	t.Helper()
	if err := d.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("sigterm: %v", err)
	}
	select {
	case err := <-d.done:
		if err != nil {
			t.Fatalf("daemon exited uncleanly after SIGTERM: %v\nlogs:\n%s", err, d.logs.String())
		}
	case <-time.After(10 * time.Second):
		_ = d.cmd.Process.Kill()
		t.Fatalf("daemon did not exit within 10s of SIGTERM\nlogs:\n%s", d.logs.String())
	}
}

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

func TestDaemonEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e: skipped in -short mode")
	}
	// This test runs on EVERY platform. Only one of its steps — graceful SIGTERM
	// shutdown — is inexpressible on Windows, and that step now lives in its own
	// test (TestGracefulShutdownSIGTERM) so the other seven contract checks are
	// not thrown away on the platform the daemon actually runs on. See
	// stopForRestart for what Windows substitutes and what it does NOT prove.
	bin := buildDaemon(t)
	// An empty dir used as BOTH the stated project root and HOME. The root is
	// what makes "no .env files → no Alpaca/LLM keys" true; HOME never did.
	home := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "e2e.db")
	port := freePort(t)

	d := startDaemon(t, bin, home, dbPath, port)
	d.waitHealthy(t, 20*time.Second)

	// An ANONYMOUS /api/health gets the signal and none of the internals. This
	// endpoint stays reachable without a credential, so on a tunnel-exposed
	// daemon its body is world-readable: worker names map the architecture and
	// the revision names the exact source to audit. Asserting the redaction here
	// keeps that boundary from regressing quietly.
	_, anonHealth := getBody(t, http.DefaultClient, d.url+"/api/health")
	for _, leaked := range []string{`"alpaca"`, `"revision"`, `"workers"`, `"alertTransports"`} {
		if strings.Contains(anonHealth, leaked) {
			t.Fatalf("anonymous health leaked %s: %s", leaked, anonHealth)
		}
	}

	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar, Timeout: 10 * time.Second}

	// Unauthenticated watchlist is 401.
	resp, _ := getBody(t, http.DefaultClient, d.url+"/api/watchlist")
	if resp.StatusCode != 401 {
		t.Fatalf("anon watchlist: %d want 401", resp.StatusCode)
	}

	// Register + session cookie.
	resp, body := postJSON(t, c, d.url+"/api/auth/register", map[string]string{
		"username": "e2euser", "password": "correct-horse-9",
	})
	if resp.StatusCode != 200 {
		t.Fatalf("register: %d %s", resp.StatusCode, body)
	}
	resp, body = getBody(t, c, d.url+"/api/auth/me")
	if resp.StatusCode != 200 || !strings.Contains(body, `"e2euser"`) {
		t.Fatalf("me: %d %s", resp.StatusCode, body)
	}

	// Health reports Alpaca disabled (keys isolated away). Asserted through the
	// AUTHENTICATED client on purpose: the anonymous payload above deliberately
	// withholds `alpaca` entirely, so checking it without a session asserts only
	// that a redacted body lacks a redacted field — which passes for the wrong
	// reason the day key isolation actually breaks.
	_, health := getBody(t, c, d.url+"/api/health")
	if !strings.Contains(health, `"alpaca":false`) {
		t.Fatalf("expected alpaca:false in health (key isolation broken): %s", health)
	}

	// Subscribing a STOCK without Alpaca keys must degrade with a clear
	// client error, not a crash.
	resp, body = postJSON(t, c, d.url+"/api/subscribe", map[string]string{
		"symbol": "AAPL", "market": "stocks",
	})
	if resp.StatusCode != 422 || !strings.Contains(body, "Alpaca") {
		t.Fatalf("keyless AAPL subscribe: %d %s (want 422 mentioning Alpaca)", resp.StatusCode, body)
	}

	// Crypto subscription needs no keys and must land on the watchlist.
	resp, body = postJSON(t, c, d.url+"/api/subscribe", map[string]string{
		"symbol": "ETH/USD", "market": "crypto",
	})
	if resp.StatusCode != 200 {
		t.Fatalf("subscribe ETH/USD: %d %s", resp.StatusCode, body)
	}
	resp, body = getBody(t, c, d.url+"/api/watchlist")
	if resp.StatusCode != 200 || !strings.Contains(body, `"ETH/USD"`) {
		t.Fatalf("watchlist: %d %s (want ETH/USD)", resp.StatusCode, body)
	}

	// The worker fleet reports runs on /api/agents (workers fire immediately
	// on start; poll briefly).
	// Wait for the condition actually asserted — three DISTINCT workers — not
	// merely for the first run of any kind. The two were mismatched: the loop
	// stopped at len(workers) > 0 and the assertion then demanded three names, so
	// a fleet that was merely slow to report failed. It surfaced under a full
	// `go test ./...`, where parallel package compilation starves the daemon of
	// CPU and only backfiller and crypto-live had reported inside the window.
	names := map[string]bool{}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		_, body = getBody(t, c, d.url+"/api/agents")
		var workers []map[string]any
		if json.Unmarshal([]byte(body), &workers) == nil {
			for _, w := range workers {
				if n, _ := w["worker"].(string); n != "" {
					names[n] = true
				}
			}
		}
		if len(names) >= 3 {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if len(names) < 3 {
		t.Fatalf("expected a fleet of workers, saw only %v\nlast body: %s\nlogs:\n%s",
			names, body, d.logs.String())
	}

	// Shut down (graceful on Unix, hard Kill on Windows — see stopForRestart).
	d.stopForRestart(t)

	// Restart on the same DB: the account must persist.
	d2 := startDaemon(t, bin, home, dbPath, port)
	d2.waitHealthy(t, 20*time.Second)
	jar2, _ := cookiejar.New(nil)
	c2 := &http.Client{Jar: jar2, Timeout: 10 * time.Second}
	resp, body = postJSON(t, c2, d2.url+"/api/auth/login", map[string]string{
		"username": "e2euser", "password": "correct-horse-9",
	})
	if resp.StatusCode != 200 {
		t.Fatalf("login after restart: %d %s (persistence broken?)", resp.StatusCode, body)
	}
	resp, body = getBody(t, c2, d2.url+"/api/watchlist")
	if resp.StatusCode != 200 || !strings.Contains(body, `"ETH/USD"`) {
		t.Fatalf("watchlist after restart: %d %s", resp.StatusCode, body)
	}
	d2.stopForRestart(t)
}

// TestGracefulShutdownSIGTERM asserts the one contract step that Windows cannot
// express: that SIGTERM produces a CLEAN exit rather than merely an exit.
//
// It is deliberately a separate test. Folding it back into TestDaemonEndToEnd is
// what forced that whole suite to be skipped on Windows, throwing away seven
// perfectly portable checks to protect one that is not.
func TestGracefulShutdownSIGTERM(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e: skipped in -short mode")
	}
	if runtime.GOOS == "windows" {
		// Go cannot deliver SIGTERM on Windows at all (os.Process.Signal returns
		// "not supported by windows"). Downgrading this to a hard Kill would keep
		// the test green while no longer testing the thing it exists to test, so
		// it is skipped outright and stays honest.
		t.Skip("e2e: graceful-SIGTERM shutdown is not expressible on Windows; run this test on Unix")
	}
	bin := buildDaemon(t)
	home := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "graceful.db")
	d := startDaemon(t, bin, home, dbPath, freePort(t))
	d.waitHealthy(t, 20*time.Second)
	d.stopGraceful(t)
}
