// Package workers is SignalDeck's in-app agent framework. Each Worker is an
// autonomous unit with a name, a cadence, and a Run method; the Runner
// schedules them, persists every run (status + detail) to worker_runs, and
// recovers from panics — the Agents page in the UI renders exactly this
// table, so what the user sees IS what ran.
package workers

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"log/slog"
	"math/rand"
	"os"
	"runtime/debug"
	"sort"
	"sync"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// ErrDegraded marks a run that completed without breaking and without
// delivering. Return it wrapped — fmt.Errorf("congress mirrors unavailable: %w",
// ErrDegraded) — and the run is filed as status "degraded" rather than "ok".
//
// It exists because the only way a worker could previously signal trouble was to
// return an error, and pollers with a dead upstream deliberately do not: a dead
// mirror is not a crash. The result was congress-poller reporting ok on every
// run while congress_trades held zero rows, and edgar-fetcher reporting ok with
// "skipped: no EDGAR client" forever. A source that never delivers must not read
// as a source that works.
var ErrDegraded = errors.New("degraded: completed without delivering")

// Worker is one in-app agent.
type Worker interface {
	// Name is the stable identifier shown on the Agents page.
	Name() string
	// Interval is the cadence between runs (0 = long-running: Run is called
	// once and expected to block until ctx is done, e.g. stream ingestors).
	Interval() time.Duration
	// Run does one unit of work and returns a one-line human detail.
	Run(ctx context.Context) (detail string, err error)
}

// TimeoutWorker is the optional escape hatch for a worker whose honest worst
// case does not fit the default deadline (a full-universe backfill on a 1h
// cadence, say). Workers that do not implement it get defaultRunTimeout.
type TimeoutWorker interface {
	Worker
	// RunTimeout bounds ONE Run. Return 0 to accept the default.
	RunTimeout() time.Duration
}

// Per-run deadline constants. WHY (measured over 7 days): the runner passed the
// daemon ROOT context into every Run, so a worker parked in a
// context-insensitive SQLite scan or a bodyless HTTP read stayed parked until
// the process restarted — 662 worker_stale plus 261 stale dq events with ZERO
// automated recoveries. A worker that is silently dead for a day leaves
// unattributed holes in a forward prediction record whose entire value is
// completeness, so "eventually a human restarts it" is not an acceptable
// recovery path.
//
// The bound is deliberately generous — timeoutFactor x Interval, floored so a
// 1m worker still gets 15m and capped so nothing runs unbounded. It is a
// LIVENESS bound, not a performance target: a run that hits it is a bug, and it
// is recorded as its own `timeout` status so it is never confused with a
// returned error.
const (
	timeoutFactor  = 3
	minRunTimeout  = 15 * time.Minute
	maxRunTimeout  = 6 * time.Hour
	quiesceDrainTO = 30 * time.Second
	// quiesceDrainWarn is the drain duration above which the pause stops being
	// free. It is deliberately well under quiesceDrainTO so the trend is visible
	// BEFORE the drain starts timing out — the point is to know the cost is
	// rising while worker #94 is still a proposal.
	quiesceDrainWarn = 5 * time.Second
)

// defaultRunTimeout is the deadline for a periodic worker with the given
// interval. Long-running workers (interval 0) are exempt: they block until the
// daemon's context ends BY DESIGN, so any deadline would kill them on a timer.
func defaultRunTimeout(interval time.Duration) time.Duration {
	if interval <= 0 {
		return 0
	}
	d := timeoutFactor * interval
	if d < minRunTimeout {
		d = minRunTimeout
	}
	if d > maxRunTimeout {
		d = maxRunTimeout
	}
	return d
}

// runTimeoutFor resolves a worker's deadline, honoring an explicit override.
func runTimeoutFor(w Worker) time.Duration {
	if tw, ok := w.(TimeoutWorker); ok {
		if d := tw.RunTimeout(); d > 0 {
			return d
		}
	}
	return defaultRunTimeout(w.Interval())
}

