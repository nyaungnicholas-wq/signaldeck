// End-to-end tests for GET /api/accuracy.
//
// The scenario that matters is the 2026-08-03 one, reproduced exactly: the
// registry's window has contracted to 9 distinct days, one short of the
// 10-block floor, so it publishes no interval and carries retire=false — while
// evidence_claims still holds the model as refuted. Before this route existed
// the two surfaces simply disagreed and nothing reconciled them.
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// thinWindowRegistry is the live 2026-08-03 shape: real accuracy, real null,
// no interval, retire=false, 9 distinct days.
const thinWindowRegistry = `{
  "graded_at": "2026-08-03T14:05:07",
  "refused_since": null,
  "grader_sha256": "6908c6f9446ab44000e4a1fd3f8e6c325f4300e22d8bf7949deba3732ad8c28f",
  "min_independent_n": 30,
  "min_distinct_blocks": 10,
  "rows": [
    {"predictor": "directional-ensemble (1d)", "family": "direction", "band": "all",
     "live_n": 2257, "live_acc": 0.4626, "ci": null, "ci_method": "withheld",
     "distinct_days": 9, "effective_n": null, "null_acc": 0.5284,
     "skill": -0.0658, "retire": false, "note": "live forward record"}
  ]
}`

// restartWith serves ONLY the accuracy route, from a Deps the caller has
// already pointed at a temp registry. newTestServer builds its own server
// before the caller can set RegistryPath, so the route has to be mounted here
// rather than there — and mounting just this one keeps the test's surface equal
// to the thing under test.
func restartWith(t *testing.T, d Deps) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(nil)
	// The host allowlist is captured by the middleware at construction, so the
	// listener address has to be known before d.secure runs — same ordering
	// newTestServer uses.
	d.Cfg.AllowedHosts = []string{srv.Listener.Addr().String()}
	mux := http.NewServeMux()
	d.registerAccuracy(mux)
	srv.Config.Handler = d.secure(mux)
	srv.Start()
	t.Cleanup(srv.Close)
	return srv
}

