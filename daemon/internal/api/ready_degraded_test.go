// Degraded is a health fact, not a readiness failure. A worker that ran and
// chose not to deliver (a benched trainer, an expected abstention) is doing
// its job; only workers that failed to deliver at all should keep /api/ready
// from answering 200. These tests pin that distinction so the ready handler
// keeps treating "degraded" as informational and "error" as a hard stop.

package api

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestReadyIgnoresDegradedWorkers(t *testing.T) {
	ctx := context.Background()
	d, st := readyDeps(t)
	id, err := st.StartWorkerRun(ctx, "gbm-trainer")
	if err != nil {
		t.Fatalf("start worker run: %v", err)
	}
	if err := st.FinishWorkerRun(ctx, id, "degraded", "no model leg cleared its OOS edge bar"); err != nil {
		t.Fatalf("finish worker run: %v", err)
	}
	code, body := callReady(t, d)
	if code != http.StatusOK {
		t.Fatalf("want 200, got %d: %v", code, body)
	}
	if ready, _ := body["ready"].(bool); !ready {
		t.Fatalf("want ready=true, got %v: %v", body["ready"], body)
	}
	var found bool
	for _, name := range toStrings(body["degraded"]) {
		if name == "gbm-trainer" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("want gbm-trainer in degraded list, got %v", body["degraded"])
	}
}

func TestReadyStillFailsOnErroredWorkers(t *testing.T) {
	ctx := context.Background()
	d, st := readyDeps(t)
	id, err := st.StartWorkerRun(ctx, "outcome-resolver")
	if err != nil {
		t.Fatalf("start worker run: %v", err)
	}
	if err := st.FinishWorkerRun(ctx, id, "error", "boom"); err != nil {
		t.Fatalf("finish worker run: %v", err)
	}
	code, body := callReady(t, d)
	if code != http.StatusServiceUnavailable {
		t.Fatalf("want 503, got %d: %v", code, body)
	}
	want := "worker not delivering (error): outcome-resolver"
	var matched bool
	for _, reason := range toStrings(body["reasons"]) {
		if strings.Contains(reason, want) {
			matched = true
			break
		}
	}
	if !matched {
		t.Fatalf("want reasons to contain %q, got %v", want, body["reasons"])
	}
}