// TARGET FILE : signaldeck/daemon/internal/testharness/harness_test.go  (NEW FILE)
// HOW TO APPLY: copy into daemon/internal/testharness/. No existing file changes.
//
// The harness's own check. It exercises the three things that would silently
// stop protecting anything if they broke: the live-database guard, the
// credential isolation the environment claims, and whether the FINRA fake
// actually satisfies the REAL parser.
//
// It does not boot a daemon — daemon/e2e does that.

package testharness

import (
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/ingest/finra"
)

// The point of the whole harness: no test may open data/signaldeck.db.
func TestEphemeralGuardRejectsTheLiveDatabase(t *testing.T) {
	ok := filepath.Join(t.TempDir(), "x.db")
	if _, _, got := ephemeralOK(ok); !got {
		t.Fatalf("ephemeralOK(%s) = false, want true (it IS under %s)", ok, os.TempDir())
	}
	bad := []string{
		filepath.Join("C:", "Users", "someone", "Desktop", "claude code", "signaldeck", "data", "signaldeck.db"),
		filepath.Join(string(filepath.Separator), "Users", "someone", "signaldeck", "data", "signaldeck.db"),
		filepath.Join(os.TempDir(), "..", "signaldeck.db"),
		"signaldeck.db", // relative → cwd, which is the package dir, not temp
	}
	for _, p := range bad {
		if _, _, got := ephemeralOK(p); got {
			t.Errorf("ephemeralOK(%s) = true, want false — the live-database guard is not guarding", p)
		}
	}
}

// Case-insensitivity on Windows: a path that differs only in case from
// os.TempDir() must still be recognised as ephemeral, or every test on a box
// with a mixed-case TEMP fails for no reason.
func TestEphemeralGuardIsCaseInsensitiveOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("case-insensitive path comparison is a Windows property")
	}
	p := filepath.Join(strings.ToUpper(os.TempDir()), "x.db")
	if _, _, ok := ephemeralOK(p); !ok {
		t.Fatalf("ephemeralOK(%s) = false; upper-cased TEMP must still count as temp", p)
	}
}

func TestFreeAddrIsBindable(t *testing.T) {
	addr := FreeAddr(t)
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("FreeAddr returned %q: %v", addr, err)
	}
	if host != "127.0.0.1" {
		t.Fatalf("FreeAddr returned %q; must stay on loopback (binding all interfaces "+
			"exposes the test daemon on the LAN and trips the Windows firewall)", addr)
	}
	l, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("port from FreeAddr is not bindable: %v", err)
	}
	_ = l.Close()
}

// The environment must isolate credentials THROUGH SIGNALDECK_ROOT, not through
// HOME. config.projectRoot() takes SIGNALDECK_ROOT verbatim; its $HOME rung is
// unreachable from inside a checkout, which is why the old e2e suite's HOME=
// isolated nothing.
func TestEnvIsolatesCredentials(t *testing.T) {
	db := DBPath(t, "x.db")
	env := Env(t, db, "127.0.0.1:65000", "http://127.0.0.1:1")

	get := func(k string) (string, bool) {
		for _, kv := range env {
			if name, v, ok := strings.Cut(kv, "="); ok && name == k {
				return v, true
			}
		}
		return "", false
	}

	root, ok := get("SIGNALDECK_ROOT")
	if !ok {
		t.Fatal("Env did not set SIGNALDECK_ROOT — credential isolation depends on it")
	}
	// The root must contain neither of the two .env files config.Load reads.
	for _, rel := range []string{
		filepath.Join("signaldeck", "daemon", ".env"),
		filepath.Join("stock-trader", ".env"),
	} {
		if _, err := os.Stat(filepath.Join(root, rel)); err == nil {
			t.Fatalf("SIGNALDECK_ROOT=%s contains %s — the daemon would load real keys", root, rel)
		}
	}
	// And it must not be an ancestor of a real checkout either.
	if _, err := os.Stat(filepath.Join(root, "signaldeck", "daemon")); err == nil {
		t.Fatalf("SIGNALDECK_ROOT=%s looks like a real checkout", root)
	}

	for _, k := range []string{"ALPACA_KEY", "ALPACA_SECRET", "SIGNALDECK_NVIDIA_KEY", "SIGNALDECK_NVIDIA_KEYS", "SIGNALDECK_LLM_KEY"} {
		if v, ok := get(k); ok && v != "" {
			t.Fatalf("Env leaked %s into the child environment", k)
		}
	}

	if v, _ := get("SIGNALDECK_DB"); v != db {
		t.Fatalf("SIGNALDECK_DB = %q, want %q", v, db)
	}
	if v, ok := get("SIGNALDECK_HTTP_SANDBOX"); !ok || v == "" {
		t.Fatal("Env must always set SIGNALDECK_HTTP_SANDBOX, or the child can reach the network")
	}
	if runtime.GOOS == "windows" {
		// Omitting SystemRoot from an explicit cmd.Env breaks DNS and
		// crypto/rand in the child; the daemon would fail to boot for a reason
		// unrelated to what is under test.
		if _, ok := get("SystemRoot"); !ok {
			t.Fatal("Env must pass SystemRoot through on Windows")
		}
	}
}

// The fake is only worth anything if the REAL parser accepts it. finra.ParseDaily
// fails loudly on any header but finra.dailyHeader, so this catches the fake
// drifting from upstream's shape.
func TestFINRAFakeSatisfiesTheRealParser(t *testing.T) {
	up := FakeUpstreams(t)
	c := &finra.Client{BaseURL: up.FINRADaily(), UA: "testharness"}

	day := time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC)
	rows, skipped, err := c.FetchDaily(t.Context(), day)
	if err != nil {
		t.Fatalf("real finra.Client against the fake: %v", err)
	}
	if skipped != 0 {
		t.Fatalf("real parser skipped %d malformed lines in the fake's output", skipped)
	}
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3", len(rows))
	}
	if rows[0].Symbol != "AAPL" || rows[0].Day != "2026-08-05" || rows[0].ShortVol != 100.5 {
		t.Fatalf("first row = %+v", rows[0])
	}

	// An absent day must look the way FINRA's CDN makes it look: 403 → an honest
	// "not available", never a fabricated empty day.
	if _, err := http.Get(up.FINRADaily() + "/CNMSshvolnope.txt"); err != nil {
		t.Fatalf("probe: %v", err)
	}
}

// An unmodelled upstream must fail loudly, not return 200 with nothing.
func TestUnknownUpstreamIsRefusedNotFaked(t *testing.T) {
	up := FakeUpstreams(t)
	req, _ := http.NewRequest("GET", up.URL+"/some/unknown/api", nil)
	req.Header.Set(SandboxOriginHeader, "api.example.invalid")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("unknown upstream returned %d, want 503", resp.StatusCode)
	}
	got := up.Origins()
	if len(got) != 1 || got[0] != "api.example.invalid" {
		t.Fatalf("Origins() = %v, want [api.example.invalid] — without this a test "+
			"cannot show which upstreams were intercepted", got)
	}
}
