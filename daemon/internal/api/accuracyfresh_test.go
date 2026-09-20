// grader_fresh must describe the GRADER, not the publication decision.
//
// The defect (audit F02, 2026-09-20): every refusal path in accuracy.go
// hard-coded GraderFresh:false, and the registry's refusal marker was checked
// BEFORE GraderStale was ever called. The live API therefore served
// grader_fresh=false while the heartbeat held success=1 at
// 2026-09-19T21:10:05.873Z — well inside the 26h window. A measured scientific
// refusal read as an operational outage.
//
// These tests walk the whole matrix: {refused, publishable} x {fresh, stale,
// missing, failed}. In every refused case no rows may be published, whatever
// the heartbeat says.
package api

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// refusingRegistry is the live 2026-09-13 shape: a self-declared publication
// refusal over a collapsed window, with rows that would otherwise publish.
const refusingRegistry = `{
  "graded_at": "2026-09-13T14:42:10",
  "refused_since": "2026-09-13T14:43:41",
  "refusal_reason": "publication gate: the graded window contains 18 collapsed cross-section(s) of 82 day(s)",
  "rows": [{"predictor": "directional-ensemble (1d)", "family": "direction",
            "live_n": 999, "live_acc": 0.9, "ci": [0.88, 0.92],
            "ci_method": "day-clustered", "distinct_days": 40,
            "effective_n": 200, "null_acc": 0.53, "retire": false}]
}`

func heartbeatAt(t *testing.T, st *store.Store, when time.Time, success bool, errText string) {
	t.Helper()
	if err := st.PutGraderHeartbeat(t.Context(), store.GraderHeartbeat{
		Task: GraderTask, Success: success, Error: errText,
		FinishedAt: when.UTC().Format("2006-01-02T15:04:05.000Z"),
	}); err != nil {
		t.Fatal(err)
	}
}

func mustNoRows(t *testing.T, body map[string]any) {
	t.Helper()
	if rows, ok := body["rows"].([]any); ok && len(rows) > 0 {
		t.Fatalf("a refusal published %d row(s); it must publish none", len(rows))
	}
}

// THE REGRESSION. A healthy grader that measured a collapsed window must not be
// reported as a broken grader.
func TestAccuracy_RefusedWindowWithFreshHeartbeatReportsFresh(t *testing.T) {
	_, st, d := newTestServer(t, nil)
	d.RegistryPath = writeRegistry(t, refusingRegistry)
	srv := restartWith(t, d)
	heartbeatAt(t, st, time.Now().Add(-1*time.Hour), true, "")

	code, body := getAccuracy(t, srv.URL)
	if code != http.StatusServiceUnavailable {
		t.Fatalf("a self-declared refusal must still refuse, got %d", code)
	}
	if body["status"] != "REFUSED" {
		t.Fatalf("status = %v, want REFUSED", body["status"])
	}
	if body["grader_fresh"] != true {
		t.Fatalf("grader_fresh = %v; the heartbeat succeeded an hour ago, so the "+
			"grader IS fresh — this refusal is a finding, not an outage", body["grader_fresh"])
	}
	if v, present := body["grader_stale_reason"]; present && v != "" {
		t.Fatalf("a fresh grader must carry no stale reason, got %v", v)
	}
	if body["refused_since"] != "2026-09-13T14:43:41" {
		t.Fatalf("refused_since = %v", body["refused_since"])
	}
	mustNoRows(t, body)
}

// Both facts, together: the window is refused AND the grader stopped running.
func TestAccuracy_RefusedWindowWithStaleHeartbeatReportsBoth(t *testing.T) {
	_, st, d := newTestServer(t, nil)
	d.RegistryPath = writeRegistry(t, refusingRegistry)
	srv := restartWith(t, d)
	heartbeatAt(t, st, time.Now().Add(-40*time.Hour), true, "")

	code, body := getAccuracy(t, srv.URL)
	if code != http.StatusServiceUnavailable {
		t.Fatalf("got %d", code)
	}
	// The publication decision still owns the status and the reason.
	if body["status"] != "REFUSED" {
		t.Fatalf("the refusal marker outranks staleness; status = %v", body["status"])
	}
	reason, _ := body["reason"].(string)
	if !strings.Contains(reason, "collapsed cross-section") {
		t.Fatalf("reason lost the publication refusal: %q", reason)
	}
	if body["grader_fresh"] != false {
		t.Fatalf("grader_fresh = %v; the last success was 40h ago", body["grader_fresh"])
	}
	sr, _ := body["grader_stale_reason"].(string)
	if !strings.Contains(sr, "last successful grade was") {
		t.Fatalf("grader_stale_reason must disclose the staleness too, got %q", sr)
	}
	mustNoRows(t, body)
}

