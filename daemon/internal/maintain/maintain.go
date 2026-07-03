// Package maintain holds the housekeeping agents: bar rollups + retention,
// score-outcome resolution (the honesty backtest's feeder), and the
// data-quality auditor. All are periodic workers.
package maintain

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/archive"
	"github.com/nyaungnicholas-wq/signaldeck/internal/marketcal"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// ── Downsampler ─────────────────────────────────────────────────────────

// Downsampler rolls 1m bars into 1h and enforces retention with COMPACTION,
// not deletion: every 1m bar past retention is rolled into its 1h bucket
// BEFORE it is pruned, so information is compacted, never lost. Daily bars
// are never pruned (enforced in store.PruneBars, not just here). It NEVER
// builds daily bars from intraday rollups — official daily bars come from
// the sources (Alpaca 1Day, Kraken interval=1440), so a partial trading day
// can never masquerade as a real daily bar.
//
// snapshots_1s are bid/ask quote midpoints, not trades, so "rolling them up"
// into 1m OHLCV would fabricate bars that collide with the real exchange 1m
// bars already ingested. They are also REDUNDANT with the crypto 1m bars we
// ingest — so they keep only a short hot window, then are ARCHIVED to cold
// gzip-CSV storage and pruned. Nothing is deleted without a durable archive.
//
// TIERED RETENTION (every window env-tunable; see fields below): snapshots_1s
// keep a short hot window (6h) → archive+prune; 1m bars keep ~60d → compact to
// 1h (information preserved) + archive the raw → prune; 1h bars keep ~3y →
// compact to 1d + archive → prune; DAILY bars are the permanent record and are
// NEVER pruned (enforced in store.PruneBars, not just here). Archive-before-
// prune is FAIL-SAFE: if the archive write errors, the matching prune is
// SKIPPED so data is never lost silently (a dq event + log record the skip).
type Downsampler struct {
	St  *store.Store
	Arc *archive.Archiver // cold-archive sink (required for archive-before-prune)
	// Retention windows (0 ⇒ env/default). Kept explicit so tests can drive
	// exact cutoffs without touching the environment.
	Keep1m    time.Duration // 1m bars kept hot; default 60d (env SIGNALDECK_1M_RETENTION_D)
	Keep1h    time.Duration // 1h bars kept hot; default 3y  (env SIGNALDECK_1H_RETENTION_D)
	KeepSnaps time.Duration // snapshots_1s hot window; default 6h (env SIGNALDECK_SNAP_RETENTION_H)
}

// archiveBatch bounds how many rows are read+archived+pruned per pass so a huge
// first-run backlog never buffers a whole table into memory. A var (not const)
// so tests can shrink it to exercise the multi-batch boundary path.
var archiveBatch = 50000

// Name implements workers.Worker.
func (d *Downsampler) Name() string { return "downsampler" }

// Interval implements workers.Worker.
func (d *Downsampler) Interval() time.Duration { return 5 * time.Minute }

