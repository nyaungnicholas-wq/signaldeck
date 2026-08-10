// TARGET FILE : signaldeck/daemon/internal/testharness/harness.go   (NEW FILE)
// HOW TO APPLY: copy this file (and fakes.go, signal_windows.go, signal_other.go
// from the same drafts directory) into daemon/internal/testharness/. No
// existing file changes. Then apply drafts/e2e/patches/0001-e2e-harness-and-windows.patch
// and drafts/e2e/patches/0002-main-http-sandbox.patch.
//
// Package testharness is the ephemeral-store / ephemeral-daemon harness for
// SignalDeck's tests. It exists because the isolation the e2e suite claimed it
// had, it did not have:
//
//   - daemon/e2e/e2e_test.go:110 set HOME= in the child env with the comment
//     "isolates config.Load's .env lookups → no keys". It does not.
//     internal/config/config.go:32-59 (projectRoot) resolves in this order:
//     SIGNALDECK_ROOT, then the nearest ancestor of the EXECUTABLE or of the
//     WORKING DIRECTORY containing signaldeck/daemon, and only then $HOME. The
//     e2e test never set SIGNALDECK_ROOT and never set cmd.Dir, so the child
//     inherited daemon/e2e as its CWD — whose ancestor DOES contain
//     signaldeck/daemon. projectRoot therefore returned the REAL checkout, and
//     config.Load read the REAL daemon/.env (exporting every key in it into the
//     child's environment, config.go:137-141) and the REAL stock-trader/.env
//     for ALPACA_KEY / ALPACA_SECRET (config.go:~218). HOME was never consulted.
//     Setting SIGNALDECK_ROOT to an empty temp dir is the only thing that
//     actually closes that door, because it is rung 1 and obeyed verbatim.
//
//   - On Windows, os.UserHomeDir reads %USERPROFILE%, not $HOME, so even the
//     fallback rung would have ignored the test's HOME=.
//
//   - os/exec with an explicit cmd.Env that omits SystemRoot breaks DNS
//     resolution and crypto/rand in the child on Windows. The old env list had
//     only HOME, PATH and TMPDIR, so the suite could not have run there even
//     with the SIGTERM problem solved.
//
// Everything here takes a testing.TB, so it works from tests and benchmarks and
// never links into the daemon binary.
package testharness

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// ErrSignalUnavailable means the platform could not deliver a graceful
// shutdown signal for a reason that is about the ENVIRONMENT, not about the
// daemon — today only "Windows test process has no console, so
// GenerateConsoleCtrlEvent cannot address the child's process group".
//
// It is a distinct error so a caller can skip that ONE step with the real
// errno printed, instead of skipping a whole platform on a guess.
var ErrSignalUnavailable = errors.New("testharness: graceful-shutdown signal unavailable in this environment")

// ─────────────────────────────── store ────────────────────────────────

// Store opens a throwaway SQLite store under t.TempDir() and closes it on
// cleanup.
//
// WHY A TEMP FILE AND NOT file::memory:?cache=shared — checked, not assumed.
// store.Open (internal/store/store.go:104) opens TWO independent *sql.DB
// handles on the same DSN: the read pool at store.go:115 (SetMaxOpenConns(4))
// and the dedicated writer at store.go:122 (SetMaxOpenConns(1)). Store.ReaderClone
// (store.go:475-486) opens further read pools on that same DSN, each with up to
// maxConns connections. So the "dedicated single-conn writer" describes the
// WRITE path only — the process as a whole holds many connections, and a
// per-connection private in-memory database would give each of them a DIFFERENT,
// empty database. The shared-cache form would paper over that, but the DSN also
// asks for journal_mode(WAL) (store.go:114), and WAL requires a real file: an
// in-memory database silently stays in "memory" journal mode, so every test
// would exercise a journal mode production never uses. A temp file is correct
// on both counts, and it is already what the rest of the tree does
// (e.g. internal/aiagents/chat/chat_test.go:47).
func Store(tb testing.TB) *store.Store {
	tb.Helper()
	st, err := store.Open(DBPath(tb, "signaldeck-test.db"))
	if err != nil {
		tb.Fatalf("testharness: store.Open: %v", err)
	}
	tb.Cleanup(func() { _ = st.Close() })
	return st
}

