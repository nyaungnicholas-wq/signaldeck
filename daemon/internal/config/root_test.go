package config

import (
	"os"
	"path/filepath"
	"testing"
)

// The daemon located its own .env, its database and its MCP audit log by
// building paths from filepath.Join(home, "claude code", ...). On a machine
// where the checkout lives anywhere else -- $HOME/Desktop/claude code, a work
// directory, CI -- every one of those paths pointed at nothing, and the failure
// was SILENT: Load() read no .env at all, so the LLM key was ignored, the Alpaca
// keys were never found, and the security toggles an operator wrote in .env
// (SIGNALDECK_PUBLIC_READS, SIGNALDECK_OPEN_SIGNUP, SIGNALDECK_API_TOKEN) never
// applied. A daemon that ignores its own security configuration while logging
// nothing about it is the failure this pins.
//
// The contract: SIGNALDECK_ROOT names the directory CONTAINING signaldeck/, and
// when set it wins outright.
func TestSignaldeckRootOverrideLocatesTheEnvFile(t *testing.T) {
	root := t.TempDir()
	envDir := filepath.Join(root, "signaldeck", "daemon")
	if err := os.MkdirAll(envDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	const key = "sentinel-key-from-test-env"
	if err := os.WriteFile(filepath.Join(envDir, ".env"),
		[]byte("SIGNALDECK_NVIDIA_KEY="+key+"\n"), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}

	// Real env wins over .env by design, so clear the one we are asserting on.
	t.Setenv("SIGNALDECK_NVIDIA_KEY", "")
	t.Setenv("SIGNALDECK_NVIDIA_KEYS", "")
	t.Setenv("SIGNALDECK_LLM_KEY", "")
	t.Setenv("SIGNALDECK_ROOT", root)

	cfg := Load()
	if cfg.LLMKey != key {
		t.Errorf("Load() read LLMKey = %q, want %q.\n"+
			"SIGNALDECK_ROOT=%s holds signaldeck/daemon/.env with that key, so Load() did not "+
			"resolve its root from the override. While this fails, the daemon cannot be run from "+
			"any checkout that is not exactly $HOME/claude code -- and it fails silently, "+
			"ignoring the operator's own security settings.",
			cfg.LLMKey, key, root)
	}
	if !cfg.LLMEnabled() {
		t.Error("LLMEnabled() is false despite a key being configured in the located .env")
	}
}

// The override must also move the paths DERIVED from the root, or the daemon
// reads its config from one tree and its database from another.
func TestSignaldeckRootOverrideMovesDerivedPaths(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "signaldeck", "daemon"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Setenv("SIGNALDECK_ROOT", root)
	t.Setenv("SIGNALDECK_DB", "")       // force the default so the root shows through
	t.Setenv("SIGNALDECK_MCP_AUDIT", "")

	cfg := Load()
	wantDB := filepath.Join(root, "signaldeck", "data", "signaldeck.db")
	if cfg.DBPath != wantDB {
		t.Errorf("DBPath = %q, want %q -- the database path ignored SIGNALDECK_ROOT", cfg.DBPath, wantDB)
	}
	wantAudit := filepath.Join(root, "signaldeck", "logs", "mcp_audit.jsonl")
	if cfg.MCPAuditPath != wantAudit {
		t.Errorf("MCPAuditPath = %q, want %q -- the audit log path ignored SIGNALDECK_ROOT", cfg.MCPAuditPath, wantAudit)
	}
}

// An explicit environment variable must still beat the root-derived default:
// the override relocates DEFAULTS, it does not seize control of paths the
// operator set by hand.
func TestExplicitDBPathStillBeatsTheRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "signaldeck", "daemon"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	explicit := filepath.Join(t.TempDir(), "chosen.db")
	t.Setenv("SIGNALDECK_ROOT", root)
	t.Setenv("SIGNALDECK_DB", explicit)

	if got := Load().DBPath; got != explicit {
		t.Errorf("DBPath = %q, want the explicit %q", got, explicit)
	}
}
