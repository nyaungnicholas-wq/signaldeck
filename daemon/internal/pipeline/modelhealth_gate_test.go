package pipeline

import (
	"os"
	"path/filepath"
	"testing"
)

// These tests pin a real defect: the accuracy registry's revision gate deletes a
// row's 'verdict' when contributing rows name builds the repo does not contain,
// but 'retire' (computed FROM that verdict) used to survive in the file, and
// the daemon read only 'retire' - so the kill switch decided on evidence the
// grader had disowned.

func writeTempJSON(t *testing.T, dir, content string) string {
	t.Helper()
	path := filepath.Join(dir, "registry.json")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRevisionGateVoidsRetireFlag(t *testing.T) {
	dir := t.TempDir()
	json := `{"rows":[{"predictor":"directional-ensemble (1d)","family":"direction","retire":true,"revision_gate":["abc123+dirty"]}]}`
	path := writeTempJSON(t, dir, json)

	result := registryFlagsFrom(path)

	f, ok := result["directional-ensemble-1d"]
	if !ok {
		t.Fatal("expected directional-ensemble-1d in map")
	}
	if !f.Unattributable {
		t.Error("expected Unattributable to be true")
	}
	if f.Retire {
		t.Error("expected Retire to be false")
	}
	if len(f.Offenders) != 1 || f.Offenders[0] != "abc123+dirty" {
		t.Errorf("expected Offenders [abc123+dirty], got %v", f.Offenders)
	}
}

func TestRevisionGateDisqualifiesWhenRetireIsFalse(t *testing.T) {
	dir := t.TempDir()
	json := `{"rows":[{"predictor":"directional-ensemble (1d)","family":"direction","retire":false,"revision_gate":["(unstamped)","deadbeef+dirty"]}]}`
	path := writeTempJSON(t, dir, json)

	result := registryFlagsFrom(path)

	f, ok := result["directional-ensemble-1d"]
	if !ok {
		t.Fatal("expected directional-ensemble-1d in map")
	}
	if !f.Unattributable {
		t.Error("expected Unattributable to be true")
	}
}

func TestRevisionGateOnHighConvictionTierDisqualifiesHorizon(t *testing.T) {
	dir := t.TempDir()
	// Case 1: ungated first, gated second
	json1 := `{"rows":[
		{"predictor":"directional-ensemble (1w)","family":"direction","retire":false},
		{"predictor":"directional-ensemble (1w, high conviction)","family":"direction","retire":true,"revision_gate":["gate1"]}
	]}`
	path1 := writeTempJSON(t, dir, json1)
	result1 := registryFlagsFrom(path1)
	f1, ok1 := result1["directional-ensemble-1w"]
	if !ok1 || !f1.Unattributable {
		t.Error("expected Unattributable true for order 1")
	}

	// Case 2: gated first, ungated second
	json2 := `{"rows":[
		{"predictor":"directional-ensemble (1w, high conviction)","family":"direction","retire":true,"revision_gate":["gate2"]},
		{"predictor":"directional-ensemble (1w)","family":"direction","retire":false}
	]}`
	path2 := writeTempJSON(t, dir, json2)
	result2 := registryFlagsFrom(path2)
	f2, ok2 := result2["directional-ensemble-1w"]
	if !ok2 || !f2.Unattributable {
		t.Error("expected Unattributable true for order 2")
	}

	// Case 3 is the one that actually bites: a gated tier followed by an
	// UNGATED tier of the same horizon carrying retire=true. Nothing may
	// re-enable a retire claim on a horizon whose evidence is disowned, and
	// the disqualification must not be cleared by the later row.
	json3 := `{"rows":[
		{"predictor":"directional-ensemble (1w, high conviction)","family":"direction","retire":true,"revision_gate":["gate3"]},
		{"predictor":"directional-ensemble (1w)","family":"direction","retire":true}
	]}`
	path3 := writeTempJSON(t, dir, json3)
	f3, ok3 := registryFlagsFrom(path3)["directional-ensemble-1w"]
	if !ok3 {
		t.Fatal("expected directional-ensemble-1w in map for order 3")
	}
	if !f3.Unattributable {
		t.Error("an ungated sibling row cleared the horizon's disqualification")
	}
	if f3.Retire {
		t.Error("an ungated sibling row revived a retire claim on disowned evidence")
	}
}

func TestUngatedRowsKeepRetireSemantics(t *testing.T) {
	dir := t.TempDir()
	json := `{"rows":[
		{"predictor":"directional-ensemble (1d)","family":"direction","retire":true},
		{"predictor":"directional-ensemble (1w)","family":"direction","retire":false}
	]}`
	path := writeTempJSON(t, dir, json)

	result := registryFlagsFrom(path)

	f, ok := result["directional-ensemble-1d"]
	if !ok {
		t.Fatal("expected directional-ensemble-1d in map")
	}
	if !f.Retire {
		t.Error("expected Retire to be true for ungated row")
	}
	if f.Unattributable {
		t.Error("expected Unattributable to be false for ungated row")
	}

	if _, ok := result["directional-ensemble-1w"]; ok {
		t.Error("expected no entry for retire=false row")
	}
}

func TestRevisionGateIgnoresNonDirectionFamilies(t *testing.T) {
	dir := t.TempDir()
	json := `{"rows":[{"predictor":"structure-ensemble (1d)","family":"structure","retire":true,"revision_gate":["gate1"]}]}`
	path := writeTempJSON(t, dir, json)

	result := registryFlagsFrom(path)

	if _, ok := result["structure-ensemble-1d"]; ok {
		t.Error("expected no entry for non-direction family")
	}
}
