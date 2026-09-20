// Package config loads SignalDeck's runtime configuration from flags/env,
// including Alpaca credentials reused from the stock-trader project's .env
// (single source of truth for those keys — never copied into this repo).
package config

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/nyaungnicholas-wq/signaldeck/internal/envcfg"
	"github.com/nyaungnicholas-wq/signaldeck/internal/llm"
)

// projectRoot returns the directory CONTAINING signaldeck/ — the anchor for the
// daemon's .env, its database and its MCP audit log.
//
// This used to be filepath.Join(home, "claude code") outright. On any machine
// where the checkout lives somewhere else — $HOME/Desktop/claude code, a work
// directory, CI, a colleague's laptop — every derived path pointed at nothing,
// and the failure was SILENT. Load() read no .env at all, so the LLM key was
// ignored, the Alpaca keys were never found, and the security toggles an
// operator had written in .env (SIGNALDECK_PUBLIC_READS, SIGNALDECK_OPEN_SIGNUP,
// SIGNALDECK_API_TOKEN, SIGNALDECK_TRUST_PROXY) never applied. A daemon quietly
// ignoring its own security configuration is worse than one that refuses to
// start, because nothing in the logs says so.
//
// Resolution order, first hit wins:
//  1. SIGNALDECK_ROOT, used verbatim whenever it is SET. An operator — or a
//     test harness — who states the root is obeyed and the tree is NEVER
//     walked. Rung 2 below is a security boundary as much as a convenience:
//     the probe starts from the WORKING DIRECTORY, so any process running from
//     inside a checkout resolves that checkout. daemon/e2e relied on a fake
//     HOME for credential isolation and got none, because rung 3 is where HOME
//     applies and rung 2 always answered first. Setting this variable is the
//     only thing that actually isolates a process from the checkout it runs in.
//  2. The nearest ancestor of the executable, then of the working directory,
//     that actually contains signaldeck/daemon. This is what makes a clone work
//     wherever it is put.
//  3. $HOME/claude code — the historical default, kept last so the existing
//     macOS launchd deployment keeps resolving exactly as it did before.
//
// A stated root that contains no .env is fine: Load() proceeds with NO
// credentials rather than aborting. Aborting would make an isolated test noisy
// to run and would teach people to unset SIGNALDECK_ROOT, which reopens the
// leak — the failure mode is worse than the one it would catch.
func projectRoot() string {
	if r, ok := os.LookupEnv("SIGNALDECK_ROOT"); ok {
		if r = strings.TrimSpace(r); r == "" {
			// Set but blank names no root at all. Falling through to the probe
			// here is exactly how a process that believed it was isolated
			// silently re-acquires the surrounding checkout's daemon/.env and
			// stock-trader/.env, so refuse instead. Only reachable when a
			// caller set the variable to an empty value, which is always a bug
			// in that caller.
			panic("config: SIGNALDECK_ROOT is set but blank; refusing to probe for a project root")
		}
		return r
	}
	// Probe the executable's directory first: under launchd the working
	// directory is not the checkout, but the binary always sits inside it.
	var starts []string
	if exe, err := os.Executable(); err == nil {
		starts = append(starts, filepath.Dir(exe))
	}
	if wd, err := os.Getwd(); err == nil {
		starts = append(starts, wd)
	}
	for _, start := range starts {
		for dir := start; ; {
			if fi, err := os.Stat(filepath.Join(dir, "signaldeck", "daemon")); err == nil && fi.IsDir() {
				return dir
			}
			parent := filepath.Dir(dir)
			if parent == dir { // filesystem root; nothing above to search
				break
			}
			dir = parent
		}
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "claude code")
}

