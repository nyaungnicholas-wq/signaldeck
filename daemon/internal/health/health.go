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
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/envcfg"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/notify"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
	"github.com/nyaungnicholas-wq/signaldeck/internal/workers"
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

// MinConsecutiveFailures is how many failed runs in a row make a worker
// FAILING. Three, so a single bad tick or a transient upstream blip is not an
// alarm, but a worker that is simply broken cannot hide behind its own cadence.
const MinConsecutiveFailures = 3

// FailingWorkers is the second pure rule, and it exists because StaleWorkers
// alone cannot see the failure mode that actually happened.
//
// Measured 2026-08-11: forecast-monitor had status='error' on 15 of 15 recorded
// runs — it had NEVER once succeeded — and appeared in neither health surface.
// Staleness could not catch it because staleness asks "has it run lately", and
// this worker ran punctually every 24h. It failed punctually too. Worse, the
// boot grace in StaleWorkers substitutes daemon boot for a worker that has never
// succeeded, and 3 x 24h = 72h against a MEASURED mean daemon uptime of 9.67h
// (18 of 19 recorded boots were shorter than 72h) means the grace clock resets
// before the threshold can ever be reached. The alarm was unreachable by
// construction.
//
// So: run ON TIME and FAIL EVERY TIME, and both surfaces called it healthy. The
// four days of false RAW MODEL COLLAPSE that this daemon dutifully recorded, and
// nobody saw, is what that costs.
//
// statusesNewestFirst maps worker → its recent run statuses, newest first.
// Only "error" counts as a failure:
//   - "degraded" is a worker honestly reporting it had nothing to deliver
//     (expectancy-trainer and gbm-trainer do this by design, and degraded
//     already suppresses lastSuccess, so staleness reports them);
//   - "orphaned" is the boot sweep marking a run the process did not outlive,
//     which is the daemon's fault and not the worker's;
//   - "running" is in flight and carries no verdict yet, so it is SKIPPED
//     rather than treated as a success — otherwise an in-flight run would reset
//     the streak of a worker that is failing underneath it.
//
// The result is sorted by name.
func FailingWorkers(statusesNewestFirst map[string][]string, longRunning map[string]bool) []string {
	var failing []string
	for name, statuses := range statusesNewestFirst {
		// A LONG-RUNNING worker (Interval <= 0: the stream ingestors) that is
		// currently in flight is connected and streaming. Its single Run blocks
		// until the daemon exits, so it produces NO completed rows that could
		// ever break an older error streak.
		//
		// Without this, skipping the "running" row and counting the errors
		// beneath it made the verdict unclearable. Measured live 2026-08-13:
		// crypto-live errored every 60s from 17:59 while tickstream was down,
		// reconnected at 18:03:50, and was still reported failing at 21:08 —
		// over three hours of healthy streaming — which also held health.json's
		// `ok` false for the whole period, since ok requires len(failing)==0.
		// A status that cannot clear is a status nobody can act on.
		//
		// api.failingFromRuns already fixed this for its own surface, using run
		// DURATIONS to tell recovery from a retry. This function only receives
		// statuses, so it uses the discriminator it does have — the declared
		// interval — which is the same one StaleWorkers already skips on.
		if longRunning[name] && len(statuses) > 0 && statuses[0] == "running" {
			continue
		}
		streak := 0
		for _, s := range statuses {
			if s == "running" {
				continue // no verdict yet — neither breaks nor extends the streak
			}
			if s != "error" {
				break
			}
			streak++
			if streak >= MinConsecutiveFailures {
				break
			}
		}
		if streak >= MinConsecutiveFailures {
			failing = append(failing, name)
		}
	}
	sort.Strings(failing)
	return failing
}

