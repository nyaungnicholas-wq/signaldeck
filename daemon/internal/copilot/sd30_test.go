package copilot

import (
	"strings"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/publication"
)

func sd30Set(t *testing.T, v bool) {
	t.Helper()
	old := publication.SD30Withheld
	publication.SD30Withheld = v
	t.Cleanup(func() { publication.SD30Withheld = old })
}

// sd30Off runs the query mechanics the SD-30 flag reverses to.
func sd30Off(t *testing.T) { t.Helper(); sd30Set(t, false) }

func TestSD30_DirectionalTrackRecordWithheldFromMembers(t *testing.T) {
	sd30Set(t, true)
	args := map[string]any{"days": "30"}
	if _, _, err := Validate(TierMember, "directional_track_record", args); err == nil || !strings.Contains(err.Error(), publication.SD30Reason) {
		t.Fatalf("a member must be refused with the SD-30 reason, got %v", err)
	}
	for _, q := range For(TierMember) {
		if q.Name == "directional_track_record" {
			t.Fatal("a withheld query is still offered to members")
		}
	}
	if _, _, err := Validate(TierOperator, "directional_track_record", args); err != nil {
		t.Fatalf("the operator keeps the raw tally: %v", err)
	}
	// Flag off removes only the SD-30 reason: since H-2 the query is
	// operator-only on its own terms, so a member is still refused, just not
	// for SD-30, and the operator keeps it.
	sd30Set(t, false)
	if _, _, err := Validate(TierMember, "directional_track_record", args); err == nil || strings.Contains(err.Error(), publication.SD30Reason) {
		t.Fatalf("flag off: a member is refused by tier (H-2), not by SD-30; got %v", err)
	}
	if _, _, err := Validate(TierOperator, "directional_track_record", args); err != nil {
		t.Fatalf("flag off: the operator keeps the query: %v", err)
	}
}
