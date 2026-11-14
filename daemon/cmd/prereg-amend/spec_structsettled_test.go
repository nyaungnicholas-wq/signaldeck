package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// The settled-bar record files only while no structural call at or after the
// cut-over exists: the guard reads CallsFromEpoch, so it must count exactly
// those rows and nothing earlier. The spec must be valid JSON naming the
// registered cut-over.
func TestStructSettledMeasuresOnlyCallsFromTheCutover(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "ss.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "SS", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}
	e := store.StructSettledEpochTS
	ins := func(ts int64) {
		t.Helper()
		if _, err := st.DB().ExecContext(ctx, `INSERT INTO regime_outcomes
			(symbol_id, kind, ts, day, horizon_days, regime, conviction, historical_accuracy, rank, naive_label)
			VALUES (?, 'vol21', ?, ?, 21, 'calm', 0.5, 0, 0, 'calm')`, sym.ID, ts, ts/86400); err != nil {
			t.Fatal(err)
		}
	}
	ins(e - 86400) // before the cut-over: governed by the old rule
	m, err := measureStructSettled(ctx, st.DB())
	if err != nil || m.CallsFromEpoch != 0 {
		t.Fatalf("before any governed call: %+v err=%v, want 0", m, err)
	}
	ins(e + 3600)
	m, err = measureStructSettled(ctx, st.DB())
	if err != nil || m.CallsFromEpoch != 1 {
		t.Fatalf("after one governed call: %+v err=%v, want 1", m, err)
	}

	var spec map[string]any
	if err := json.Unmarshal([]byte(structSettledSpec(structSettledMeasured{})), &spec); err != nil {
		t.Fatalf("spec is not valid JSON: %v", err)
	}
	if spec["kind"] != StructSettledKind || spec["cutoverTs"] != float64(e) {
		t.Fatalf("spec kind/cutover = %v / %v", spec["kind"], spec["cutoverTs"])
	}
	if spec["claimsChanged"] != false || spec["storedRowsRewritten"] != false {
		t.Fatal("the record must state that no claim and no stored row changes")
	}
}
