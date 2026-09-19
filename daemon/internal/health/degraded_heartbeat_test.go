package health

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

func TestDegradedRunIsAHeartbeat(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close() //nolint:errcheck
	ctx := context.Background()

	id, err := st.StartWorkerRun(ctx, "benched-trainer")
	if err != nil {
		t.Fatalf("StartWorkerRun: %v", err)
	}
	err = st.FinishWorkerRun(ctx, id, "degraded", "")
	if err != nil {
		t.Fatalf("FinishWorkerRun: %v", err)
	}
	startedAt := time.Now().Add(-120 * time.Second).Unix()
	if _, err := st.DB().Exec(`UPDATE worker_runs SET started_at = ? WHERE id = ?`, startedAt, id); err != nil {
		t.Fatalf("DB.Exec: %v", err)
	}

	w := &Watchdog{
		St:         st,
		Specs:      []WorkerSpec{{Name: "benched-trainer", Interval: 5 * time.Minute}},
		StatusPath: filepath.Join(dir, "health.json"),
		Notify:     func(string) error { return nil },
	}
	w.started, w.wasOK, w.inited = time.Now().Add(-3*time.Hour), true, true
	_, err = w.Run(ctx)
	if err != nil {
		t.Fatalf("Watchdog.Run: %v", err)
	}

	data, err := os.ReadFile(w.StatusPath)
	if err != nil {
		t.Fatalf("Read health.json: %v", err)
	}
	var status Status
	if err := json.Unmarshal(data, &status); err != nil {
		t.Fatalf("JSON unmarshal: %v", err)
	}
	if !status.OK {
		t.Errorf("expected OK=true, got %v", status)
	}
	if !reflect.DeepEqual(status.StaleWorkers, []string{}) {
		t.Errorf("expected StaleWorkers empty, got %v", status.StaleWorkers)
	}
	events, err := st.RecentDQ(ctx, 10)
	if err != nil {
		t.Fatalf("RecentDQ: %v", err)
	}
	for _, e := range events {
		if e.Kind == "worker_stale" {
			t.Errorf("unexpected worker_stale dq_event: %+v", e)
			break
		}
	}
}

func TestDegradedThenSilentIsStillStale(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close() //nolint:errcheck
	ctx := context.Background()

	id, err := st.StartWorkerRun(ctx, "benched-trainer")
	if err != nil {
		t.Fatalf("StartWorkerRun: %v", err)
	}
	err = st.FinishWorkerRun(ctx, id, "degraded", "")
	if err != nil {
		t.Fatalf("FinishWorkerRun: %v", err)
	}
	startedAt := time.Now().Add(-2 * time.Hour).Unix()
	if _, err := st.DB().Exec(`UPDATE worker_runs SET started_at = ? WHERE id = ?`, startedAt, id); err != nil {
		t.Fatalf("DB.Exec: %v", err)
	}

	w := &Watchdog{
		St:         st,
		Specs:      []WorkerSpec{{Name: "benched-trainer", Interval: 5 * time.Minute}},
		StatusPath: filepath.Join(dir, "health.json"),
		Notify:     func(string) error { return nil },
	}
	w.started, w.wasOK, w.inited = time.Now().Add(-3*time.Hour), true, true
	_, err = w.Run(ctx)
	if err != nil {
		t.Fatalf("Watchdog.Run: %v", err)
	}

	data, err := os.ReadFile(w.StatusPath)
	if err != nil {
		t.Fatalf("Read health.json: %v", err)
	}
	var status Status
	if err := json.Unmarshal(data, &status); err != nil {
		t.Fatalf("JSON unmarshal: %v", err)
	}
	if status.OK {
		t.Errorf("expected OK=false, got %v", status)
	}
	if !reflect.DeepEqual(status.StaleWorkers, []string{"benched-trainer"}) {
		t.Errorf("expected StaleWorkers [%q], got %v", "benched-trainer", status.StaleWorkers)
	}
}