// ShutdownGrace is how long Start waits after ctx cancellation for workers to
// drain before force-exiting the process. A worker parked in a
// context-insensitive call (SQLite exec, HTTP with no timeout, a bare channel
// receive) would otherwise hang wg.Wait() forever — observed 2026-07-24 as a
// daemon stuck 30+ min post-SIGTERM until SIGKILL.
var ShutdownGrace = 75 * time.Second

// forceExit is swappable so tests can observe the escalation without dying.
var forceExit = func(code int) { os.Exit(code) }

// inflight is one Run currently executing.
type inflight struct {
	since    time.Time
	deadline time.Time // zero = no deadline (long-running worker)
	cancel   context.CancelFunc
	canceled bool // an actuator already cancelled this run
}

// Runner schedules workers and records their runs.
type Runner struct {
	st      *store.Store
	workers []Worker

	mu      sync.Mutex
	running map[string]*inflight // worker name → in-flight Run
	// quiesce is non-nil while the fleet is quiesced; it is closed on release.
	quiesce chan struct{}
	// lastQuiesce is what the most recent pause cost (see QuiesceStat).
	lastQuiesce QuiesceStat
}

// NewRunner builds a runner over the given workers.
func NewRunner(st *store.Store, ws ...Worker) *Runner {
	return &Runner{st: st, workers: ws, running: make(map[string]*inflight)}
}

// Add appends workers before Start. It exists so callers can build the Runner
// FIRST and hand it to components that need to act on the fleet (the health
// watchdog's actuator, the storage governor's quiesce window) before the fleet
// itself is assembled.
func (r *Runner) Add(ws ...Worker) { r.workers = append(r.workers, ws...) }

// Start launches every worker and blocks until ctx is done and all exit —
// or until ShutdownGrace after cancellation, at which point it logs which
// workers are still in-flight and force-exits the process. SQLite (WAL) and
// every worker's persistence are crash-safe, so a hard exit beats a daemon
// parked forever behind a stuck goroutine.
func (r *Runner) Start(ctx context.Context) {
	var wg sync.WaitGroup
	for _, w := range r.workers {
		wg.Add(1)
		go func(w Worker) {
			defer wg.Done()
			r.loop(ctx, w)
		}(w)
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
		return
	case <-ctx.Done():
	}
	select {
	case <-done:
	case <-time.After(ShutdownGrace):
		slog.Error("shutdown deadline exceeded — forcing exit",
			"grace", ShutdownGrace, "stuckWorkers", r.stuckWorkers())
		forceExit(1)
	}
}

// InFlightNames names the workers with a Run still in flight, oldest first,
// each annotated with how long it has been running. Exported so a diagnosis
// that needs a HOLDER CENSUS (e.g. a WAL checkpoint that keeps stalling at the
// same frame) can name the daemon's own candidates instead of guessing.
func (r *Runner) InFlightNames() []string { return r.stuckWorkers() }

// stuckWorkers names the workers with a Run still in flight, oldest first.
func (r *Runner) stuckWorkers() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	type entry struct {
		name  string
		since time.Time
	}
	entries := make([]entry, 0, len(r.running))
	for name, in := range r.running {
		entries = append(entries, entry{name, in.since})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].since.Before(entries[j].since) })
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = fmt.Sprintf("%s (running %s)", e.name, time.Since(e.since).Round(time.Second))
	}
	return names
}

// streamBackoff returns how long to wait before redialling a long-running
// stream worker after its session ended, given how many consecutive short
// sessions preceded it.
//
// This replaces a flat 5-second cooldown whose comment claimed the streamer did
// "finer backoff" internally. It did not, and nothing else did either. Measured
// on 2026-07-31 with a dependency down: ~60 restarts in 68 seconds, and it would
// have continued at that rate for as long as the outage lasted. Against a
// market-data vendor rather than a local service, that is precisely how an
// outage becomes a rate-limit or an IP ban -- the client hammers hardest at the
// moment the provider is least able to answer.
//
// Full jitter (wait drawn from [half, full] of the capped exponential) rather
// than a fixed schedule, because every stream worker recovering from a SHARED
// outage would otherwise redial in the same instant. The cap keeps a recovered
// provider from waiting hours to be noticed.
func streamBackoff(attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	shift := attempt
	if shift > 20 { // 2s<<20 is ~24 days; clamp long before int64 overflow
		shift = 20
	}
	window := 2 * time.Second << uint(shift)
	if window > streamBackoffCap {
		window = streamBackoffCap
	}
	return window/2 + time.Duration(rand.Int63n(int64(window/2)))
}

