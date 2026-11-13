package api

import (
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// A rate shown beside the baseline must come from the same rows and clear the
// day floor on those rows; a thin baseline subset must not publish a rate.
func TestGradeOfPairsTheRateWithItsBaselineRows(t *testing.T) {
	g := gradeOf(store.RegimeKindTally{Kind: "vol21", Graded: 500, Days: 40, Correct: 400, BaselineN: 3, BaselineCorrect: 1, CorrectOnBaseline: 2, BaselineDays: 2})
	if g.Accuracy != nil || g.Status != "too few graded days with a baseline to show a rate" {
		t.Fatalf("thin baseline subset published a rate: %+v", g)
	}
	g = gradeOf(store.RegimeKindTally{Kind: "vol21", Graded: 500, Days: 40, Correct: 400, BaselineN: 200, BaselineCorrect: 100, CorrectOnBaseline: 150, BaselineDays: 30})
	if g.Accuracy == nil || *g.Accuracy != 0.75 || *g.Baseline != 0.5 || g.Compared != 200 {
		t.Fatalf("want 0.75 vs 0.5 on the same 200 rows, got %+v", g)
	}
	g = gradeOf(store.RegimeKindTally{Kind: "vol63", Graded: 50, Days: 12, Correct: 30})
	if g.Accuracy == nil || *g.Accuracy != 0.6 || g.Baseline != nil || g.Compared != 0 {
		t.Fatalf("no baseline: want the plain rate, got %+v", g)
	}
}