// DBPath returns a path for a throwaway database inside t.TempDir(), after
// proving it is not the live one.
func DBPath(tb testing.TB, name string) string {
	tb.Helper()
	p := filepath.Join(tb.TempDir(), name)
	MustBeEphemeral(tb, p)
	return p
}

// MustBeEphemeral fails the test unless p is inside the OS temp directory.
//
// This is the harness's one hard safety property: data/signaldeck.db is the
// live database, and no test may ever open it. A comment saying "don't" is not
// a control; this is. It is exported so a caller assembling its own path still
// goes through the check.
func MustBeEphemeral(tb testing.TB, p string) {
	tb.Helper()
	abs, tmp, ok := ephemeralOK(p)
	if !ok {
		tb.Fatalf("testharness: refusing %s — a test database MUST live under %s. "+
			"The live database (data/signaldeck.db) is never a test target.", abs, tmp)
	}
}

// ephemeralOK is the predicate behind MustBeEphemeral, split out so it can be
// unit-tested without a testing.TB (testing.TB cannot be implemented outside
// package testing, so a stub is not an option).
func ephemeralOK(p string) (abs, tmp string, ok bool) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return p, "", false
	}
	tmp, err = filepath.Abs(os.TempDir())
	if err != nil {
		return abs, os.TempDir(), false
	}
	a, root := abs, tmp
	if runtime.GOOS == "windows" {
		// Windows paths are case-insensitive but filepath.Rel is not.
		a, root = strings.ToLower(a), strings.ToLower(root)
	}
	rel, err := filepath.Rel(root, a)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return abs, tmp, false
	}
	return abs, tmp, true
}

// ─────────────────────────────── network ───────────────────────────────

// FreeAddr returns a free loopback address, "127.0.0.1:PORT".
//
// It binds 127.0.0.1:0 rather than :0 on purpose: binding every interface pops
// the Windows Defender Firewall dialog and exposes the test daemon on the LAN
// for the lifetime of the probe.
//
// ponytail: there is an unavoidable TOCTOU window — the kernel may hand this
// port to another process between Close here and Bind in the child. Nothing
// portable closes it (SO_REUSEADDR does not help a DIFFERENT process), so the
// window is named rather than hidden; Start surfaces a bind failure as a
// daemon-never-became-healthy failure with the child's log attached.
func FreeAddr(tb testing.TB) string {
	tb.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		tb.Fatalf("testharness: reserve port: %v", err)
	}
	addr := l.Addr().String()
	if err := l.Close(); err != nil {
		tb.Fatalf("testharness: release reserved port: %v", err)
	}
	return addr
}

// ─────────────────────────────── env ───────────────────────────────────

