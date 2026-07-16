package adaptive

import (
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/ensemble"
)

// The unit of every gate in this package is ONE independent (symbol, UTC-day)
// observation. These tests pin that contract: they are the regression wall for
// the pseudo-replication class of bug, where a cell or a leg reaches a floor by
// counting the same market move many times.

// mkDayExamples builds n examples that all land on ONE UTC day, with a perfect
// pressure leg — i.e. exactly what a hot symbol's ~150 same-day feature rows
// looked like to the old row-counting gate.
func mkDayExamples(n int, regime string, day int64) []Example {
	out := make([]Example, 0, n)
	for i := 0; i < n; i++ {
		up := i%2 == 0
		fwd, pPressure := 0.01, 0.75
		if !up {
			fwd, pPressure = -0.01, 0.25
		}
		ex := Example{
			Legs:      map[string]float64{ensemble.LegPressure: pPressure},
			Regime:    regime,
			FwdReturn: fwd,
			Day:       day,
		}
		if up {
			ex.Up = 1
		}
		out = append(out, ex)
	}
	return out
}

// TestCompute_CellDaysCountDistinctDays_NotRows is the core assertion: the
// reported Days is the number of UNIQUE days, never the number of examples.
func TestCompute_CellDaysCountDistinctDays_NotRows(t *testing.T) {
	// 200 examples, all on one day -> N=200 but Days=1.
	w := Compute(mkDayExamples(200, "uptrend", 19000), 0)
	c := w.Cells["uptrend"]
	if c.N != 200 {
		t.Fatalf("N should count the examples handed in: got %d, want 200", c.N)
	}
	if c.Days != 1 {
		t.Fatalf("Days must count DISTINCT days, not rows: got %d, want 1", c.Days)
	}
}

// TestCompute_DayGateRefusesOneDayOfHistory proves the defect this fix closes:
// a cell with a huge sample and a perfect hit-rate must STILL be refused when
// its whole sample came from a single market session.
func TestCompute_DayGateRefusesOneDayOfHistory(t *testing.T) {
	w := Compute(mkDayExamples(200, "squeeze", 19000), 0)
	c := w.Cells["squeeze"]
	// Sanity: this cell would sail past a pure sample floor.
	if c.N < MinCellSamples {
		t.Fatalf("precondition: N=%d must exceed MinCellSamples=%d", c.N, MinCellSamples)
	}
	if c.HitRates[ensemble.LegPressure] != 1.0 {
		t.Fatalf("precondition: pressure leg must look perfect, got %+v", c.HitRates)
	}
	// ...and must be refused anyway, on the day axis.
	if !c.Gated || c.Weights != nil {
		t.Fatalf("200 observations from ONE day must be day-gated: gated=%v weights=%+v", c.Gated, c.Weights)
	}
	if c.GateReason == "" {
		t.Fatal("a gated cell must say WHY (GateReason), never a bare absence")
	}
}

// TestCompute_DayGateOpensWithEnoughDistinctDays is the other half: the same
// evidence spread across enough distinct sessions DOES earn weights, so the
// gate is not simply always-off.
func TestCompute_DayGateOpensWithEnoughDistinctDays(t *testing.T) {
	var exs []Example
	for d := 0; d < MinCellDays; d++ {
		exs = append(exs, mkDayExamples(4, "uptrend", int64(19000+d))...)
	}
	w := Compute(exs, 0)
	c := w.Cells["uptrend"]
	if c.Days != MinCellDays {
		t.Fatalf("Days = %d, want %d", c.Days, MinCellDays)
	}
	if c.N != 4*MinCellDays {
		t.Fatalf("N = %d, want %d", c.N, 4*MinCellDays)
	}
	if c.Gated || len(c.Weights) == 0 {
		t.Fatalf("%d obs across %d days must learn: gated=%v weights=%+v reason=%q",
			c.N, c.Days, c.Gated, c.Weights, c.GateReason)
	}
}

// TestCompute_PerLegDayGate is the H3 regression. A leg whose entire sample sits
// on a couple of sessions must get NO learned weight even when its row count
// clears the floor and its hit-rate is perfect — this is the live `sentiment in
// squeeze` case (LegN=895 across 22 symbol-days, measured 2026-07-16).
func TestCompute_PerLegDayGate(t *testing.T) {
	// A cell that comfortably passes on both axes...
	var exs []Example
	for d := 0; d < 30; d++ {
		exs = append(exs, mkDayExamples(4, "squeeze", int64(19000+d))...)
	}
	// ...but sentiment appears ONLY on the first 3 days, perfectly, many times.
	sentimentRows := 0
	for i := range exs {
		if exs[i].Day >= 19003 {
			continue
		}
		p := 0.65
		if exs[i].Up == 0 {
			p = 0.35
		}
		exs[i].Legs[ensemble.LegSentiment] = p
		sentimentRows++
	}
	w := Compute(exs, 0)
	c := w.Cells["squeeze"]

	if c.Gated {
		t.Fatalf("precondition: the CELL must pass both gates, got reason %q", c.GateReason)
	}
	if c.HitRates[ensemble.LegSentiment] != 1.0 {
		t.Fatalf("precondition: sentiment must look perfect, got %+v", c.HitRates)
	}
	if c.LegDays[ensemble.LegSentiment] != 3 {
		t.Fatalf("sentiment LegDays = %d, want 3 (it only appeared on 3 days)", c.LegDays[ensemble.LegSentiment])
	}
	if _, has := c.Weights[ensemble.LegSentiment]; has {
		t.Fatalf("sentiment on only 3 distinct days must earn NO weight (LegN=%d looked like plenty): %+v",
			c.LegN[ensemble.LegSentiment], c.Weights)
	}
	// The honest half: pressure spans all 30 days and keeps its weight.
	if _, has := c.Weights[ensemble.LegPressure]; !has {
		t.Fatalf("pressure spans 30 days and must keep its weight: %+v", c.Weights)
	}
}

// TestCompute_LegDaysNeverExceedsCellDays is an invariant guard: a leg is a
// subset of its cell, so it can never span more days than the cell does.
func TestCompute_LegDaysNeverExceedsCellDays(t *testing.T) {
	var exs []Example
	for d := 0; d < 12; d++ {
		exs = append(exs, mkDayExamples(3, "range", int64(19000+d))...)
	}
	c := Compute(exs, 0).Cells["range"]
	for leg, ld := range c.LegDays {
		if ld > c.Days {
			t.Fatalf("leg %q spans %d days but the cell spans %d", leg, ld, c.Days)
		}
	}
}
