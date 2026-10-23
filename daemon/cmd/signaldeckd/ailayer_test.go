package main

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/config"
	"github.com/nyaungnicholas-wq/signaldeck/internal/llm"
)

// With a key and the default NVIDIA base URL (retired 2026-10-23), startup must
// log the retirement, not the "no LLM key" warning that would send the operator
// looking for a key.
func TestStartupLogsAILayerRetired(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	cfg := config.Config{LLMKeys: []string{"nvapi-test"}, LLMBaseURL: "https://integrate.api.nvidia.com/v1"}
	logAILayer(llm.New(cfg.LLMKeys, cfg.LLMBaseURL, "m", "", "", 10), cfg)
	out := buf.String()
	if !strings.Contains(out, "AI layer retired: NVIDIA trial API terms do not permit production use (2026-10-23)") ||
		strings.Contains(out, "no LLM key") || strings.Contains(out, "AI layer enabled") {
		t.Fatalf("startup log = %q", out)
	}
}