// Config is the daemon configuration.
type Config struct {
	DBPath       string
	HTTPAddr     string
	AlpacaKey    string
	AlpacaSecret string
	// AlpacaFeed overrides the REST bars feed (SIGNALDECK_ALPACA_FEED). Empty →
	// the client default "sip" (full-market historical, already requested on the
	// free tier with a 16-min guarded tail). On a paid Algo Trader Plus upgrade
	// nothing else changes — Alpaca simply serves the full real-time SIP the code
	// already asks for; set this to "iex" only to force the free real-time feed.
	AlpacaFeed    string
	HudURL        string // trader-hud summary endpoint
	TickstreamURL string // tickstream dashboard snapshot endpoint
	GeminiKey     string // optional: LLM polish for insights ("" = rule-based only)
	CryptoSymbol  string // TickStream consolidated symbol label

	// HTTP security (see internal/api/security.go).
	WebOrigins   []string // CORS-allowlisted browser origins for the web app
	AllowedHosts []string // Host-header allowlist (blocks DNS rebinding); empty = deny all
	APIToken     string   // optional bearer token; alternative to a session cookie, maps to the admin user (scripts)

	// TVWebhookSecret gates POST /api/tv-webhook. TradingView servers cannot
	// send a session cookie or the CSRF header, so the inbound webhook is
	// authenticated by this shared secret (in the JSON body or ?secret= query).
	// Empty = the webhook is disabled (fails closed).
	TVWebhookSecret string

	// LocalProxyKey lets the loopback-bound private web launcher
	// (ops/start-local-workspace.ps1) assert that a request is local even though
	// it arrives through the Next.js proxy. The proxy proves it by sending this
	// key in X-Signaldeck-Local; the daemon honours it ONLY together with a
	// loopback RemoteAddr. Empty = the assertion is disabled (fails closed).
	// Replaces the old scheme of deleting X-Forwarded-For, which any process
	// could copy onto a wildcard bind and turn into a redistribution hole.
	LocalProxyKey string

	// Multi-user + exposure controls.
	OpenSignup     bool // SIGNALDECK_OPEN_SIGNUP (default true): allow POST /api/auth/register
	AllowRawExport bool // SIGNALDECK_ALLOW_RAW_EXPORT (default false): serve raw licensed bars
	// PublicReads (SIGNALDECK_PUBLIC_READS): read-only endpoints answer without
	// auth. The default is NOT true -- it is reachablePrivately(), i.e. open
	// only when the daemon is on loopback AND no tunnel is in the allowlist.
	// The comment here said "default true" long after that stopped being so,
	// and SHIP_READINESS.md quoted it back as a shipping blocker (audit F11).
	PublicReads bool

	// PublicSurface (SIGNALDECK_PUBLIC_SURFACE, default false) turns the
	// anonymous-read rule from a DENYLIST into an ALLOWLIST.
	//
	// PublicReads answers "is this route one of the ones we chose to keep
	// private?" — so every route added later is public by forgetting. That is
	// the wrong default for a deployment strangers can reach: this daemon
	// registers 164 routes (161 mux.HandleFunc patterns + 3 mux.Handle,
	// counted 2026-09-13), and among them are the personal PUSH-20 HUD,
	// paper-trading positions and the portfolio. Publishing those would be a
	// different product than the one being published.
	//
	// With PublicSurface on, api.publicRoutes is the ENTIRE anonymous surface
	// and everything else is closed whatever PublicReads says. A route added
	// later is private by forgetting, which is the direction that fails safe.
	//
	// It is deliberately NOT derived from the bind address. PublicReads and
	// OpenSignup follow reachability because their safe answer is "closed",
	// and reachability is a good proxy for danger. Deciding to publish is an
	// intent, not a network fact, and inferring an intent is how fly.toml
	// ended up publishing every read endpoint it never named.
	PublicSurface bool
	TrustProxy    bool // SIGNALDECK_TRUST_PROXY (default false): honor X-Forwarded-For / X-Forwarded-Proto
	RateRPS       int  // SIGNALDECK_RATE_RPS: override read-tier requests/sec (0 = default 10)
	RateBurst     int  // SIGNALDECK_RATE_BURST: override read-tier burst (0 = default 30)

	// MCP server (internal/mcp) — advisory methodology + current regime
	// verdicts for AI clients. OFF unless explicitly enabled, because it is
	// the one surface designed to be consumed by a third party's agent and a
	// default-on third-party interface is not a default anyone chose.
	MCPEnabled    bool     // SIGNALDECK_MCP_ENABLED (default false)
	MCPSecret     string   // SIGNALDECK_MCP_SECRET: HMAC key signing client keys; empty = no key can verify
	MCPAuditPath  string   // SIGNALDECK_MCP_AUDIT: append-only JSONL audit sink
	MCPDailyCalls int      // SIGNALDECK_MCP_DAILY_CALLS: per-client daily call cap (0 = default)
	MCPRevoked    []string // SIGNALDECK_MCP_REVOKED: comma-separated revoked client ids

	// LLM layer (OpenAI-compatible; NVIDIA by default). Empty key = the AI
	// agents stay in safe no-op mode.
	LLMKey       string   // first key (kept for LLMEnabled + display)
	LLMKeys      []string // failover pool (round-robin); SIGNALDECK_NVIDIA_KEYS, comma-separated
	LLMBaseURL   string   // e.g. https://integrate.api.nvidia.com/v1
	LLMModel     string   // default (workhorse) model
	LLMModelDeep string   // on-demand reasoning model (analyst/debate/scenario)
	LLMModelFast string   // highest-frequency, lowest-stakes model (sentiment tagging)
	LLMDailyCap  int      // hard cap on LLM calls per day (spend guard); 0 = default
}