// streamBackoffCap bounds the wait so a provider that recovers is retried
// promptly. Two minutes: long enough to stop hammering, short enough that an
// unattended daemon reconnects on its own within one bar period.
const streamBackoffCap = 2 * time.Minute

// healthySession is how long a stream must hold before its next failure is
// treated as a fresh incident rather than a continuing one. A feed that ran for
// hours and then dropped should be retried quickly, not at the interval its
// last bad day ended on.
const healthySession = 60 * time.Second

func (r *Runner) loop(ctx context.Context, w Worker) {
	iv := w.Interval()
	if iv == 0 {
		// Long-running stream worker: keep it alive, redialling on error with a
		// jittered, capped exponential backoff that resets after a healthy run.
		attempt := 0
		for ctx.Err() == nil {
			start := time.Now()
			r.runOnce(ctx, w)
			if time.Since(start) >= healthySession {
				attempt = 0
			} else {
				attempt++
			}
			select {
			case <-ctx.Done():
			case <-time.After(streamBackoff(attempt)):
			}
		}
		return
	}
	// Calendar worker: sleep to the next real publication instant instead of
	// ticking fast and skipping. See ScheduledWorker in schedule.go.
	if _, ok := w.(ScheduledWorker); ok {
		r.scheduledLoop(ctx, w)
		return
	}
	// Periodic worker: stagger the first run, then hold that phase.
	//
	// WHY (measured 2026-07-16): every periodic worker used to call runOnce
	// immediately at t=0 and then start its ticker, so (a) the whole fleet
	// stampeded on boot and (b) tickers created in the same instant stayed
	// PHASE-LOCKED forever — all hourly workers firing together every hour,
	// all 10-minute workers together every 10 minutes. Observed: 10 heavy
	// workers scanning a 5.5GB database concurrently, starving the API's read
	// pool (a 61s /api/honesty) and denying the WAL checkpoint the reader-free
	// moment a TRUNCATE needs. De-phasing the fleet is the root fix.
	//
	// The offset is DETERMINISTIC (FNV of the worker name, not RNG) so runs
	// stay reproducible and the Agents page cadence is legible: a given worker
	// always occupies the same slot in its interval, and distinct names get
	// distinct slots.
	if off := startOffset(w.Name(), iv); off > 0 {
		select {
		case <-ctx.Done():
			return
		case <-time.After(off):
		}
	}
	t := time.NewTicker(iv)
	defer t.Stop()
	r.runOnce(ctx, w)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.runOnce(ctx, w)
		}
	}
}

// scheduledLoop drives a ScheduledWorker: compute the next real fire instant,
// sleep to it, run, repeat. There is no ticker and no stagger — a calendar
// worker's phase IS its schedule, and two of them landing together is a
// non-event because they fire minutes apart per day, not seconds apart per
// minute.
//
// `last` is seeded from worker_runs so a restart does not re-fire a weekly job:
// before this loop existed, the internal week-key gate in the store was the only
// thing preventing exactly that, and a gate is a worse place for the rule than
// the schedule.
func (r *Runner) scheduledLoop(ctx context.Context, w Worker) {
	last := r.lastRunAt(ctx, w.Name())
	for ctx.Err() == nil {
		now := time.Now()
		next, ok := nextFireFor(w, last, now)
		if !ok {
			// No calendar opinion right now: fall back to the plain interval.
			iv := w.Interval()
			if iv <= 0 {
				iv = time.Hour
			}
			next = now.Add(iv)
		}
		wake := next
		// Never sleep past maxScheduledGap in one hop: a 7-day dead-source
		// backoff should still notice a clock change or a config reload, and a
		// week-long unwakeable goroutine is indistinguishable from a hung one.
		if limit := now.Add(maxScheduledGap); wake.After(limit) {
			wake = limit
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Until(wake)):
		}
		if time.Now().Before(next) {
			continue // woke early to re-evaluate; not yet due
		}
		last = time.Now()
		r.runOnce(ctx, w)
	}
}

