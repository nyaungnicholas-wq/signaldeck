package main

import (
	"encoding/json"
	"testing"
)

// The session-block record must be valid JSON naming its cut-over, and must
// state that no claim and no stored row changes.
func TestSessionBlocksSpecIsValidJSON(t *testing.T) {
	var spec map[string]any
	if err := json.Unmarshal([]byte(sessionBlocksSpec(sessionBlocksMeasured{})), &spec); err != nil {
		t.Fatalf("spec is not valid JSON: %v", err)
	}
	if spec["kind"] != SessionBlocksKind || spec["cutoverTs"] != float64(SessionBlocksEpochTS) {
		t.Fatalf("spec kind/cutover = %v / %v", spec["kind"], spec["cutoverTs"])
	}
	if spec["claimsChanged"] != false || spec["storedRowsRewritten"] != false {
		t.Fatal("the record must state that no claim and no stored row changes")
	}
}