// Run rolls up recent minutes, then enforces tiered retention with
// archive-before-prune at every tier.
func (d *Downsampler) Run(ctx context.Context) (string, error) {
	syms, err := d.St.ListSymbols(ctx, false)
	if err != nil {
		return "", err
	}
	now := time.Now()
	// Align the window's leading edge DOWN to an hour boundary. Rollup's
	// open/close subqueries are not ts-bounded, so a bucket only partially
	// covered by [from,to) gets full-bucket open/close but tail-only
	// high/low/volume — and INSERT OR REPLACE would overwrite the previously
	// correct 1h bar with corrupt aggregates. A boundary-aligned `from` means
	// every historical bucket is fully covered; only the current in-progress
	// hour is partial, which is correct (it is re-rolled every pass).
	from := (now.Add(-72*time.Hour).Unix() / 3600) * 3600
	for _, s := range syms {
		if err := d.St.Rollup(ctx, s.ID, md.TF1m, md.TF1h, 3600, from, now.Unix()); err != nil {
			return "", fmt.Errorf("rollup %s: %w", s.Symbol, err)
		}
	}

	keep1m := d.retention1m()
	keep1h := d.retention1h()
	keepSnaps := d.retentionSnaps()

	// COMPACTION, NOT DELETION: before pruning 1m bars past retention, roll
	// every one of them into its 1h bucket so the information survives in
	// compact form. The prune cutoff is aligned DOWN to an hour boundary so
	// (a) every pruned minute sits inside a bucket the rollup fully covered,
	// and (b) no partially-covered bucket is ever written (same invariant as
	// the recent-window rollup above). Only symbols that actually hold 1m
	// bars older than the cutoff pay the rollup cost — after the first pass
	// their history is compacted and this is a no-op.
	cutoff1m := (now.Add(-keep1m).Unix() / 3600) * 3600
	for _, s := range syms {
		n, minTs, _, err := d.St.BarCount(ctx, s.ID, md.TF1m)
		if err != nil {
			return "", err
		}
		if n == 0 || minTs >= cutoff1m {
			continue // nothing older than retention; nothing to compact
		}
		fromOld := (minTs / 3600) * 3600
		// OR IGNORE: an already-present 1h bar (backfilled from the source)
		// is authoritative; only fill buckets that would otherwise be lost.
		if err := d.St.RollupMissing(ctx, s.ID, md.TF1m, md.TF1h, 3600, fromOld, cutoff1m); err != nil {
			return "", fmt.Errorf("compaction rollup %s: %w", s.Symbol, err)
		}
	}

	// 1h → 1d compaction before the 1h prune: bucket to the calendar day so no
	// information is lost when the raw 1h bars past ~3y are archived + pruned.
	// (Daily itself is never pruned; store.PruneBars refuses tf=1d.)
	cutoff1h := (now.Add(-keep1h).Unix() / 86400) * 86400
	for _, s := range syms {
		n, minTs, _, err := d.St.BarCount(ctx, s.ID, md.TF1h)
		if err != nil {
			return "", err
		}
		if n == 0 || minTs >= cutoff1h {
			continue
		}
		fromOld := (minTs / 86400) * 86400
		if err := d.St.RollupMissing(ctx, s.ID, md.TF1h, md.TF1d, 86400, fromOld, cutoff1h); err != nil {
			return "", fmt.Errorf("compaction rollup 1h→1d %s: %w", s.Symbol, err)
		}
	}

	names, err := d.St.SymbolNameMap(ctx)
	if err != nil {
		return "", err
	}

	// ARCHIVE-BEFORE-PRUNE at each tier. archivePruneBars/Snaps archive every
	// row below the cutoff to cold gzip-CSV storage FIRST and only prune what
	// was durably archived — on any archive error it prunes NOTHING for that
	// tier and records a dq event, so retention can never silently lose data.
	prunedMin, minSkipped := d.archivePruneBars(ctx, md.TF1m, cutoff1m, names, now)
	pruned1h, hourSkipped := d.archivePruneBars(ctx, md.TF1h, cutoff1h, names, now)
	prunedSnaps, snapSkipped := d.archivePruneSnaps(ctx, now.Add(-keepSnaps).Unix(), names, now)

	if err := d.St.PruneWorkerRuns(ctx, 2000); err != nil {
		return "", err
	}

	msg := fmt.Sprintf("rolled up %d symbols; archived+pruned %d 1m, %d 1h bars, %d snaps",
		len(syms), prunedMin, pruned1h, prunedSnaps)
	if minSkipped || hourSkipped || snapSkipped {
		msg += " (SOME PRUNES SKIPPED — archive failed, data retained; see dq)"
	}
	return msg, nil
}

// retention1m / retention1h / retentionSnaps resolve the tier windows: the
// explicit field wins (for tests), else the env var, else the default.
func (d *Downsampler) retention1m() time.Duration {
	if d.Keep1m > 0 {
		return d.Keep1m
	}
	return time.Duration(envIntOr("SIGNALDECK_1M_RETENTION_D", 60)) * 24 * time.Hour
}