// lastRunAt reads a worker's most recent run start, so NextFire survives a
// restart. A miss (never run, or pruned) returns the zero time, which every
// NextFire must treat as "run at the next scheduled slot", not "run now".
func (r *Runner) lastRunAt(ctx context.Context, name string) time.Time {
	if r.st == nil {
		return time.Time{}
	}
	t, err := r.st.LastWorkerRunAt(ctx, name)
	if err != nil {
		slog.Warn("worker: last run lookup", "worker", name, "err", err)
		return time.Time{}
	}
	return t
}

// maxStartOffset caps the de-phasing delay: long enough to spread the fleet
// across the heaviest scans, short enough that a fresh daemon is fully warm in
// under a minute.
const maxStartOffset = 45 * time.Second

// startOffset returns a worker's deterministic phase offset, bounded by both
// maxStartOffset and the worker's own interval (a 5s worker must not wait 45s).
// Long-running workers (interval 0) never reach here.
func startOffset(name string, interval time.Duration) time.Duration {
	span := maxStartOffset
	if interval < span {
		span = interval
	}
	if span <= 0 {
		return 0
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(name))
	return time.Duration(uint64(h.Sum32()) % uint64(span))
}

// runOnce executes one run with persistence + panic isolation, under a per-run
// deadline (see the timeout constants above). The run's cancel func is parked
// in r.running so an actuator — the health watchdog — can cut a hung run loose
// without waiting for the deadline or a daemon restart.
func (r *Runner) runOnce(ctx context.Context, w Worker) {
	if ctx.Err() != nil {
		return
	}
	if !r.awaitQuiesce(ctx) {
		return
	}
	runID, err := r.st.StartWorkerRun(ctx, w.Name())
	if err != nil {
		slog.Error("worker: start record", "worker", w.Name(), "err", err)
	}

	var (
		runCtx context.Context
		cancel context.CancelFunc
	)
	in := &inflight{since: time.Now()}
	if to := runTimeoutFor(w); to > 0 {
		runCtx, cancel = context.WithTimeout(ctx, to)
		in.deadline = in.since.Add(to)
	} else {
		runCtx, cancel = context.WithCancel(ctx)
	}
	in.cancel = cancel
	defer cancel()

	r.mu.Lock()
	r.running[w.Name()] = in
	r.mu.Unlock()
	detail, runErr := r.safeRun(runCtx, w)
	r.mu.Lock()
	intervened := in.canceled
	delete(r.running, w.Name())
	r.mu.Unlock()

	status := "ok"
	if runErr != nil {
		switch {
		// A cancellation at shutdown is not a failure worth alarming on.
		case ctx.Err() != nil:
			status, detail = "ok", "stopped (shutdown)"
		// The run blew its own deadline (or was cut loose by the watchdog) while
		// the daemon kept running. Recorded as its OWN status so a hung worker is
		// never filed as an ordinary error — the two have different causes and
		// different fixes.
		case runCtx.Err() != nil:
			status = "timeout"
			reason := fmt.Sprintf("exceeded run deadline %s", runTimeoutFor(w))
			if intervened {
				reason = "cancelled by watchdog (stale + past deadline)"
			}
			detail = fmt.Sprintf("%s after %s: %v", reason, time.Since(in.since).Round(time.Second), runErr)
			slog.Warn("worker run timed out", "worker", w.Name(), "after", time.Since(in.since), "err", runErr)
		// The run finished without breaking and without delivering. A poller whose
		// upstream is dead returns nil today, because a dead mirror is not a crash
		// and nobody wants to be paged for it — so congress-poller filed status=ok
		// on every run while congress_trades held ZERO rows, and 94
		// congress_mirror_error dq events accumulated over seven days behind a
		// uniformly green fleet view. Recorded as its own status for the same
		// reason "timeout" is: a source that delivered nothing has a different
		// cause and a different fix than one that failed, and neither is "ok".
		//
		// Deliberately LAST before default: a shutdown or a blown deadline that
		// happens to wrap ErrDegraded is still a shutdown or a timeout.
		case errors.Is(runErr, ErrDegraded):
			status, detail = "degraded", runErr.Error()
			slog.Warn("worker degraded", "worker", w.Name(), "err", runErr)
		default:
			status, detail = "error", runErr.Error()
			slog.Warn("worker failed", "worker", w.Name(), "err", runErr)
		}
	}
	if runID != 0 {
		r.finishRecord(w.Name(), runID, status, clip(detail, 500))
	}
}

