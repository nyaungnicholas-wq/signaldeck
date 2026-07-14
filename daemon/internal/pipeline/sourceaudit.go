// SourceAuditor (data-source freshness wave): the "self-honest, not
// self-healing" watchdog for EXTERNAL data. The fleet's own watchdog
// (internal/health) flags workers that stop RUNNING; this flags sources that
// stop PRODUCING while their worker keeps returning ok — a scraper that drifts
// or a source that goes quietly dead. Once an hour it asks internal/srchealth
// how old the newest row of every registered source is, and for each source
// past its expected-max-staleness budget records a dq_events(source_stale),
// deduped to one per source per UTC day. Fresh sources are silent. Sources that
// are legitimately quiet because the market is closed are NOT flagged (srchealth
// reports them "market closed"). The webhook source is judged only when a secret
// is configured and the market is open — the case that catches a silent tunnel
// failure while trading is live. GET /api/source-health serves the same
// evaluation live.
package pipeline

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/srchealth"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// SourceAuditor is the freshness worker (implements workers.Worker).
type SourceAuditor struct {
	St *store.Store
	// WebhookSecretSet mirrors cfg.TVWebhookSecret != "": the tv_signals source
	// is only judged when the inbound webhook is actually enabled.
	WebhookSecretSet bool
}

// Name implements workers.Worker.
func (w *SourceAuditor) Name() string { return "source-audit" }

// Interval implements workers.Worker: hourly.
func (w *SourceAuditor) Interval() time.Duration { return time.Hour }

// Run evaluates every source and records a deduped dq event per stale source.
// Never fails the fleet on a dq write hiccup — a failed audit is logged, and
// the honest count is still returned.
func (w *SourceAuditor) Run(ctx context.Context) (string, error) {
	now := time.Now()
	reports, err := srchealth.Evaluate(ctx, w.St, now, w.WebhookSecretSet)
	if err != nil {
		return "", fmt.Errorf("evaluate sources: %w", err)
	}
	stale := 0
	for _, r := range reports {
		if !r.Stale {
			continue
		}
		stale++
		if err := w.recordDQ(ctx, r, now); err != nil {
			slog.Warn("source-audit: record dq", "source", r.Source, "err", err)
		}
	}
	if stale == 0 {
		return fmt.Sprintf("all %d sources fresh", len(reports)), nil
	}
	return fmt.Sprintf("%d/%d sources STALE", stale, len(reports)), nil
}

// recordDQ inserts a "source_stale" dq_event unless one for the same source
// already exists today (dedup per source per UTC day — the auditor runs hourly).
func (w *SourceAuditor) recordDQ(ctx context.Context, r srchealth.Report, now time.Time) error {
	dayStart := time.Date(now.UTC().Year(), now.UTC().Month(), now.UTC().Day(), 0, 0, 0, 0, time.UTC).Unix()
	prefix := "source=" + r.Source + " "
	var n int
	err := w.St.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM dq_events WHERE kind='source_stale' AND ts >= ? AND detail LIKE ?`,
		dayStart, prefix+"%").Scan(&n)
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	return w.St.InsertDQ(ctx, md.DQEvent{
		Ts:     now.Unix(),
		Kind:   "source_stale",
		Detail: srchealth.DetailLine(r),
	})
}
