package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/researchx"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// The /api/research-graph handler returns the evidence graph: hypothesis
// nodes first (deterministic order), concept nodes for families / attacks /
// eras / evidence kinds, and weighted edges carrying mean Bayes factors.
func TestResearchGraphEndpoint(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "rgraph_api.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	now := int64(1_800_000_000)
	seedResearchLedger(t, ctx, st, now)

	d := Deps{St: st, Cfg: baseCfg(), Version: "test", Started: time.Now()}
	req := httptest.NewRequest(http.MethodGet, "/api/research-graph", nil)
	rec := httptest.NewRecorder()
	d.researchGraph(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Graph researchx.Graph `json:"graph"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// Seed shape: 2 hypotheses + 2 families + 1 attack + 1 era + 4 kinds.
	if len(resp.Graph.Nodes) != 10 {
		t.Fatalf("nodes = %d, want 10: %+v", len(resp.Graph.Nodes), resp.Graph.Nodes)
	}
	first := resp.Graph.Nodes[0]
	if first.Kind != "hypothesis" || first.ID != "H002" || first.Posterior != 0.52 {
		t.Errorf("nodes[0] = %+v, want H002 hypothesis node first", first)
	}
	byID := map[string]researchx.GraphNode{}
	for _, n := range resp.Graph.Nodes {
		byID[n.ID] = n
	}
	for id, kind := range map[string]string{
		"family:meanrev":       "family",
		"family:momentum":      "family",
		"attack:single-regime": "attack",
		"era:bear_2022":        "era",
		"kind:backtest":        "evidence_kind",
	} {
		if byID[id].Kind != kind {
			t.Errorf("node %s missing or wrong kind: %+v", id, byID[id])
		}
	}

	// One edge per (hyp, concept): 2 family + 4 kind + 1 attack + 1 era.
	if len(resp.Graph.Edges) != 8 {
		t.Fatalf("edges = %d, want 8: %+v", len(resp.Graph.Edges), resp.Graph.Edges)
	}
	byPair := map[[2]string]researchx.GraphEdge{}
	for _, e := range resp.Graph.Edges {
		byPair[[2]string{e.From, e.To}] = e
	}
	if e := byPair[[2]string{"H002", "era:bear_2022"}]; e.Weight != 1 || e.AvgBF != 1.5 {
		t.Errorf("era edge = %+v, want weight 1 avgBF 1.5", e)
	}
	if e := byPair[[2]string{"H002", "family:meanrev"}]; e.Weight != 1 || e.AvgBF != 0 {
		t.Errorf("family edge = %+v, want structural (weight 1, avgBF 0)", e)
	}
	if e := byPair[[2]string{"H008", "family:momentum"}]; e.Weight != 1 {
		t.Errorf("H008 family edge = %+v", e)
	}
	if e := byPair[[2]string{"H002", "attack:single-regime"}]; e.Weight != 1 || e.AvgBF != 0.6 {
		t.Errorf("attack edge = %+v, want weight 1 avgBF 0.6", e)
	}
}
