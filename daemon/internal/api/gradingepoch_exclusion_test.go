package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// The 2026-09-20 window re-registration moved the graded population to
// store.GradingEpoch. Every surface that publishes a directional number must
// read the same population as /api/accuracy, or the endpoints disagree about
// which rows exist — the defect audits/prepub-2026-09-09 found on
// /api/predictions/latest and /api/composite/top while /api/accuracy refused.
// These tests seed a rich record entirely BEFORE the epoch and assert it is
// invisible.

func TestFleetEdgeSkillIgnoresRowsBeforeGradingEpoch(t *testing.T) {
	resetFleetSkillCache(t)
	_, st := newCompositeServer(t)
	epochDay := store.GradingEpoch / 86400
	// 12 dispersed days, 100 symbols each — a record that grades once it is in
	// the window (see TestFleetEdgeGrade_PublishesMeasuredClustering) — ending
	// the day before the epoch.
	seedCompositeClusteredRecordFrom(t, st, epochDay-12, 12, 100)

	proven, winRate, note := (Deps{St: st}).fleetEdgeSkill(context.Background())
	if proven || winRate != 0 {
		t.Fatalf("pre-epoch rows graded the fleet: proven=%v winRate=%.4f note=%q", proven, winRate, note)
	}
}

func TestTrackRecordIgnoresRowsBeforeGradingEpoch(t *testing.T) {
	_, st := newCompositeServer(t)
	ctx := context.Background()
	epochDay := store.GradingEpoch / 86400
	// Two full days just before the epoch, 60 symbols each.
	for i := 0; i < 60; i++ {
		sym, err := st.UpsertSymbol(ctx, "PE"+string(rune('A'+i/26))+string(rune('A'+i%26)), md.Stocks, "")
		if err != nil {
			t.Fatal(err)
		}
		for _, day := range []int64{epochDay - 2, epochDay - 1} {
			ts := day*86400 + 14*3600
			if err := st.UpsertPrediction(ctx, store.Prediction{
				SymbolID: sym.ID, Horizon: md.H1d, Ts: ts, RawProb: 0.6, CalProb: 0.6, NUsed: 2,
			}); err != nil {
				t.Fatal(err)
			}
			if err := st.ResolvePrediction(ctx, sym.ID, md.H1d, ts, 0.01); err != nil {
				t.Fatal(err)
			}
		}
	}
	rr := httptest.NewRecorder()
	(Deps{St: st}).trackRecord(rr, httptest.NewRequest("GET", "/api/track-record?horizon=1d", nil))
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("bad json: %v", err)
	}
	if dd, _ := resp["distinctDays"].(float64); dd != 0 {
		t.Fatalf("distinctDays = %v, want 0: pre-epoch days entered the track record", resp["distinctDays"])
	}
	if n, _ := resp["independentN"].(float64); n != 0 {
		t.Fatalf("independentN = %v, want 0", resp["independentN"])
	}
}