func (d *Downsampler) retention1h() time.Duration {
	if d.Keep1h > 0 {
		return d.Keep1h
	}
	return time.Duration(envIntOr("SIGNALDECK_1H_RETENTION_D", 3*365)) * 24 * time.Hour
}

func (d *Downsampler) retentionSnaps() time.Duration {
	if d.KeepSnaps > 0 {
		return d.KeepSnaps
	}
	return time.Duration(envIntOr("SIGNALDECK_SNAP_RETENTION_H", 6)) * time.Hour
}

// archivePruneBars archives every bar of tf below cutoff to cold storage in
// bounded batches, then prunes ONLY after a successful archive of that batch.
// Returns the number of rows pruned and whether any prune was skipped because
// the archive failed (fail-safe: rows stay in the hot store, a dq event fires).
func (d *Downsampler) archivePruneBars(ctx context.Context, tf md.Timeframe, cutoff int64, names map[int64]string, now time.Time) (pruned int64, skipped bool) {
	if d.Arc == nil {
		// No archive sink wired: fail safe by NOT pruning (never lose data).
		d.dqSkip(ctx, now, string(tf), "no cold-archive sink configured")
		return 0, true
	}
	for {
		rows, err := d.St.BarsBelow(ctx, tf, cutoff, archiveBatch)
		if err != nil {
			d.dqSkip(ctx, now, string(tf), "read for archive failed: "+err.Error())
			return pruned, true
		}
		if len(rows) == 0 {
			return pruned, skipped
		}
		// When the batch is FULL there may be MORE rows sharing the max ts that
		// LIMIT cut off. Archiving the whole batch and pruning [<maxTs] would
		// leave those extra rows to be re-read and re-archived next pass (a
		// duplicate in cold storage), while archiving the whole batch and
		// pruning [<=maxTs] would delete the cut-off rows WITHOUT archiving them
		// (data loss). Fix: on a full batch, drop the trailing max-ts group so
		// the archived set == the pruned set exactly; that group is picked up,
		// whole, on the next read. Safe + terminating: a 50k batch spans many
		// timestamps (≤ #symbols rows per ts), so the retained head is non-empty
		// and maxTs strictly advances.
		full := len(rows) == archiveBatch
		prune := rows
		var upper int64 = cutoff // exclusive prune bound
		if full {
			maxTs := rows[len(rows)-1].Ts
			cut := len(rows)
			for cut > 0 && rows[cut-1].Ts == maxTs {
				cut--
			}
			if cut == 0 {
				// Degenerate: a whole batch at ONE ts (would loop forever).
				// Archive+prune it inclusively — correct because every row at
				// this ts is in hand only if we read them all; guard by reading
				// the full ts group is overkill here since #rows per ts ≪ batch.
				upper = maxTs + 1
			} else {
				prune = rows[:cut]
				upper = maxTs // exclusive: the max-ts group waits for next read
			}
		}
		if _, err := d.Arc.ArchiveBars(ctx, tf, prune, names); err != nil {
			// FAIL SAFE: archive failed → do NOT prune this (or any) batch.
			d.dqSkip(ctx, now, string(tf), "archive write failed: "+err.Error())
			return pruned, true
		}
		n, err := d.St.PruneBars(ctx, tf, upper)
		if err != nil {
			return pruned, true
		}
		pruned += n
		if !full {
			return pruned, skipped // drained everything below cutoff
		}
	}
}

