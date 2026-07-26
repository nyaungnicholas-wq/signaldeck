// SURVIVORSHIP WAVE (2026-07-26) — the worker that actually populates
// symbols.delisted_at.
//
// # Why this exists
//
// The survivorship machinery was already built: delisted_at, MarkDelisted,
// TradableAt, ResearchUniverse, UniverseCoverage — all present, all tested. An
// adversarial audit then found the column empty fleet-wide (0 of 1,080 symbols)
// because NOTHING EVER CALLED MarkDelisted. Every study on this platform was
// therefore still measured on survivors while the code documented a control that
// did not run. That is worse than not claiming the control: a reader takes the
// comment as evidence.
//
// # The detection rule, and the guard that makes it safe
//
// A stock is treated as delisted when its newest DAILY bar is older than
// staleSessions trading days. Daily bars are pruning-protected in code, so "no
// recent daily bar" is a statement about the market rather than about our
// retention — which is the only reason this evidence is usable at all.
//
// The load-bearing part is the guard, not the rule. Our own ingestion failing
// looks EXACTLY like the whole market delisting at once, so before marking
// anything the worker checks fleet liveness: a healthy majority of stocks must
// have printed a bar recently. If they have not, the pipeline is broken, and the
// worker marks NOTHING and says so. Without that check the first Alpaca outage
// would have declared the entire universe dead and permanently corrupted every
// point-in-time universe built afterwards.
//
// # Delisting is reversible
//
// A symbol that prints a bar again was never delisted — it was halted, or our
// fetch was failing. The marker is cleared on that evidence. A one-way marker
// would turn every false positive into a permanent market "fact".
//
// The recorded timestamp is the symbol's LAST BAR, not the detection time: the
// market fact is when it stopped trading, and dating it "today" would place a
// still-tradable name outside a point-in-time universe for the weeks between.
package pipeline

import (
	"context"
	"fmt"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

const (
	// staleSessions is how many trading days of silence mark a stock delisted.
	// Deliberately generous: a halt (SEC suspensions run 10 sessions) must NOT
	// register, and a false positive costs more than a late detection because it
	// removes a tradable name from every historical universe.
	staleSessions = 25
	// fleetLiveFraction is the share of stocks that must have printed a bar
	// within staleSessions before ANY symbol may be marked. Below it the fault
	// is ours, not the market's.
	fleetLiveFraction = 0.60
	// minFleetSize is the floor below which fleet liveness cannot be judged.
	minFleetSize = 50
	// sessionsPerCalendarDay converts trading sessions to calendar days (5
	// trading days per 7 calendar days), plus slack for holidays.
	calendarDaysFor25Sessions = 25*7/5 + 7
)

// DelistingDetector marks stocks that have stopped trading, so a point-in-time
// universe can be rebuilt and survivorship bias can finally be measured.
type DelistingDetector struct {
	St  *store.Store
	Now func() time.Time
}

func (w *DelistingDetector) Name() string { return "delisting-detector" }

// Interval is 24h: delisting is a daily-resolution market fact and the whole
// point of staleSessions is that nothing here is urgent.
func (w *DelistingDetector) Interval() time.Duration { return 24 * time.Hour }

func (w *DelistingDetector) Run(ctx context.Context) (string, error) {
	now := time.Now()
	if w.Now != nil {
		now = w.Now()
	}
	rows, err := w.St.StockLastBars(ctx)
	if err != nil {
		return "", err
	}
	if len(rows) == 0 {
		return "no stock symbols yet", nil
	}

	cutoff := now.AddDate(0, 0, -calendarDaysFor25Sessions).Unix()

	// Fleet liveness FIRST. Marking on a broken pipeline is the one failure this
	// worker must never commit, so the check runs before any decision is taken.
	live := 0
	for _, r := range rows {
		if r.LastTs >= cutoff {
			live++
		}
	}
	if len(rows) < minFleetSize {
		return fmt.Sprintf("fleet too small to judge liveness (%d < %d) — nothing marked", len(rows), minFleetSize), nil
	}
	frac := float64(live) / float64(len(rows))
	if frac < fleetLiveFraction {
		// This is the honest reading: our ingestion is behind, not the market.
		_ = w.St.InsertDQ(ctx, md.DQEvent{
			Ts:   now.Unix(),
			Kind: "delisting_check_skipped",
			Detail: fmt.Sprintf("only %.0f%% of %d stocks printed a bar within %d sessions — "+
				"treating this as an ingestion fault, not %d delistings; nothing marked",
				frac*100, len(rows), staleSessions, len(rows)-live),
		})
		return fmt.Sprintf("fleet liveness %.0f%% below %.0f%% — ingestion fault suspected, nothing marked",
			frac*100, fleetLiveFraction*100), nil
	}

	var marked, cleared int
	for _, r := range rows {
		switch {
		case r.LastTs == 0:
			// Never printed a bar at all. That is a subscription/backfill state,
			// not evidence of delisting — the symbol may simply be new or have
			// failed its first fetch. Refuse to guess.
			continue

		case r.LastTs < cutoff && r.DelistedAt == 0:
			// Stopped printing while the fleet kept printing. Date the fact at
			// the last bar, not today.
			if err := w.St.MarkDelisted(ctx, r.SymbolID, r.LastTs); err != nil {
				return "", err
			}
			marked++

		case r.LastTs >= cutoff && r.DelistedAt != 0:
			// It came back. It was halted or our fetch was failing; either way
			// the marker was wrong and must not persist.
			if err := w.St.ClearDelisted(ctx, r.SymbolID); err != nil {
				return "", err
			}
			cleared++
		}
	}

	return fmt.Sprintf("fleet %.0f%% live; marked %d delisted, cleared %d resumed (of %d stocks)",
		frac*100, marked, cleared, len(rows)), nil
}
