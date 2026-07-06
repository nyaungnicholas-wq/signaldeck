package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// TestTrackRecord_DayClusterGate: hundreds of symbols resolving on ONE market
// day are one market move, not hundreds of independent tests — the record must
// stay GATED until >= trackMinDistinctDays distinct days exist, no matter how
// many symbols resolved (regression for the 995-obs-over-3-days ungating).
func TestTrackRecord_DayClusterGate(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "daygate.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	// 60 symbols all resolving on the SAME 2 days: indepN=120 >= 30, days=2 < 10.
	day1 := time.Date(2026, 7, 3, 14, 0, 0, 0, time.UTC).Unix()
	day2 := day1 + 86400
	for i := 0; i < 60; i++ {
		sym, err := st.UpsertSymbol(ctx, "S"+time.Unix(int64(i)+1e6, 0).Format("040506"), md.Stocks, "")
		if err != nil {
			t.Fatal(err)
		}
		for _, ts := range []int64{day1, day2} {
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

	d := Deps{St: st}
	rr := httptest.NewRecorder()
	d.trackRecord(rr, httptest.NewRequest("GET", "/api/track-record?horizon=1d", nil))
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("bad json: %v", err)
	}
	if g, _ := resp["gated"].(bool); !g {
		t.Fatalf("record must stay gated at 2 distinct days (indepN=%v, days=%v)",
			resp["independentN"], resp["distinctDays"])
	}
	if resp["winRate"] != nil || resp["ic"] != nil {
		t.Fatal("skill numbers must be withheld while day-gated")
	}
	if dd, _ := resp["distinctDays"].(float64); dd != 2 {
		t.Fatalf("distinctDays = %v, want 2", resp["distinctDays"])
	}
}