// CancelOverdue cancels the named worker's in-flight run if it is past its
// deadline, returning how long it had been running. It is the ACTUATOR half of
// staleness detection: before this, the watchdog observed 662 worker_stale
// events over 7 days, wrote health.json, and recovered exactly nothing.
//
// It deliberately does NOT touch a run that is merely long — only one that has
// already blown the deadline the runner itself set — so it cannot shorten any
// worker's honest budget.
func (r *Runner) CancelOverdue(name string) (time.Duration, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	in, ok := r.running[name]
	if !ok || in.canceled || in.deadline.IsZero() || time.Now().Before(in.deadline) {
		return 0, false
	}
	in.canceled = true
	in.cancel()
	return time.Since(in.since), true
}

// awaitQuiesce blocks a starting run while the fleet is quiesced. Returns false
// if ctx ended first (shutdown), in which case the run is skipped entirely.
func (r *Runner) awaitQuiesce(ctx context.Context) bool {
	for {
		r.mu.Lock()
		ch := r.quiesce
		r.mu.Unlock()
		if ch == nil {
			return true
		}
		select {
		case <-ch:
		case <-ctx.Done():
			return false
		}
	}
}

// Quiesce holds the fleet still for d: no new runs start, in-flight runs are
// drained (bounded by quiesceDrainTO — a worker that ignores cancellation must
// not be able to block the window forever), and the window is then held open
// for d before the fleet is released. Workers named in except are ignored when
// draining, so the caller's OWN run does not deadlock the wait.
//
// This exists because the WAL's only reclaim path — a TRUNCATE checkpoint —
// needs an instant with no active readers, and a permanently-running fleet
// never yields one (measured: 0/22 TRUNCATE successes, 21 BUSY, WAL 5,396 MB
// against a 64 MB journal_size_limit). Quiesce MANUFACTURES that instant
// instead of waiting for luck. It returns ctx.Err() if the window is cut short.
func (r *Runner) Quiesce(ctx context.Context, d time.Duration, except ...string) error {
	return r.QuiesceDo(ctx, d, nil, except...)
}

