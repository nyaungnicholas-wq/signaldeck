package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// The /api/research handler returns status counts + the hypothesis list and
// surfaces the advisory feedback signal when present.
func TestResearchEndpoint(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "research_api.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	if err := st.InsertShadowHypothesis(ctx, store.HypothesisRow{
		ID: "h1", Kind: "ablation", Spec: `{"drop":"noise"}`, Description: "drop noise",
		Status: "shadow", DiscoveredAt: 1000, DiscLift: 0.04, LastLift: 0.04, LastN: 300,
		PassStreak: 1, Evals: 1, UpdatedAt: 1000,
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := st.SetMeta(ctx, "research_feedback:v1", `{"promotedTonight":0,"note":"advisory only"}`); err != nil {
		t.Fatalf("meta: %v", err)
	}

	d := Deps{St: st, Cfg: baseCfg(), Version: "test", Started: time.Now()}
	req := httptest.NewRequest(http.MethodGet, "/api/research", nil)
	rec := httptest.NewRecorder()
	d.research(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Counts     map[string]int `json:"counts"`
		Hypotheses []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"hypotheses"`
		Feedback map[string]any `json:"feedback"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Counts["shadow"] != 1 {
		t.Fatalf("want 1 shadow, got %+v", resp.Counts)
	}
	if len(resp.Hypotheses) != 1 || resp.Hypotheses[0].ID != "h1" {
		t.Fatalf("hypothesis list wrong: %+v", resp.Hypotheses)
	}
	if resp.Feedback == nil {
		t.Fatal("feedback signal not surfaced")
	}
}
