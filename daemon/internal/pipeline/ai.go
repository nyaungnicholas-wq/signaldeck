package pipeline

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/aiagents/analyst"
	"github.com/nyaungnicholas-wq/signaldeck/internal/aiagents/watcher"
	"github.com/nyaungnicholas-wq/signaldeck/internal/llm"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
	"github.com/nyaungnicholas-wq/signaldeck/internal/workers"
)

// AnalystWorker runs the Market Analyst agent on a cadence and persists its
// market brief to the insights feed. It no-ops safely when no LLM key is set.
type AnalystWorker struct {
	St  *store.Store
	LLM llm.Client
}

// Name implements workers.Worker.
func (w *AnalystWorker) Name() string { return "ai-analyst" }

// Interval implements workers.Worker.
func (w *AnalystWorker) Interval() time.Duration { return time.Hour }

// Run generates and stores an analyst brief.
func (w *AnalystWorker) Run(ctx context.Context) (string, error) {
	if !w.LLM.Enabled() {
		return "skipped: no LLM key configured", nil
	}
	b, err := analyst.Run(ctx, w.LLM, w.St)
	if err != nil {
		// A SPENT DAILY BUDGET IS NOT A FAILURE. The cap is a control that is
		// working when it fires, and this worker ticks hourly, so once the day's
		// calls are gone every remaining tick filed status=error — 10+ error
		// rows a day, the worker counted as failing on the Agents page, and the
		// watchdog holding the fleet unhealthy over a budget doing its job.
		// Measured 2026-08-05: llm_spend hit 2,162 against a 2,000 cap and
		// ai-analyst had errored on every hourly tick since.
		//
		// Reported as a skip, alongside the two skips already above it: no key
		// and AI-disabled are the same category — a reason there is no brief
		// that says nothing is broken. It resets at the UTC day boundary on its
		// own. A REAL failure still returns an error.
		if errors.Is(err, llm.ErrCapReached) {
			return "skipped: daily LLM call cap reached — resets at the UTC day boundary", nil
		}
		// NOR IS AN EXHAUSTED PROVIDER POOL. ErrTransient means every retry met
		// a retryable condition (429 / 5xx / network / timeout) — for the shared
		// free-tier pool that is "ResourceExhausted: Worker local total request
		// limit reached". That is upstream capacity, not a fault here, and the
		// next hourly tick resumes. Filed as a hard error it was the ONLY reason
		// /api/ready answered 503 fleet-wide on 2026-09-10; readiness asks "can
		// this daemon serve CORRECT answers", and "no brief this hour" is a
		// correct answer, not a wrong one.
		//
		// DEGRADED, not a skip. sentiment-tagger may report ErrTransient as a
		// success because it carries the count it did land; there is no partial
		// brief, so a skip here would let a provider that never answers again
		// read as ok forever — the exact shape workers.ErrDegraded exists to
		// stop. Degraded keeps it on /api/health and the Agents page without
		// failing the probe.
		if errors.Is(err, llm.ErrTransient) {
			const detail = "provider pool busy, no brief this pass; resuming next tick"
			return detail, fmt.Errorf("%s: %w", detail, workers.ErrDegraded)
		}
		return "", err
	}
	if b.Disabled {
		return "skipped: AI disabled", nil
	}
	if err := analyst.Persist(ctx, w.St, b); err != nil {
		return "", err
	}
	return fmt.Sprintf("wrote analyst brief (%d symbols)", len(b.PerSymbol)), nil
}

// WatcherWorker runs the Risk & Anomaly Watcher and persists heads-ups. It
// degrades to a rule-based list when no key is set (still useful).
type WatcherWorker struct {
	St  *store.Store
	LLM llm.Client
}

// Name implements workers.Worker.
func (w *WatcherWorker) Name() string { return "ai-watcher" }

// Interval implements workers.Worker.
func (w *WatcherWorker) Interval() time.Duration { return 15 * time.Minute }

// Run scans for risk changes and stores a heads-up when there is something to
// say.
func (w *WatcherWorker) Run(ctx context.Context) (string, error) {
	h, err := watcher.Run(ctx, w.LLM, w.St)
	if err != nil {
		return "", err
	}
	if h.NFlags == 0 {
		return "no notable risk changes", nil
	}
	if err := watcher.Persist(ctx, w.St, h); err != nil {
		return "", err
	}
	mode := "llm"
	if h.Disabled {
		mode = "rule-based (no key)"
	}
	return fmt.Sprintf("%d flag(s), %s", h.NFlags, mode), nil
}