// LLMEnabled reports whether the AI agents can run (a key is configured).
func (c Config) LLMEnabled() bool { return c.LLMKey != "" || len(c.LLMKeys) > 0 }

// Load builds the config. Precedence: environment > project .env files > default.
func Load() Config {
	// The daemon runs under launchd, which does NOT auto-load a .env, so we
	// read the daemon's own .env here (owner-only file holding the LLM key).
	dotenv := parseDotEnv(filepath.Join(projectRoot(), "signaldeck", "daemon", ".env"))
	// Export EVERY key from .env into the process env before anything reads it.
	// The daemon runs under launchd, which does not load .env, and much of the
	// codebase reads settings straight from os.Getenv rather than through pick()
	// below — notably the security toggles (SIGNALDECK_PUBLIC_READS,
	// SIGNALDECK_OPEN_SIGNUP, SIGNALDECK_API_TOKEN, SIGNALDECK_TRUST_PROXY, the
	// rate limits), the notify webhooks (Discord/Telegram/generic) and FRED.
	// Exporting only a couple of keys here meant those settings were silently
	// ignored: an operator could set SIGNALDECK_PUBLIC_READS=false in .env and
	// the daemon would still serve anonymous reads — while SIGNALDECK_ALLOWED_HOSTS
	// (which does go through pick()) happily admitted the public tunnel. The door
	// opened and the locks never engaged. Real env always wins; we only fill gaps.
	for k, v := range dotenv {
		if v != "" && os.Getenv(k) == "" {
			_ = os.Setenv(k, v)
		}
	}
	pick := func(env, def string) string {
		if v := os.Getenv(env); v != "" {
			return v
		}
		if v := dotenv[env]; v != "" {
			return v
		}
		return def
	}
	// LLM key pool: SIGNALDECK_NVIDIA_KEYS (comma-separated) is the failover
	// pool; a single SIGNALDECK_NVIDIA_KEY / SIGNALDECK_LLM_KEY still works and
	// seeds a one-key pool. The first key is kept for LLMEnabled + display.
	llmKeys := splitList(pick("SIGNALDECK_NVIDIA_KEYS", ""))
	if len(llmKeys) == 0 {
		if single := pick("SIGNALDECK_NVIDIA_KEY", pick("SIGNALDECK_LLM_KEY", "")); single != "" {
			llmKeys = []string{single}
		}
	}
	llmFirst := ""
	if len(llmKeys) > 0 {
		llmFirst = llmKeys[0]
	}
	// Resolved ONCE and shared: the Host allowlist is both a config field and
	// the evidence tunnelConfigured() uses to decide whether this daemon is
	// published. Computing it twice invites the two to drift, which is how the
	// open-by-default flags got their value from a signal that disagreed with
	// the allowlist sitting next to them.
	allowedHostsRaw := pick("SIGNALDECK_ALLOWED_HOSTS", defaultAllowedHosts)
	httpAddr := envOr("SIGNALDECK_HTTP", "127.0.0.1:8322")
	private := reachablePrivately(httpAddr, allowedHostsRaw)
	cfg := Config{
		LLMKey:     llmFirst,
		LLMKeys:    llmKeys,
		LLMBaseURL: pick("SIGNALDECK_LLM_BASE_URL", "https://integrate.api.nvidia.com/v1"),
		// ONE DEFINITION OF EACH MODEL ID, in internal/llm. These literals used
		// to be spelled out here as well, and the duplicate is what made the
		// 2026-08-27 repair fail its first verification: llm.go was corrected,
		// this file was not, pick() returned the stale literal because no env
		// var was set, and sentiment-tagger went on answering HTTP 410 across a
		// daemon restart. A second copy of a value that a vendor can retire is
		// not redundancy, it is a second thing to forget.
		LLMModel:        pick("SIGNALDECK_LLM_MODEL", llm.DefaultModel),
		LLMModelDeep:    pick("SIGNALDECK_LLM_MODEL_DEEP", llm.DefaultDeep),
		LLMModelFast:    pick("SIGNALDECK_LLM_MODEL_FAST", llm.DefaultFast),
		LLMDailyCap:     atoiOr("SIGNALDECK_LLM_DAILY_CAP", pick("SIGNALDECK_LLM_DAILY_CAP", ""), 2000),
		DBPath:          envOr("SIGNALDECK_DB", filepath.Join(projectRoot(), "signaldeck", "data", "signaldeck.db")),
		HTTPAddr:        httpAddr,
		HudURL:          envOr("SIGNALDECK_HUD_URL", "http://127.0.0.1:8787/api/summary"),
		TickstreamURL:   envOr("SIGNALDECK_TICKSTREAM_URL", "http://127.0.0.1:8321/api/snapshot"),
		GeminiKey:       os.Getenv("SIGNALDECK_GEMINI_KEY"),
		CryptoSymbol:    "BTC/USD",
		WebOrigins:      splitList(pick("SIGNALDECK_WEB_ORIGINS", "http://localhost:8323,http://127.0.0.1:8323,http://localhost:3000,http://127.0.0.1:3000")),
		AllowedHosts:    splitList(allowedHostsRaw),
		APIToken:        os.Getenv("SIGNALDECK_API_TOKEN"),
		TVWebhookSecret: pick("SIGNALDECK_TV_WEBHOOK_SECRET", ""),
		LocalProxyKey:   pick("SIGNALDECK_LOCAL_PROXY_KEY", ""),
		// SAFE BY DEFAULT (2026-07-25): open registration is a localhost
		// convenience. On a reachable deployment it lets any stranger create an
		// account and spend the LLM budget, so it follows the bind address for
		// the same reason PublicReads does.
		OpenSignup: boolEnv("SIGNALDECK_OPEN_SIGNUP", private),
		// SAFE BY DEFAULT (2026-07-25): unauthenticated reads are a localhost
		// convenience, not a deployment posture. The default now follows the
		// BIND ADDRESS — true on loopback, false the moment the daemon listens
		// anywhere reachable — so exposing it can no longer silently publish
		// every read endpoint. An explicit env var still wins either way.
		PublicReads: boolEnv("SIGNALDECK_PUBLIC_READS", private),
		// Never inherits `private`. See the field comment: publishing is an
		// intent the operator states, never a fact inferred from a bind.
		PublicSurface: boolEnv("SIGNALDECK_PUBLIC_SURFACE", false),
		// Asserting you hold redistribution rights for the stored price data.
		// The flag records the operator's assertion; it does not grant a right.
		AllowRawExport: boolEnv("SIGNALDECK_ALLOW_RAW_EXPORT", false),
		TrustProxy:     boolEnv("SIGNALDECK_TRUST_PROXY", false),
		RateRPS:        atoiOr("SIGNALDECK_RATE_RPS", os.Getenv("SIGNALDECK_RATE_RPS"), 0),
		RateBurst:      atoiOr("SIGNALDECK_RATE_BURST", os.Getenv("SIGNALDECK_RATE_BURST"), 0),
		// The MCP server never inherits an "open on loopback" default the way
		// PublicReads does. Exposing an interface built for someone else's AI
		// agent is a decision with compliance implications, so it is made once,
		// explicitly, by setting this.
		MCPEnabled:    boolEnv("SIGNALDECK_MCP_ENABLED", false),
		MCPSecret:     pick("SIGNALDECK_MCP_SECRET", ""),
		MCPAuditPath:  pick("SIGNALDECK_MCP_AUDIT", filepath.Join(projectRoot(), "signaldeck", "logs", "mcp_audit.jsonl")),
		MCPDailyCalls: atoiOr("SIGNALDECK_MCP_DAILY_CALLS", os.Getenv("SIGNALDECK_MCP_DAILY_CALLS"), 0),
		MCPRevoked:    splitList(pick("SIGNALDECK_MCP_REVOKED", "")),
	}
	cfg.AlpacaKey = os.Getenv("ALPACA_KEY")
	cfg.AlpacaSecret = os.Getenv("ALPACA_SECRET")
	if cfg.AlpacaKey == "" || cfg.AlpacaSecret == "" {
		// Reuse the stock-trader keys (paper account; market data works with it).
		env := parseDotEnv(filepath.Join(projectRoot(), "stock-trader", ".env"))
		if cfg.AlpacaKey == "" {
			cfg.AlpacaKey = env["ALPACA_KEY"]
		}
		if cfg.AlpacaSecret == "" {
			cfg.AlpacaSecret = env["ALPACA_SECRET"]
		}
	}
	cfg.AlpacaFeed = os.Getenv("SIGNALDECK_ALPACA_FEED") // "" → client default "sip"
	return cfg
}

