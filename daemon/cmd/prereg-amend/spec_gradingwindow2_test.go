package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// The second re-registration must measure on ITS boundaries: the squeezed day
// between 2026-08-07 and 2026-09-25 is the refusal it clears (old window) and
// must not count against the window it opens (new). The first record's spec
// stays pinned to the boundary it filed.
func TestGradingWindow2MeasuresItsOwnBoundaries(t *testing.T) {
	if window2OldEpochTS != 1786060800 || newGradingEpochTS != 1786060800 {
		t.Fatalf("seq 117's boundary moved: window2Old=%d newGradingEpochTS=%d", window2OldEpochTS, newGradingEpochTS)
	}
	if window2NewEpochTS != store.GradingEpoch || store.GradingEpoch != 1790294400 {
		t.Fatalf("new epoch %d, store.GradingEpoch %d; want 1790294400 (2026-09-25)", window2NewEpochTS, store.GradingEpoch)
	}
	dbPath := filepath.Join(t.TempDir(), "w2.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	squeezed := int64(1789344000) // 2026-09-14T00:00:00Z: one market-wide call
	clean := int64(1790380800)    // 2026-09-26T00:00:00Z: dispersed
	for i := 0; i < 40; i++ {
		sym, err := st.UpsertSymbol(ctx, "S"+itoa(i), md.Stocks, "")
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range []struct {
			ts   int64
			prob float64
		}{{squeezed + 3600, 0.48}, {clean + 3600, 0.30 + 0.01*float64(i)}} {
			if err := st.UpsertPrediction(ctx, store.Prediction{SymbolID: sym.ID, Horizon: md.H1d,
				Ts: row.ts, RawProb: row.prob, CalProb: row.prob, NUsed: 2, Components: "{}"}); err != nil {
				t.Fatal(err)
			}
			if err := st.ResolvePrediction(ctx, sym.ID, md.H1d, row.ts, 0.01); err != nil {
				t.Fatal(err)
			}
		}
	}
	_ = st.Close()
	m, err := measureGradingWindow(ctx, dbPath, window2OldEpochTS, window2NewEpochTS)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.CollapsedOld["1d"]) != 1 || m.collapsedOnOrAfterNew() != 0 || m.RawDaysNew["1d"] != 1 {
		t.Fatalf("old %v new %v rawNew %v; want the 09-14 collapse in the old window only", m.CollapsedOld, m.CollapsedNew, m.RawDaysNew)
	}
	var spec map[string]any
	if err := json.Unmarshal([]byte(gradingWindow2Spec(m)), &spec); err != nil {
		t.Fatalf("spec is not valid JSON: %v", err)
	}
	if spec["kind"] != GradingWindow2Kind || spec["claimsChanged"] != false {
		t.Fatalf("spec kind/claims wrong: %v / %v", spec["kind"], spec["claimsChanged"])
	}
}
