// Package health is the daemon's watchdog: it inspects worker_runs for every
// registered periodic worker, flags workers whose last successful run is
// suspiciously old, records dq_events, writes a machine-readable
// data/health.json for outside tooling (launchd, cron, dashboards), and fires
// a macOS notification when the fleet transitions to unhealthy.
package health

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/notify"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// minThreshold is the floor on staleness: fast workers (1m cadence) shouldn't
// alarm on a brief hiccup.
const minThreshold = 30 * time.Minute

// notifyCooldown limits macOS notifications to at most one per 6h.
const notifyCooldown = 6 * time.Hour

// WorkerSpec describes one registered worker for staleness checks.
type WorkerSpec struct {
	Name     string
	Interval time.Duration
}

// StaleWorkers is the pure staleness rule: a worker is stale when its last
// successful run is older than 3x its interval (floored at 30 minutes).
// Long-running workers (Interval <= 0, e.g. stream ingestors) are skipped —
// their runs block for days by design.
//
// Boot/wake grace: any lastOK that PRE-DATES `fallback` (typically daemon boot
// time) belongs to a previous daemon life — right after a restart or a Mac
// wake-from-sleep EVERY worker's last success is older than boot, which used
// to flag the whole fleet as stale while it was running fine. The reference
// time is therefore max(lastOK, fallback): a worker only goes stale once it
// has had at least one full threshold window (>= its interval, min 30m floor)
// SINCE BOOT to succeed. Workers missing from lastOK entirely use `fallback`
// the same way. A lastOK AFTER boot is judged as-is, so a genuine stall
// (succeeded since boot, then silent for > 3x interval) still flags.
// The result is sorted by name.
func StaleWorkers(specs []WorkerSpec, lastOK map[string]time.Time, fallback, now time.Time) []string {
	var stale []string
	for _, s := range specs {
		if s.Interval <= 0 {
			continue
		}
		threshold := 3 * s.Interval
		if threshold < minThreshold {
			threshold = minThreshold
		}
		last, ok := lastOK[s.Name]
		if !ok || last.Before(fallback) {
			last = fallback // boot/wake grace: pre-boot successes don't count against the worker
		}
		if now.Sub(last) > threshold {
			stale = append(stale, s.Name)
		}
	}
	sort.Strings(stale)
	return stale
}

// Status is the shape of data/health.json.
type Status struct {
	OK           bool     `json:"ok"`
	StaleWorkers []string `json:"staleWorkers"`
	Ts           int64    `json:"ts"`
}

// Actuator is the half of the watchdog that can actually DO something about a
// stale worker. *workers.Runner implements it. Kept as an interface here so
// health does not import workers (workers already owns run bookkeeping) and so
// tests can observe interventions.
type Actuator interface {
	// CancelOverdue cancels the named worker's in-flight run IF that run is
	// already past its own deadline, reporting how long it had been running.
	CancelOverdue(name string) (time.Duration, bool)
}

// Watchdog is the periodic health worker (implements workers.Worker).
type Watchdog struct {
	St         *store.Store
	Specs      []WorkerSpec
	StatusPath string // where health.json goes (required)
	// Runner, when set, lets the watchdog INTERVENE instead of only reporting:
	// a worker that is both stale AND sitting in a run past its deadline gets
	// that run cancelled, so the next tick can start clean. Nil = observe-only
	// (the pre-2026-07-27 behaviour). The staleness rule itself is unchanged —
	// this only changes what happens after a worker is judged stale.
	Runner Actuator
	// Notify shows a user-facing alert; nil = osascript display notification.
	Notify func(msg string) error
	// Remote fans the unhealthy-transition message out to the env-configured
	// remote transports (Discord/Telegram/webhook — internal/notify); nil or
	// unconfigured = macOS-only. Fires under the SAME 6h cooldown +
	// healthy→unhealthy transition gate as the local notification, and its
	// failures degrade to dq events (never the watchdog run).
	Remote *notify.Notifier

	started    time.Time
	lastNotify time.Time
	wasOK      bool
	inited     bool
}

// Name implements workers.Worker.
func (w *Watchdog) Name() string { return "watchdog" }

// Interval implements workers.Worker.
func (w *Watchdog) Interval() time.Duration { return 10 * time.Minute }