// HasAlpaca reports whether stock ingestion can run.
func (c Config) HasAlpaca() bool { return c.AlpacaKey != "" && c.AlpacaSecret != "" }

// splitList splits a comma-separated string, trimming blanks. Callers pass the
// already-resolved value (via pick, so env > .env > default all work under
// launchd, which does not load .env into the process environment).
func splitList(v string) []string {
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// atoiOr parses s as an int, returning def on empty/invalid input.
// atoiOr takes the KEY as well as the value so a refused override can name
// itself. It used to take only the string, which is why a rejected value here
// could not be reported even in principle.
//
// The digit loop is stricter than strconv.Atoi on purpose (it rejects "-5",
// "+5", " 5" and "5 "), so the reasons below are the ones an operator actually
// hits: a stray sign, a unit suffix, or surrounding whitespace.
func atoiOr(key, s string, def int) int {
	if s == "" {
		return def
	}
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			envcfg.Reject(key, s, "not a plain non-negative integer (no sign, unit or spaces)", strconv.Itoa(def))
			return def
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// boolEnv reads a boolean env var ("false"/"0"/"no" = false, "true"/"1"/"yes" = true).
//
// An unrecognised NON-EMPTY value fails CLOSED (false) rather than falling
// through to the default. The two callers that matter — SIGNALDECK_PUBLIC_READS
// and SIGNALDECK_OPEN_SIGNUP — both default to true on a loopback bind, so a
// value the parser did not understand used to silently mean "open". An operator
// who typed something is expressing an intent to restrict far more often than an
// intent to open, and a security default should never be reachable by a typo.
func boolEnv(k string, def bool) bool {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv(k)))
	switch raw {
	case "":
		return def
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		// Fails CLOSED on purpose — an unreadable security toggle must not be
		// read as "on". But failing closed silently is how an operator who
		// meant to ENABLE something ends up with it off and no way to tell, so
		// the refusal is now recorded.
		envcfg.Reject(k, raw, "not a boolean", "false")
		return false
	}
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// parseDotEnv reads KEY=VALUE lines; comments and blanks ignored. Returns an
// empty map when the file is missing (callers treat that as "no keys").
func parseDotEnv(path string) map[string]string {
	out := map[string]string{}
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer f.Close() //nolint:errcheck
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		// Strip a trailing ` #…` comment on an UNQUOTED value. Without this,
		// `SIGNALDECK_PUBLIC_READS=false  # locked down for the tunnel` parses
		// as the literal string "false  # locked down for the tunnel", which
		// boolEnv cannot recognise — and the exact remediation ops/GO-LIVE.md
		// tells the operator to type then does nothing. A `#` inside a quoted
		// value is left alone, because secrets legitimately contain one.
		if !strings.HasPrefix(v, `"`) && !strings.HasPrefix(v, `'`) {
			if i := strings.Index(v, " #"); i >= 0 {
				v = strings.TrimSpace(v[:i])
			}
			if i := strings.Index(v, "\t#"); i >= 0 {
				v = strings.TrimSpace(v[:i])
			}
		}
		out[strings.TrimSpace(k)] = strings.Trim(v, `"'`)
	}
	return out
}