// archivePruneSnaps is archivePruneBars for snapshots_1s.
func (d *Downsampler) archivePruneSnaps(ctx context.Context, cutoff int64, names map[int64]string, now time.Time) (pruned int64, skipped bool) {
	if d.Arc == nil {
		d.dqSkip(ctx, now, "snapshots_1s", "no cold-archive sink configured")
		return 0, true
	}
	for {
		rows, err := d.St.SnapsBelow(ctx, cutoff, archiveBatch)
		if err != nil {
			d.dqSkip(ctx, now, "snapshots_1s", "read for archive failed: "+err.Error())
			return pruned, true
		}
		if len(rows) == 0 {
			return pruned, skipped
		}
		// Same trailing-max-ts trim as archivePruneBars (see its comment).
		full := len(rows) == archiveBatch
		prune := rows
		var upper int64 = cutoff
		if full {
			maxTs := rows[len(rows)-1].Ts
			cut := len(rows)
			for cut > 0 && rows[cut-1].Ts == maxTs {
				cut--
			}
			if cut == 0 {
				upper = maxTs + 1
			} else {
				prune = rows[:cut]
				upper = maxTs
			}
		}
		if _, err := d.Arc.ArchiveSnapshots(ctx, prune, names); err != nil {
			d.dqSkip(ctx, now, "snapshots_1s", "archive write failed: "+err.Error())
			return pruned, true
		}
		n, err := d.St.PruneSnaps(ctx, upper)
		if err != nil {
			return pruned, true
		}
		pruned += n
		if !full {
			return pruned, skipped
		}
	}
}

// dqSkip logs and records a data-quality event when a prune is skipped because
// its archive step failed — the fail-safe is observable, never silent.
func (d *Downsampler) dqSkip(ctx context.Context, now time.Time, table, reason string) {
	slog.Warn("retention prune skipped (fail-safe)", "table", table, "reason", reason)
	_ = d.St.InsertDQ(ctx, md.DQEvent{
		Ts:     now.Unix(),
		Kind:   "archive_skip",
		Detail: fmt.Sprintf("prune of %s skipped, data retained: %s", table, reason),
	})
}

// RetentionSnapsHours / Retention1mDays / Retention1hDays expose the ACTIVE
// tiered-retention windows (env-resolved, in operator units) so the storage
// report can show exactly how long each tier stays hot. They mirror the same
// env vars + defaults the Downsampler uses, keeping one source of truth.
func RetentionSnapsHours() int { return envIntOr("SIGNALDECK_SNAP_RETENTION_H", 6) }
func Retention1mDays() int     { return envIntOr("SIGNALDECK_1M_RETENTION_D", 60) }
func Retention1hDays() int     { return envIntOr("SIGNALDECK_1H_RETENTION_D", 3*365) }

// envIntOr parses an integer env var, returning def on empty/invalid input.
func envIntOr(k string, def int) int {
	v := os.Getenv(k)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return def
	}
	return n
}

// ── OutcomeResolver ─────────────────────────────────────────────────────

// OutcomeResolver fills score_outcomes with realized forward returns once a
// score's horizon window has closed. The forward window is anchored to the
// ACTUAL bar the score was made against (base = bar at-or-before the score),
// so it is correct whether daily bars open at 00:00 UTC (crypto) or ~04:00/
// 05:00 UTC (US equities = midnight ET). Weekends/holidays are handled by
// taking the first bar at-or-after base+horizon.
type OutcomeResolver struct {
	St *store.Store
}

// Name implements workers.Worker.
func (o *OutcomeResolver) Name() string { return "outcome-resolver" }

// Interval implements workers.Worker.
func (o *OutcomeResolver) Interval() time.Duration { return 10 * time.Minute }

func horizonSeconds(h md.Horizon) int64 {
	switch h {
	case md.H1h:
		return 3600
	case md.H1d:
		return 86400
	default:
		return 7 * 86400 // calendar week ≈ 5 trading days via at-or-after
	}
}

func horizonTF(h md.Horizon) md.Timeframe {
	if h == md.H1h {
		return md.TF1m
	}
	return md.TF1d
}