// Status is the shape of data/health.json.
type Status struct {
	OK           bool     `json:"ok"`
	StaleWorkers []string `json:"staleWorkers"`
	// FailingWorkers ran on time and errored anyway — a distinct failure from
	// stale, and deliberately a separate field so a consumer that only knew
	// about staleness does not silently start counting these as the same thing.
	FailingWorkers []string `json:"failingWorkers,omitempty"`
	// RejectedEnv lists operator overrides the daemon refused to use, so a
	// config the operator wrote but is NOT running is visible instead of silent.
	// See internal/envcfg. Only a rejection on a key that governs DELETION makes
	// the fleet unhealthy; the rest are reported without paging anyone.
	RejectedEnv []envcfg.Rejection `json:"rejectedEnv,omitempty"`
	Ts          int64              `json:"ts"`
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
	// Notify shows a user-facing alert; nil = notify.Local (the platform's
	// desktop popup, or an explicit unsupported error).
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
	// OFFSITE BACKUP FRESHNESS. ops/signaldeck-backup-offline.sh is scrupulously
	// honest -- it detects that the configured offsite directory is on the same
	// volume as the database and logs "NOT OFFSITE ... backup_last_offsite NOT
	// updated" on every run -- and then nothing escalates it. No task result goes
	// non-zero, and the DR runbook's own precondition ("check the log says
	// offsite OK before you need this page") has never been met. Measured
	// 2026-08-23: backup_last_offsite was 10 days behind backup_last_ts with 8
	// consecutive NOT OFFSITE lines. Honest and unheard is still unheard.
	if msg, ok := w.staleOffsiteBackup(ctx, now); !ok {
		stale = append(stale, msg)
	}
	// The number this pass actually EXAMINED. StaleWorkers skips every
	// Interval<=0 spec (the stream ingestors), so len(w.Specs) overstates it.
	checked := 0
	for _, s := range w.Specs {
		if s.Interval > 0 {
			checked++
		}
	}

	// A failing worker is unhealthy even when it is perfectly punctual. An error
	// reading the streaks must not read as "nothing is failing", so it degrades
	// the run rather than being swallowed — the whole point of this check is
	// that an unnoticed failure is the expensive kind.
	recent, err := w.recentStatuses(ctx)
	if err != nil {
		return "", fmt.Errorf("query worker_runs statuses: %w", err)
	}
	// Long-running workers come from the SAME Specs that StaleWorkers skips on,
	// so the two rules cannot disagree about which workers are stream ingestors.
	longRunning := make(map[string]bool, len(w.Specs))
	for _, s := range w.Specs {
		if s.Interval <= 0 {
			longRunning[s.Name] = true
		}
	}
	failing := FailingWorkers(recent, longRunning)

	// A refused override means the daemon is running a configuration the
	// operator did not choose. Reported always; unhealthy ONLY when it governs
	// deletion, because that consequence is irreversible and a typo in a
	// retention window silently deletes rows someone meant to keep. Making every
	// rejected knob page an operator would be the red-by-construction mistake.
	rejected := envcfg.Rejected()
	ok := len(stale) == 0 && len(failing) == 0 && !envcfg.HasCritical()
	var writeErr error

	if err := w.writeStatus(Status{
		OK:             ok,
		StaleWorkers:   append([]string{}, stale...),
		FailingWorkers: append([]string{}, failing...),
		RejectedEnv:    rejected,
		Ts:             now.Unix(),
	}); err != nil {
		// health.json is the artifact this worker EXISTS to produce, for
		// launchd/cron and the dashboards. Warning and continuing meant that on
		// a full disk or a read-only data dir the file froze at its last content
		// -- possibly ok:true from days ago, since Ts lives inside the frozen
		// blob -- while the watchdog filed status=ok every 10 minutes and
		// nothing anywhere checks the file's freshness. Degrade instead: the
		// run completed without delivering the one thing it delivers.
		writeErr = err
		slog.Warn("watchdog: write health.json", "err", err)
	}

	for _, name := range failing {
		if err := w.St.InsertDQ(ctx, md.DQEvent{
			Ts:   now.Unix(),
			Kind: "worker_failing",
			Detail: fmt.Sprintf("worker=%s has failed its last %d consecutive runs — it is running on schedule and erroring every time, which staleness cannot see",
				name, MinConsecutiveFailures),
		}); err != nil {
			slog.Warn("watchdog: record failing dq", "worker", name, "err", err)
		}
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
			local = notify.Local
		}
		if err := local(unhealthyMsg(stale, failing)); err != nil {
			slog.Warn("watchdog: notification failed", "err", err) // never fatal
		}
		// Stage 3: same transition + 6h cooldown, delivered beyond the Mac.
		// Send degrades to dq events internally and never errors.
		if w.Remote != nil {
			w.Remote.Send(ctx, notify.Message{
				Title: "SignalDeck watchdog: fleet unhealthy",
				Body:  unhealthyMsg(stale, failing),
				Kind:  "watchdog",
				Ts:    now.Unix(),
			})
		}
	}
	w.wasOK = ok

	if writeErr != nil {
		return fmt.Sprintf("could not write health.json (%v) — the fleet verdict was computed but not published", writeErr),
			fmt.Errorf("write health.json: %w: %w", writeErr, workers.ErrDegraded)
	}
	if ok {
		// COUNTS WHAT IT CHECKED, not what is registered. len(w.Specs) included
		// workers this pass never examined: StaleWorkers skips every Interval<=0
		// stream ingestor, and FailingWorkers iterates worker_runs rows, so a
		// registered worker with no rows is invisible to both. apiprobe.go
		// documents the same miscount independently.
		return fmt.Sprintf("healthy: %d of %d workers checked", checked, len(w.Specs)), nil
	}
	if len(recovered) > 0 {
		return fmt.Sprintf("UNHEALTHY: %s (cancelled overdue runs: %v)", unhealthyMsg(stale, failing), recovered), nil
	}
	return "UNHEALTHY: " + unhealthyMsg(stale, failing), nil
}

