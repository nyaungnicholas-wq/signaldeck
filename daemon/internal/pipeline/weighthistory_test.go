package pipeline

import (
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/adaptive"
	"github.com/nyaungnicholas-wq/signaldeck/internal/ensemble"
)

// TestWeightHistoryRows: only cells that actually yielded learned weights emit
// rows (a gated/static cell is an honest gap), stamped with the computed ts.
func TestWeightHistoryRows(t *testing.T) {
	w := adaptive.Weights{
		ComputedTs: 4242,
		Cells: map[string]adaptive.Cell{
			adaptive.AllCell: {
				N:       100,
				Weights: map[string]float64{ensemble.LegPressure: 0.6, ensemble.LegForecast: 0.4},
			},
			"trending_up": { // gated: no learned weights → no rows
				N: 5,
			},
		},
	}
	rows := weightHistoryRows(w)
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2 (only the learned 'all' cell)", len(rows))
	}
	for _, r := range rows {
		if r.Regime != adaptive.AllCell || r.Ts != 4242 {
			t.Errorf("row %+v: want regime 'all' ts 4242", r)
		}
	}
}
