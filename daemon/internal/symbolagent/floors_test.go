package symbolagent

import (
	"strings"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/adaptive"
	"github.com/nyaungnicholas-wq/signaldeck/internal/ensemble"
)

// syntheticDays builds one symbol's own labeled history with the shape the
// predictor actually produces: `days` distinct UTC days with `perDay` rows on
// each, where every row on a day repeats that day's single call and outcome.
// Pressure is right on 90% of the days; forecast is a coin flip.
//
// Row count is days*perDay, which is what MinPersonal used to count; the
// independent evidence is `days`.
func syntheticDays(days, perDay int) ([]adaptive.Example, []ensemble.Pair) {
	var ex []adaptive.Example
	var pairs []ensemble.Pair
	for d := 0; d < days; d++ {
		up := d % 2
		fwd := 0.01
		if up == 0 {
			fwd = -0.01
		}
		pr := 0.7
		if up == 0 {
			pr = 0.3
		}
		if d%10 == 0 { // pressure is wrong one day in ten
			pr = 1 - pr
		}
		fc := 0.7
		if d%4 >= 2 { // forecast: uncorrelated with the outcome
			fc = 0.3
		}
		ts := int64(d) * 86400
		for i := 0; i < perDay; i++ {
			ex = append(ex, adaptive.Example{
				Legs:      map[string]float64{ensemble.LegPressure: pr, ensemble.LegForecast: fc},
				Regime:    "trend_up",
				Ts:        ts + int64(i)*600, // every 10 minutes, same UTC day
				Up:        up,
				FwdReturn: fwd,
			})
			pairs = append(pairs, ensemble.Pair{
				Pred: 0.45 + 0.1*float64(up), Actual: float64(up), Ts: ts + int64(i)*600,
			})
		}
	}
	return ex, pairs
}

// H5 — A SYMBOL EARNED A PERSONAL MODEL ON A FORTNIGHT OF EVIDENCE.
//
// MinPersonal counted raw rows, and the predictor writes ~12 rows per
// symbol-day, so 40 rows is about three days. Measured live at the time of the
// fix: 1,045 of 1,050 symbols cleared the 40-ROW floor for the 1d horizon while
// the whole labeled history spanned 23 distinct days.
//
// 25 days x 12 rows is 300 rows — more than seven times the old floor — and
// enough days that the GLOBAL per-regime floor (adaptive.MinCellDays = 20) is
// cleared, so this test isolates the per-symbol floor: the symbol has learnable
// weights and must still not be promoted to a personal model.
func TestLearn_PersonalNeedsDistinctDays(t *testing.T) {
	ex, pairs := syntheticDays(25, 12)
	if len(ex) <= MinPersonal {
		t.Fatalf("fixture must clear the row floor to isolate the day floor: %d rows", len(ex))
	}
	// Precondition: the symbol's own pooled cell DOES yield weights, so nothing
	// but the day floor can be responsible for the refusal below.
	cell := adaptive.Compute(ex, 0).Cells[adaptive.AllCell]
	if len(cell.Weights) == 0 {
		t.Fatalf("fixture broken: pooled cell must yield weights (reason %q)", cell.Reason)
	}

	m := Learn(ex, pairs, true, true)
	if m.Tier == TierPersonal {
		t.Fatalf("%d rows over 25 days must NOT graduate (floor is %d days): tier=%q weights=%v",
			len(ex), MinPersonalDays, m.Tier, m.Weights)
	}
	if m.NDays != 25 {
		t.Fatalf("nDays = %d, want 25", m.NDays)
	}
	if m.Weights != nil || m.Calibration.Fitted {
		t.Fatalf("a still-learning symbol must ship neither personal weights nor a fitted map: %+v", m)
	}
	// The honest still-learning line must be counted in the unit that binds.
	if !strings.Contains(m.Personality, "day") {
		t.Fatalf("personality must state progress in DAYS, not rows: %q", m.Personality)
	}

	// The same symbol, same rows per day, once it has enough distinct days.
	ex, pairs = syntheticDays(MinPersonalDays+5, 12)
	m = Learn(ex, pairs, true, true)
	if m.Tier != TierPersonal {
		t.Fatalf("%d days of evidence must graduate: tier=%q", m.NDays, m.Tier)
	}
	if m.Weights[ensemble.LegPressure] <= 0 {
		t.Fatalf("pressure is right 90%% of days — it must earn weight: %v", m.Weights)
	}
}

// The personal isotonic calibration is fitted from the SAME clustered rows, so
// it needs its own distinct-day floor: 360 pairs drawn from 3 market moves is
// not evidence that a symbol's probabilities are miscalibrated.
func TestLearn_CalibrationNeedsDistinctDays(t *testing.T) {
	_, pairs := syntheticDays(3, 120)
	if len(pairs) < ensemble.MinCalibrationPairs {
		t.Fatalf("fixture must clear the pair floor: %d pairs", len(pairs))
	}
	if _, _, ok := ensemble.CalibrateKnots(pairs); ok {
		t.Fatalf("%d pairs spanning 3 distinct days must not fit a map (floor is %d days)",
			len(pairs), ensemble.MinCalibrationDays)
	}
	// The same number of pairs, spread over enough days, does fit.
	_, pairs = syntheticDays(ensemble.MinCalibrationDays+5, 15)
	if _, _, ok := ensemble.CalibrateKnots(pairs); !ok {
		t.Fatalf("%d pairs over %d days must fit", len(pairs), ensemble.MinCalibrationDays+5)
	}
}
