package pipeline

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/prereg"
)

// TestRegistryCandidatesAnchorOnTheBinary: the registry readers used to look
// only relative to the working directory, so a daemon started from anywhere
// else refused publication as "registry unavailable" (2026-10-05, AUD-27).
// The first candidate is now beside the running binary; the old forms follow.
func TestRegistryCandidatesAnchorOnTheBinary(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Skip("no executable path on this platform")
	}
	got := RegistryCandidates()
	want := filepath.Join(filepath.Dir(exe), "..", prereg.RegistryRel)
	if len(got) < 4 || got[0] != want {
		t.Fatalf("candidates = %v, want %q first and the three working-directory forms after it", got, want)
	}
	if got[2] != prereg.RegistryRel {
		t.Fatalf("the repo-root form moved: %v", got)
	}
}
