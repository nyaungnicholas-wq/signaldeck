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

// newCongressServer wires only the Stage-2 congress route behind the real
// middleware (separate harness so parallel edits never collide).
func newCongressServer(t *testing.T) (*httptest.Server, *store.Store) {
	t.Helper()
	srv, st, d := newTestServer(t, func(c *config.Config) { c.PublicReads = true })
	mux := http.NewServeMux()
	d.registerCongress(mux)
	srv.Config.Handler = d.secure(mux)
	return srv, st
}

func congressGet(t *testing.T, url string, out any) int {
	t.Helper()
	res, err := newClient(t).Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer res.Body.Close() //nolint:errcheck
	if res.StatusCode == 200 {
		if err := json.NewDecoder(res.Body).Decode(out); err != nil {
			t.Fatalf("decode %s: %v", url, err)
		}
	}
	return res.StatusCode
}

func TestCongressEndpoint(t *testing.T) {
	srv, st := newCongressServer(t)
	ctx := context.Background()
	now := time.Now().Unix()

	sid := int64(11)
	seed := []store.CongressTradeRow{
		{ID: "c1", Chamber: "senate", Member: "Jane Q Senator", Symbol: "AAPL", SymbolID: &sid,
			TxType: "purchase", AmountRange: "$15,001 - $50,000",
			TxTs: now - 10*86400, DisclosedTs: now - 86400}, // inside the 90d chip window
		{ID: "c2", Chamber: "house", Member: "Hon. Alexis Example", Symbol: "AAPL",
			TxType: "sale_full", AmountRange: "$1,001 - $15,000",
			TxTs: now - 200*86400, DisclosedTs: now - 160*86400}, // outside 90d
		{ID: "c3", Chamber: "house", Member: "Hon. Morgan Sample", Symbol: "ZZTOP",
			TxType: "sale_partial", AmountRange: "$1,001 - $15,000",
			TxTs: now - 5*86400, DisclosedTs: now - 86400},
	}
	for _, r := range seed {
		if _, err := st.InsertCongressTrade(ctx, r); err != nil {
			t.Fatalf("seed %s: %v", r.ID, err)
		}
	}

	type resp struct {
		Trades    []store.CongressTradeRow `json:"trades"`
		Count     int                      `json:"count"`
		Recent90d *int64                   `json:"recent90d"`
		LagNote   string                   `json:"lagNote"`
		Note      string                   `json:"note"`
		Source    map[string]any           `json:"source"`
	}

	// Fleet-wide: all three, newest tx first; the EXPLICIT lag note present.
	var body resp
	if code := congressGet(t, srv.URL+"/api/congress", &body); code != 200 {
		t.Fatalf("status = %d", code)
	}
	if body.Count != 3 || body.Trades[0].ID != "c3" {
		t.Fatalf("payload = %+v", body)
	}
	if body.LagNote == "" || body.Note == "" {
		t.Error("missing lagNote/note — the honest lag labeling is REQUIRED")
	}
	if body.Recent90d != nil {
		t.Error("recent90d must only appear on symbol queries")
	}

	// Symbol filter + the 90d chip fields (only c1 is inside the window).
	if code := congressGet(t, srv.URL+"/api/congress?symbol=aapl", &body); code != 200 {
		t.Fatalf("status = %d", code)
	}
	if body.Count != 2 || body.Recent90d == nil || *body.Recent90d != 1 {
		t.Fatalf("symbol payload = %+v (recent90d=%v)", body, body.Recent90d)
	}
	// Untracked ticker still queryable — congress data isn't limited to our universe.
	if code := congressGet(t, srv.URL+"/api/congress?symbol=ZZTOP", &body); code != 200 || body.Count != 1 {
		t.Fatalf("untracked ticker: status=%d body=%+v", code, body)
	}
	if body.Trades[0].SymbolID != nil {
		t.Errorf("untracked ticker must keep null symbolId: %+v", body.Trades[0])
	}

	// Member substring + chamber filters.
	if code := congressGet(t, srv.URL+"/api/congress?member=morgan", &body); code != 200 || body.Count != 1 {
		t.Fatalf("member filter: status=%d body=%+v", code, body)
	}
	if code := congressGet(t, srv.URL+"/api/congress?chamber=house", &body); code != 200 || body.Count != 2 {
		t.Fatalf("chamber filter: status=%d body=%+v", code, body)
	}
	if code := congressGet(t, srv.URL+"/api/congress?chamber=bogus", &body); code != 400 {
		t.Fatalf("bogus chamber status = %d, want 400", code)
	}

	// Mirror status meta (when present) is surfaced as `source`.
	if err := st.SetJSON(ctx, "congress_mirror_status", map[string]any{
		"checkedTs": now,
		"senate":    map[string]any{"ok": false, "detail": "status 403"},
		"house":     map[string]any{"ok": false, "detail": "status 403"},
	}); err != nil {
		t.Fatalf("seed status: %v", err)
	}
	if code := congressGet(t, srv.URL+"/api/congress", &body); code != 200 {
		t.Fatalf("status = %d", code)
	}
	if body.Source == nil {
		t.Fatal("source status missing")
	}
	if sen, _ := body.Source["senate"].(map[string]any); sen == nil || sen["ok"] != false {
		t.Errorf("source.senate = %+v (must honestly report the outage)", body.Source["senate"])
	}
}
