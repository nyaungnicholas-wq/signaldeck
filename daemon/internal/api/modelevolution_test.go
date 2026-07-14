package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/config"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

func newModelEvoServer(t *testing.T) (*httptest.Server, *store.Store) {
	t.Helper()
	srv, st, d := newTestServer(t, func(c *config.Config) { c.PublicReads = true })
	mux := http.NewServeMux()
	d.registerModelEvolution(mux)
	srv.Config.Handler = d.secure(mux)
	return srv, st
}

type modelEvoResp struct {
	Note        string                 `json:"note"`
	Days        int                    `json:"days"`
	Weights     []store.WeightSeries   `json:"weights"`
	FactorSkill []store.FactorICSeries `json:"factorSkill"`
}

// TestModelEvolutionEndpoint: weight_history groups into per-(regime,leg) series
// (ascending), the self_audit factor-IC trend excludes insufficient points, and
// points outside the window are dropped.
func TestModelEvolutionEndpoint(t *testing.T) {
	srv, st := newModelEvoServer(t)
	ctx := context.Background()
	now := time.Now().Unix()

	// Two weight snapshots for (all, pressure) inside the window + one stale one.
	stale := now - 400*24*3600
	if err := st.InsertWeightHistory(ctx, []store.WeightHistoryRow{
		{Ts: stale, Regime: "all", Leg: "pressure", Weight: 0.9}, // outside 30d
		{Ts: now - 2*86400, Regime: "all", Leg: "pressure", Weight: 0.4},
		{Ts: now - 1*86400, Regime: "all", Leg: "pressure", Weight: 0.5},
		{Ts: now - 1*86400, Regime: "all", Leg: "forecast", Weight: 0.5},
	}); err != nil {
		t.Fatal(err)
	}
	// Factor-IC trend: one measured, one insufficient (must be dropped).
	if err := st.InsertSelfAudit(ctx, store.SelfAuditRow{Ts: now - 2*86400, Metric: "factor_ic:pressure", Value: 0.11, Status: "ok"}); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertSelfAudit(ctx, store.SelfAuditRow{Ts: now - 1*86400, Metric: "factor_ic:pressure", Value: 0, Status: "insufficient"}); err != nil {
		t.Fatal(err)
	}

	res, err := newClient(t).Get(srv.URL + "/api/model-evolution?days=30")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer res.Body.Close() //nolint:errcheck
	if res.StatusCode != 200 {
		t.Fatalf("status = %d, want 200 (public read)", res.StatusCode)
	}
	var body modelEvoResp
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}

	var pressure *store.WeightSeries
	for i := range body.Weights {
		if body.Weights[i].Regime == "all" && body.Weights[i].Leg == "pressure" {
			pressure = &body.Weights[i]
		}
	}
	if pressure == nil {
		t.Fatalf("missing (all,pressure) weight series: %+v", body.Weights)
	}
	// Stale point excluded → exactly the two in-window points, ascending.
	if len(pressure.Points) != 2 || pressure.Points[0].Weight != 0.4 || pressure.Points[1].Weight != 0.5 {
		t.Fatalf("weight series points = %+v, want [0.4, 0.5]", pressure.Points)
	}
	if len(body.FactorSkill) != 1 || body.FactorSkill[0].Leg != "pressure" ||
		len(body.FactorSkill[0].Points) != 1 || body.FactorSkill[0].Points[0].IC != 0.11 {
		t.Fatalf("factor-IC trend must drop the insufficient point: %+v", body.FactorSkill)
	}
	if body.Note == "" || body.Days != 30 {
		t.Fatalf("note/days: %+v", body)
	}
}
