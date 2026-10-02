package api

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/config"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// CAL-N (2026-10-02, the remainder of PR #1): /api/calibration reported
// "n": len(raw rows) with no gate. The runner re-scores a symbol many times a
// day and every row resolves against one forward move, so n must count
// distinct (symbol, day) observations, and the payload is gated on the same
// two floors as /api/track-record.

// seedSymbolDays writes perDay re-scores for each of nSyms symbols on each of
// nDays days inside the graded window; every re-score of a symbol-day resolves
// against the same move.
func seedSymbolDays(t *testing.T, st *store.Store, nSyms, nDays, perDay int) {
	t.Helper()
	ctx := context.Background()
	for s := 0; s < nSyms; s++ {
		sym, err := st.UpsertSymbol(ctx, fmt.Sprintf("IND%02d", s), md.Stocks, "Independence fixture")
		if err != nil {
			t.Fatalf("upsert symbol: %v", err)
		}
		for d := 0; d < nDays; d++ {
			for k := 0; k < perDay; k++ {
				ts := int64(store.GradingEpochTS) + int64(d)*86400 + 14*3600 + int64(k)*600
				prob := 0.3
				if (d+k+s)%2 == 0 {
					prob = 0.7
				}
				fwd := -0.01
				if d%2 == 0 {
					fwd = 0.01
				}
				if err := st.UpsertPrediction(ctx, store.Prediction{
					SymbolID:   sym.ID,
					Horizon:    md.H1d,
					Ts:         ts,
					RawProb:    prob,
					CalProb:    prob,
					NUsed:      3,
					Components: "{}",
				}); err != nil {
					t.Fatalf("upsert prediction: %v", err)
				}
				if err := st.ResolvePrediction(ctx, sym.ID, md.H1d, ts, fwd); err != nil {
					t.Fatalf("resolve prediction: %v", err)
				}
			}
		}
	}
}

func TestCalibrationNCountsIndependentSymbolDays(t *testing.T) {
	_, st, d := newTestServer(t, func(c *config.Config) {})
	seedSymbolDays(t, st, 3, 12, 5)
	body := callCalibration(t, d, "1d")
	if v := body["n"]; v != float64(36) {
		t.Fatalf("n = %v, want 36 (not 180 rows)", v)
	}
	if v := body["distinctDays"]; v != float64(12) {
		t.Fatalf("distinctDays = %v, want 12", v)
	}
	if v := body["gated"]; v != false {
		t.Fatalf("gated = %v, want false", v)
	}
	liveRecord, ok := body["liveRecord"].(map[string]any)
	if !ok {
		t.Fatalf("liveRecord not a map: %#v", body["liveRecord"])
	}
	if liveRecord["independentN"] != body["n"] {
		t.Fatalf("liveRecord.independentN = %v, want n = %v", liveRecord["independentN"], body["n"])
	}
	if _, ok := body["brier"].(float64); !ok {
		t.Fatalf("brier missing or not a float64: %#v", body["brier"])
	}
}

// 40 independent observations over 5 days clear the N floor and must still be
// gated: observations on one day share one market move.
func TestCalibrationGatesOnDistinctDays(t *testing.T) {
	_, st, d := newTestServer(t, func(c *config.Config) {})
	seedSymbolDays(t, st, 8, 5, 1)
	body := callCalibration(t, d, "1d")
	if v := body["n"]; v != float64(40) {
		t.Fatalf("n = %v, want 40", v)
	}
	if v := body["distinctDays"]; v != float64(5) {
		t.Fatalf("distinctDays = %v, want 5", v)
	}
	if v := body["gated"]; v != true {
		t.Fatalf("gated = %v, want true", v)
	}
	if v := body["minDistinctDays"]; v != float64(10) {
		t.Fatalf("minDistinctDays = %v, want 10", v)
	}
	if v := body["minIndependentN"]; v != float64(30) {
		t.Fatalf("minIndependentN = %v, want 30", v)
	}
	if body["brier"] != nil {
		t.Fatalf("brier = %v, want nil", body["brier"])
	}
	if body["reliability"] != nil {
		t.Fatalf("reliability = %v, want nil", body["reliability"])
	}
	if body["brierSkill"] != nil {
		t.Fatalf("brierSkill = %v, want nil", body["brierSkill"])
	}
	if body["baseRate"] != nil {
		t.Fatalf("baseRate = %v, want nil", body["baseRate"])
	}
	note, ok := body["brierNote"].(string)
	if !ok || !strings.Contains(note, "5/10 distinct market days") {
		t.Fatalf("brierNote = %q, want string containing '5/10 distinct market days'", note)
	}
	bins, ok := body["bins"].([]any)
	if !ok || len(bins) == 0 {
		t.Fatalf("bins = %v, want non-empty []any", body["bins"])
	}
}

func TestCalibrationGatesThinIndependentN(t *testing.T) {
	_, st, d := newTestServer(t, func(c *config.Config) {})
	seedSymbolDays(t, st, 2, 12, 3)
	body := callCalibration(t, d, "1d")
	if v := body["n"]; v != float64(24) {
		t.Fatalf("n = %v, want 24", v)
	}
	if v := body["gated"]; v != true {
		t.Fatalf("gated = %v, want true", v)
	}
	if body["brierSkill"] != nil {
		t.Fatalf("brierSkill = %v, want nil", body["brierSkill"])
	}
	note, ok := body["brierNote"].(string)
	if !ok || !strings.Contains(note, "24/30 independent resolutions") {
		t.Fatalf("brierNote = %q, want string containing '24/30 independent resolutions'", note)
	}
}