// QuiesceDo is Quiesce with work to perform INSIDE the held window, once the
// drain has completed. A caller that needs the pause for something (the WAL
// TRUNCATE checkpoint) must use this: with plain Quiesce it cannot tell when
// the drain ended, so it would race its own window.
func (r *Runner) QuiesceDo(ctx context.Context, d time.Duration, fn func(context.Context), except ...string) error {
	skip := make(map[string]bool, len(except))
	for _, n := range except {
		skip[n] = true
	}
	r.mu.Lock()
	if r.quiesce != nil {
		r.mu.Unlock()
		return fmt.Errorf("fleet already quiesced")
	}
	gate := make(chan struct{})
	r.quiesce = gate
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		r.quiesce = nil
		r.mu.Unlock()
		close(gate)
	}()

	// Measure the stall. WHY: quiescing holds 90+ workers still so the WAL can
	// be TRUNCATE-checkpointed, and the cost of that pause grows with every
	// worker added to the fleet — but nothing measured it, so "is the governor
	// still cheap?" was unanswerable and would only surface as unexplained
	// worker lateness. Drain time is the number that matters (the held window is
	// a constant the caller chose); blocked is how many runs were still in
	// flight when the drain began.
	quiesceStart := time.Now()
	r.mu.Lock()
	blocked := 0
	for name, in := range r.running {
		if !skip[name] && !in.deadline.IsZero() {
			blocked++
		}
	}
	r.mu.Unlock()

	drainBy := time.Now().Add(quiesceDrainTO)
	for {
		r.mu.Lock()
		n := 0
		for name, in := range r.running {
			// Long-running workers (no deadline: stream ingestors) are never
			// drainable — they block until the daemon exits by design — so
			// waiting on them would guarantee the drain always times out. They
			// are excluded from the wait, and any WAL frames they pin simply
			// show up as a BUSY rung in the checkpoint ladder's honest report.
			if !skip[name] && !in.deadline.IsZero() {
				n++
			}
		}
		r.mu.Unlock()
		if n == 0 || time.Now().After(drainBy) {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}

	stat := QuiesceStat{
		At: quiesceStart, Drain: time.Since(quiesceStart),
		Blocked: blocked, Fleet: len(r.workers),
		DrainTimedOut: time.Now().After(drainBy),
	}
	r.mu.Lock()
	r.lastQuiesce = stat
	r.mu.Unlock()
	slog.Info("worker: fleet quiesced", "drain", stat.Drain, "blocked", stat.Blocked,
		"fleet", stat.Fleet, "drainTimedOut", stat.DrainTimedOut)
	// A drain that runs long is the fleet's growth showing up as latency for
	// every worker at once; a drain that times out means the pause was paid for
	// and the checkpoint still ran against live readers. Both are worth a dq
	// event rather than a log line nobody greps.
	if r.st != nil && (stat.DrainTimedOut || stat.Drain > quiesceDrainWarn) {
		_ = r.st.InsertDQ(ctx, md.DQEvent{
			Ts: time.Now().Unix(), Kind: "quiesce_stall",
			Detail: fmt.Sprintf("fleet drain %s (%d in flight of %d workers, timedOut=%v)",
				stat.Drain.Round(time.Millisecond), stat.Blocked, stat.Fleet, stat.DrainTimedOut),
		})
	}

	if fn != nil {
		fn(ctx)
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

// QuiesceStat is what the last fleet pause actually cost. DrainTimedOut means
// at least one worker ignored cancellation for the full quiesceDrainTO and the
// checkpoint proceeded with readers still active — the case where the pause is
// paid for and the WAL reclaim still fails.
type QuiesceStat struct {
	At            time.Time
	Drain         time.Duration
	Blocked       int
	Fleet         int
	DrainTimedOut bool
}

// LastQuiesce returns the cost of the most recent fleet pause. The zero value
// means the fleet has never been quiesced in this process.
func (r *Runner) LastQuiesce() QuiesceStat {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lastQuiesce
}

// InFlight reports how many runs are currently executing (test/telemetry).
func (r *Runner) InFlight() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.running)
}

// finishRecord persists the run outcome with its OWN context — never the run
// context, which may already be expired after a slow/timed-out run (a worker
// that blew its deadline must still record that failure). The single SQLite
// write conn can queue for a while when many workers finish together (the
// startup herd), so the deadline is generous and one retry absorbs a
// transient "context deadline exceeded"/busy window.
func (r *Runner) finishRecord(worker string, runID int64, status, detail string) {
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			time.Sleep(2 * time.Second)
		}
		fctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		lastErr = r.st.FinishWorkerRun(fctx, runID, status, detail)
		cancel()
		if lastErr == nil {
			return
		}
	}
	slog.Error("worker: finish record", "worker", worker, "err", lastErr)
}

func (r *Runner) safeRun(ctx context.Context, w Worker) (detail string, err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("panic: %v", p)
			slog.Error("worker panicked", "worker", w.Name(), "panic", p, "stack", string(debug.Stack()))
		}
	}()
	return w.Run(ctx)
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