// Env builds the complete environment for a throwaway signaldeckd.
//
// sandboxURL is the base URL of a FakeUpstreams server. Empty means "block":
// the child refuses every outbound request instead of redirecting it. Either
// way no live third-party host is reachable — see the SIGNALDECK_HTTP_SANDBOX
// hook in cmd/signaldeckd/main.go.
func Env(tb testing.TB, dbPath, addr, sandboxURL string) []string {
	tb.Helper()
	MustBeEphemeral(tb, dbPath)

	// An EMPTY directory. projectRoot() takes SIGNALDECK_ROOT verbatim
	// (config.go:33-35), so config.Load looks for <root>/signaldeck/daemon/.env
	// and <root>/stock-trader/.env, finds neither, and no real key exists in
	// this process. This — not HOME — is what isolates credentials.
	root := tb.TempDir()

	sandbox := "block"
	if sandboxURL != "" {
		sandbox = sandboxURL
	}

	env := []string{
		"SIGNALDECK_ROOT=" + root,
		// Belt to the SIGNALDECK_ROOT brace: if the resolution order ever changes,
		// the $HOME / %USERPROFILE% rung must also land in the empty temp root.
		"HOME=" + root,
		"USERPROFILE=" + root,
		"SIGNALDECK_DB=" + dbPath,
		"SIGNALDECK_HTTP=" + addr,
		"SIGNALDECK_ALLOWED_HOSTS=" + addr,
		"SIGNALDECK_OPEN_SIGNUP=true",
		"SIGNALDECK_PUBLIC_READS=true",
		// The daemon refuses to start from an unattributable build because rows
		// it freezes could not be graded (cmd/signaldeckd/main.go:85-95). Right
		// in production, wrong here: this daemon writes to a throwaway temp DB
		// whose rows nothing grades, and a working tree is dirty by definition
		// while it is being worked on. Without this the suite fails on every
		// developer machine mid-change — which is how a suite stops being run.
		"SIGNALDECK_ALLOW_DIRTY_BUILD=1",
		// Present-but-empty. main.go:39 uses os.LookupEnv, so this means
		// "stderr only" — otherwise the daemon derives a log path from the DB
		// path and writes a rotating log OUTSIDE the test's own temp dir.
		"SIGNALDECK_LOG_FILE=",
		"SIGNALDECK_MCP_ENABLED=false",
		// Companion services point at a dead port so the test can never reach a
		// real hud/tickstream on this machine even if the sandbox is off.
		"SIGNALDECK_HUD_URL=http://127.0.0.1:1/api/summary",
		"SIGNALDECK_TICKSTREAM_URL=http://127.0.0.1:1/api/snapshot",
		"SIGNALDECK_HTTP_SANDBOX=" + sandbox,
		"PATH=" + os.Getenv("PATH"),
	}

	// Variables the child needs from the real environment. On Windows, omitting
	// SystemRoot from an explicit cmd.Env breaks DNS resolution and crypto/rand
	// in the child — the daemon would fail to boot for a reason that has nothing
	// to do with what is under test.
	passthrough := []string{"TMPDIR"}
	if runtime.GOOS == "windows" {
		passthrough = []string{
			"SystemRoot", "windir", "ComSpec", "PATHEXT",
			"TEMP", "TMP", "NUMBER_OF_PROCESSORS", "SystemDrive",
		}
	}
	for _, k := range passthrough {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	return env
}

// ─────────────────────────────── daemon ────────────────────────────────

// safeBuffer guards a bytes.Buffer with a mutex. cmd.Stdout/cmd.Stderr are
// written by exec's copy goroutines for as long as the child is alive, while
// the test goroutine reads Logs() on every failure path (including ones that
// fire before the child exits, e.g. WaitHealthy timing out). A bare
// bytes.Buffer is not safe for that, and go test -race catches it reliably.
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

// Daemon is a running throwaway signaldeckd.
type Daemon struct {
	cmd  *exec.Cmd
	URL  string // "http://127.0.0.1:PORT"
	logs *safeBuffer
	// done receives cmd.Wait()'s result exactly once; exited flips true in the
	// same goroutine right after, so other goroutines can ask "has this process
	// been reaped" without touching cmd.ProcessState. Reading ProcessState from
	// Cleanup while cmd.Wait() writes it is a race go test -race reports.
	done   chan error
	exited atomic.Bool
}

// Build compiles cmd/signaldeckd once into a temp dir and returns the path.
// moduleDir is the daemon module root (from daemon/e2e that is "..").
func Build(tb testing.TB, moduleDir string) string {
	tb.Helper()
	// Windows will not exec a file without .exe, and `go build -o` writes
	// exactly the name it is given.
	name := "signaldeckd"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	bin := filepath.Join(tb.TempDir(), name)
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/signaldeckd")
	cmd.Dir = moduleDir
	if out, err := cmd.CombinedOutput(); err != nil {
		tb.Fatalf("testharness: go build: %v\n%s", err, out)
	}
	return bin
}

// Start launches the binary with the given environment and returns once the
// process has been spawned (not once it is healthy — call WaitHealthy).
func Start(tb testing.TB, bin string, env []string, addr string) *Daemon {
	tb.Helper()
	cmd := exec.Command(bin)
	cmd.Env = env
	// Pin the working directory to an empty temp dir. SIGNALDECK_ROOT already
	// wins, but projectRoot()'s SECOND rung walks the CWD upward looking for
	// signaldeck/daemon — inheriting the test's CWD (daemon/e2e) is exactly how
	// the old suite reached the real checkout's .env files. Two independent
	// reasons the child cannot find the repo is the right number.
	cmd.Dir = tb.TempDir()
	logs := &safeBuffer{}
	cmd.Stdout = logs
	cmd.Stderr = logs
	// Windows: give the child its own console process group so a CTRL_BREAK can
	// be addressed to it and not to the test binary. No-op elsewhere.
	newProcessGroup(cmd)

	if err := cmd.Start(); err != nil {
		tb.Fatalf("testharness: start daemon: %v", err)
	}
	d := &Daemon{cmd: cmd, URL: "http://" + addr, logs: logs, done: make(chan error, 1)}
	go func() {
		err := cmd.Wait()
		d.exited.Store(true)
		d.done <- err
	}()
	tb.Cleanup(func() {
		if !d.exited.Load() {
			_ = cmd.Process.Kill()
			<-d.done
		}
	})
	return d
}

// Logs returns everything the child has written to stdout+stderr so far.
func (d *Daemon) Logs() string { return d.logs.String() }

// WaitHealthy polls /api/health until the API answers 200.
func (d *Daemon) WaitHealthy(tb testing.TB, within time.Duration) {
	tb.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		resp, err := http.Get(d.URL + "/api/health")
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode == 200 {
				return
			}
		}
		select {
		case err := <-d.done:
			tb.Fatalf("testharness: daemon exited before healthy: %v\nlogs:\n%s", err, d.Logs())
		case <-time.After(100 * time.Millisecond):
		}
	}
	tb.Fatalf("testharness: daemon never became healthy within %s\nlogs:\n%s", within, d.Logs())
}

