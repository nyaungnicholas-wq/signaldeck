package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// readyDeps builds a daemon that would answer requests correctly: store open,
// no boot refusals, credentials present.
func readyDeps(t *testing.T) (Deps, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "ready.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	cfg := baseCfg()
	cfg.AlpacaKey, cfg.AlpacaSecret = "present", "present" // not real values
	return Deps{St: st, Cfg: cfg, Version: "test", Started: time.Now()}, st
}

func callReady(t *testing.T, d Deps) (int, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	// Authenticated: the REASONS these tests assert on are the operator view.
	// An anonymous probe gets the same 503 with the detail withheld, because
	// the reasons name workers, schema gaps and missing credentials, and
	// /api/ready must stay reachable without a credential. See
	// TestAnonymousReadyWithholdsTheReasons.
	req := withUser(httptest.NewRequest(http.MethodGet, "/api/ready", nil), 1)
	d.ready(rec, req)
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("ready body is not JSON: %v (%s)", err, rec.Body.String())
	}
	return rec.Code, body
}

func TestReadyWhenEverythingIsWired(t *testing.T) {
	d, _ := readyDeps(t)
	code, body := callReady(t, d)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200; reasons=%v", code, body["reasons"])
	}
	if body["ready"] != true {
		t.Errorf("ready = %v, want true", body["ready"])
	}
}

// A worker refused at boot never ran and never will during this process
// lifetime. The daemon still serves — it just serves an empty answer where that
// worker's rows should be — so readiness must fail even though health passes.
func TestNotReadyWhenWorkersWereRefusedAtBoot(t *testing.T) {
	d, st := readyDeps(t)
	refusals := `{"pressure-agent":["bars.vwap missing"]}`
	if err := st.SetMeta(context.Background(), store.SchemaContractMetaKey, refusals); err != nil {
		t.Fatalf("SetMeta: %v", err)
	}
	code, body := callReady(t, d)
	if code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 — a daemon with refused workers is not ready", code)
	}
	if body["ready"] != false {
		t.Errorf("ready = %v, want false", body["ready"])
	}
	if !strings.Contains(strings.Join(toStrings(body["reasons"]), " "), "pressure-agent") {
		t.Errorf("reasons must name the refused worker, got %v", body["reasons"])
	}
}

// Without credentials equity ingestion is inert: the API answers, the data
// never arrives. That is exactly the "builds but does not work" state readiness
// exists to catch.
func TestNotReadyWithoutProviderCredentials(t *testing.T) {
	d, _ := readyDeps(t)
	d.Cfg.AlpacaKey, d.Cfg.AlpacaSecret = "", ""
	code, body := callReady(t, d)
	if code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 without provider credentials", code)
	}
	if !strings.Contains(strings.Join(toStrings(body["reasons"]), " "), "Alpaca") {
		t.Errorf("reasons must say why, got %v", body["reasons"])
	}
}

// Readiness must be STRICTER than health: health reports process liveness and
// stays 200 in states where readiness fails. If they always agree, the split
// bought nothing.
func TestReadyIsStricterThanHealth(t *testing.T) {
	d, _ := readyDeps(t)
	d.Cfg.AlpacaKey, d.Cfg.AlpacaSecret = "", ""

	hrec := httptest.NewRecorder()
	d.health(hrec, httptest.NewRequest(http.MethodGet, "/api/health", nil))
	if hrec.Code != http.StatusOK {
		t.Fatalf("health = %d, want 200 (the process IS alive)", hrec.Code)
	}
	if code, _ := callReady(t, d); code == http.StatusOK {
		t.Fatal("ready returned 200 in a state where the daemon cannot ingest; " +
			"readiness that mirrors health is not worth having")
	}
}

func toStrings(v any) []string {
	raw, _ := v.([]any)
	out := make([]string, 0, len(raw))
	for _, x := range raw {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// The probe answer is the STATUS CODE, and it is identical either way. What an
// anonymous caller must not get is the prose: reasons name refused workers,
// schema gaps and which provider credentials are missing, and this endpoint is
// world-reachable on a tunnel-exposed daemon by design.
func TestAnonymousReadyWithholdsTheReasons(t *testing.T) {
	d, st := readyDeps(t)
	if err := st.SetMeta(context.Background(), store.SchemaContractMetaKey,
		`{"pressure-agent":["bars.vwap missing"]}`); err != nil {
		t.Fatalf("SetMeta: %v", err)
	}
	rec := httptest.NewRecorder()
	d.ready(rec, httptest.NewRequest(http.MethodGet, "/api/ready", nil)) // no identity
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 — an anonymous probe must still get the real answer", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not JSON: %v", err)
	}
	if body["ready"] != false {
		t.Errorf("ready = %v, want false", body["ready"])
	}
	if _, leaked := body["reasons"]; leaked {
		t.Errorf("reasons exposed to an anonymous caller: %v", body["reasons"])
	}
	if !strings.Contains(rec.Body.String(), "sign in") {
		t.Errorf("no pointer to how an operator gets the detail: %s", rec.Body.String())
	}
}