// defaultAllowedHosts is the Host-header allowlist when the operator sets none.
// Named rather than inlined because tunnelConfigured() reads the same setting to
// decide whether this daemon is published, and the two must not drift.
const defaultAllowedHosts = "127.0.0.1:8322,localhost:8322"

// loopbackOnly reports whether addr binds only to the local machine.
//
// IT IS NOT SUFFICIENT ON ITS OWN, and the reason is the whole point of
// reachablePrivately below: a REVERSE TUNNEL makes the daemon public without
// changing the bind at all. ngrok dials out and connects back from 127.0.0.1,
// so this function answers "loopback" at precisely the moment a stranger can
// reach the process, and every tunneled request also presents a loopback
// RemoteAddr. A default derived from this alone is safest-looking exactly when
// it is least safe. Callers must use reachablePrivately.
func loopbackOnly(addr string) bool {
	host := addr
	if i := strings.LastIndex(addr, ":"); i >= 0 {
		host = addr[:i]
	}
	host = strings.Trim(host, "[]")
	// An EMPTY host (":8322") binds every interface, so it is emphatically not
	// loopback — treating it as local would hand out the safe-looking default
	// on exactly the configuration that is reachable from the network.
	switch host {
	case "127.0.0.1", "localhost", "::1":
		return true
	}
	return strings.HasPrefix(host, "127.")
}

