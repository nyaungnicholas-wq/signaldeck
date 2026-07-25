// ScoresCompactor — intraday compaction for the derived score tables.
//
// DerivedRetention (maintain.go, appended wave) bounds the FAR tier: full
// archive+prune of scores/score_outcomes at 90d. This worker bounds the NEAR
// tier, which is what actually dominates the database today: at the 10-minute
// full-universe cadence, scores rows carry ~1KB of components JSON and
// composite_scores rows ~3KB of payload JSON that only the LATEST row per
// symbol ever renders (measured 2026-07-16: scores = 3.2GB of a 5.4GB file,
// with EVERY row younger than the 90d far tier).
//
// Tier ladder after this worker (all archive-before-transform, fail-safe):
//
//	0-2d      full rows (components/payload hot — symbol pages render them)
//	2d-30d    numeric rows only (blobs stripped; full rows in cold archive)
//	30d-90d   one row per (symbol, horizon, UTC-day) — the daily-last
//	>90d      cold archive only (DerivedRetention's tier)
//
// FAIL-SAFE, structural on BOTH transforms (council rounds 1-2):
//   - STRIP: the update targets the EXACT key set that was just durably
//     archived (atomic .tmp→rename), so strip-set == archive-set by
//     construction — no concurrent write can enlarge it. On any archive error
//     the strip is skipped and a dq_events(archive_skip) row records it.
//   - DESTROY (daily-downsample): the DELETEs refuse any row still carrying
//     its blob (sentinel predicates components='[]' / payload='{}'), and a
//     stripped row is by construction an archived row — so no archive failure,
//     crash, or window misconfig can destroy an un-archived blob.
//
// Each (symbol, horizon)'s newest row is carved out of stripping so a delisted
// or stalled symbol keeps its last rendered decomposition. The freed pages
// return to the filesystem via the StorageGovernor's threshold VACUUM (which
// prechecks disk headroom before rewriting the file).
package maintain

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/archive"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// ScoresCompactor strips heavy JSON past KeepHeavy and daily-downsamples
// intraday rows past KeepIntraday, for scores and composite_scores.
type ScoresCompactor struct {
	St  *store.Store
	Arc *archive.Archiver // cold-archive sink (required for archive-before-strip)
	// Windows (0 ⇒ env/default). Explicit so tests drive exact cutoffs.
	KeepHeavy    time.Duration // full blobs hot; default 2d (env SIGNALDECK_SCORES_HEAVY_RETENTION_D)
	KeepIntraday time.Duration // intraday rows hot; default 30d (env SIGNALDECK_SCORES_INTRADAY_RETENTION_D)
}

// Name implements workers.Worker.
func (c *ScoresCompactor) Name() string { return "scores-compactor" }

// Interval implements workers.Worker.
func (c *ScoresCompactor) Interval() time.Duration { return time.Hour }

// compactBatch bounds rows read+archived+stripped per pass (var for tests).
var compactBatch = 50000