// Run resolves matured outcomes, per horizon so short windows never starve
// behind the large immature 1w backlog.
func (o *OutcomeResolver) Run(ctx context.Context) (string, error) {
	now := time.Now().Unix()
	resolved, voided, waiting := 0, 0, 0
	for _, h := range md.Horizons {
		tf := horizonTF(h)
		// Only fetch rows old enough that the window COULD have closed.
		pending, err := o.St.UnresolvedOutcomesByHorizon(ctx, h, now-horizonSeconds(h), 1500)
		if err != nil {
			return "", err
		}
		for _, p := range pending {
			base, okBase, err := o.St.BarAtOrBefore(ctx, p.SymbolID, tf, p.Ts)
			if err != nil {
				return "", err
			}
			if !okBase {
				// No base bar: unusual (score implies data). Void if very old.
				if now-p.Ts > 30*24*3600 {
					if err := o.St.ResolveOutcomeVoid(ctx, p.SymbolID, h, p.Ts); err != nil {
						return "", err
					}
					voided++
				} else {
					waiting++
				}
				continue
			}
			// Anchor the window to the base bar, not to a UTC-midnight guess.
			target := base.Ts + horizonSeconds(h)
			if now < target {
				waiting++
				continue
			}
			fwd, okFwd, err := o.St.BarAtOrAfter(ctx, p.SymbolID, tf, target)
			if err != nil {
				return "", err
			}
			switch {
			case okFwd && base.Close > 0:
				// A forward bar far past the target means a data/session hole;
				// resolving would mislabel a multi-period move as one horizon.
				if fwd.Ts-target > 3*horizonSeconds(h) {
					if err := o.St.ResolveOutcomeVoid(ctx, p.SymbolID, h, p.Ts); err != nil {
						return "", err
					}
					voided++
					continue
				}
				ret := fwd.Close/base.Close - 1
				if err := o.St.ResolveOutcome(ctx, p.SymbolID, h, p.Ts, ret); err != nil {
					return "", err
				}
				resolved++
			case now-p.Ts > 30*24*3600:
				if err := o.St.ResolveOutcomeVoid(ctx, p.SymbolID, h, p.Ts); err != nil {
					return "", err
				}
				voided++
			default:
				waiting++ // forward data not in yet; next pass
			}
		}
	}
	return fmt.Sprintf("resolved %d, voided %d, waiting %d", resolved, voided, waiting), nil
}

// ── DQAuditor ───────────────────────────────────────────────────────────

// DQAuditor detects stale feeds and bar gaps per active symbol and records
// them as dq_events (rate-limited via meta keys, one per symbol per hour).
type DQAuditor struct {
	St *store.Store
}

// Name implements workers.Worker.
func (a *DQAuditor) Name() string { return "dq-auditor" }

// Interval implements workers.Worker.
func (a *DQAuditor) Interval() time.Duration { return 5 * time.Minute }

// Run checks freshness for every active symbol.
func (a *DQAuditor) Run(ctx context.Context) (string, error) {
	syms, err := a.St.ListSymbols(ctx, true)
	if err != nil {
		return "", err
	}
	now := time.Now()
	flagged := 0
	for _, s := range syms {
		latest, err := a.St.LatestBarTs(ctx, s.ID, md.TF1m)
		if err != nil {
			return "", err
		}
		var stale bool
		var detail string
		age := now.Unix() - latest
		switch s.Market {
		case md.Crypto:
			stale = latest > 0 && age > 45*60 // Kraken minute refresh cadence + slack
			detail = fmt.Sprintf("last 1m bar %dm old (crypto trades 24/7)", age/60)
		case md.Stocks:
			// Only an incident when the NYSE is actually open for bars:
			// marketcal excludes weekends, holidays, and post-close hours,
			// and closes half-days at 1:00pm ET — so a market holiday like
			// July 4th no longer false-flags every symbol.
			stale = latest > 0 && age > 20*60 && marketcal.OpenForBars(now)
			detail = fmt.Sprintf("last 1m bar %dm old during market hours", age/60)
		}
		if !stale {
			continue
		}
		// Rate-limit: one event per symbol per hour.
		key := "dq_last_stale_" + s.Symbol + string(s.Market)
		if lastStr, _ := a.St.GetMeta(ctx, key); lastStr != "" {
			if last, _ := strconv.ParseInt(lastStr, 10, 64); now.Unix()-last < 3600 {
				continue
			}
		}
		if err := a.St.SetMeta(ctx, key, strconv.FormatInt(now.Unix(), 10)); err != nil {
			return "", err
		}
		sid := s.ID
		if err := a.St.InsertDQ(ctx, md.DQEvent{SymbolID: &sid, Ts: now.Unix(), Kind: "stale", Detail: detail}); err != nil {
			return "", err
		}
		flagged++
	}
	return fmt.Sprintf("checked %d active symbols, flagged %d", len(syms), flagged), nil
}

