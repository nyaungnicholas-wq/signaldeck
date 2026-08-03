package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// C-2 (2026-08-02 re-audit): /api/calibration and /api/track-record both
// publish "the live record" and disagree on every headline figure, because
// they are scoped differently. Neither number is wrong; the defect was that
// neither payload referenced the other, so the pair read as a contradiction.
//
// The failure mode these guard is the notes drifting apart or being dropped —
// the moment one surface stops naming the other, the contradiction is back.

func TestScopeNotesReferenceEachOther(t *testing.T) {
	if !strings.Contains(calibrationScopeNote, "/api/track-record") {
		t.Error("calibrationScopeNote does not name /api/track-record")
	}
	if !strings.Contains(trackRecordScopeNote, "/api/calibration") {
		t.Error("trackRecordScopeNote does not name /api/calibration")
	}
	// Each must say the other's numbers legitimately differ, or a reader still
	// has no reason to believe both.
	for name, note := range map[string]string{
		"calibrationScopeNote": calibrationScopeNote,
		"trackRecordScopeNote": trackRecordScopeNote,
	} {
		if !strings.Contains(note, "DIFFERENT") {
			t.Errorf("%s does not state that the other surface's figures differ", name)
		}
		// The conclusion is the part that must survive edits: both scopes agree
		// there is no measured skill, and that agreement is the reason the
		// difference is safe to publish.
		if !strings.Contains(note, "no measured probabilistic skill") {
			t.Errorf("%s drops the shared verdict", name)
		}
	}
}

// The note has to reach the wire, not just exist as a constant.
func TestTrackRecordPayloadCarriesScopeNote(t *testing.T) {
	srv, _ := newTrackRecordServer(t)

	resp, err := http.Get(srv.URL + "/api/track-record?horizon=1d")
	if err != nil {
		t.Fatalf("GET /api/track-record: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	got, _ := body["scopeNote"].(string)
	if got != trackRecordScopeNote {
		t.Errorf("scopeNote missing or altered on /api/track-record:\n got %q", got)
	}
}
