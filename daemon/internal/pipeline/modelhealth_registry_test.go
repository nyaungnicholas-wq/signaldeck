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
	got, err := registryFlagsFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if !got["directional-ensemble-1d"].Retire {
		t.Error("a FAILED 1d row did not retire directional-ensemble-1d")
	}
	// A FAILED high-conviction tier retires its horizon — the horizon is the
	// unit that publishes, and its actionable tier just lost its evidence.
	if !got["directional-ensemble-1w"].Retire {
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
	// FAILS SAFE IN BOTH DIRECTIONS. The kill switch must never fire on evidence
	// nobody can read -- that was this test's original point and it still holds.
	// But it must not CLEAR on that evidence either: an empty map is an
	// affirmative "no model is retired and none is unattributable", and returning
	// it silently let a retired model be regraded without its flag, come out
	// healthy, and be readmitted to the prediction path.
	//
	// The honest answer to unreadable evidence is neither retire nor clear: it is
	// an error, on which ModelHealthWorker.Run returns ErrDegraded and writes no
	// verdict at all. Nothing is retired, and nothing is cleared.
	for _, tc := range []struct {
		name string
		path string
	}{
		{"missing file", filepath.Join(t.TempDir(), "absent.json")},
		{"no path resolved", ""},
	} {
		got, err := registryFlagsFrom(tc.path)
		if err == nil {
			t.Errorf("%s: returned no error (flags %v) -- an unreadable kill switch must not read as all-clear", tc.name, got)
		}
		if got != nil {
			t.Errorf("%s: returned a usable map alongside the error", tc.name)
		}
	}
	// Malformed JSON: same posture.
	path := filepath.Join(t.TempDir(), "broken.json")

	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := registryFlagsFrom(path); err == nil {
		t.Errorf("malformed registry returned no error (flags %v)", got)
	}
}
