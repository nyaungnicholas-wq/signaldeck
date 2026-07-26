package pipeline

import (
	"math"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// The mean-reversion leg must invert the SAME quantity it was graded on.
//
// internal/gbm/selfref.go names this defect in its own doctrine comment: the
// leg reads pred_raw, and pred_raw is the blend output computed WITH the
// mean-reversion leg inside it, so the leg inverts a number that already
// contains its own inversion. The sample builder was moved off pred_raw and
// onto pressure_score; the SERVE path forty lines away was not, which left two
// separate defects live:
//
//  1. the recursive self-fit the doctrine comment describes, and
//  2. an admission gate measured on a different variable than the one served —
//     the OOS lift that lets this leg into the blend is computed from
//     (pressure_score+1)/2 while the probability actually published inverts
//     pred_raw.
//
// Measured on the live database 2026-07-26: 7 of 37 meanrev rows carried
// lift>0 and were therefore live in the blend.
func TestMeanRevServedInputMatchesGradedInput(t *testing.T) {
	rows := []store.LabeledFeature{
		// rows[0] is the newest resolved row — the one the serve path reads.
		{Ts: 300, Up: 1, FwdReturn: 0.01, Vec: map[string]float64{
			"pressure_score": 0.634,
			"pred_raw":       0.528, // the blend's own output; must NOT be the input
		}},
		{Ts: 200, Up: 0, FwdReturn: -0.01, Vec: map[string]float64{
			"pressure_score": 0.100, "pred_raw": 0.400,
		}},
		{Ts: 100, Up: 1, FwdReturn: 0.02, Vec: map[string]float64{
			"pressure_score": -0.200, "pred_raw": 0.610,
		}},
	}

	got, ok := meanRevLatestInput(rows)
	if !ok {
		t.Fatal("no latest input derived from rows that carry pressure_score")
	}

	// The graded samples are built as (pressure_score+1)/2. The served input
	// must be built the same way from the same newest row.
	samples := meanRevSamplesFromLabeled(rows)
	if len(samples) == 0 {
		t.Fatal("sample builder produced nothing")
	}
	want := samples[len(samples)-1].RawProb // newest, builder walks oldest->newest

	if math.Abs(got-want) > 1e-12 {
		t.Fatalf("served input %v != graded input %v — the leg is graded on one "+
			"variable and serves another", got, want)
	}
	if math.Abs(got-rows[0].Vec["pred_raw"]) < 1e-12 {
		t.Fatalf("served input equals pred_raw (%v): the leg is inverting the "+
			"blend output that already contains its own inversion", got)
	}
}

// A row missing pressure_score must not fall back to pred_raw. Falling back is
// how the self-reference would return the moment the pressure source has an
// outage — exactly the systematic-outage case this platform has already been
// burned by.
func TestMeanRevLatestInputRefusesWithoutPressure(t *testing.T) {
	rows := []store.LabeledFeature{
		{Ts: 300, Up: 1, Vec: map[string]float64{"pred_raw": 0.528}},
		{Ts: 200, Up: 0, Vec: map[string]float64{"pressure_score": 0.1, "pred_raw": 0.4}},
	}
	if got, ok := meanRevLatestInput(rows); ok {
		t.Fatalf("derived an input (%v) from a newest row with no pressure_score", got)
	}
}

func TestMeanRevLatestInputEmpty(t *testing.T) {
	if _, ok := meanRevLatestInput(nil); ok {
		t.Fatal("derived an input from no rows")
	}
}
