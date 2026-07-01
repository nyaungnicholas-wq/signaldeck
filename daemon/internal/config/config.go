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
	DBPath       string
	HTTPAddr     string
	AlpacaKey    string
	AlpacaSecret string
	HudURL        string // trader-hud summary endpoint
	TickstreamURL string // tickstream dashboard snapshot endpoint
	GeminiKey     string // optional: LLM polish for insights ("" = rule-based only)
	CryptoSymbol  string // TickStream consolidated symbol label
}

// Load builds the config. Precedence: environment > stock-trader/.env > default.
func Load() Config {
	home, _ := os.UserHomeDir()
	cfg := Config{
		DBPath:       envOr("SIGNALDECK_DB", filepath.Join(home, "claude code", "signaldeck", "data", "signaldeck.db")),
		HTTPAddr:     envOr("SIGNALDECK_HTTP", "127.0.0.1:8322"),
		HudURL:        envOr("SIGNALDECK_HUD_URL", "http://127.0.0.1:8787/api/summary"),
		TickstreamURL: envOr("SIGNALDECK_TICKSTREAM_URL", "http://127.0.0.1:8321/api/snapshot"),
		GeminiKey:     os.Getenv("SIGNALDECK_GEMINI_KEY"),
		CryptoSymbol:  "BTC/USD",
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
