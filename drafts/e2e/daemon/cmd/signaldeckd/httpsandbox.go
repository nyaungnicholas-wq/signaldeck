// TARGET FILE : signaldeck/daemon/cmd/signaldeckd/httpsandbox.go  (NEW FILE)
// HOW TO APPLY: copy into daemon/cmd/signaldeckd/, then apply
//               drafts/e2e/patches/0002-main-http-sandbox.patch, which adds the
//               single call site (installHTTPSandbox) to main.go.
//
// It is a separate file so the one-line change to main.go stays a one-line
// change, and so nothing in this file can be reached unless main.go calls it.

package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// sandboxOriginHeader carries the host a request was ORIGINALLY addressed to,
// so a test server can report which real upstreams the daemon tried to reach.
// Keep in sync with internal/testharness.SandboxOriginHeader.
const sandboxOriginHeader = "X-Signaldeck-Sandbox-Origin"

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// installHTTPSandbox makes outbound HTTP unable to leave the machine, for tests
// that boot this binary as a subprocess.
//
// WHY AT THE TRANSPORT AND NOT PER CLIENT. Every upstream client in this tree
// builds &http.Client{Timeout: ...} and leaves Transport nil, so every one of
// them falls through to http.DefaultTransport. (Checked: the only `Transport:`
// literal outside _test.go is internal/ingest/edgar/bulk.go:70, which inherits
// its parent client's — also nil.) Meanwhile there is no env override for any
// upstream base URL: run.go:1289 wires finra.New() — the production CDN — with
// no key gate whatsoever, and run.go:1439-1450 does the same for cftc,
// stocktwits, wikimedia, cboe, hyperliquid and tvscanner. Adding six or eight
// per-client env vars is six or eight chances to forget one and have the e2e
// suite quietly call a real CDN. One hook, one place, all callers.
//
//	SIGNALDECK_HTTP_SANDBOX unset or empty → no hook at all (production).
//	SIGNALDECK_HTTP_SANDBOX=block          → every outbound request errors.
//	SIGNALDECK_HTTP_SANDBOX=http://host:p  → every outbound request is
//	                                         rewritten to that host, path and
//	                                         query intact.
//
// SAFETY. A sandboxed daemon serves FABRICATED upstream data. If it were ever
// pointed at the live database those fabrications would be indistinguishable
// from real ingested rows, so this refuses to run unless the database is a
// throwaway under the OS temp directory. That check is the reason this is safe
// to have in the shipping binary at all; do not weaken it.
func installHTTPSandbox(dbPath string) {
	v := strings.TrimSpace(os.Getenv("SIGNALDECK_HTTP_SANDBOX"))
	if v == "" {
		return
	}
	if !underTempDir(dbPath) {
		slog.Error("refusing to start: SIGNALDECK_HTTP_SANDBOX is set but the database is not a throwaway",
			"db", dbPath,
			"required_under", os.TempDir(),
			"why", "a sandboxed daemon serves fabricated upstream data; writing it into a real database would be indistinguishable from real ingest")
		os.Exit(1)
	}

	base := http.DefaultTransport // capture BEFORE replacing, or the hook recurses

	if strings.EqualFold(v, "block") {
		http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return nil, fmt.Errorf("SIGNALDECK_HTTP_SANDBOX=block: outbound %s %s refused", r.Method, r.URL.Redacted())
		})
		slog.Warn("HTTP SANDBOX ACTIVE: every outbound request is refused", "mode", "block", "db", dbPath)
		return
	}

	to, err := url.Parse(v)
	if err != nil || to.Host == "" || (to.Scheme != "http" && to.Scheme != "https") {
		slog.Error("refusing to start: SIGNALDECK_HTTP_SANDBOX is not 'block' and not a valid http(s) URL",
			"value", v, "err", err)
		os.Exit(1)
	}
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		r2 := r.Clone(r.Context())
		r2.Header.Set(sandboxOriginHeader, r.URL.Host)
		r2.URL.Scheme, r2.URL.Host = to.Scheme, to.Host
		r2.Host = "" // let the transport derive Host from the rewritten URL
		return base.RoundTrip(r2)
	})
	slog.Warn("HTTP SANDBOX ACTIVE: every outbound request is redirected",
		"to", to.Redacted(), "db", dbPath)
}

// underTempDir reports whether p resolves inside the OS temp directory.
func underTempDir(p string) bool {
	abs, err := filepath.Abs(p)
	if err != nil {
		return false
	}
	tmp, err := filepath.Abs(os.TempDir())
	if err != nil {
		return false
	}
	if runtime.GOOS == "windows" {
		// Windows paths are case-insensitive; filepath.Rel is not.
		abs, tmp = strings.ToLower(abs), strings.ToLower(tmp)
	}
	rel, err := filepath.Rel(tmp, abs)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
