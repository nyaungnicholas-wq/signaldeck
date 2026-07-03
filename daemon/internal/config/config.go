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
	HudURL        string // trader-hud summary endpoint
	TickstreamURL string // tickstream dashboard snapshot endpoint
	GeminiKey     string // optional: LLM polish for insights ("" = rule-based only)
	CryptoSymbol  string // TickStream consolidated symbol label

	// HTTP security (see internal/api/security.go).
	WebOrigins   []string // CORS-allowlisted browser origins for the web app
	AllowedHosts []string // Host-header allowlist (blocks DNS rebinding)
	APIToken     string   // optional bearer token; when set, every request must present it (enables safe remote exposure)

	// LLM layer (OpenAI-compatible; NVIDIA by default). Empty key = the AI
	// agents stay in safe no-op mode.
	LLMKey      string
	LLMBaseURL  string // e.g. https://integrate.api.nvidia.com/v1
	LLMModel    string // e.g. meta/llama-3.3-70b-instruct
	LLMDailyCap int    // hard cap on LLM calls per day (spend guard); 0 = default
}

// LLMEnabled reports whether the AI agents can run (a key is configured).
func (c Config) LLMEnabled() bool { return c.LLMKey != "" }

// Load builds the config. Precedence: environment > project .env files > default.
func Load() Config {
	home, _ := os.UserHomeDir()
	// The daemon runs under launchd, which does NOT auto-load a .env, so we
	// read the daemon's own .env here (owner-only file holding the LLM key).
	dotenv := parseDotEnv(filepath.Join(home, "claude code", "signaldeck", "daemon", ".env"))
	pick := func(env, def string) string {
		if v := os.Getenv(env); v != "" {
			return v
		}
		if v := dotenv[env]; v != "" {
			return v
		}
		return def
	}
	cfg := Config{
		LLMKey:        pick("SIGNALDECK_NVIDIA_KEY", pick("SIGNALDECK_LLM_KEY", "")),
		LLMBaseURL:    pick("SIGNALDECK_LLM_BASE_URL", "https://integrate.api.nvidia.com/v1"),
		LLMModel:      pick("SIGNALDECK_LLM_MODEL", "meta/llama-3.1-8b-instruct"), // 8B is fast+reliable on NVIDIA free tier (70B times out); override via SIGNALDECK_LLM_MODEL
		LLMDailyCap:   atoiOr(pick("SIGNALDECK_LLM_DAILY_CAP", ""), 2000),
		DBPath:        envOr("SIGNALDECK_DB", filepath.Join(home, "claude code", "signaldeck", "data", "signaldeck.db")),
		HTTPAddr:      envOr("SIGNALDECK_HTTP", "127.0.0.1:8322"),
		HudURL:        envOr("SIGNALDECK_HUD_URL", "http://127.0.0.1:8787/api/summary"),
		TickstreamURL: envOr("SIGNALDECK_TICKSTREAM_URL", "http://127.0.0.1:8321/api/snapshot"),
		GeminiKey:     os.Getenv("SIGNALDECK_GEMINI_KEY"),
		CryptoSymbol:  "BTC/USD",
		WebOrigins:    splitEnv("SIGNALDECK_WEB_ORIGINS", "http://localhost:8323,http://127.0.0.1:8323,http://localhost:3000,http://127.0.0.1:3000"),
		AllowedHosts:  splitEnv("SIGNALDECK_ALLOWED_HOSTS", "127.0.0.1:8322,localhost:8322"),
		APIToken:      os.Getenv("SIGNALDECK_API_TOKEN"),
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
	return cfg
}

// HasAlpaca reports whether stock ingestion can run.
func (c Config) HasAlpaca() bool { return c.AlpacaKey != "" && c.AlpacaSecret != "" }

// splitEnv reads a comma-separated env var (or def), trimming blanks.
func splitEnv(k, def string) []string {
	v := def
	if e := os.Getenv(k); e != "" {
		v = e
	}
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
