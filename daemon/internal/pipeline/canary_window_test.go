package pipeline

import (
	"context"
	"path/filepath"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// The canary grades only the current window (owner, 2026-10-04). With no
// comparable version pair inside it, the runner drops the stored verdict rather
// than leave an earlier window's to be served as current.
func TestCanaryRunnerGradesOnlyTheWindow(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "canary.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "CNY", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}
	seed := func(day int64, version int) {
		t.Helper()
		ts := day*86400 + 14*3600
		if err := st.InsertFeatures(ctx, sym.ID, md.H1d, ts, version, map[string]float64{"x": 1}); err != nil {
			t.Fatal(err)
		}
		if err := st.UpsertPrediction(ctx, store.Prediction{SymbolID: sym.ID, Horizon: md.H1d, Ts: ts,
			RawProb: 0.9, CalProb: 0.9, NUsed: 3, Components: "{}"}); err != nil {
			t.Fatal(err)
		}
		if err := st.ResolvePrediction(ctx, sym.ID, md.H1d, ts, 0.01); err != nil {
			t.Fatal(err)
		}
	}
	epochDay := store.GradingEpoch / 86400
	for i := int64(1); i <= 5; i++ { // two versions, both before the window
		seed(epochDay-2*i, 1)
		seed(epochDay-2*i+1, 2)
	}
	if err := st.UpsertCanaryTrial(ctx, store.CanaryTrial{Model: "directional-ensemble-1d",
		Incumbent: "v1", Challenger: "v2", Decision: "promote", DecidedAt: 1}); err != nil {
		t.Fatal(err)
	}
	w := &CanaryRunner{St: st}
	if _, err := w.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if trials, err := st.CanaryTrials(ctx); err != nil || len(trials) != 0 {
		t.Fatalf("a verdict graded before the window is still stored: %+v err %v", trials, err)
	}

	for i := int64(0); i < 3; i++ { // the same pair inside the window
		seed(epochDay+2*i, 1)
		seed(epochDay+2*i+1, 2)
	}
	if _, err := w.Run(ctx); err != nil {
		t.Fatal(err)
	}
	trials, err := st.CanaryTrials(ctx)
	if err != nil || len(trials) != 1 || trials[0].IncN != 3 || trials[0].ChN != 3 {
		t.Fatalf("want one trial graded on the 3+3 in-window calls only: %+v err %v", trials, err)
	}
}
