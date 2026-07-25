package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/postmortem"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// The /api/postmortems handler returns clustered failure modes + a recent feed.
func TestPostmortemsEndpoint(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "pm_api.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	sym, _ := st.UpsertSymbol(ctx, "AAA", md.Stocks, "")
	// two resolved misses, both unexplained (no evidence seeded)
	for _, ts := range []int64{1000, 2000} {
		if err := st.UpsertPrediction(ctx, store.Prediction{
			SymbolID: sym.ID, Horizon: md.H1d, Ts: ts, RawProb: 0.7, CalProb: 0.7, NUsed: 40, Components: "{}",
		}); err != nil {
			t.Fatalf("upsert: %v", err)
		}
		if err := st.ResolvePrediction(ctx, sym.ID, md.H1d, ts, -0.02); err != nil {
			t.Fatalf("resolve: %v", err)
		}
		misses, _ := st.UnPostmortemedMisses(ctx, md.H1d, 10)
		for _, m := range misses {
			if m.Ts == ts {
				// Disagreement 0.5 = "unknown" (no legs), matching the worker's
				// legDisagreement("{}") — so no false thin-conviction trigger.
				rep := postmortem.Classify(postmortem.Case{Prob: m.Prob, Up: m.Up, FwdReturn: m.FwdReturn, Disagreement: 0.5}, postmortem.DefaultThresholds())
				if err := st.InsertPostmortem(ctx, m, rep, 5000); err != nil {
					t.Fatalf("insert pm: %v", err)
				}
			}
		}
	}

	d := Deps{St: st, Cfg: baseCfg(), Version: "test", Started: time.Now()}
	req := httptest.NewRequest(http.MethodGet, "/api/postmortems?days=0", nil)
	rec := httptest.NewRecorder()
	d.postmortems(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		TotalMisses int `json:"totalMisses"`
		Clusters    []struct {
			Code  string `json:"code"`
			Count int    `json:"count"`
		} `json:"clusters"`
		Recent []struct {
			Symbol  string `json:"symbol"`
			Primary string `json:"primary"`
		} `json:"recent"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.TotalMisses != 2 {
		t.Fatalf("want 2 total misses, got %d", resp.TotalMisses)
	}
	if len(resp.Clusters) != 1 || resp.Clusters[0].Code != string(postmortem.ReasonUnexplained) || resp.Clusters[0].Count != 2 {
		t.Fatalf("cluster wrong: %+v", resp.Clusters)
	}
	if len(resp.Recent) != 2 || resp.Recent[0].Symbol != "AAA" {
		t.Fatalf("recent feed wrong: %+v", resp.Recent)
	}
}
