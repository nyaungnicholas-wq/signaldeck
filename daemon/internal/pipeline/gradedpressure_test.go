package pipeline

import (
	"context"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// seedGradedPressureLeg gives a fixture's pressure leg a MEASURED positive
// lift, so the blend has an admitted leg under production mode.
//
// It exists because the runner now defaults to RequireMeasuredLegs (see
// requireMeasuredLegs): a leg no trainer has graded is benched rather than kept.
// The fixtures below predate that contract and seeded a bare composite score,
// which used to be admitted on availability alone — so every test that merely
// needed SOME published row to inspect was silently relying on cold start.
//
// Seeding the grade is the honest repair rather than switching those tests back
// to cold start: on the live fleet the pressure leg IS graded (0 of 753 rows
// ungraded on 2026-08-07), so a graded leg is what production actually looks
// like, and the tests keep exercising the default path instead of an escape
// hatch. What they assert — features persisted, ledger appended, calibration
// tier chosen, alphax blended — is unchanged and none of it is about admission.
func seedGradedPressureLeg(t *testing.T, st *store.Store, symbolID, ts int64) {
	t.Helper()
	for _, h := range predHorizons {
		if err := st.UpsertModelForecast(context.Background(), store.ModelForecast{
			SymbolID: symbolID,
			Horizon:  h,
			Model:    store.ModelPressure,
			Ts:       ts,
			Prob:     0.5,
			Accuracy: 0.55,
			BaseRate: 0.5,
			Lift:     0.05,
			NTrain:   500,
			NEval:    200,
		}); err != nil {
			t.Fatalf("seed graded pressure leg: %v", err)
		}
	}
}
