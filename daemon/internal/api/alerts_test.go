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
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// newAlertsServer wires the alerts routes behind the REAL middleware (own
// harness so this file never collides with parallel edits to api_test.go).
func newAlertsServer(t *testing.T) (*httptest.Server, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "alerts_api.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	d := Deps{St: st, Cfg: baseCfg(), Version: "test", Started: time.Now()}
	srv := httptest.NewUnstartedServer(nil)
	t.Cleanup(srv.Close)
	d.Cfg.AllowedHosts = []string{srv.Listener.Addr().String()}

	mux := http.NewServeMux()
	d.registerAuth(mux)
	d.registerAlerts(mux)
	srv.Config.Handler = d.secure(mux)
	srv.Start()
	return srv, st
}

func TestAlertsEndpoints(t *testing.T) {
	srv, st := newAlertsServer(t)
	ctx := context.Background()
	c := newClient(t)

	// Anonymous: alerts are session-scoped even with PublicReads=true.
	resp, err := c.Get(srv.URL + "/api/alerts")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if resp.StatusCode != 401 {
		t.Fatalf("anonymous alerts: %d want 401 (%s)", resp.StatusCode, drain(t, resp))
	}
	drain(t, resp)

	// Register (sets the session cookie in the jar).
	resp = postJSON(t, c, srv.URL+"/api/auth/register", map[string]string{
		"username": "alice", "password": "hunter2secret",
	})
	var me struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal([]byte(drain(t, resp)), &me); err != nil || me.ID == 0 {
		t.Fatalf("register: %v (id=%d)", err, me.ID)
	}

	// Seed two alerts for alice via the store.
	sym, err := st.UpsertSymbol(ctx, "NVDA", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}
	sid := sym.ID
	for _, kind := range []string{"breakout", "prediction_high"} {
		if err := st.InsertAlert(ctx, store.Alert{
			UserID: me.ID, SymbolID: &sid, Kind: kind, Detail: "test " + kind, Ts: time.Now().Unix(),
		}); err != nil {
			t.Fatal(err)
		}
	}

	// List: both alerts, unseen.
	resp, err = c.Get(srv.URL + "/api/alerts?unseen=1")
	if err != nil {
		t.Fatalf("get alerts: %v", err)
	}
	var rows []store.Alert
	if err := json.Unmarshal([]byte(drain(t, resp)), &rows); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(rows) != 2 || rows[0].Symbol != "NVDA" || rows[0].Seen {
		t.Fatalf("alerts = %+v", rows)
	}

	// Mark-seen without the CSRF header → 403.
	req, _ := http.NewRequest("POST", srv.URL+"/api/alerts/seen", nil)
	resp, err = c.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	if resp.StatusCode != 403 {
		t.Fatalf("no-CSRF mark-seen: %d want 403", resp.StatusCode)
	}
	drain(t, resp)

	// Mark-seen with the header → ok, marked 2; unseen list drains.
	resp = postJSON(t, c, srv.URL+"/api/alerts/seen", map[string]string{})
	var seen struct {
		OK     bool  `json:"ok"`
		Marked int64 `json:"marked"`
	}
	if err := json.Unmarshal([]byte(drain(t, resp)), &seen); err != nil || !seen.OK || seen.Marked != 2 {
		t.Fatalf("mark-seen: %+v (%v)", seen, err)
	}
	resp, _ = c.Get(srv.URL + "/api/alerts?unseen=1")
	if err := json.Unmarshal([]byte(drain(t, resp)), &rows); err != nil || len(rows) != 0 {
		t.Fatalf("unseen after mark = %d (%v)", len(rows), err)
	}
	// Full list still has both, seen=true.
	resp, _ = c.Get(srv.URL + "/api/alerts")
	if err := json.Unmarshal([]byte(drain(t, resp)), &rows); err != nil || len(rows) != 2 || !rows[0].Seen {
		t.Fatalf("full list after mark = %+v (%v)", rows, err)
	}
}

func TestAlertsAreUserScoped(t *testing.T) {
	srv, st := newAlertsServer(t)
	ctx := context.Background()

	// alice owns an alert; bob must not see it.
	alice := newClient(t)
	resp := postJSON(t, alice, srv.URL+"/api/auth/register", map[string]string{
		"username": "alice", "password": "hunter2secret",
	})
	var me struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal([]byte(drain(t, resp)), &me)
	if err := st.InsertAlert(ctx, store.Alert{
		UserID: me.ID, Kind: "breakout", Detail: "alice-only", Ts: time.Now().Unix(),
	}); err != nil {
		t.Fatal(err)
	}

	bob := newClient(t)
	drain(t, postJSON(t, bob, srv.URL+"/api/auth/register", map[string]string{
		"username": "bob", "password": "hunter2secret",
	}))
	resp, err := bob.Get(srv.URL + "/api/alerts")
	if err != nil {
		t.Fatal(err)
	}
	var rows []store.Alert
	if err := json.Unmarshal([]byte(drain(t, resp)), &rows); err != nil || len(rows) != 0 {
		t.Fatalf("bob sees alice's alerts: %+v (%v)", rows, err)
	}
}
