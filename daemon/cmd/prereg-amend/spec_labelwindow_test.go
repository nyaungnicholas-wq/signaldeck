package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// The label-window record (SD-30) files only while no 1d/1w row in the new
// window has resolved: the guard reads resolvedTotal, so it must count exactly
// the resolved rows at or after the cutoff, twins included, and nothing before
// it. The spec it files must be valid JSON naming both boundaries.
func TestLabelWindowMeasuresOnlyTheNewWindow(t *testing.T) {
	if labelWindowNewEpochTS != store.GradingEpoch || labelWindowOldEpochTS != window2NewEpochTS {
		t.Fatalf("label window %d..%d must run from seq 130's boundary %d to store.GradingEpoch %d",
			labelWindowOldEpochTS, labelWindowNewEpochTS, window2NewEpochTS, store.GradingEpoch)
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "lw.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "LW", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}
	e := labelWindowNewEpochTS
	ins := func(h string, ts int64, resolved bool) {
		t.Helper()
		var up, at any
		if resolved {
			up, at = 1, ts+86400
		}
		if _, err := st.DB().ExecContext(ctx, `INSERT INTO prediction_outcomes
			(symbol_id, horizon, ts, prob, up, fwd_return, resolved_at) VALUES (?,?,?,?,?,?,?)`,
			sym.ID, h, ts, 0.6, up, nil, at); err != nil {
			t.Fatal(err)
		}
	}
	ins("1d", e-3600, true)  // before the cutoff: not this window's business
	ins("1d", e+3600, false) // pending in the window
	ins("1w", e+3600, false) // pending in the window
	ins("1h", e+3600, true)  // not a directional 1d/1w horizon
	m, err := measureLabelWindow(ctx, st.DB())
	if err != nil {
		t.Fatal(err)
	}
	if m.resolvedTotal() != 0 || m.Pending["1d"] != 1 || m.Pending["1w"] != 1 {
		t.Fatalf("clean window measured %+v; want 0 resolved, 1 pending 1d and 1w", m)
	}
	ins("1d#pm", e+7200, true) // a resolved twin in the window must block filing
	if m, err = measureLabelWindow(ctx, st.DB()); err != nil || m.resolvedTotal() != 1 || m.Resolved["1d#pm"] != 1 {
		t.Fatalf("a resolved 1d#pm twin in the window must count: %+v (err %v)", m, err)
	}

	var spec map[string]any
	if err := json.Unmarshal([]byte(labelWindowSpec(m)), &spec); err != nil {
		t.Fatalf("spec is not valid JSON: %v", err)
	}
	if spec["kind"] != LabelWindowKind || spec["newGradingEpochTs"] != float64(1791072000) ||
		spec["oldGradingEpochTs"] != float64(1790294400) || spec["claimsChanged"] != false {
		t.Fatalf("spec fields wrong: kind=%v new=%v old=%v claimsChanged=%v",
			spec["kind"], spec["newGradingEpochTs"], spec["oldGradingEpochTs"], spec["claimsChanged"])
	}
}
