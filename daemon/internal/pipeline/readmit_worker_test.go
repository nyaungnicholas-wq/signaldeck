package pipeline

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/canary"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// seedDay writes one resolved 1d prediction for sym at 14:00Z on `day` (a UTC
// day index): the call (predUp) and what happened (actualUp). Mid-session so
// md.TradingDay(ts) == day. Directions are passed separately because the
// re-admission null is the PREQUENTIAL MAJORITY: a shadow that is right on an
// all-up tape cannot beat a follower who just says up, so the post-epoch days
// must alternate direction for the record to mean anything.
func seedDay(t *testing.T, st *store.Store, sym int64, day int64, predUp, actualUp bool) {
	t.Helper()
	ctx := context.Background()
	ts := day*86400 + 14*3600
	prob := 0.1
	if predUp {
		prob = 0.9
	}
	if err := st.InsertFeatures(ctx, sym, md.H1d, ts, 1, map[string]float64{"x": 1}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertPrediction(ctx, store.Prediction{
		SymbolID: sym, Horizon: md.H1d, Ts: ts, RawProb: prob, CalProb: prob, NUsed: 3, Components: "{}",
	}); err != nil {
		t.Fatal(err)
	}
	fwd := -0.01
	if actualUp {
		fwd = 0.01
	}
	if err := st.ResolvePrediction(ctx, sym, md.H1d, ts, fwd); err != nil {
		t.Fatal(err)
	}
}

// The end-to-end shape the API-only path never had: a model whose LIFETIME
// record retires it (30 pre-epoch days, every call wrong) but whose post-epoch
// shadow clears the coded threshold (20+ days, every call right). After one
// worker pass the STORED verdict — the one ModelEmitting reads — is readmitted.
func TestModelHealthWorkerPersistsReadmission(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "readmit.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	ctx := context.Background()

	// An empty registry: no retire flag, so the composite score decides.
	reg := filepath.Join(t.TempDir(), "registry.json")
	if err := os.WriteFile(reg, []byte(`{"rows":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	sym, err := st.UpsertSymbol(ctx, "RDM", md.Stocks, "Readmit Co")
	if err != nil {
		t.Fatal(err)
	}
	epochDay := store.GradingEpoch / 86400
	for i := int64(1); i <= 30; i++ {
		seedDay(t, st, sym.ID, epochDay-i, true, false) // lifetime: called up, fell — below any null
	}
	postDays := int64(canary.ReadmitMinDistinctDays + 2)
	for i := int64(0); i < postDays; i++ {
		up := i%2 == 0
		seedDay(t, st, sym.ID, epochDay+i, up, up) // shadow: perfect on an alternating tape, past the floor
	}
	// The retirement must be genuine before re-admission can mean anything.
	if shadow, ok := DirectionalShadow(ctx, st, md.H1d); !ok || shadow.Days != int(postDays) {
		t.Fatalf("shadow = %+v ok=%v, want %d post-epoch days", shadow, ok, postDays)
	}

	w := &ModelHealthWorker{St: st, RegistryPath: reg}
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("worker: %v", err)
	}
	raw, err := st.GetMeta(ctx, MetaKeyPrefix+"directional-ensemble-1d")
	if err != nil || raw == "" {
		t.Fatalf("no stored verdict (err=%v)", err)
	}
	var v map[string]any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		t.Fatal(err)
	}
	if v["verdict"] != "readmitted" || v["emitting"] != true {
		t.Fatalf("stored verdict = %v emitting = %v, want readmitted/true (reasons=%v)", v["verdict"], v["emitting"], v["reasons"])
	}
	if v["readmission"] == nil {
		t.Fatal("readmission block not persisted")
	}
	if emitting, _ := ModelEmitting(ctx, st, "directional-ensemble-1d"); !emitting {
		t.Fatal("ModelEmitting still reads the model as withheld after re-admission was persisted")
	}
}

// A registry retire flag is the pre-registered auto-retire rule; the shadow
// record cannot lift it.
func TestModelHealthWorkerNeverReadmitsOverRegistryRetire(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "readmit2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	reg := filepath.Join(t.TempDir(), "registry.json")
	if err := os.WriteFile(reg, []byte(`{"rows":[{"predictor":"directional-ensemble (1d)","family":"direction","retire":true}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	sym, err := st.UpsertSymbol(ctx, "RDM", md.Stocks, "Readmit Co")
	if err != nil {
		t.Fatal(err)
	}
	epochDay := store.GradingEpoch / 86400
	for i := int64(0); i < int64(canary.ReadmitMinDistinctDays+2); i++ {
		up := i%2 == 0
		seedDay(t, st, sym.ID, epochDay+i, up, up)
	}
	w := &ModelHealthWorker{St: st, RegistryPath: reg}
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("worker: %v", err)
	}
	raw, _ := st.GetMeta(ctx, MetaKeyPrefix+"directional-ensemble-1d")
	var v map[string]any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		t.Fatalf("bad verdict json: %v (%q)", err, raw)
	}
	if v["verdict"] != "retired" || v["emitting"] != false {
		t.Fatalf("registry retire was overridden: verdict=%v emitting=%v", v["verdict"], v["emitting"])
	}
}
