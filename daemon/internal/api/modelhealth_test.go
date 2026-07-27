// Model-health tests, focused on the UNGRADED explanation.
//
// The endpoint used to answer every ungraded model with one sentence — "not yet
// graded — the health worker runs hourly" — which is only true in one of three
// genuinely different situations, and is actively misleading in the common one:
// a predictor that has emitted thousands of calls whose horizons simply have not
// elapsed. That reads as a stalled scheduler when it is the passage of time, and
// it is the difference between "something is broken" and "come back in August".
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
	"github.com/nyaungnicholas-wq/signaldeck/internal/structregime"
)

func newModelHealthServer(t *testing.T) (*httptest.Server, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "modelhealth.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	d := Deps{St: st, Cfg: baseCfg(), Version: "test", Started: time.Now()}
	srv := httptest.NewUnstartedServer(nil)
	t.Cleanup(srv.Close)
	d.Cfg.AllowedHosts = []string{srv.Listener.Addr().String()}
	mux := http.NewServeMux()
	d.registerModelHealth(mux)
	srv.Config.Handler = d.secure(mux)
	srv.Start()
	return srv, st
}

type healthModel struct {
	Model              string `json:"model"`
	Graded             bool   `json:"graded"`
	Note               string `json:"note"`
	Pending            int    `json:"pending"`
	AwaitingResolution *bool  `json:"awaitingResolution"`
}

func fetchModels(t *testing.T, srv *httptest.Server) map[string]healthModel {
	t.Helper()
	resp, err := http.Get(srv.URL + "/api/model-health")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body struct {
		Models []healthModel `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	out := map[string]healthModel{}
	for _, m := range body.Models {
		out[m.Model] = m
	}
	return out
}

// No forecasts at all: the honest answer is that nothing has been emitted, not
// that a worker is behind.
func TestModelHealthUngradedWithNoForecasts(t *testing.T) {
	srv, _ := newModelHealthServer(t)
	models := fetchModels(t, srv)

	m, ok := models["structural-trend"]
	if !ok {
		for k := range models {
			if strings.HasPrefix(k, "structural-") {
				m = models[k]
				ok = true
				break
			}
		}
	}
	if !ok {
		t.Fatal("no structural model in the payload")
	}
	if m.Graded {
		t.Fatal("model reports graded on an empty database")
	}
	if !strings.Contains(m.Note, "has not emitted") {
		t.Errorf("note = %q, want it to say nothing has been emitted", m.Note)
	}
	if strings.Contains(m.Note, "runs hourly") {
		t.Error("note blames the hourly worker when no forecast exists at all")
	}
}

// Forecasts outstanding whose horizons have NOT elapsed: the note must give the
// date and say plainly that this is time passing, not a stalled worker.
func TestModelHealthUngradedWhileHorizonsAreOpen(t *testing.T) {
	srv, st := newModelHealthServer(t)
	ctx := t.Context()
	sym, err := st.UpsertSymbol(ctx, "AAA", md.Stocks, "")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	kind := store.StructuralKinds()[0]
	// Called today, 21 sessions to run: nothing can be graded yet.
	if _, err := st.InsertRegimeOutcome(ctx, store.RegimeCall{
		SymbolID: sym.ID, Kind: structregime.Kind(kind), Ts: time.Now().Unix(),
		HorizonDays: 21, Regime: "up", Conviction: 0.8, HistoricalAccuracy: 0.7,
		NaiveLabel: "up",
	}); err != nil {
		t.Fatalf("insert outcome: %v", err)
	}

	m := fetchModels(t, srv)["structural-"+kind]
	if m.Graded {
		t.Fatal("graded on an unresolved forecast")
	}
	if m.Pending != 1 {
		t.Errorf("pending = %d, want 1", m.Pending)
	}
	if m.AwaitingResolution == nil || !*m.AwaitingResolution {
		t.Error("awaitingResolution not set — an open horizon is not an overdue one")
	}
	if !strings.Contains(m.Note, "passage of time") {
		t.Errorf("note = %q, want it to distinguish waiting from a stalled worker", m.Note)
	}
	// The date is the actionable part: "come back on X" beats "runs hourly".
	due := time.Now().AddDate(0, 0, 21).UTC().Format("2006-01-02")
	if !strings.Contains(m.Note, due) {
		t.Errorf("note = %q, want the due date %s", m.Note, due)
	}
}

// Horizons elapsed and still no verdict: THIS is the case where the worker is
// the explanation, and it must read as overdue rather than pending.
func TestModelHealthUngradedWhenResolutionIsOverdue(t *testing.T) {
	srv, st := newModelHealthServer(t)
	ctx := t.Context()
	sym, err := st.UpsertSymbol(ctx, "AAA", md.Stocks, "")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	kind := store.StructuralKinds()[0]
	if _, err := st.InsertRegimeOutcome(ctx, store.RegimeCall{
		SymbolID: sym.ID, Kind: structregime.Kind(kind),
		Ts:          time.Now().AddDate(0, 0, -60).Unix(),
		HorizonDays: 21, Regime: "up", Conviction: 0.8, HistoricalAccuracy: 0.7,
	}); err != nil {
		t.Fatalf("insert outcome: %v", err)
	}

	m := fetchModels(t, srv)["structural-"+kind]
	if m.AwaitingResolution == nil || *m.AwaitingResolution {
		t.Error("an elapsed horizon must NOT read as awaiting resolution")
	}
	if !strings.Contains(m.Note, "overdue") {
		t.Errorf("note = %q, want it to call an elapsed horizon overdue", m.Note)
	}
}
