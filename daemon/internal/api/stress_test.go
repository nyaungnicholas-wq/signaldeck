package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// newStressServer wires the stress routes behind the real middleware (own
// harness — same pattern as newAlertsServer, so parallel edits to api_test.go
// never collide with this file).
func newStressServer(t *testing.T) (*httptest.Server, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "stress_api.db"))
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
	d.registerStress(mux)
	srv.Config.Handler = d.secure(mux)
	srv.Start()
	return srv, st
}

func TestStressScenariosCatalog(t *testing.T) {
	srv, _ := newStressServer(t)
	c := newClient(t)

	resp, err := c.Get(srv.URL + "/api/stress/scenarios")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	var body struct {
		Scenarios []struct {
			Name    string `json:"name"`
			Effects struct {
				PriceShock float64 `json:"priceShock"`
				SpreadMult float64 `json:"spreadMult"`
			} `json:"effects"`
		} `json:"scenarios"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	_ = resp.Body.Close()
	if len(body.Scenarios) != 8 {
		t.Fatalf("catalog has %d scenarios, want 8", len(body.Scenarios))
	}
	names := map[string]bool{}
	for _, s := range body.Scenarios {
		names[s.Name] = true
		if s.Name == "flash_crash" && (s.Effects.PriceShock != -0.08 || s.Effects.SpreadMult != 3) {
			t.Fatalf("flash_crash effects wrong: %+v", s.Effects)
		}
	}
	for _, want := range []string{"flash_crash", "liquidity_drought", "vol_spike", "spread_widening",
		"gap_open", "delayed_feed", "missing_candles", "exchange_outage"} {
		if !names[want] {
			t.Fatalf("catalog missing %q", want)
		}
	}
}

func TestStressRunAuthAndLimits(t *testing.T) {
	srv, _ := newStressServer(t)
	c := newClient(t)

	// Anonymous -> 401 (it's compute).
	resp := postJSON(t, c, srv.URL+"/api/stress/run", map[string]any{
		"scenarios": []string{"flash_crash"}, "symbols": []string{"AAPL"}, "market": "stocks",
	})
	if resp.StatusCode != 401 {
		t.Fatalf("anonymous run: %d want 401 (%s)", resp.StatusCode, drain(t, resp))
	}
	drain(t, resp)

	// Register a session.
	resp = postJSON(t, c, srv.URL+"/api/auth/register", map[string]string{
		"username": "stress", "password": "hunter2secret",
	})
	if resp.StatusCode != 200 {
		t.Fatalf("register: %d (%s)", resp.StatusCode, drain(t, resp))
	}
	drain(t, resp)

	// Unknown scenario -> 400.
	resp = postJSON(t, c, srv.URL+"/api/stress/run", map[string]any{
		"scenarios": []string{"volcano"}, "symbols": []string{"AAPL"}, "market": "stocks",
	})
	if resp.StatusCode != 400 {
		t.Fatalf("unknown scenario: %d want 400 (%s)", resp.StatusCode, drain(t, resp))
	}
	drain(t, resp)

	// Symbol fan-out over the cap -> 400.
	resp = postJSON(t, c, srv.URL+"/api/stress/run", map[string]any{
		"scenarios": []string{"flash_crash"},
		"symbols":   []string{"A", "B", "C", "D", "E", "F"}, "market": "stocks",
	})
	if resp.StatusCode != 400 {
		t.Fatalf("too many symbols: %d want 400", resp.StatusCode)
	}
	drain(t, resp)

	// Window over the cap -> 400.
	resp = postJSON(t, c, srv.URL+"/api/stress/run", map[string]any{
		"scenarios": []string{"flash_crash"}, "symbols": []string{"AAPL"},
		"market": "stocks", "windowBars": 10_000,
	})
	if resp.StatusCode != 400 {
		t.Fatalf("oversized window: %d want 400", resp.StatusCode)
	}
	drain(t, resp)

	// Bootstrap paths over the cap -> 400.
	resp = postJSON(t, c, srv.URL+"/api/stress/run", map[string]any{
		"scenarios": []string{"flash_crash"}, "symbols": []string{"AAPL"},
		"market": "stocks", "bootstrapPaths": 500,
	})
	if resp.StatusCode != 400 {
		t.Fatalf("too many bootstrap paths: %d want 400", resp.StatusCode)
	}
	drain(t, resp)

	// Untracked symbol -> 200 with an honest skip, not a fabricated replay.
	resp = postJSON(t, c, srv.URL+"/api/stress/run", map[string]any{
		"scenarios": []string{"flash_crash"}, "symbols": []string{"ZZZZ"}, "market": "stocks", "seed": 1,
	})
	if resp.StatusCode != 200 {
		t.Fatalf("run: %d (%s)", resp.StatusCode, drain(t, resp))
	}
	var out struct {
		Results []struct {
			Symbol  string `json:"symbol"`
			Skipped string `json:"skipped"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	_ = resp.Body.Close()
	if len(out.Results) != 1 || out.Results[0].Skipped == "" {
		t.Fatalf("untracked symbol should be skipped with a reason: %+v", out.Results)
	}
}

func TestStressRunConcurrencyCap(t *testing.T) {
	srv, _ := newStressServer(t)
	c := newClient(t)
	resp := postJSON(t, c, srv.URL+"/api/auth/register", map[string]string{
		"username": "capper", "password": "hunter2secret",
	})
	drain(t, resp)

	// Fill the semaphore, then any run must 429 immediately (the A11
	// pattern: refuse, never queue CPU-bound work).
	for i := 0; i < stressRunConcurrency; i++ {
		stressRunSem <- struct{}{}
	}
	defer func() {
		for i := 0; i < stressRunConcurrency; i++ {
			<-stressRunSem
		}
	}()

	resp = postJSON(t, c, srv.URL+"/api/stress/run", map[string]any{
		"scenarios": []string{"flash_crash"}, "symbols": []string{"AAPL"}, "market": "stocks",
	})
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("saturated stress lab: %d want 429 (%s)", resp.StatusCode, drain(t, resp))
	}
	drain(t, resp)
}
