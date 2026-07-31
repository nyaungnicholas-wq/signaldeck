package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/researchx"
)

// tools/research_liveness.py reads the database out of process and refuses to
// certify an evidential-null narration written on a corpus that could not have
// produced a survivor. To do that it must know the SAME floors the in-daemon
// gate enforces — and a Python copy of those numbers is exactly the drift that
// makes an auditor agree with a binary it is supposed to check.
//
// So the numbers are EMITTED here, by the packages that own them, and the probe
// reads the generated file (failing loudly when it is absent). This test writes
// the file and then FAILS if what was on disk differed, so a constant changed in
// Go without regenerating is a red test rather than a silently stale auditor.
// It exports values; it cannot alter a gate or a result.
func TestWriteResearchxConstantsJSON(t *testing.T) {
	// The scheduled loop's config, constructed exactly as researchloop.go does
	// it, so the emitted holdout floor is the one that will actually be applied.
	cfg := researchx.DiscoverConfig{HoldoutEra: researchx.PreregHoldoutEra}
	got, err := json.MarshalIndent(map[string]any{
		"_generatedBy":    "daemon/internal/store/researchx_constants_export_test.go (go test ./internal/store -run TestWriteResearchxConstantsJSON)",
		"_purpose":        "floors the in-daemon research gate enforces, for tools/research_liveness.py",
		"minPositiveEras": researchx.MinPositiveEras,
		"minHoldoutWeeks": cfg.HoldoutWeeksFloor(),
		"holdoutEra":      researchx.PreregHoldoutEra,
		"weekBucketSecs":  WeekBucketSecs,
	}, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got = append(got, '\n')

	path := filepath.Join("..", "..", "..", "tools", "researchx_constants.json")
	prev, readErr := os.ReadFile(path)
	if err := os.WriteFile(path, got, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	if readErr != nil || string(prev) != string(got) {
		t.Fatalf("%s was absent or stale and has been regenerated — commit it; "+
			"tools/research_liveness.py reads these floors and must not drift from the gate", path)
	}
}
