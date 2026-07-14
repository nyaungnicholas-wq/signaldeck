// SIGNALS-hub overhaul: /api/alert-outcomes endpoint tests (separate harness
// file so parallel edits never collide with alerts_test.go).
package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/config"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

func newAlertOutcomesServer(t *testing.T) (*httptest.Server, *store.Store) {
	t.Helper()
	srv, st, d := newTestServer(t, func(c *config.Config) { c.PublicReads = true })
	mux := http.NewServeMux()
	d.registerAuth(mux)
	d.registerAlerts(mux)
	srv.Config.Handler = d.secure(mux)
	return srv, st
}

type alertOutcomesResp struct {
	Kinds []store.AlertKindOutcome `json:"kinds"`
	Days  int                      `json:"days"`
	MinN  int                      `json:"minN"`
	Note  string                   `json:"note"`
}

func TestAlertOutcomesEndpoint(t *testing.T) {
	srv, st := newAlertOutcomesServer(t)
	ctx := context.Background()

	// Empty store: an empty kinds LIST (never null) + the honesty note.
	var body alertOutcomesResp
	if code := s8Get(t, srv.URL+"/api/alert-outcomes", &body); code != 200 {
		t.Fatalf("status = %d", code)
	}
	if body.Kinds == nil || len(body.Kinds) != 0 {
		t.Fatalf("empty store kinds = %#v, want []", body.Kinds)
	}
	if body.Note != alertOutcomesNote {
		t.Fatalf("note = %q, want the verbatim honesty note", body.Note)
	}
	if body.Days != 90 || body.MinN != store.MinAlertOutcomeN {
		t.Fatalf("defaults = %+v", body)
	}

	// Seed: a symbol with rising daily closes + 25 recent breakout alerts
	// (ungated) and 2 regime_change alerts (gated, n shown).
	sym, err := st.UpsertSymbol(ctx, "NVDA", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}
	sid := sym.ID
	now := time.Now().Unix()
	t0 := now - 40*86400
	var bars []md.Bar
	for i := 0; i < 40; i++ {
		c := 100 + float64(i)
		bars = append(bars, md.Bar{
			SymbolID: sid, TF: md.TF1d, Ts: t0 + int64(i)*86400,
			Open: c, High: c, Low: c, Close: c, Volume: 100,
		})
	}
	if err := st.UpsertBars(ctx, bars); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 25; i++ {
		if err := st.InsertAlert(ctx, store.Alert{
			UserID: 1, SymbolID: &sid, Kind: "breakout", Detail: "d",
			Ts: t0 + int64(i)*86400 + 3600,
		}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 2; i++ {
		if err := st.InsertAlert(ctx, store.Alert{
			UserID: 1, SymbolID: &sid, Kind: "regime_change", Detail: "d",
			Ts: t0 + int64(i)*86400 + 7200,
		}); err != nil {
			t.Fatal(err)
		}
	}

	if code := s8Get(t, srv.URL+"/api/alert-outcomes?days=60", &body); code != 200 {
		t.Fatalf("status = %d", code)
	}
	if body.Days != 60 || len(body.Kinds) != 2 {
		t.Fatalf("payload = %+v", body)
	}
	bo, rc := body.Kinds[0], body.Kinds[1] // alphabetical
	if bo.Kind != "breakout" || bo.N != 25 || bo.Gated1d ||
		bo.Mean1d == nil || bo.Median1d == nil || bo.HitRate1d == nil {
		t.Fatalf("breakout kind = %+v, want ungated stats over 25 events", bo)
	}
	if *bo.HitRate1d != 1.0 {
		t.Fatalf("hitRate1d = %v, want 1.0 (monotonic closes)", *bo.HitRate1d)
	}
	if rc.Kind != "regime_change" || rc.N != 2 || !rc.Gated1d || rc.Mean1d != nil {
		t.Fatalf("regime_change kind = %+v, want n shown + NULL stats (n<%d)", rc, store.MinAlertOutcomeN)
	}

	// The session-scoped alerts list stays auth-required even though the
	// aggregate outcomes read is public.
	resp, err := newClient(t).Get(srv.URL + "/api/alerts")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 401 {
		t.Fatalf("anonymous /api/alerts = %d, want 401", resp.StatusCode)
	}
	drain(t, resp)
}
