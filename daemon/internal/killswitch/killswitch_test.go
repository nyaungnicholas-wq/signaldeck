package killswitch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The truth table is the contract. Only a definitive "not exists" may permit
// trading; every other reading halts.
func TestCheckPathTruthTable(t *testing.T) {
	dir := t.TempDir()

	absent := filepath.Join(dir, "HALT")
	if st := CheckPath(absent); st.Halted {
		t.Fatalf("absent halt file must permit trading, got halted: %s", st.Reason)
	}

	present := filepath.Join(dir, "HALT_PRESENT")
	if err := os.WriteFile(present, []byte("  drawdown breach 2026-08-04  "), 0o600); err != nil {
		t.Fatal(err)
	}
	st := CheckPath(present)
	if !st.Halted {
		t.Fatal("present halt file must halt")
	}
	if !strings.Contains(st.Reason, "drawdown breach 2026-08-04") {
		t.Fatalf("the file's contents must become the audited reason, got %q", st.Reason)
	}

	// An empty file still halts — the operator's reason is optional, the halt
	// is not.
	empty := filepath.Join(dir, "HALT_EMPTY")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if st := CheckPath(empty); !st.Halted || st.Reason == "" {
		t.Fatalf("empty halt file must halt with a reason, got %+v", st)
	}

	// A directory at the halt path counts as present.
	asDir := filepath.Join(dir, "HALT_DIR")
	if err := os.Mkdir(asDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if st := CheckPath(asDir); !st.Halted {
		t.Fatal("a directory at the halt path must halt")
	}
}

// THE FAIL-CLOSED CASE. A path that cannot be read at all is not evidence of
// safety. The NUL byte produces a stat error that is not IsNotExist on every
// platform, which is exactly the third branch of the truth table.
func TestUnreadablePathFailsClosed(t *testing.T) {
	st := CheckPath("bad\x00path")
	if !st.Halted {
		t.Fatal("an unreadable halt path must fail CLOSED — absence of evidence is not evidence of absence")
	}
	if !strings.Contains(st.Reason, "could not be read") {
		t.Fatalf("the refusal must say the switch was unreadable, got %q", st.Reason)
	}
}

func TestEnvHalt(t *testing.T) {
	dir := t.TempDir()
	absent := filepath.Join(dir, "HALT")

	for _, v := range []string{"1", "true", "YES", "on", "maybe", "banana"} {
		t.Setenv(EnvHalt, v)
		if st := CheckPath(absent); !st.Halted {
			t.Fatalf("%s=%q must halt (unrecognised values halt on purpose)", EnvHalt, v)
		}
	}
	for _, v := range []string{"", "0", "false", "no", "OFF"} {
		t.Setenv(EnvHalt, v)
		if st := CheckPath(absent); st.Halted {
			t.Fatalf("%s=%q must not halt, got %q", EnvHalt, v, st.Reason)
		}
	}
}

// The env override may only ever halt. It must not be able to talk the system
// past a halt file sitting on disk.
func TestEnvCannotClearAFileHalt(t *testing.T) {
	dir := t.TempDir()
	present := filepath.Join(dir, "HALT")
	if err := os.WriteFile(present, []byte("stop"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvHalt, "0")
	if st := CheckPath(present); !st.Halted {
		t.Fatal("SIGNALDECK_HALT=0 must not clear a halt file on disk")
	}
}

func TestPathOverride(t *testing.T) {
	if got := Path(); got != DefaultPath {
		t.Fatalf("default path = %q, want %q", got, DefaultPath)
	}
	t.Setenv(EnvPath, "  /var/lib/signaldeck/HALT  ")
	if got := Path(); got != "/var/lib/signaldeck/HALT" {
		t.Fatalf("override path = %q", got)
	}
	t.Setenv(EnvPath, "   ")
	if got := Path(); got != DefaultPath {
		t.Fatalf("blank override must fall back to the default, got %q", got)
	}
}

// HALT SIMULATION. The switch is read fresh on every call, so tripping it
// part-way through a run stops the very next order rather than waiting for a
// restart. This is the property the whole control depends on.
func TestHaltMidRunStopsTheNextOrder(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "HALT")

	var placed, refused int
	for i := 0; i < 6; i++ {
		if i == 3 {
			// The operator trips the switch between orders 3 and 4.
			if err := os.WriteFile(path, []byte("manual halt during run"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if CheckPath(path).Halted {
			refused++
			continue
		}
		placed++
	}
	if placed != 3 || refused != 3 {
		t.Fatalf("halt must take effect on the next order: placed=%d refused=%d, want 3/3", placed, refused)
	}

	// And clearing it resumes, so the control is recoverable without a restart.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if CheckPath(path).Halted {
		t.Fatal("removing the halt file must resume trading")
	}
}
