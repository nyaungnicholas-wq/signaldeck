package forecastmon

import (
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/ensemble"
)

// The publication gate must refuse every cross-section this detector would call
// collapsed: a published pass the detector rejects refuses the whole graded
// window (2026-09-20 grading-window amendment). Until 2026-09-24 the gate asked
// for one distinct value per 20 symbols (5%) against this detector's 15%.
func TestPublishGateMatchesCollapseDetector(t *testing.T) {
	if ensemble.MinDistinctRatio < MinDistinctRatio {
		t.Fatalf("publish gate distinct ratio %.3f is looser than the collapse detector's %.3f",
			ensemble.MinDistinctRatio, MinDistinctRatio)
	}
	// A wide pass at 12% distinct: collapsed by the detector, so never published.
	cs := ensemble.CrossSection{N: 329, Distinct: 40, Spread: 0.20}
	if ok, _ := cs.Usable(); ok {
		t.Fatal("a 40/329 (12%) cross-section passed the publish gate; the detector calls it collapsed")
	}
}
