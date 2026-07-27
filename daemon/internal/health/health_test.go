package health

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/notify"
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
			// fast succeeded AFTER boot then went silent past the 30m floor →
			// genuine stall, flags. hourly's lastOK (4h ago) PRE-DATES boot
			// (2h ago) so it gets boot grace: reference clamps to boot, and
			// 2h since boot < its 3h threshold → not stale (yet).
			name: "genuine stall flags; pre-boot lastOK gets boot grace",
			lastOK: map[string]time.Time{
				"fast":      now.Add(-31 * time.Minute),
				"hourly":    now.Add(-4 * time.Hour),
				"never-ran": now,
			},
			want: []string{"fast"},
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

// TestStaleWorkersSleepGrace is the wake-from-sleep regression (observed
// 2026-07-06: daemon uptime 298s after a Mac wake, all 35 workers flagged
// stale + macOS ping while running fine). A lastOK that pre-dates boot must
// be clamped to boot, so the fleet only pages once workers have had a full
// threshold window since boot to succeed — while a worker that HAS succeeded
// since boot and then stalled still flags.
func TestStaleWorkersSleepGrace(t *testing.T) {
	now := time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)
	specs := []WorkerSpec{{Name: "tenmin", Interval: 10 * time.Minute}} // threshold = 30m floor
	cases := []struct {
		name   string
		boot   time.Time
		lastOK map[string]time.Time
		want   []string
	}{
		{
			// The verified false alarm: last success 2 days ago (pre-sleep),
			// daemon booted/woke 5 minutes ago → NOT stale (5m < 30m floor).
			name:   "wake-from-sleep: 2-day-old lastOK, boot 5m ago → grace",
			boot:   now.Add(-5 * time.Minute),
			lastOK: map[string]time.Time{"tenmin": now.Add(-48 * time.Hour)},
			want:   nil,
		},
		{
			// Grace expires: booted 2h ago and still no success since boot →
			// stale (2h since boot > 30m threshold).
			name:   "boot 2h ago, no success since boot → stale",
			boot:   now.Add(-2 * time.Hour),
			lastOK: map[string]time.Time{"tenmin": now.Add(-48 * time.Hour)},
			want:   []string{"tenmin"},
		},
		{
			// Genuine stall is NOT masked: succeeded after boot, then silent
			// for > 3x interval (and past the 30m floor) → stale.
			name:   "lastOK after boot but 40m old → genuine stall flags",
			boot:   now.Add(-3 * time.Hour),
			lastOK: map[string]time.Time{"tenmin": now.Add(-40 * time.Minute)},
			want:   []string{"tenmin"},
		},
		{
			// Healthy after boot: succeeded 5m ago → not stale.
			name:   "lastOK after boot and fresh → healthy",
			boot:   now.Add(-3 * time.Hour),
			lastOK: map[string]time.Time{"tenmin": now.Add(-5 * time.Minute)},
			want:   nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := StaleWorkers(specs, tc.lastOK, tc.boot, now)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
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

// TestWatchdogRemoteNotifyOnTransition (Stage 3): the healthy→unhealthy
// transition must fan out EXACTLY ONE remote message (same 6h cooldown as the
// local notification), and a second unhealthy run must deliver nothing more.
func TestWatchdogRemoteNotifyOnTransition(t *testing.T) {
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

	var mu sync.Mutex
	var msgs []notify.Message
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var m notify.Message
		_ = json.NewDecoder(r.Body).Decode(&m)
		mu.Lock()
		msgs = append(msgs, m)
		mu.Unlock()
	}))
	defer srv.Close()

	w := &Watchdog{
		St:         st,
		Specs:      []WorkerSpec{{Name: "slowpoke", Interval: 5 * time.Minute}},
		StatusPath: filepath.Join(dir, "health.json"),
		Notify:     func(string) error { return nil },
		Remote:     &notify.Notifier{WebhookURL: srv.URL},
	}
	w.started, w.wasOK, w.inited = time.Now().Add(-3*time.Hour), true, true

	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	mu.Lock()
	got := append([]notify.Message{}, msgs...)
	mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("remote messages = %d, want 1 (transition only)", len(got))
	}
	if got[0].Kind != "watchdog" || !strings.Contains(got[0].Body, "slowpoke") {
		t.Errorf("watchdog message = %+v, want kind=watchdog naming the stale worker", got[0])
	}

	// Second unhealthy run: no transition + cooldown → nothing delivered.
	if _, err := w.Run(ctx); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	n := len(msgs)
	mu.Unlock()
	if n != 1 {
		t.Errorf("remote fired again without a transition: %d messages", n)
	}
}

// fakeActuator records which workers the watchdog tried to recover.
type fakeActuator struct {
	overdue  map[string]bool
	attempts []string
}

func (a *fakeActuator) CancelOverdue(name string) (time.Duration, bool) {
	a.attempts = append(a.attempts, name)
	if a.overdue[name] {
		return 42 * time.Minute, true
	}
	return 0, false
}

// TestWatchdogActuatesOnStale: the watchdog must now ACT on a stale worker
// whose run has blown its deadline — cancel it and record the intervention as
// its own dq kind — not merely write health.json. Detection alone produced 662
// worker_stale events over 7 days and zero recoveries.
func TestWatchdogActuatesOnStale(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close() //nolint:errcheck
	ctx := context.Background()

	act := &fakeActuator{overdue: map[string]bool{"hung": true}}
	w := &Watchdog{
		St: st,
		// "hung" is stale and overdue; "healthy-ish" is stale but its run is
		// within budget, so it must be reported and NOT cancelled.
		Specs:      []WorkerSpec{{Name: "hung", Interval: 5 * time.Minute}, {Name: "patient", Interval: 5 * time.Minute}},
		StatusPath: filepath.Join(dir, "health.json"),
		Notify:     func(string) error { return nil },
		Runner:     act,
	}
	w.started, w.wasOK, w.inited = time.Now().Add(-3*time.Hour), true, true

	detail, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(detail, "cancelled overdue runs: [hung]") {
		t.Errorf("detail %q does not report the intervention", detail)
	}
	if len(act.attempts) != 2 {
		t.Errorf("actuator consulted for %v, want both stale workers", act.attempts)
	}

	var n int
	if err := st.DB().QueryRow(
		`SELECT COUNT(*) FROM dq_events WHERE kind='worker_run_cancelled' AND detail LIKE 'worker=hung%'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("worker_run_cancelled dq events = %d, want exactly 1 (and none for the patient worker)", n)
	}
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM dq_events WHERE kind='worker_stale'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("worker_stale dq events = %d, want 2 — the staleness rule must be unchanged", n)
	}
}