// Run compacts both tables; each tier is independently fail-safe.
func (c *ScoresCompactor) Run(ctx context.Context) (string, error) {
	now := time.Now()
	names, err := c.St.SymbolNameMap(ctx)
	if err != nil {
		return "", err
	}
	keepHeavy, keepIntraday := c.retentionHeavy(), c.retentionIntraday()
	// Window sanity (council-mandated): the intraday prune tier must sit
	// STRICTLY beyond the strip tier, or rows could reach the prune cutoff
	// with their blobs never stripped/archived. A misconfig is clamped (prune
	// pushed out to heavy+7d) and recorded — never obeyed.
	if keepIntraday <= keepHeavy {
		clamped := keepHeavy + 7*24*time.Hour
		c.dqSkip(ctx, now, "config", fmt.Sprintf("SIGNALDECK_SCORES_INTRADAY_RETENTION_D (%v) <= HEAVY (%v) — clamped prune tier to %v", keepIntraday, keepHeavy, clamped))
		keepIntraday = clamped
	}
	heavyCut := now.Add(-keepHeavy).Unix()
	intradayCut := now.Add(-keepIntraday).Unix()

	// ── scores: archive-then-strip components, batched ──
	// The strip targets the EXACT archived key set, so no ts-window trimming or
	// giant-ts-group special case is needed: whatever this batch archived is
	// precisely what it strips (council round 2 — the window form had a TOCTOU).
	stripped, skipped := 0, false
	for {
		rows, err := c.St.ScoresHeavyBelow(ctx, heavyCut, compactBatch)
		if err != nil {
			return "", err
		}
		if len(rows) == 0 {
			break
		}
		if _, err := c.Arc.ArchiveScores(ctx, rows, names); err != nil {
			c.dqSkip(ctx, now, "scores", err.Error())
			skipped = true
			break
		}
		n, err := c.St.StripScoreComponents(ctx, rows)
		if err != nil {
			return "", err
		}
		stripped += int(n)
	}

	// ── composite_scores: identical archive-then-strip ──
	strippedComp := 0
	for {
		rows, err := c.St.CompositeHeavyBelow(ctx, heavyCut, compactBatch)
		if err != nil {
			return "", err
		}
		if len(rows) == 0 {
			break
		}
		if _, err := c.Arc.ArchiveComposite(ctx, rows, names); err != nil {
			c.dqSkip(ctx, now, "composite_scores", err.Error())
			skipped = true
			break
		}
		n, err := c.St.StripCompositePayload(ctx, rows)
		if err != nil {
			return "", err
		}
		strippedComp += int(n)
	}

	// ── daily-downsample the already-stripped intraday tier ──
	// DEFENSE IN DEPTH (council-mandated, unanimous): (1) the store DELETEs
	// refuse any row still carrying its blob (components/payload sentinel — a
	// stripped row is by construction an archived row), so the invariant holds
	// even if this worker misbehaves; (2) the prunes are additionally skipped
	// outright when THIS pass had an archive failure; (3) the windows were
	// sanity-ordered above, so a misconfig cannot open a strip-free gap.
	var prunedScores, prunedComp int64
	if !skipped {
		var err error
		prunedScores, err = c.St.PruneScoresKeepDailyLast(ctx, intradayCut)
		if err != nil {
			return "", err
		}
		prunedComp, err = c.St.PruneCompositeKeepDailyLast(ctx, intradayCut)
		if err != nil {
			return "", err
		}
	}

	msg := fmt.Sprintf("stripped %d scores + %d composite blobs; daily-downsampled %d + %d intraday rows",
		stripped, strippedComp, prunedScores, prunedComp)
	if skipped {
		msg += " (SOME STRIPS SKIPPED — archive failed, blobs retained; see dq)"
	}
	return msg, nil
}

func (c *ScoresCompactor) retentionHeavy() time.Duration {
	if c.KeepHeavy > 0 {
		return c.KeepHeavy
	}
	return time.Duration(envIntOr("SIGNALDECK_SCORES_HEAVY_RETENTION_D", 2)) * 24 * time.Hour
}

func (c *ScoresCompactor) retentionIntraday() time.Duration {
	if c.KeepIntraday > 0 {
		return c.KeepIntraday
	}
	return time.Duration(envIntOr("SIGNALDECK_SCORES_INTRADAY_RETENTION_D", 30)) * 24 * time.Hour
}

// dqSkip logs + records the fail-safe (mirrors Downsampler/DerivedRetention).
func (c *ScoresCompactor) dqSkip(ctx context.Context, now time.Time, table, reason string) {
	slog.Warn("scores compaction skipped (fail-safe)", "table", table, "reason", reason)
	_ = c.St.InsertDQ(ctx, md.DQEvent{
		Ts:     now.Unix(),
		Kind:   "archive_skip",
		Detail: fmt.Sprintf("compaction of %s skipped, blobs retained: %s", table, reason),
	})
}