// GracefulStop asks the daemon to shut down the way its deployment does, and
// requires a clean exit within `within`.
//
// Unix   : SIGTERM.
// Windows: CTRL_BREAK_EVENT to the child's own console process group.
//
// Both arrive at the SAME listener. cmd/signaldeckd/main.go:67 is
//
//	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
//
// and the Go runtime translates a console control event into os.Interrupt for
// signal.Notify consumers on Windows. So this is the daemon's real graceful
// path on Windows, not a stand-in for one, which is what makes running the test
// there honest rather than decorative.
//
// The returned error is non-nil ONLY when the signal could not be DELIVERED
// (wrapping ErrSignalUnavailable when the reason is environmental). An unclean
// or late exit fails the test here — a caller must not be able to turn a real
// shutdown bug into a skip.
func (d *Daemon) GracefulStop(tb testing.TB, within time.Duration) error {
	tb.Helper()
	if err := interrupt(d.cmd); err != nil {
		return err
	}
	select {
	case err := <-d.done:
		if err != nil {
			tb.Fatalf("testharness: daemon exited uncleanly after graceful signal: %v\nlogs:\n%s", err, d.Logs())
		}
	case <-time.After(within):
		_ = d.cmd.Process.Kill()
		tb.Fatalf("testharness: daemon did not exit within %s of the graceful signal\nlogs:\n%s", within, d.Logs())
	}
	return nil
}

// SignalName describes the graceful-shutdown mechanism on this platform, for
// test failure messages that must say what was actually attempted.
func SignalName() string {
	if runtime.GOOS == "windows" {
		return "CTRL_BREAK_EVENT (console control event → os.Interrupt)"
	}
	return fmt.Sprintf("SIGTERM (%s)", runtime.GOOS)
}
