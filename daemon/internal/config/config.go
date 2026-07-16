// Package config loads SignalDeck's runtime configuration from flags/env,
// including Alpaca credentials reused from the stock-trader project's .env
// (single source of truth for those keys — never copied into this repo).
package config

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// Config is the daemon configuration.
type Config struct {
	DBPath        string
	HTTPAddr      string
	AlpacaKey     string
	AlpacaSecret  string
	// AlpacaFeed overrides the REST bars feed (SIGNALDECK_ALPACA_FEED). Empty →
	// the client default "sip" (full-market historical, already requested on the
	// free tier with a 16-min guarded tail). On a paid Algo Trader Plus upgrade
	// nothing else changes — Alpaca simply serves the full real-time SIP the code
	// already asks for; set this to "iex" only to force the free real-time feed.
	AlpacaFeed string
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

	// Multi-user + exposure controls.
	OpenSignup  bool // SIGNALDECK_OPEN_SIGNUP (default true): allow POST /api/auth/register
	PublicReads bool // SIGNALDECK_PUBLIC_READS (default true): read-only endpoints work without auth (localhost compatibility)
	TrustProxy  bool // SIGNALDECK_TRUST_PROXY (default false): honor X-Forwarded-For / X-Forwarded-Proto
	RateRPS     int  // SIGNALDECK_RATE_RPS: override read-tier requests/sec (0 = default 10)
	RateBurst   int  // SIGNALDECK_RATE_BURST: override read-tier burst (0 = default 30)

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
	home, _ := os.UserHomeDir()
	// The daemon runs under launchd, which does NOT auto-load a .env, so we
	// read the daemon's own .env here (owner-only file holding the LLM key).
	dotenv := parseDotEnv(filepath.Join(home, "claude code", "signaldeck", "daemon", ".env"))
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
	cfg := Config{
		LLMKey:          llmFirst,
		LLMKeys:         llmKeys,
		LLMBaseURL:      pick("SIGNALDECK_LLM_BASE_URL", "https://integrate.api.nvidia.com/v1"),
		LLMModel:        pick("SIGNALDECK_LLM_MODEL", "qwen/qwen3.5-122b-a10b"),                        // MoE: 122B knowledge / ~10B active → strong + ~4s on NVIDIA free tier
		LLMModelDeep:    pick("SIGNALDECK_LLM_MODEL_DEEP", "nvidia/llama-3.3-nemotron-super-49b-v1.5"), // reasoning-tuned; on-demand only (~30s)
		LLMModelFast:    pick("SIGNALDECK_LLM_MODEL_FAST", "meta/llama-3.1-8b-instruct"),              // ultra-fast for high-frequency low-stakes calls
		LLMDailyCap:     atoiOr(pick("SIGNALDECK_LLM_DAILY_CAP", ""), 2000),
		DBPath:          envOr("SIGNALDECK_DB", filepath.Join(home, "claude code", "signaldeck", "data", "signaldeck.db")),
		HTTPAddr:        envOr("SIGNALDECK_HTTP", "127.0.0.1:8322"),
		HudURL:          envOr("SIGNALDECK_HUD_URL", "http://127.0.0.1:8787/api/summary"),
		TickstreamURL:   envOr("SIGNALDECK_TICKSTREAM_URL", "http://127.0.0.1:8321/api/snapshot"),
		GeminiKey:       os.Getenv("SIGNALDECK_GEMINI_KEY"),
		CryptoSymbol:    "BTC/USD",
		WebOrigins:      splitList(pick("SIGNALDECK_WEB_ORIGINS", "http://localhost:8323,http://127.0.0.1:8323,http://localhost:3000,http://127.0.0.1:3000")),
		AllowedHosts:    splitList(pick("SIGNALDECK_ALLOWED_HOSTS", "127.0.0.1:8322,localhost:8322")),
		APIToken:        os.Getenv("SIGNALDECK_API_TOKEN"),
		TVWebhookSecret: pick("SIGNALDECK_TV_WEBHOOK_SECRET", ""),
		OpenSignup:      boolEnv("SIGNALDECK_OPEN_SIGNUP", true),
		PublicReads:     boolEnv("SIGNALDECK_PUBLIC_READS", true),
		TrustProxy:      boolEnv("SIGNALDECK_TRUST_PROXY", false),
		RateRPS:         atoiOr(os.Getenv("SIGNALDECK_RATE_RPS"), 0),
		RateBurst:       atoiOr(os.Getenv("SIGNALDECK_RATE_BURST"), 0),
	}
	cfg.AlpacaKey = os.Getenv("ALPACA_KEY")
	cfg.AlpacaSecret = os.Getenv("ALPACA_SECRET")
	if cfg.AlpacaKey == "" || cfg.AlpacaSecret == "" {
		// Reuse the stock-trader keys (paper account; market data works with it).
		env := parseDotEnv(filepath.Join(home, "claude code", "stock-trader", ".env"))
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
func atoiOr(s string, def int) int {
	if s == "" {
		return def
	}
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return def
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// boolEnv reads a boolean env var ("false"/"0"/"no" = false, "true"/"1"/"yes" = true).
func boolEnv(k string, def bool) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(k))) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return def
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
		out[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"'`)
	}
	return out
}
