package api

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/alerts"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// TestAlertsSmokeEndToEnd is the wave's smoke test on a TEMP db: seed a
// breakout + a high-conviction prediction for a user's watchlist symbol, run
// the REAL alert-runner once, then verify GET /api/alerts shows both and the
// mark-seen flow works — all through the real HTTP middleware.
func TestAlertsSmokeEndToEnd(t *testing.T) {
	srv, st := newAlertsServer(t)
	ctx := context.Background()
	c := newClient(t)

	// Register (session cookie lands in the jar).
	resp := postJSON(t, c, srv.URL+"/api/auth/register", map[string]string{
		"username": "smoke", "password": "hunter2secret",
	})
	var me struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal([]byte(drain(t, resp)), &me); err != nil || me.ID == 0 {
		t.Fatalf("register: %v (id=%d)", err, me.ID)
	}

	// Watchlist symbol + the two seeded events.
	sym, err := st.UpsertSymbol(ctx, "NVDA", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddUserSymbol(ctx, me.ID, sym.ID); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	sid := sym.ID
	if err := st.InsertBreakout(ctx, &sid, now.Unix()-30, "high_break", "20d high broken", 1.1); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertPrediction(ctx, store.Prediction{
		SymbolID: sid, Horizon: md.H1d, Ts: now.Unix(), RawProb: 0.8, CalProb: 0.8, NUsed: 3,
	}); err != nil {
		t.Fatal(err)
	}

	// One real alert-runner sweep (notifications stubbed out).
	runner := &alerts.Runner{St: st, Notify: func(string) error { return nil }}
	if _, err := runner.Run(ctx); err != nil {
		t.Fatalf("alert-runner: %v", err)
	}

	// GET /api/alerts shows both alerts.
	resp, err = c.Get(srv.URL + "/api/alerts?unseen=1")
	if err != nil {
		t.Fatal(err)
	}
	var rows []store.Alert
	if err := json.Unmarshal([]byte(drain(t, resp)), &rows); err != nil {
		t.Fatalf("decode: %v", err)
	}
	kinds := map[string]bool{}
	for _, a := range rows {
		kinds[a.Kind] = true
		if a.Symbol != "NVDA" {
			t.Errorf("unexpected symbol: %+v", a)
		}
	}
	if len(rows) != 2 || !kinds["breakout"] || !kinds["prediction_high"] {
		t.Fatalf("smoke alerts = %+v", rows)
	}

	// Seen flow: mark all, unseen drains, history stays.
	drain(t, postJSON(t, c, srv.URL+"/api/alerts/seen", map[string]string{}))
	resp, _ = c.Get(srv.URL + "/api/alerts?unseen=1")
	if err := json.Unmarshal([]byte(drain(t, resp)), &rows); err != nil || len(rows) != 0 {
		t.Fatalf("unseen after mark = %d (%v)", len(rows), err)
	}
	resp, _ = c.Get(srv.URL + "/api/alerts")
	if err := json.Unmarshal([]byte(drain(t, resp)), &rows); err != nil || len(rows) != 2 {
		t.Fatalf("history after mark = %d (%v)", len(rows), err)
	}
}
