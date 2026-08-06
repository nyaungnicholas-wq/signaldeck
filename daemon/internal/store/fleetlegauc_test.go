package store

import (
	"context"
	"fmt"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// A fleet AUC benches a leg for EVERY symbol, so it must not be computable from
// a handful of them. Grading the sentiment leg on the live tables returns 0.3934
// — on 12 symbols and 202 symbol-days, which is not a fleet claim. Below the
// floor the key is absent entirely, so rankGate finds nothing, casts no veto,
// and the per-symbol bound decides on its own.
func TestFleetLegAUCWithholdsAVerdictUntilEnoughSymbolsAreGraded(t *testing.T) {
	ctx := context.Background()
	st := openDashStore(t)
	write := func(n int) {
		for i := 0; i < n; i++ {
			sym, err := st.UpsertSymbol(ctx, fmt.Sprintf("FL%d", i), md.Stocks, "")
			if err != nil {
				t.Fatalf("UpsertSymbol: %v", err)
			}
			if err := st.UpsertModelForecast(ctx, ModelForecast{
				SymbolID: sym.ID, Horizon: md.H1d, Model: ModelPressure, Ts: 1000,
				Prob: 0.5, AUC: 0.32, NEval: 200, // clearly anti-predictive
			}); err != nil {
				t.Fatalf("UpsertModelForecast: %v", err)
			}
		}
	}

	write(FleetLegMinSymbols - 1)
	got, err := st.FleetLegAUC(ctx)
	if err != nil {
		t.Fatalf("FleetLegAUC: %v", err)
	}
	if v, ok := got[ModelPressure+"|1d"]; ok {
		t.Fatalf("returned %v on %d symbols; a veto must not be available below the %d-symbol floor",
			v, FleetLegMinSymbols-1, FleetLegMinSymbols)
	}

	write(FleetLegMinSymbols) // now over the floor (UpsertSymbol is idempotent)
	got, err = st.FleetLegAUC(ctx)
	if err != nil {
		t.Fatalf("FleetLegAUC: %v", err)
	}
	v, ok := got[ModelPressure+"|1d"]
	if !ok {
		t.Fatalf("no verdict at %d symbols; the floor should be satisfied: %v", FleetLegMinSymbols, got)
	}
	if v > 0.5 {
		t.Fatalf("fleet AUC = %v; rows graded 0.32 must average below chance", v)
	}
}