// tunnelConfigured reports whether this daemon is published beyond loopback.
//
// It used to ALSO stat $HOME/Library/LaunchAgents/com.signaldeck.tunnel.plist,
// on the reasoning that a launchd-managed tunnel can start at any moment, so a
// default that is only correct while the tunnel happens to be down is a race
// rather than a default. That reasoning was right; the mechanism rotted twice.
// First the list was headed by a RELATIVE repo path, which os.Stat found in
// every checkout on every machine forever, making this function a constant
// rather than a signal. Then the fix - an absolute $HOME path - became a
// constant FALSE the day this machine moved to Windows, where that path can
// never exist. The check was removed on 2026-09-19 along with the plists
// themselves: it could no longer confirm anything on any machine this code
// runs on, and a signal that cannot fire is worse than none, because the next
// reader trusts it to mean what it says.
//
// What remains is the signal that needs no per-OS knowledge: serving a host you
// cannot reach from loopback IS publication, on every platform and every tunnel
// implementation. An operator running a tunnel this cannot see still has
// SIGNALDECK_ASSUME_TUNNEL.
//
// allowedHosts is the RESOLVED allowlist string (already through pick, so it
// includes values set in daemon/.env — which is exactly where the ngrok
// hostname lives; reading os.Getenv here would have missed it).
func tunnelConfigured(allowedHosts string) bool {
	if v := strings.TrimSpace(os.Getenv("SIGNALDECK_ASSUME_TUNNEL")); v != "" {
		return v == "1" || strings.EqualFold(v, "true")
	}
	// A9 (2026-07-26 re-audit): reachablePrivately() once answered "private"
	// while daemon/.env allowlisted `spearfish-dwindle-module.ngrok-free.dev`,
	// so PublicReads/OpenSignup both defaulted OPEN on a box one `ngrok start`
	// away from being served to the internet. Deriving publication from the
	// operator's own allowlist cannot rot the way a hardcoded path does.
	for _, h := range splitList(allowedHosts) {
		if h != "" && !loopbackOnly(h) {
			return true
		}
	}
	return false
}

// reachablePrivately is the signal the open-by-default settings actually need:
// bound to loopback AND with no reverse tunnel configured that could publish it.
//
// Deriving convenience defaults from the bind address alone was a real finding
// (A9, 2026-07-26 re-audit): this machine runs a launchd-managed ngrok agent
// pointed at :8322 with a reserved public hostname, and the daemon's own
// allowlist already names that hostname. So the "safe by default" heuristic
// evaluated safe on the exact deployment that is public. Both signals must
// agree before anything opens; when they disagree the answer is closed, because
// an operator who wants reads open can say so in one env var, and a stranger
// who gets them by accident cannot be un-given them.
func reachablePrivately(addr, allowedHosts string) bool {
	return loopbackOnly(addr) && !tunnelConfigured(allowedHosts)
}

// ReachablePrivately is the exported form of the safe-by-default signal, for
// packages outside config that must inherit the same posture — notably the MCP
// server, which permits an anonymous caller only when this is true. Exported as
// a function over the ADDRESS rather than as a stored bool so a caller cannot
// hold a stale copy taken before the tunnel agent appeared.
func (c Config) ReachablePrivately() bool {
	return reachablePrivately(c.HTTPAddr, strings.Join(c.AllowedHosts, ","))
}

// PublicOriginMissing reports a stated public deployment whose browser-origin
// allowlist names no HTTPS origin.
//
// WebOrigins defaults to the four localhost spellings, which is right for a dev
// box and wrong for every published deployment: api/security.go rejects a
// non-GET whose Origin is not on this list, and a browser on https://<host>
// sends exactly that Origin. So the read-only pages worked and the waitlist
// form and the operator login answered 403 — the two things a launch is for.
// Neither the hosted recipe nor fly.toml set the variable, so this was the
// DEFAULT rather than a mistake someone had to make, and the symptom points at
// the form rather than at a config file nobody edited.
//
// This asks about intent, not reachability: it fires only once the operator has
// said PublicSurface. It looks for a scheme rather than a hostname because the
// hostname is a deploy-time input this repository deliberately does not carry.
func (c Config) PublicOriginMissing() bool {
	if !c.PublicSurface {
		return false
	}
	for _, o := range c.WebOrigins {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(o)), "https://") {
			return false
		}
	}
	return true
}
