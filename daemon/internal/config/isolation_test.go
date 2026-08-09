package config

import (
	"os"
	"path/filepath"
	"testing"
)

// The e2e suite spawns the daemon with a fake HOME and the comment "no .env
// files -> no Alpaca/LLM keys". That isolation never held. projectRoot()'s
// probe walks the WORKING DIRECTORY upward for an ancestor holding
// signaldeck/daemon, and `go test ./e2e` runs with cwd inside the real
// checkout -- so the walk reached the real root and Load() read the operator's
// real daemon/.env (LLM key) and stock-trader/.env (Alpaca keys). The
// os.UserHomeDir() rung that HOME controls is LAST and was never reached.
//
// These tests reproduce that geometry against a DECOY checkout in a temp dir,
// so they prove both the leak and the fix without reading a byte of the real
// .env files. They assert on what Load() actually ingested rather than on path
// strings, which are not stable across Windows short/long name forms.

const (
	decoyLLMKey    = "decoy-llm-key-must-never-be-loaded"
	decoyAlpacaKey = "decoy-alpaca-key-must-never-be-loaded"
)

// decoyCheckout builds <root>/signaldeck/daemon/.env and <root>/stock-trader/.env
// holding sentinel credentials, and returns a working directory buried inside
// it -- the stand-in for daemon/e2e. Any loader that ingests a sentinel has
// walked the tree into the surrounding checkout, which is the bug.
func decoyCheckout(t *testing.T) (workdir string) {
	t.Helper()
	root := t.TempDir()
	daemonDir := filepath.Join(root, "signaldeck", "daemon")
	workdir = filepath.Join(daemonDir, "e2e")
	traderDir := filepath.Join(root, "stock-trader")
	for _, d := range []string{workdir, traderDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir decoy %s: %v", d, err)
		}
	}
	if err := os.WriteFile(filepath.Join(daemonDir, ".env"),
		[]byte("SIGNALDECK_NVIDIA_KEY="+decoyLLMKey+"\n"), 0o600); err != nil {
		t.Fatalf("write decoy daemon/.env: %v", err)
	}
	if err := os.WriteFile(filepath.Join(traderDir, ".env"),
		[]byte("ALPACA_KEY="+decoyAlpacaKey+"\nALPACA_SECRET="+decoyAlpacaKey+"\n"), 0o600); err != nil {
		t.Fatalf("write decoy stock-trader/.env: %v", err)
	}
	return workdir
}

// isolate blanks every variable that could carry a credential in from the real
// process environment, and neutralises the HOME/USERPROFILE rung, so anything
// Load() reports came from a .env file the loader chose to open.
func isolate(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"SIGNALDECK_NVIDIA_KEY", "SIGNALDECK_NVIDIA_KEYS", "SIGNALDECK_LLM_KEY",
		"ALPACA_KEY", "ALPACA_SECRET",
	} {
		t.Setenv(k, "")
	}
	t.Setenv("HOME", t.TempDir())        // the e2e suite's idea of "isolation"
	t.Setenv("USERPROFILE", t.TempDir()) // os.UserHomeDir() on Windows
}

// loadConfig calls Load(), reporting a panic as a fail-closed refusal. Refusing
// is an acceptable answer to a misconfigured root; silently reading the
// surrounding checkout is not.
func loadConfig() (cfg Config, refused bool) {
	defer func() {
		if recover() != nil {
			refused = true
		}
	}()
	return Load(), false
}

// THE LEAK. With no override set, running from inside a checkout resolves that
// checkout no matter what HOME says. This is production behaviour by design --
// the probe is what makes a clone work wherever it is put -- and it is exactly
// why HOME isolation alone is a fiction. Passes before and after the fix.
func TestHomeAloneDoesNotIsolateTheDaemonFromItsCheckout(t *testing.T) {
	workdir := decoyCheckout(t)
	isolate(t)
	t.Setenv("SIGNALDECK_ROOT", "unused") // registers restore
	_ = os.Unsetenv("SIGNALDECK_ROOT")
	t.Chdir(workdir) // == cwd of `go test ./e2e`

	cfg, refused := loadConfig()
	if refused {
		t.Fatal("Load() refused with no override set; the probe must still work")
	}
	if cfg.LLMKey != decoyLLMKey {
		t.Fatalf("LLMKey = %q, want the checkout's .env key: the leak did not reproduce", cfg.LLMKey)
	}
	if !cfg.HasAlpaca() {
		t.Fatal("HasAlpaca() = false; the stock-trader/.env leak did not reproduce")
	}
	t.Logf("LEAK REPRODUCED: HOME=%q holds no .env, yet Load() ingested credentials "+
		"from the checkout surrounding the working directory", os.Getenv("HOME"))
}

// THE FIX. Once SIGNALDECK_ROOT is set, no value of it may cause the
// surrounding checkout's credentials to be read. Blank values are the hole:
// before the fix a set-but-empty root fell through to the probe, so a harness
// that computed its root wrongly re-acquired the real repo's keys in silence --
// the precise failure the override exists to prevent.
func TestExplicitRootNeverReadsTheSurroundingCheckout(t *testing.T) {
	for _, tc := range []struct{ name, value string }{
		{"blank", ""},
		{"whitespace", "   "},
		{"tab", "\t"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			workdir := decoyCheckout(t)
			isolate(t)
			t.Setenv("SIGNALDECK_ROOT", tc.value)
			t.Chdir(workdir)

			cfg, refused := loadConfig()
			if refused {
				return // fail-closed is an acceptable answer
			}
			if cfg.LLMKey == decoyLLMKey || cfg.AlpacaKey == decoyAlpacaKey {
				t.Fatalf("SIGNALDECK_ROOT=%q still loaded the surrounding checkout's credentials "+
					"(llm=%v alpaca=%v).\nAn explicitly set root must never fall through to the "+
					"tree walk: a harness that computed a blank root silently loads the real "+
					"daemon/.env and stock-trader/.env instead of running with no keys.",
					tc.value, cfg.LLMKey == decoyLLMKey, cfg.AlpacaKey == decoyAlpacaKey)
			}
		})
	}
}

// The e2e contract itself: a stated root holding no .env yields NO credentials
// and keeps running. Aborting on a missing .env would only teach people to
// unset SIGNALDECK_ROOT, which reopens the leak.
func TestStatedEmptyRootYieldsNoCredentialsAndDoesNotAbort(t *testing.T) {
	workdir := decoyCheckout(t)
	isolate(t)
	t.Setenv("SIGNALDECK_ROOT", t.TempDir()) // no signaldeck/, no stock-trader/, no .env
	t.Chdir(workdir)

	cfg, refused := loadConfig()
	if refused {
		t.Fatal("Load() refused a valid stated root; it must proceed with no credentials")
	}
	if cfg.LLMKey != "" || cfg.LLMEnabled() {
		t.Errorf("LLMKey = %q / LLMEnabled = %v, want no LLM credentials under an empty stated root",
			cfg.LLMKey, cfg.LLMEnabled())
	}
	if cfg.HasAlpaca() {
		t.Error("HasAlpaca() = true under an empty stated root: Alpaca keys leaked in")
	}
}
