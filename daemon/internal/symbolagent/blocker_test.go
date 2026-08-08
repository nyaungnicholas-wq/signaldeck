package symbolagent

import (
	"strings"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/adaptive"
	"github.com/nyaungnicholas-wq/signaldeck/internal/ensemble"
)

// A refused tier must say WHICH floor refused it.
//
// Measured 2026-08-08: 0 of 2,950 stored models were personal, and the learner
// had reported "0 personal" hourly for weeks without once naming the reason.
// Finding it meant tracing the learner, the tier gate, the adaptive panel and
// finally the stored weights blob — where every regime cell held EMPTY weights
// because it carried 14-16 distinct days against adaptive.MinCellDays of 20.
// Every symbol therefore fell through to the fleet-wide calibration map, which
// is the mechanism behind the cross-section collapse.
//
// A floor that blocks silently is indistinguishable from a broken feature.
func TestTierBlockerNamesTheBindingFloor(t *testing.T) {
	// Too few rows: the first floor in the gate's own order.
	m := Learn(nil, nil, false, false)
	if !strings.Contains(m.TierBlocker, "rows") {
		t.Errorf("an empty history did not report the row floor, got %q", m.TierBlocker)
	}
	if m.Tier == TierPersonal {
		t.Error("an empty history graduated to personal")
	}

	// Enough rows, but all on ONE day: the day floor binds, not the row floor.
	ex := make([]adaptive.Example, 0, MinPersonal+5)
	for i := 0; i < MinPersonal+5; i++ {
		ex = append(ex, adaptive.Example{
			Legs: map[string]float64{"pressure": 0.6}, Regime: "all",
			Ts: 1786000000, Up: 1, FwdReturn: 0.01,
		})
	}
	m = Learn(ex, nil, false, false)
	if !strings.Contains(m.TierBlocker, "distinct days") {
		t.Errorf("rows on a single day did not report the DAY floor, got %q", m.TierBlocker)
	}

	// A model that reaches personal carries no blocker.
	if m.Tier == TierPersonal && m.TierBlocker != "" {
		t.Errorf("a personal model still reported a blocker: %q", m.TierBlocker)
	}
}

// The blocker must never be a bare "blocked" with no number: the whole value is
// knowing how far short the evidence is, so an operator can tell "two more days"
// from "this will never happen".
func TestTierBlockerCarriesTheShortfall(t *testing.T) {
	m := Learn(nil, []ensemble.Pair{}, false, false)
	if m.TierBlocker == "" {
		t.Fatal("no blocker recorded for an empty history")
	}
	if !strings.Contains(m.TierBlocker, "/") {
		t.Errorf("blocker %q carries no have/need shortfall", m.TierBlocker)
	}
}