// A heartbeat that exists and says the run FAILED is a different operational
// fact from one that is merely old, and it must survive alongside the refusal.
func TestAccuracy_RefusedWindowWithFailedHeartbeatReportsBoth(t *testing.T) {
	_, st, d := newTestServer(t, nil)
	d.RegistryPath = writeRegistry(t, refusingRegistry)
	srv := restartWith(t, d)
	heartbeatAt(t, st, time.Now().Add(-1*time.Hour), false, "collapsecheck exited 2")

	_, body := getAccuracy(t, srv.URL)
	if body["grader_fresh"] != false {
		t.Fatalf("a failed run is not a fresh grade; grader_fresh = %v", body["grader_fresh"])
	}
	sr, _ := body["grader_stale_reason"].(string)
	if !strings.Contains(sr, "last grader run failed") || !strings.Contains(sr, "collapsecheck exited 2") {
		t.Fatalf("grader_stale_reason must carry the failure text, got %q", sr)
	}
	mustNoRows(t, body)
}

// No heartbeat at all, with a refused registry: still REFUSED (the marker
// outranks), still not fresh, and the missing heartbeat is disclosed.
func TestAccuracy_RefusedWindowWithNoHeartbeatReportsBoth(t *testing.T) {
	_, _, d := newTestServer(t, nil)
	d.RegistryPath = writeRegistry(t, refusingRegistry)
	srv := restartWith(t, d)

	code, body := getAccuracy(t, srv.URL)
	if code != http.StatusServiceUnavailable {
		t.Fatalf("got %d", code)
	}
	if body["status"] != "REFUSED" {
		t.Fatalf("status = %v, want REFUSED", body["status"])
	}
	if body["grader_fresh"] != false {
		t.Fatalf("grader_fresh = %v with no heartbeat at all", body["grader_fresh"])
	}
	sr, _ := body["grader_stale_reason"].(string)
	if !strings.Contains(sr, "no grader heartbeat") {
		t.Fatalf("grader_stale_reason = %q", sr)
	}
	mustNoRows(t, body)
}

// The other corner: a stale grader with a registry that is NOT refusing still
// reports REFUSED_STALE, and now also carries the reason in the dedicated field.
func TestAccuracy_StaleGraderOnPublishableRegistry(t *testing.T) {
	_, st, d := newTestServer(t, nil)
	d.RegistryPath = writeRegistry(t, thinWindowRegistry)
	srv := restartWith(t, d)
	heartbeatAt(t, st, time.Now().Add(-40*time.Hour), true, "")

	code, body := getAccuracy(t, srv.URL)
	if code != http.StatusServiceUnavailable {
		t.Fatalf("got %d", code)
	}
	if body["status"] != "REFUSED_STALE" {
		t.Fatalf("status = %v, want REFUSED_STALE", body["status"])
	}
	if body["grader_fresh"] != false {
		t.Fatalf("grader_fresh = %v", body["grader_fresh"])
	}
	sr, _ := body["grader_stale_reason"].(string)
	if !strings.Contains(sr, "last successful grade was") {
		t.Fatalf("grader_stale_reason = %q", sr)
	}
	mustNoRows(t, body)
}

// And the all-clear corner keeps working: fresh grader, publishable registry.
func TestAccuracy_FreshGraderPublishableRegistryStillPublishes(t *testing.T) {
	_, st, d := newTestServer(t, nil)
	d.RegistryPath = writeRegistry(t, thinWindowRegistry)
	srv := restartWith(t, d)
	freshHeartbeat(t, st)

	code, body := getAccuracy(t, srv.URL)
	if code != http.StatusOK {
		t.Fatalf("got %d (%v)", code, body["reason"])
	}
	if body["grader_fresh"] != true {
		t.Fatalf("grader_fresh = %v on the happy path", body["grader_fresh"])
	}
	if v, present := body["grader_stale_reason"]; present && v != "" {
		t.Fatalf("no stale reason expected, got %v", v)
	}
	if rows, _ := body["rows"].([]any); len(rows) != 1 {
		t.Fatalf("expected the row to publish, got %d", len(rows))
	}
}