func writeRegistry(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "accuracy_registry.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// freshHeartbeat makes the grader look healthy so the tests exercise the ROW
// logic rather than stopping at the staleness gate.
func freshHeartbeat(t *testing.T, st *store.Store) {
	t.Helper()
	if err := st.PutGraderHeartbeat(t.Context(), store.GraderHeartbeat{
		Task: GraderTask, Success: true,
		FinishedAt: time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
	}); err != nil {
		t.Fatal(err)
	}
}

func getAccuracy(t *testing.T, url string) (int, map[string]any) {
	t.Helper()
	resp, err := newClient(t).Get(url + "/api/accuracy")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return resp.StatusCode, body
}

// THE 2026-08-03 DEFECT, end to end.
func TestAccuracy_RefutedModelReadsRetiredNotInsufficient(t *testing.T) {
	_, st, d := newTestServer(t, nil)
	d.RegistryPath = writeRegistry(t, thinWindowRegistry)
	srv := restartWith(t, d)
	freshHeartbeat(t, st)

	if err := st.PutEvidenceClaim(t.Context(), store.EvidenceClaimRow{
		ID: "directional-ensemble-1d", Text: "REFUTED on the live record",
		ScopeJSON: `{"horizons":["1d"]}`, Tier: "refuted", Status: "refuted",
	}); err != nil {
		t.Fatal(err)
	}

	code, body := getAccuracy(t, srv.URL)
	if code != http.StatusOK {
		t.Fatalf("expected 200 with a fresh grader, got %d (%v)", code, body["reason"])
	}
	rows, _ := body["rows"].([]any)
	if len(rows) != 1 {
		t.Fatalf("expected one row, got %d", len(rows))
	}
	row, _ := rows[0].(map[string]any)

	if got := row["publication_status"]; got != "RETIRED" {
		t.Fatalf("a refuted model with a thin window must publish RETIRED, got %v", got)
	}
	if row["retired"] != true || row["retirement_sticky"] != true {
		t.Fatalf("retired=%v sticky=%v; both must be true", row["retired"], row["retirement_sticky"])
	}
	if refs, _ := row["evidence_refs"].([]any); len(refs) == 0 {
		t.Fatal("the verdict must cite the claim that condemned it")
	}
}

// Without the evidence claim the same thin row is merely INSUFFICIENT — which
// confirms the RETIRED above comes from the reconciliation, not from the floors.
func TestAccuracy_ThinWindowAloneIsInsufficient(t *testing.T) {
	_, st, d := newTestServer(t, nil)
	d.RegistryPath = writeRegistry(t, thinWindowRegistry)
	srv := restartWith(t, d)
	freshHeartbeat(t, st)

	code, body := getAccuracy(t, srv.URL)
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	rows, _ := body["rows"].([]any)
	row, _ := rows[0].(map[string]any)
	if got := row["publication_status"]; got != "INSUFFICIENT" {
		t.Fatalf("a thin uncondemned row must publish INSUFFICIENT, got %v", got)
	}
	if reasons, _ := row["reasons"].([]any); len(reasons) == 0 {
		t.Fatal("an INSUFFICIENT row must name the floor it failed")
	}
}

// Fail-closed: no heartbeat at all means the grade cannot be shown to be
// current, so nothing publishes.
func TestAccuracy_RefusesWithoutAFreshGrader(t *testing.T) {
	_, _, d := newTestServer(t, nil)
	d.RegistryPath = writeRegistry(t, thinWindowRegistry)
	srv := restartWith(t, d)

	code, body := getAccuracy(t, srv.URL)
	if code != http.StatusServiceUnavailable {
		t.Fatalf("a missing heartbeat must refuse with 503, got %d", code)
	}
	if body["status"] != "REFUSED_STALE" {
		t.Fatalf("expected REFUSED_STALE, got %v", body["status"])
	}
	if body["reason"] == nil || body["reason"] == "" {
		t.Fatal("a refusal must carry its reason")
	}
	if rows, ok := body["rows"].([]any); ok && len(rows) > 0 {
		t.Fatal("a refusal must publish no rows")
	}
}

// A registry that marks itself refusing outranks everything in its own rows.
func TestAccuracy_RegistryRefusalOutranksRows(t *testing.T) {
	_, st, d := newTestServer(t, nil)
	d.RegistryPath = writeRegistry(t, `{
      "graded_at": "2026-08-01T14:05:07",
      "refused_since": "2026-08-02T06:15:00",
      "rows": [{"predictor": "directional-ensemble (1d)", "family": "direction",
                "live_n": 999, "live_acc": 0.9, "ci": [0.88, 0.92],
                "ci_method": "day-clustered", "distinct_days": 40,
                "effective_n": 200, "null_acc": 0.53, "retire": false}]
    }`)
	srv := restartWith(t, d)
	freshHeartbeat(t, st)

	code, body := getAccuracy(t, srv.URL)
	if code != http.StatusServiceUnavailable {
		t.Fatalf("a self-declared refusal must refuse, got %d", code)
	}
	if rows, ok := body["rows"].([]any); ok && len(rows) > 0 {
		t.Fatal("a refused registry must not publish its rows, however flattering")
	}
}

// An unreadable registry refuses rather than reporting zero rows, which would
// render as a clean page with nothing wrong.
func TestAccuracy_UnreadableRegistryRefuses(t *testing.T) {
	_, st, d := newTestServer(t, nil)
	d.RegistryPath = filepath.Join(t.TempDir(), "does-not-exist.json")
	srv := restartWith(t, d)
	freshHeartbeat(t, st)

	code, body := getAccuracy(t, srv.URL)
	if code != http.StatusServiceUnavailable {
		t.Fatalf("an unreadable registry must refuse, got %d", code)
	}
	if body["status"] != "REFUSED" {
		t.Fatalf("expected REFUSED, got %v", body["status"])
	}
}
