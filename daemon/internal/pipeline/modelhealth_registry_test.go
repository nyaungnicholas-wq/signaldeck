// The registry's retire flag is the enforcement half of the pre-registered
// auto-retire rule: a FAILED directional row must actually switch its horizon
// off, and a registry nobody can read must switch nothing off. These tests pin
// the flag -> model mapping so the kill path cannot rot silently.
package pipeline

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRetiredFromRegistryMapsFlaggedRowsToHorizons(t *testing.T) {
	path := filepath.Join(t.TempDir(), "accuracy_registry.json")
	blob := `{"rows": [
		{"predictor": "directional-ensemble (1d)", "family": "direction", "retire": true},
		{"predictor": "directional-ensemble (1w)", "family": "direction", "retire": false},
		{"predictor": "directional-ensemble (1w, high conviction)", "family": "direction", "retire": true},
		{"predictor": "trend21", "family": "structure", "retire": true}
	]}`
	if err := os.WriteFile(path, []byte(blob), 0o644); err != nil {
		t.Fatal(err)
	}
	got := retiredFromRegistry(path)
	if !got["directional-ensemble-1d"] {
		t.Error("a FAILED 1d row did not retire directional-ensemble-1d")
	}
	// A FAILED high-conviction tier retires its horizon — the horizon is the
	// unit that publishes, and its actionable tier just lost its evidence.
	if !got["directional-ensemble-1w"] {
		t.Error("a FAILED 1w high-conviction row did not retire directional-ensemble-1w")
	}
	// Structural rows are governed by the DECAYED path against their frozen
	// claims, not by the directional auto-retire rule; a stray retire flag on
	// one must not leak into the directional kill switch.
	if len(got) != 2 {
		t.Errorf("retire map = %v, want exactly the two flagged horizons", got)
	}
}

func TestRetiredFromRegistryFailsSafe(t *testing.T) {
	// Missing file: the kill switch must never fire on evidence nobody can read.
	if got := retiredFromRegistry(filepath.Join(t.TempDir(), "absent.json")); len(got) != 0 {
		t.Errorf("missing registry retired %v", got)
	}
	if got := retiredFromRegistry(""); len(got) != 0 {
		t.Errorf("empty path retired %v", got)
	}
	// Malformed JSON: same posture.
	path := filepath.Join(t.TempDir(), "broken.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := retiredFromRegistry(path); len(got) != 0 {
		t.Errorf("malformed registry retired %v", got)
	}
}