// ── StorageGovernor ─────────────────────────────────────────────────────

// StorageGovernor keeps on-disk footprint bounded independently of the logical
// retention the Downsampler enforces:
//
//   - WAL CHECKPOINT (TRUNCATE) every pass — a busy WAL database grows its
//     -wal sidecar without bound until a checkpoint flushes it back into the
//     main file; TRUNCATE also returns that space to the filesystem. This is
//     cheap and always safe, so it runs unconditionally.
//   - VACUUM when the database file has grown past VacuumThreshold — the DB is
//     auto_vacuum=NONE, so pages freed by retention deletes are reused but
//     never returned to disk until a VACUUM rewrites the file. VACUUM briefly
//     takes a write lock, so it is gated behind a size threshold and a minimum
//     interval (meta cursor) to run rarely.
//
// It is a lightweight standalone worker (registered in the run.go fleet).
type StorageGovernor struct {
	St *store.Store
	// VacuumThreshold: DB byte size above which a VACUUM is considered
	// (0 ⇒ env SIGNALDECK_VACUUM_THRESHOLD_MB, default 2048 MB).
	VacuumThreshold int64
	// MinVacuumInterval: minimum wall time between VACUUMs (0 ⇒ 24h).
	MinVacuumInterval time.Duration
}

// Name implements workers.Worker.
func (g *StorageGovernor) Name() string { return "storage-governor" }

// Interval implements workers.Worker.
func (g *StorageGovernor) Interval() time.Duration { return time.Hour }

// Run checkpoints the WAL and, when warranted, vacuums.
func (g *StorageGovernor) Run(ctx context.Context) (string, error) {
	if err := g.St.WALCheckpointTruncate(ctx); err != nil {
		return "", fmt.Errorf("wal checkpoint: %w", err)
	}
	dbBytes, walBytes := g.St.FileSizes()

	threshold := g.VacuumThreshold
	if threshold == 0 {
		threshold = int64(envIntOr("SIGNALDECK_VACUUM_THRESHOLD_MB", 2048)) * 1024 * 1024
	}
	minInterval := g.MinVacuumInterval
	if minInterval == 0 {
		minInterval = 24 * time.Hour
	}

	vacuumed := false
	if dbBytes >= threshold {
		last, _ := g.St.GetMeta(ctx, "storage_last_vacuum")
		var lastTs int64
		if last != "" {
			lastTs, _ = strconv.ParseInt(last, 10, 64)
		}
		if time.Since(time.Unix(lastTs, 0)) >= minInterval {
			if err := g.St.Vacuum(ctx); err != nil {
				return "", fmt.Errorf("vacuum: %w", err)
			}
			_ = g.St.SetMeta(ctx, "storage_last_vacuum", strconv.FormatInt(time.Now().Unix(), 10))
			vacuumed = true
			dbBytes, walBytes = g.St.FileSizes()
		}
	}
	return fmt.Sprintf("checkpointed wal; db=%.1fMB wal=%.1fMB vacuumed=%v",
		float64(dbBytes)/(1024*1024), float64(walBytes)/(1024*1024), vacuumed), nil
}