// Run performs one health sweep.
func (w *Watchdog) Run(ctx context.Context) (string, error) {
	now := time.Now()
	if !w.inited {
		w.started, w.wasOK, w.inited = now, true, true
	}

	lastOK, err := w.lastSuccess(ctx)
	if err != nil {
		return "", fmt.Errorf("query worker_runs: %w", err)
	}
	stale := StaleWorkers(w.Specs, lastOK, w.started, now)
	ok := len(stale) == 0

	if err := w.writeStatus(Status{OK: ok, StaleWorkers: append([]string{}, stale...), Ts: now.Unix()}); err != nil {
		slog.Warn("watchdog: write health.json", "err", err)
	}

	var recovered []string
	for _, name := range stale {
		if err := w.recordDQ(ctx, name, lastOK[name], now); err != nil {
			slog.Warn("watchdog: record dq", "worker", name, "err", err)
		}
		// ACTUATE. Detection without a recovery path is why 662 worker_stale
		// events over 7 days produced zero recoveries: the daemon knew, and
		// waited for a human restart while the prediction record grew holes.
		if w.Runner == nil {
			continue
		}
		if ran, ok := w.Runner.CancelOverdue(name); ok {
			recovered = append(recovered, name)
			slog.Warn("watchdog: cancelled overdue run", "worker", name, "running", ran)
			if err := w.St.InsertDQ(ctx, md.DQEvent{
				Ts:   now.Unix(),
				Kind: "worker_run_cancelled",
				Detail: fmt.Sprintf("worker=%s stale AND its run was %s past start with the deadline blown — run cancelled by the watchdog so the next tick can start clean",
					name, ran.Round(time.Second)),
			}); err != nil {
				slog.Warn("watchdog: record intervention dq", "worker", name, "err", err)
			}
		}
	}

	// Notify only on the healthy→unhealthy transition, at most once per 6h.
	if !ok && w.wasOK && now.Sub(w.lastNotify) > notifyCooldown {
		w.lastNotify = now
		local := w.Notify
		if local == nil {
			local = osascriptNotify
		}
		if err := local(fmt.Sprintf("SignalDeck: %d stale worker(s): %v", len(stale), stale)); err != nil {
			slog.Warn("watchdog: notification failed", "err", err) // never fatal
		}
		// Stage 3: same transition + 6h cooldown, delivered beyond the Mac.
		// Send degrades to dq events internally and never errors.
		if w.Remote != nil {
			w.Remote.Send(ctx, notify.Message{
				Title: "SignalDeck watchdog: fleet unhealthy",
				Body:  fmt.Sprintf("%d stale worker(s): %v", len(stale), stale),
				Kind:  "watchdog",
				Ts:    now.Unix(),
			})
		}
	}
	w.wasOK = ok

	if ok {
		return fmt.Sprintf("healthy: %d workers checked", len(w.Specs)), nil
	}
	if len(recovered) > 0 {
		return fmt.Sprintf("UNHEALTHY: stale %v (cancelled overdue runs: %v)", stale, recovered), nil
	}
	return fmt.Sprintf("UNHEALTHY: stale %v", stale), nil
}

// lastSuccess maps worker → time of most recent successful run (read pool).
func (w *Watchdog) lastSuccess(ctx context.Context) (map[string]time.Time, error) {
	rows, err := w.St.DB().QueryContext(ctx,
		`SELECT worker, MAX(started_at) FROM worker_runs WHERE status='ok' GROUP BY worker`)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	out := map[string]time.Time{}
	for rows.Next() {
		var name string
		var ts int64
		if err := rows.Scan(&name, &ts); err != nil {
			return nil, err
		}
		out[name] = time.Unix(ts, 0)
	}
	return out, rows.Err()
}

// recordDQ inserts a "worker_stale" dq_event unless one for the same worker
// already exists within the last hour (dedup — the watchdog runs every 10m).
func (w *Watchdog) recordDQ(ctx context.Context, worker string, last time.Time, now time.Time) error {
	prefix := "worker=" + worker + " "
	var n int
	err := w.St.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM dq_events WHERE kind='worker_stale' AND ts > ? AND detail LIKE ?`,
		now.Add(-time.Hour).Unix(), prefix+"%").Scan(&n)
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	lastStr := "never"
	if !last.IsZero() {
		lastStr = last.Format(time.RFC3339)
	}
	return w.St.InsertDQ(ctx, md.DQEvent{
		Ts:     now.Unix(),
		Kind:   "worker_stale",
		Detail: fmt.Sprintf("%slast successful run %s", prefix, lastStr),
	})
}

// writeStatus atomically replaces health.json (write temp + rename), so
// pollers never read a torn file.
func (w *Watchdog) writeStatus(s Status) error {
	if w.StatusPath == "" {
		return fmt.Errorf("status path not configured")
	}
	if err := os.MkdirAll(filepath.Dir(w.StatusPath), 0o755); err != nil {
		return err
	}
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	tmp := w.StatusPath + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, w.StatusPath)
}

// osascriptNotify pops a macOS notification. Best-effort: any failure (no
// osascript, headless session) is returned for logging but never fatal.
func osascriptNotify(msg string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	script := fmt.Sprintf("display notification %q with title %q", msg, "SignalDeck watchdog")
	return exec.CommandContext(ctx, "osascript", "-e", script).Run()
}
