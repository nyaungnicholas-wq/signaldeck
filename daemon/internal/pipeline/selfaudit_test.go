package pipeline

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/adaptive"
	"github.com/nyaungnicholas-wq/signaldeck/internal/ensemble"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// latestAudit indexes the newest finding per metric.
func latestAudit(t *testing.T, st *store.Store) map[string]store.SelfAuditRow {
	t.Helper()
	rows, err := st.LatestSelfAudit(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]store.SelfAuditRow{}
	for _, r := range rows {
		m[r.Metric] = r
	}
	return m
}

// TestSelfAuditorGatesAndDrift drives every check to a definite status: a
// measured horizon that DEGRADES vs a seeded prior, a thin horizon that is
// INSUFFICIENT, a factor whose IC SIGN FLIPPED, and thin factors that gate.
func TestSelfAuditorGatesAndDrift(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)

	// 35 independent resolved 1d predictions (distinct symbols): prob 0.55, 20
	// up / 15 down — clears n>=30, bias small ("ok"), reliability ~0.49.
	predTs := time.Now().Add(-10 * 24 * time.Hour).Unix()
	for i := 0; i < 35; i++ {
		sym, err := st.UpsertSymbol(ctx, fmt.Sprintf("SYM%d", i), md.Stocks, "")
		if err != nil {
			t.Fatal(err)
		}
		if err := st.UpsertPrediction(ctx, store.Prediction{
			SymbolID: sym.ID, Horizon: md.H1d, Ts: predTs, RawProb: 0.55, CalProb: 0.55, NUsed: 3,
		}); err != nil {
			t.Fatal(err)
		}
		fwd := -0.01
		if i < 20 {
			fwd = 0.01
		}
		if err := st.ResolvePrediction(ctx, sym.ID, md.H1d, predTs, fwd); err != nil {
			t.Fatal(err)
		}
	}

	// Seed a LOW prior reliability so the current ~0.49 registers as degrading.
	if err := st.InsertSelfAudit(ctx, store.SelfAuditRow{
		Ts: predTs, Metric: "calibration:1d", Value: 0.10, Status: "ok", Detail: "prior",
	}); err != nil {
		t.Fatal(err)
	}
	// Seed a prior factor IC of the OPPOSITE sign to force a sign flip.
	if err := st.InsertSelfAudit(ctx, store.SelfAuditRow{
		Ts: predTs, Metric: "factor_ic:" + ensemble.LegPressure, Value: -0.2, Status: "ok", Detail: "prior",
	}); err != nil {
		t.Fatal(err)
	}
	// Adaptive attribution meta: pressure leg graded (n=50, IC +0.1 → flip vs the
	// -0.2 prior); the other legs stay thin → insufficient.
	if err := st.SetJSON(ctx, adaptive.MetaKey, adaptive.Weights{
		ComputedTs: predTs,
		Cells: map[string]adaptive.Cell{
			adaptive.AllCell: {
				N:    120,
				IC:   map[string]float64{ensemble.LegPressure: 0.1},
				LegN: map[string]int{ensemble.LegPressure: 50},
			},
		},
	}); err != nil {
		t.Fatal(err)
	}

	msg, err := (&SelfAuditor{St: st}).Run(ctx)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if msg == "" {
		t.Fatal("empty run detail")
	}

	got := latestAudit(t, st)
	if f := got["calibration:1d"]; f.Status != "degrading" {
		t.Errorf("calibration:1d status = %q, want degrading (%s)", f.Status, f.Detail)
	}
	if f := got["prediction_bias:1d"]; f.Status != "ok" {
		t.Errorf("prediction_bias:1d status = %q, want ok (%s)", f.Status, f.Detail)
	}
	if f := got["calibration:1w"]; f.Status != "insufficient" {
		t.Errorf("calibration:1w status = %q, want insufficient", f.Status)
	}
	if f := got["prediction_bias:1w"]; f.Status != "insufficient" {
		t.Errorf("prediction_bias:1w status = %q, want insufficient", f.Status)
	}
	if f := got["factor_ic:"+ensemble.LegPressure]; f.Status != "sign_flip" {
		t.Errorf("factor_ic:pressure status = %q, want sign_flip (%s)", f.Status, f.Detail)
	}
	// A thin leg gates as insufficient (any other ensemble leg has no samples).
	other := ensemble.LegForecast
	if f := got["factor_ic:"+other]; f.Status != "insufficient" {
		t.Errorf("factor_ic:%s status = %q, want insufficient", other, f.Status)
	}

	// One summary insight (kind self_audit) was written.
	ins, err := st.InsightsByKind(ctx, "self_audit", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(ins) != 1 {
		t.Fatalf("self_audit insights = %d, want 1", len(ins))
	}

	// Once/UTC-day gate: a second run is a no-op (no new rows).
	before := len(latestAuditAll(t, st))
	if _, err := (&SelfAuditor{St: st}).Run(ctx); err != nil {
		t.Fatal(err)
	}
	if after := len(latestAuditAll(t, st)); after != before {
		t.Errorf("second same-day run wrote rows (%d → %d) — once/day gate failed", before, after)
	}
}

// latestAuditAll returns the raw count of self_audit rows (not deduped) to catch
// a broken once/day gate that appends a second run's rows.
func latestAuditAll(t *testing.T, st *store.Store) []int64 {
	t.Helper()
	var ids []int64
	rows, err := st.DB().QueryContext(context.Background(), `SELECT id FROM self_audit`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close() //nolint:errcheck
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	return ids
}
