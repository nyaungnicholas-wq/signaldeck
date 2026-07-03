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

func TestStaleWorkers(t *testing.T) {
	now := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	boot := now.Add(-2 * time.Hour)
	specs := []WorkerSpec{
		{Name: "fast", Interval: time.Minute},        // threshold floors at 30m
		{Name: "hourly", Interval: time.Hour},        // threshold 3h
		{Name: "stream", Interval: 0},                // skipped
		{Name: "never-ran", Interval: 5 * time.Minute}, // falls back to boot
	}
	cases := []struct {
		name   string
		lastOK map[string]time.Time
		want   []string
	}{
		{
			name: "all healthy",
			lastOK: map[string]time.Time{
				"fast":      now.Add(-5 * time.Minute),
				"hourly":    now.Add(-2 * time.Hour),
				"never-ran": now.Add(-10 * time.Minute),
			},
			want: nil,
		},
		{
			name: "fast within 30m floor despite 20x interval",
			lastOK: map[string]time.Time{
				"fast":      now.Add(-20 * time.Minute),
				"hourly":    now.Add(-time.Hour),
				"never-ran": now,
			},
			want: nil,
		},
		{
			name: "fast stale past floor, hourly stale past 3x",
			lastOK: map[string]time.Time{
				"fast":      now.Add(-31 * time.Minute),
				"hourly":    now.Add(-4 * time.Hour),
				"never-ran": now,
			},
			want: []string{"fast", "hourly"},
		},
		{
			name: "missing worker uses boot fallback (2h ago > 30m floor)",
			lastOK: map[string]time.Time{
				"fast":   now.Add(-time.Minute),
				"hourly": now.Add(-time.Hour),
			},
			want: []string{"never-ran"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := StaleWorkers(specs, tc.lastOK, boot, now)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestStaleWorkersFreshBootNotStale(t *testing.T) {
	now := time.Now()
	boot := now.Add(-2 * time.Minute) // just booted; nothing succeeded yet
	specs := []WorkerSpec{{Name: "w", Interval: time.Minute}}
	if got := StaleWorkers(specs, nil, boot, now); len(got) != 0 {
		t.Errorf("fresh boot flagged stale: %v", got)
	}
}

// TestWatchdogEndToEnd runs the worker against a throwaway store: a stale
// worker must produce health.json ok=false, exactly one dq_event (dedup on
// the second run), and one notification on the transition.
func TestWatchdogEndToEnd(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close() //nolint:errcheck
	ctx := context.Background()

	// One successful run 2h ago for a 5m-interval worker → stale (floor 30m).
	id, err := st.StartWorkerRun(ctx, "slowpoke")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.FinishWorkerRun(ctx, id, "ok", "old"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().Exec(`UPDATE worker_runs SET started_at = ? WHERE id = ?`,
		time.Now().Add(-2*time.Hour).Unix(), id); err != nil {
		t.Fatal(err)
	}

	notified := 0
	w := &Watchdog{
		St:         st,
		Specs:      []WorkerSpec{{Name: "slowpoke", Interval: 5 * time.Minute}},
		StatusPath: filepath.Join(dir, "health.json"),
		Notify:     func(string) error { notified++; return nil },
	}
	// Force a boot time old enough that fallback doesn't mask staleness.
	w.started, w.wasOK, w.inited = time.Now().Add(-3*time.Hour), true, true

	detail, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if detail == "" || notified != 1 {
		t.Errorf("detail=%q notified=%d, want unhealthy detail and 1 notification", detail, notified)
	}

	b, err := os.ReadFile(w.StatusPath)
	if err != nil {
		t.Fatal(err)
	}
	var status Status
	if err := json.Unmarshal(b, &status); err != nil {
		t.Fatal(err)
	}
	if status.OK || !reflect.DeepEqual(status.StaleWorkers, []string{"slowpoke"}) {
		t.Errorf("health.json = %+v", status)
	}

	// Second run within the hour: dq dedup, no second notification.
	if _, err := w.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if notified != 1 {
		t.Errorf("notified %d times, want 1 (cooldown + no transition)", notified)
	}
	events, err := st.RecentDQ(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	staleEvents := 0
	for _, ev := range events {
		if ev.Kind == "worker_stale" {
			staleEvents++
		}
	}
	if staleEvents != 1 {
		t.Errorf("dq worker_stale events = %d, want 1 (deduped)", staleEvents)
	}
}