// unhealthyMsg names both conditions separately. "Stale" and "failing" have
// different causes and different fixes — a stale worker is not running, a
// failing one is running and erroring — so collapsing them into one word would
// send an operator looking for the wrong thing.
func unhealthyMsg(stale, failing []string) string {
	var parts []string
	if len(stale) > 0 {
		parts = append(parts, fmt.Sprintf("%d stale worker(s): %v", len(stale), stale))
	}
	if len(failing) > 0 {
		parts = append(parts, fmt.Sprintf("%d worker(s) failing every run: %v", len(failing), failing))
	}
	// Named separately from the worker conditions: this one is a CONFIG fault,
	// not a runtime one, and it is fixed by correcting an environment variable
	// rather than by looking at a worker.
	var crit []string
	for _, r := range envcfg.Rejected() {
		if r.Critical {
			crit = append(crit, r.Key)
		}
	}
	if len(crit) > 0 {
		parts = append(parts, fmt.Sprintf(
			"%d rejected override(s) governing DATA DELETION — the default retention is in force, not yours: %v",
			len(crit), crit))
	}
	if len(parts) == 0 {
		return "unhealthy"
	}
	return strings.Join(parts, "; ")
}

// lastSuccess maps worker → time of most recent successful run (read pool).
func (w *Watchdog) lastSuccess(ctx context.Context) (map[string]time.Time, error) {
	rows, err := w.St.DB().QueryContext(ctx,
		// 'degraded' counts as a heartbeat: the worker ran on schedule and
		// honestly reported it had nothing to deliver (the benched trainers do
		// this every run by design). Counting only 'ok' held health.json at
		// ok=false with two "stale" workers for weeks — an alarm that can never
		// clear is an alarm nobody acts on. /api/ready lists them as degraded.
		`SELECT worker, MAX(started_at) FROM worker_runs WHERE status IN ('ok','degraded') GROUP BY worker`)
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

// statusWindow is how many recent runs per worker recentStatuses fetches.
//
// Deliberately WIDER than MinConsecutiveFailures. FailingWorkers skips rows with
// status "running", and a stream ingestor always has one in flight, so a window
// of exactly MinConsecutiveFailures would let an in-flight run shrink the
// evidence below the threshold and hide a worker that is failing underneath it.
// The slack absorbs that.
const statusWindow = MinConsecutiveFailures + 2

// recentStatuses maps worker → its most recent run statuses, newest first,
// capped at statusWindow per worker so no worker's history outweighs another's.
//
// The window function is evaluated over worker_runs' (started_at DESC, id DESC)
// ordering, matching RecentWorkerRuns, so "newest first" means the same thing in
// both health surfaces.
func (w *Watchdog) recentStatuses(ctx context.Context) (map[string][]string, error) {
	rows, err := w.St.DB().QueryContext(ctx, `
		SELECT worker, status FROM (
		  SELECT worker, status,
		         ROW_NUMBER() OVER (PARTITION BY worker
		                            ORDER BY started_at DESC, id DESC) rn
		  FROM worker_runs
		) WHERE rn <= ? ORDER BY worker, rn`, statusWindow)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	out := map[string][]string{}
	for rows.Next() {
		var name, status string
		if err := rows.Scan(&name, &status); err != nil {
			return nil, err
		}
		out[name] = append(out[name], status)
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

// The local desktop popup now lives in notify.Local, which dispatches on the
// running platform. The osascriptNotify that stood here was macOS-only and
// unguarded, so after the move to Windows every watchdog alert died as
// `exec: "osascript": executable file not found in %PATH%`. See
// internal/notify/local.go.

// offsiteMaxAge is how old the last GENUINE off-volume backup may be before the
// watchdog calls the fleet unhealthy. Generous: the backup runs on weekdays via
// market-close, so a long weekend plus a holiday is normal and must not cry
// wolf. Anything past this is not a schedule gap, it is a broken offsite path.
const offsiteMaxAge = 5 * 24 * time.Hour

// staleOffsiteBackup reports whether the last off-volume backup is recent
// enough. ok=true when it is, or when no offsite backup has ever been recorded
// AND none is configured -- an operator who has not set one up is not lied to
// about it, but one who HAS must hear when it silently stopped.
func (w *Watchdog) staleOffsiteBackup(ctx context.Context, now time.Time) (string, bool) {
	var lastStr, dir string
	if err := w.St.DB().QueryRowContext(ctx,
		`SELECT COALESCE((SELECT v FROM meta WHERE k='backup_last_offsite'),''),
		        COALESCE((SELECT v FROM meta WHERE k='backup_offsite_dir'),'')`).
		Scan(&lastStr, &dir); err != nil {
		// An unreadable meta table is the watchdog's own problem, reported
		// elsewhere; do not manufacture a backup verdict from it.
		return "", true
	}
	if lastStr == "" {
		return "", true // never recorded one; nothing to call stale
	}
	last, err := strconv.ParseInt(lastStr, 10, 64)
	if err != nil || last <= 0 {
		return "", true
	}
	age := now.Sub(time.Unix(last, 0))
	if age <= offsiteMaxAge {
		return "", true
	}
	return fmt.Sprintf("offsite backup is %.1f days old (last %s) — the local copy is not a disaster-recovery copy",
		age.Hours()/24, time.Unix(last, 0).Format("2006-01-02")), false
}
