// Package maintain holds the housekeeping agents: bar rollups + retention,
// score-outcome resolution (the honesty backtest's feeder), and the
// data-quality auditor. All are periodic workers.
package maintain

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/archive"
	"github.com/nyaungnicholas-wq/signaldeck/internal/envcfg"
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
// compact to 1d + archive → prune; anomalies keep ~90d hot → archive+prune
// (they are descriptive detections, not market history — no compaction form
// exists, so the full rows go to cold storage); DAILY bars are the permanent
// record and are NEVER pruned (enforced in store.PruneBars, not just here).
// Archive-before-prune is FAIL-SAFE: if the archive write errors, the matching
// prune is SKIPPED so data is never lost silently (a dq event + log record the
// skip).
type Downsampler struct {
	St  *store.Store
	Arc *archive.Archiver // cold-archive sink (required for archive-before-prune)
	// Retention windows (0 ⇒ env/default). Kept explicit so tests can drive
	// exact cutoffs without touching the environment.
	Keep1m    time.Duration // 1m bars kept hot; default 60d (env SIGNALDECK_1M_RETENTION_D)
	Keep1h    time.Duration // 1h bars kept hot; default 3y  (env SIGNALDECK_1H_RETENTION_D)
	KeepSnaps time.Duration // snapshots_1s hot window; default 6h (env SIGNALDECK_SNAP_RETENTION_H)
	KeepAnoms time.Duration // anomalies kept hot; default 90d (env SIGNALDECK_ANOM_RETENTION_D)
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
	prunedAnoms, anomSkipped := d.archivePruneAnomalies(ctx, now.Add(-d.retentionAnoms()).Unix(), names, now)

	if err := d.St.PruneWorkerRuns(ctx, 2000); err != nil {
		return "", err
	}

	msg := fmt.Sprintf("rolled up %d symbols; archived+pruned %d 1m, %d 1h bars, %d snaps, %d anomalies",
		len(syms), prunedMin, pruned1h, prunedSnaps, prunedAnoms)
	if minSkipped || hourSkipped || snapSkipped || anomSkipped {
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

func (d *Downsampler) retentionAnoms() time.Duration {
	if d.KeepAnoms > 0 {
		return d.KeepAnoms
	}
	return time.Duration(envIntOr("SIGNALDECK_ANOM_RETENTION_D", 90)) * 24 * time.Hour
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
		upper := cutoff // exclusive prune bound
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
			// Every OTHER failure path in this function calls dqSkip, which logs
			// and inserts an archive_skip DQ event. This one returned skipped=true
			// silently, so the caller emitted "SOME PRUNES SKIPPED - archive failed,
			// data retained; see dq" for a run where the ARCHIVE SUCCEEDED and the
			// PRUNE failed, and pointed the operator at a dq record that was never
			// written. Wrong cause, missing evidence, and retention quietly stops
			// reclaiming.
			d.dqSkip(ctx, now, string(tf), "prune failed after a successful archive: "+err.Error())
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
		upper := cutoff
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
			// See archivePruneBars: a silent skipped=true misreports the cause.
			d.dqSkip(ctx, now, "snapshots", "prune failed after a successful archive: "+err.Error())
			return pruned, true
		}
		pruned += n
		if !full {
			return pruned, skipped
		}
	}
}

// archivePruneAnomalies is archivePruneBars for the anomalies table (the
// ~90d-hot descriptive-detection tier). Identical fail-safe contract: any
// archive error skips the prune and records a dq event; nothing is ever
// deleted without a durable cold copy.
func (d *Downsampler) archivePruneAnomalies(ctx context.Context, cutoff int64, names map[int64]string, now time.Time) (pruned int64, skipped bool) {
	if d.Arc == nil {
		d.dqSkip(ctx, now, "anomalies", "no cold-archive sink configured")
		return 0, true
	}
	for {
		rows, err := d.St.AnomaliesBelow(ctx, cutoff, archiveBatch)
		if err != nil {
			d.dqSkip(ctx, now, "anomalies", "read for archive failed: "+err.Error())
			return pruned, true
		}
		if len(rows) == 0 {
			return pruned, skipped
		}
		// Same trailing-max-ts trim as archivePruneBars (see its comment).
		full := len(rows) == archiveBatch
		prune := rows
		upper := cutoff
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
		if _, err := d.Arc.ArchiveAnomalies(ctx, prune, names); err != nil {
			d.dqSkip(ctx, now, "anomalies", "archive write failed: "+err.Error())
			return pruned, true
		}
		n, err := d.St.PruneAnomalies(ctx, upper)
		if err != nil {
			// See archivePruneBars: a silent skipped=true misreports the cause.
			d.dqSkip(ctx, now, "anomalies", "prune failed after a successful archive: "+err.Error())
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
func RetentionAnomDays() int   { return envIntOr("SIGNALDECK_ANOM_RETENTION_D", 90) }

// envIntOr parses an integer env var, returning def on empty/invalid input.
// envIntOr reads a positive integer override, falling back to def.
//
// Every key that reaches this helper governs RETENTION — how long rows survive
// before a sweep deletes them — so a rejected override here is recorded as
// CRITICAL rather than merely logged. The dangerous direction is not a bad
// value that keeps too much data: it is an operator LENGTHENING a window to
// protect data, mistyping it, silently getting the shorter default, and having
// the sweep delete rows they meant to keep. That deletion is irreversible, and
// until 2026-08-11 nothing anywhere recorded that the override had been
// refused.
//
// The runtime behaviour is deliberately unchanged — the default still applies
// and the daemon still runs. Only the silence is fixed.
func envIntOr(k string, def int) int {
	v := os.Getenv(k)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		envcfg.RejectCritical(k, v, "not an integer", strconv.Itoa(def))
		return def
	}
	if n <= 0 {
		envcfg.RejectCritical(k, v, "must be > 0", strconv.Itoa(def))
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

// Run checks freshness for every active symbol that is still listed.
//
// THE DELISTED SKIP BELOW IS A SYMPTOM MASK, AND NAMING IT AS ONE IS THE POINT.
// The root fault is that 1,868 permanently-delisted rows carry active=1 at all:
// the delisting detector writes symbols.delisted_at but never clears `active`
// (store.MarkDelisted via pipeline/delisting.go), so roughly 45 callers of
// ListSymbols(ctx, true) — every scorer, poller and trainer in the fleet, not
// only this auditor — iterate the dead. Repairing the universe is the real fix
// and it does not live in this function.
//
// The guard earns its place anyway, on two grounds that survive that repair:
// freshness is UNDEFINED for a security that stopped trading, so an auditor
// that alarms on delisted names reports a tautology even on a perfectly groomed
// universe; and delisting is detected only once every 24h, so a name is
// legitimately delisted-and-still-active for up to a day however the universe
// bug is resolved. Defence in depth for the one monitor whose entire value is
// its signal-to-noise ratio.
func (a *DQAuditor) Run(ctx context.Context) (string, error) {
	syms, err := a.St.ListSymbols(ctx, true)
	if err != nil {
		return "", err
	}
	// Delisted names the paper book still holds, or that still carry an
	// unresolved graded outcome, are NOT in this set: a feed fault on those is
	// real and actionable. See store.DQSilencedSymbols.
	silenced, err := a.St.DQSilencedSymbols(ctx)
	if err != nil {
		return "", err
	}
	now := time.Now()
	flagged, skipped := 0, 0
	for _, s := range syms {
		if silenced[s.ID] {
			skipped++
			continue
		}
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
			//
			// ONLY the streamed hot set (stream=1) receives live 1m bars; the
			// broad daily-only universe is polled for DAILY bars every ~6h and
			// must be graded on those — grading it on 1m freshness false-flagged
			// ~500 symbols every sweep (measured 1,559 stale events/24h,
			// drowning real incidents).
			if s.Stream {
				stale = latest > 0 && age > 20*60 && marketcal.OpenForBars(now)
				detail = fmt.Sprintf("last 1m bar %dm old during market hours", age/60)
			} else {
				latestD, err := a.St.LatestBarTs(ctx, s.ID, md.TF1d)
				if err != nil {
					return "", err
				}
				// >4 calendar days with no daily bar spans any weekend or
				// single holiday; longer means the 6h poller is missing it.
				ageD := now.Unix() - latestD
				stale = latestD > 0 && ageD > 4*86400
				detail = fmt.Sprintf("last daily bar %dd old (daily-only universe)", ageD/86400)
			}
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
	return fmt.Sprintf("checked %d live symbols, flagged %d (%d delisted skipped)",
		len(syms)-skipped, flagged, skipped), nil
}

// ── StorageGovernor ─────────────────────────────────────────────────────

// StorageGovernor keeps on-disk footprint bounded independently of the logical
// retention the Downsampler enforces:
//
//   - WAL CHECKPOINT (TRUNCATE) every pass — a busy WAL database grows its
//     -wal sidecar without bound until a checkpoint flushes it back into the
//     main file; TRUNCATE also returns that space to the filesystem. Deferred
//     during US market hours (the read fleet denies it a reader-free window
//     anyway) unless the WAL is past walBusyAlertBytes; the pass cadence is
//     env-tunable via SIGNALDECK_WAL_CHECKPOINT_MIN (minutes, default 60).
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
	// Quiescer, when set, can hold the worker fleet still briefly so a TRUNCATE
	// checkpoint gets the reader-free instant it requires (*workers.Runner).
	// Nil = the top rung of the ladder is attempted unquiesced, exactly as
	// before — the ladder's lower rungs still run.
	Quiescer Quiescer
	// Now supplies the wall clock the market-hours gate reads. Nil ⇒ time.Now.
	//
	// The TRUNCATE rung is gated on marketcal.OpenForBars, so for roughly a
	// third of every weekday the ladder returns before TRUNCATE and no test
	// could reach the rung it exists to cover — TestStorageGovernorCheckpointLadder
	// silently asserted nothing during market hours, and additionally misread
	// the deferral message as a run. A seam here is the smallest way to make the
	// gated branch reachable on purpose rather than by the hour the suite ran.
	Now func() time.Time
}

// now reads the governor's clock, defaulting to the real one.
func (g *StorageGovernor) now() time.Time {
	if g.Now != nil {
		return g.Now()
	}
	return time.Now()
}

// Quiescer manufactures a brief fleet-wide pause. Declared here (not imported
// from workers) to keep maintain free of a dependency on the scheduler.
type Quiescer interface {
	// QuiesceDo gates new runs, drains in-flight ones, then calls fn inside the
	// held window and keeps holding for d before releasing the fleet.
	QuiesceDo(ctx context.Context, d time.Duration, fn func(context.Context), except ...string) error
}

// quiesceWindow is how long the fleet is held still for the TRUNCATE rung. Long
// enough for one checkpoint on a multi-GB database, short enough that no
// worker's cadence is meaningfully perturbed (the shortest periodic worker runs
// every minute and its deadline is 15m).
const quiesceWindow = 3 * time.Second

// walIneffectiveRuns is how many consecutive passes may reclaim ZERO frames
// while the WAL is still growing before that becomes its own dq event. Distinct
// from wal_checkpoint_busy, which fires on SIZE: a WAL can sit under the size
// alert and still be un-reclaimable, which is the failure that went unnoticed
// for 22 straight passes.
const walIneffectiveRuns = 3

// walBusyAlertBytes is the WAL size above which a BLOCKED checkpoint stops
// being noise and becomes an incident worth a dq event (the WAL is growing and
// nothing is reclaiming it).
const walBusyAlertBytes = 128 * 1024 * 1024

// Name implements workers.Worker.
func (g *StorageGovernor) Name() string { return "storage-governor" }

// Interval implements workers.Worker. Env-tunable (SIGNALDECK_WAL_CHECKPOINT_MIN,
// minutes, default 60) so the checkpoint cadence can be tightened or relaxed
// without a rebuild.
func (g *StorageGovernor) Interval() time.Duration {
	return time.Duration(envIntOr("SIGNALDECK_WAL_CHECKPOINT_MIN", 60)) * time.Minute
}

// Run checkpoints the WAL and, when warranted, vacuums.
func (g *StorageGovernor) Run(ctx context.Context) (string, error) {
	// Only the WAL size is read before the checkpoint; the db size that matters
	// is the one AFTER it, measured below.
	_, walBytes := g.St.FileSizes()

	// MARKET-HOURS GATE: a TRUNCATE checkpoint needs a reader-free moment, and
	// during the US session the worker fleet reads constantly — the attempt
	// mostly comes back Busy while still contending with the live pipeline for
	// the write connection. Defer it to off-hours UNLESS the WAL has already
	// grown past the alert bound, where reclaiming space outweighs the
	// contention (journal_size_limit only bounds the file AFTER a successful
	// truncate, so an untried checkpoint reclaims nothing).
	walNote, reclaimed := g.checkpointLadder(ctx, walBytes)
	dbBytes, walBytes := g.St.FileSizes()
	g.trackEffectiveness(ctx, reclaimed, walBytes)

	threshold := g.VacuumThreshold
	if threshold == 0 {
		threshold = int64(envIntOr("SIGNALDECK_VACUUM_THRESHOLD_MB", 2048)) * 1024 * 1024
	}
	minInterval := g.MinVacuumInterval
	if minInterval == 0 {
		minInterval = 24 * time.Hour
	}

	// OFF-HOURS GATE: a VACUUM rewrites the whole file and briefly stalls the
	// single writer, so restrict it to a quiet overnight window (2–6am
	// America/New_York) instead of letting it fire mid-session under load —
	// UNLESS the file has blown to 2× the threshold, where reclaiming space
	// outweighs the stall. This is the "schedule VACUUM off-hours" fix.
	offHours := inETWindow(time.Now(), 2, 6)
	emergency := dbBytes >= 2*threshold

	vacuumed := false
	if dbBytes >= threshold && (offHours || emergency) {
		last, _ := g.St.GetMeta(ctx, "storage_last_vacuum")
		var lastTs int64
		if last != "" {
			lastTs, _ = strconv.ParseInt(last, 10, 64)
		}
		if time.Since(time.Unix(lastTs, 0)) >= minInterval {
			// DISK-FREE PRECHECK (council-mandated): VACUUM rewrites the whole
			// database into a temp copy, so it needs ~dbBytes of headroom; a
			// mid-VACUUM disk-full errors every writer on the box. Require
			// 1.2× the file size free or skip loudly (dq) and retry next pass.
			// FAIL-CLOSED: if headroom cannot be VERIFIED, do not rewrite a
			// multi-GB file. An unverifiable statfs is not permission to
			// proceed (council note: the earlier form failed open, so a
			// statfs error silently restored the pre-guard behavior).
			free, ferr := diskFree(filepath.Dir(g.St.Path()))
			need := dbBytes + dbBytes/5
			if ferr != nil || free < need {
				reason := fmt.Sprintf("%.1fGB free < %.1fGB needed (1.2x db)", float64(free)/(1<<30), float64(need)/(1<<30))
				if ferr != nil {
					reason = "could not verify free disk: " + ferr.Error()
				}
				_ = g.St.InsertDQ(ctx, md.DQEvent{
					Ts:     time.Now().Unix(),
					Kind:   "vacuum_skip",
					Detail: "vacuum skipped: " + reason + " — data safe, retrying next pass",
				})
			} else {
				if err := g.St.Vacuum(ctx); err != nil {
					return "", fmt.Errorf("vacuum: %w", err)
				}
				_ = g.St.SetMeta(ctx, "storage_last_vacuum", strconv.FormatInt(time.Now().Unix(), 10))
				vacuumed = true
				dbBytes, walBytes = g.St.FileSizes()
			}
		}
	}
	return fmt.Sprintf("%s; db=%.1fMB wal=%.1fMB vacuumed=%v",
		walNote, float64(dbBytes)/(1024*1024), float64(walBytes)/(1024*1024), vacuumed), nil
}

// checkpointLadder walks PASSIVE → RESTART → TRUNCATE, stopping at the first
// rung that leaves nothing to reclaim, and reports which rung ran and how many
// frames each moved. It returns the note for worker_runs.detail and the total
// frames reclaimed this pass.
//
// WHY A LADDER. The governor previously attempted ONLY TRUNCATE, which requires
// an instant with no active readers. Measured over 22 consecutive passes: 0
// truncations, 21 BUSY, 0 frames deferred elsewhere — while the WAL grew to
// 5,396 MB against a 64 MB journal_size_limit. ~97 workers on a 4-connection
// read pool plus the API's 4-connection ReaderClone never leave that instant
// open, so the single mechanism the daemon relied on was structurally incapable
// of ever firing. PASSIVE always makes progress (it never waits for anyone);
// RESTART caps the file at its high-water mark instead of letting it grow; only
// the top rung needs the quiesce window. Nothing here relaxes an alert: the
// existing size-based wal_checkpoint_busy dq still fires on a blocked TRUNCATE.
func (g *StorageGovernor) checkpointLadder(ctx context.Context, walBefore int64) (note string, reclaimed int) {
	parts := make([]string, 0, 3)

	passive, err := g.St.WALCheckpointPassive(ctx)
	if err != nil {
		return "wal passive checkpoint failed: " + err.Error(), 0
	}
	reclaimed += passive.Checkpointed
	parts = append(parts, fmt.Sprintf("PASSIVE %d/%d frames", passive.Checkpointed, passive.LogFrames))
	if passive.LogFrames == 0 {
		return "wal checkpoint: " + strings.Join(parts, "; ") + " (wal empty)", reclaimed
	}

	// RESTART blocks until every frame is checkpointed and then forces the WAL
	// to rewind — the file stops growing even when it cannot shrink.
	restart, err := g.St.WALCheckpointRestart(ctx)
	if err != nil {
		return "wal checkpoint: " + strings.Join(parts, "; ") + "; RESTART failed: " + err.Error(), reclaimed
	}
	reclaimed += restart.Checkpointed
	rn := fmt.Sprintf("RESTART %d/%d frames", restart.Checkpointed, restart.LogFrames)
	if restart.Busy {
		rn += " BUSY"
	}
	parts = append(parts, rn)

	// TRUNCATE is the only rung that returns bytes to the filesystem. Keep the
	// original market-hours gate — it governs WHEN the expensive rung is worth
	// contending for, not whether the WAL is checkpointed at all (the two rungs
	// above just ran regardless, which is the actual fix).
	if marketcal.OpenForBars(g.now()) && walBefore < walBusyAlertBytes {
		parts = append(parts, "TRUNCATE deferred (market hours)")
		return "wal checkpoint: " + strings.Join(parts, "; "), reclaimed
	}

	// Run TRUNCATE inside a quiesce window when one is available: the fleet is
	// gated and drained FIRST, so the pragma actually meets the reader-free
	// instant it needs instead of racing the readers that denied it 21 times.
	var trunc store.WALCheckpointResult
	quiesced := g.Quiescer != nil
	err = nil
	checkpoint := func(c context.Context) { trunc, err = g.St.WALCheckpointTruncate(c) }
	if quiesced {
		if qerr := g.Quiescer.QuiesceDo(ctx, quiesceWindow, checkpoint, g.Name()); qerr != nil && err == nil {
			parts = append(parts, "quiesce: "+qerr.Error())
		}
	} else {
		checkpoint(ctx)
	}
	if err != nil {
		return "wal checkpoint: " + strings.Join(parts, "; ") + "; TRUNCATE failed: " + err.Error(), reclaimed
	}

	// A single TRUNCATE attempt per pass is structurally starved: measured live
	// against the running fleet, 25 consecutive PRAGMA wal_checkpoint(RESTART)
	// attempts one second apart returned BUSY 24 times and succeeded exactly
	// once, and that one success collapsed a 153 MB WAL to 64 MB. The
	// reader-free instant DOES occur, it is just rare — so when the first
	// attempt comes back Busy, retry the same call once per second UNQUIESCED
	// (the fleet must keep running; the measurement above was taken with it
	// running) until it succeeds or the env-tunable budget expires. A value of
	// 0 disables retrying entirely, keeping the old single-shot behaviour.
	// Frames moved by earlier attempts are NOT re-moved by a later one, so the
	// pass reclaimed their sum. Counting only the last attempt would report a
	// pass that did real work as having reclaimed nothing, which is exactly the
	// input walIneffectiveRuns escalates on.
	attempts, prior := 1, 0
	if trunc.Busy {
		// 300s, not 60s, because a blocked attempt does not fail fast: the store
		// opens its connections with busy_timeout(15000), so each denied
		// TRUNCATE sits in SQLite's busy handler for up to 15 seconds. Measured
		// on the first live pass after this shipped, a 60-second budget bought
		// 14 attempts, not 60, and lost - 1-0.96^14 is only ~43%. 300 seconds
		// buys ~70 attempts (~94%) and still costs a fraction of this worker's
		// 180-minute deadline, and the retries run UNQUIESCED so nothing else
		// is held up while it waits.
		retrySec := envIntOr("SIGNALDECK_WAL_TRUNCATE_RETRY_SEC", 300)
		if retrySec > 0 {
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			deadline := time.NewTimer(time.Duration(retrySec) * time.Second)
			defer deadline.Stop()
		retryLoop:
			for {
				select {
				case <-ctx.Done():
					break retryLoop
				case <-deadline.C:
					break retryLoop
				case <-ticker.C:
					attempts++
					prior += trunc.Checkpointed
					trunc, err = g.St.WALCheckpointTruncate(ctx)
					if err != nil {
						break retryLoop
					}
					if !trunc.Busy {
						break retryLoop
					}
				}
			}
		}
	}

	if err != nil {
		return "wal checkpoint: " + strings.Join(parts, "; ") +
			fmt.Sprintf("; TRUNCATE failed after %d attempts: ", attempts) + err.Error(), reclaimed + prior
	}
	reclaimed += prior + trunc.Checkpointed
	tn := fmt.Sprintf("TRUNCATE %d/%d frames", trunc.Checkpointed, trunc.LogFrames)
	if quiesced {
		tn += " (quiesced"
		if attempts > 1 {
			tn += fmt.Sprintf(", %d attempts", attempts)
		}
		tn += ")"
	} else if attempts > 1 {
		tn += fmt.Sprintf(" (%d attempts)", attempts)
	}
	if trunc.Busy {
		tn += " BUSY — WAL NOT truncated"
		_, walAfter := g.St.FileSizes()
		g.trackTruncateStall(ctx, trunc.LogFrames, walAfter)
		if walAfter >= walBusyAlertBytes {
			_ = g.St.InsertDQ(ctx, md.DQEvent{
				Ts:   time.Now().Unix(),
				Kind: "wal_checkpoint_busy",
				Detail: fmt.Sprintf("WAL %.1fMB and growing: TRUNCATE blocked by active readers (%d/%d frames moved, quiesced=%v). Worker read pressure is denying the checkpoint a reader-free window.",
					float64(walAfter)/(1024*1024), trunc.Checkpointed, trunc.LogFrames, quiesced),
			})
		}
	} else {
		g.clearTruncateStall(ctx)
	}
	parts = append(parts, tn)
	return "wal checkpoint: " + strings.Join(parts, "; "), reclaimed
}

// walStallRuns is how many CONSECUTIVE passes must stall at the SAME WAL frame
// index before the situation stops being "the checkpoint was unlucky" and
// becomes a named starvation verdict. Two is the smallest number that can tell
// those apart: one stall is a busy instant, the same frame twice in a row is a
// held snapshot that has survived a whole checkpoint cadence.
const walStallRuns = 2

// trackTruncateStall records WHERE a blocked TRUNCATE stopped and escalates a
// repeat at the same frame into its own dq event.
//
// WHY THIS EXISTS AND WHY IT IS NOT THE SIZE ALERT. wal_checkpoint_busy says
// the WAL is large; it names no cause and implies no action — the pattern this
// codebase itself calls out as a defect ("detection without a recovery path").
// The live incident it failed on: TRUNCATE stalled at the IDENTICAL frame index
// 581124 at 05:06, 06:06 and 08:09 while the WAL grew to 5,396MB, and the
// actual holder was only ever identified by an operator running lsof by hand. A
// frame index that does not move across passes is proof that one specific read
// snapshot is pinned, not that readers are merely busy — so this verdict
// carries the repeated frame, how long it has been stuck, how much WAL has
// accumulated since it first stuck, and a census of who could be holding it
// (the daemon's own in-flight workers, plus any non-daemon process with the db
// or -wal file open). Nothing here changes a threshold or suppresses the
// size-based alert, which still fires on exactly its old terms.
func (g *StorageGovernor) trackTruncateStall(ctx context.Context, frame int, walBytes int64) {
	now := time.Now().Unix()
	prevFrameStr, _ := g.St.GetMeta(ctx, "storage_wal_stall_frame")
	prevFrame, err := strconv.Atoi(prevFrameStr)
	if prevFrameStr == "" || err != nil || prevFrame != frame {
		_ = g.St.SetMeta(ctx, "storage_wal_stall_frame", strconv.Itoa(frame))
		_ = g.St.SetMeta(ctx, "storage_wal_stall_since", strconv.FormatInt(now, 10))
		_ = g.St.SetMeta(ctx, "storage_wal_stall_bytes", strconv.FormatInt(walBytes, 10))
		_ = g.St.SetMeta(ctx, "storage_wal_stall_runs", "1")
		return
	}
	runsStr, _ := g.St.GetMeta(ctx, "storage_wal_stall_runs")
	runs, _ := strconv.Atoi(runsStr)
	runs++
	_ = g.St.SetMeta(ctx, "storage_wal_stall_runs", strconv.Itoa(runs))
	if runs < walStallRuns {
		return
	}
	sinceStr, _ := g.St.GetMeta(ctx, "storage_wal_stall_since")
	since, _ := strconv.ParseInt(sinceStr, 10, 64)
	firstBytesStr, _ := g.St.GetMeta(ctx, "storage_wal_stall_bytes")
	firstBytes, _ := strconv.ParseInt(firstBytesStr, 10, 64)

	_ = g.St.InsertDQ(ctx, md.DQEvent{
		Ts:   now,
		Kind: "wal_checkpoint_starved",
		Detail: fmt.Sprintf(
			"WAL checkpoint STARVED: TRUNCATE has stalled at the same frame %d for %d consecutive passes (%s since first stall); WAL %.1fMB, +%.1fMB since the stall began. A frame index that does not move means one read snapshot is pinned open, not that readers are merely busy. Daemon read connections are lifetime-bounded at %s, so any holder older than that is NOT a daemon read pool. Holders: %s",
			frame, runs, time.Since(time.Unix(since, 0)).Round(time.Second),
			float64(walBytes)/(1024*1024), float64(walBytes-firstBytes)/(1024*1024),
			store.ReadConnMaxLifetime, g.holderCensus()),
	})
}

// clearTruncateStall resets the stall cursor once a TRUNCATE completes.
func (g *StorageGovernor) clearTruncateStall(ctx context.Context) {
	if s, _ := g.St.GetMeta(ctx, "storage_wal_stall_runs"); s == "" || s == "0" {
		return
	}
	_ = g.St.SetMeta(ctx, "storage_wal_stall_runs", "0")
	_ = g.St.SetMeta(ctx, "storage_wal_stall_frame", "")
}

// inFlightNamer is the fleet-side half of the holder census. Satisfied by
// *workers.Runner (already wired in as Quiescer); declared here so maintain
// keeps no dependency on the scheduler package.
type inFlightNamer interface{ InFlightNames() []string }

// holderCensus lists who could be holding the pinned read snapshot: the
// daemon's own in-flight workers, and any process other than this one with the
// database or its -wal open. The external half is what an operator previously
// had to produce by hand with lsof.
func (g *StorageGovernor) holderCensus() string {
	parts := make([]string, 0, 2)
	if n, ok := g.Quiescer.(inFlightNamer); ok && n != nil {
		names := n.InFlightNames()
		if len(names) == 0 {
			parts = append(parts, "daemon in-flight workers: none")
		} else {
			parts = append(parts, "daemon in-flight workers: "+strings.Join(names, ", "))
		}
	} else {
		parts = append(parts, "daemon in-flight workers: unavailable")
	}
	parts = append(parts, "external holders: "+externalHolders(g.St.Path()))
	return strings.Join(parts, "; ")
}

// externalHolders shells out to lsof for the db and -wal files and reports the
// non-daemon processes holding them (pid/command), or why it could not tell.
// Best-effort and bounded: a census that cannot be taken says so rather than
// implying "nobody else".
func externalHolders(dbPath string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "lsof", "-F", "pc", "--", dbPath, dbPath+"-wal").Output()
	if err != nil && len(out) == 0 {
		// lsof exits non-zero when nothing has the files open, which is a real
		// answer only when it also printed nothing AND the binary exists.
		if errors.Is(err, exec.ErrNotFound) {
			return "unknown (lsof not available)"
		}
		return "none"
	}
	self := os.Getpid()
	seen := map[string]bool{}
	var pid string
	var holders []string
	for _, line := range strings.Split(string(out), "\n") {
		if len(line) < 2 {
			continue
		}
		switch line[0] {
		case 'p':
			pid = line[1:]
		case 'c':
			if pid == strconv.Itoa(self) {
				continue
			}
			h := line[1:] + " (pid " + pid + ")"
			if !seen[h] {
				seen[h] = true
				holders = append(holders, h)
			}
		}
	}
	if len(holders) == 0 {
		return "none"
	}
	return strings.Join(holders, ", ")
}

// trackEffectiveness records a dq event once the checkpoint machinery has been
// measurably USELESS for walIneffectiveRuns consecutive passes — zero frames
// reclaimed while the WAL kept growing. The pre-existing wal_checkpoint_busy
// alert only fires once the WAL is already past 128MB; this one catches the
// "mechanism does nothing" state directly, which is what actually went
// unobserved for 22 passes. Counters live in meta so they survive restarts.
func (g *StorageGovernor) trackEffectiveness(ctx context.Context, reclaimed int, walBytes int64) {
	prevStr, _ := g.St.GetMeta(ctx, "storage_wal_bytes_last")
	prev, _ := strconv.ParseInt(prevStr, 10, 64)
	_ = g.St.SetMeta(ctx, "storage_wal_bytes_last", strconv.FormatInt(walBytes, 10))

	if reclaimed > 0 || walBytes <= prev {
		_ = g.St.SetMeta(ctx, "storage_wal_ineffective_runs", "0")
		return
	}
	nStr, _ := g.St.GetMeta(ctx, "storage_wal_ineffective_runs")
	n, _ := strconv.Atoi(nStr)
	n++
	_ = g.St.SetMeta(ctx, "storage_wal_ineffective_runs", strconv.Itoa(n))
	if n < walIneffectiveRuns {
		return
	}
	_ = g.St.InsertDQ(ctx, md.DQEvent{
		Ts:   time.Now().Unix(),
		Kind: "wal_checkpoint_ineffective",
		Detail: fmt.Sprintf("%d consecutive passes reclaimed ZERO WAL frames while the WAL grew (%.1fMB → %.1fMB). Every rung of the checkpoint ladder is being denied; the WAL is unbounded until read pressure drops.",
			n, float64(prev)/(1024*1024), float64(walBytes)/(1024*1024)),
	})
	_ = g.St.SetMeta(ctx, "storage_wal_ineffective_runs", "0")
}

// inETWindow reports whether t's hour in America/New_York falls in [lo, hi).
// The daemon embeds tzdata (marketcal), so LoadLocation succeeds; UTC is a
// safe fallback that only shifts the window, never breaks it.
func inETWindow(t time.Time, lo, hi int) bool {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		loc = time.UTC
	}
	h := t.In(loc).Hour()
	return h >= lo && h < hi
}

// ─────────────────────────────────────────────────────────────────────────
// DERIVED-TABLE RETENTION (appended block — tiered-storage wave, phase 2).
// ─────────────────────────────────────────────────────────────────────────

// DerivedRetention is the Downsampler's sibling for the high-volume DERIVED
// tables that grow unbounded (unlike the bars tiers): scores, score_outcomes,
// and the feature store. It mirrors the Downsampler's archive-before-prune
// FAIL-SAFE contract EXACTLY — every row past its tier's retention is exported
// to the cold gzip-CSV archive FIRST and only pruned after a durable archive;
// on any archive error the matching prune is SKIPPED and a dq_events(kind
// archive_skip) records the fail-safe, so retention can never silently lose
// data.
//
// PERMANENCE (the flywheel's labels are forever):
//   - predictions + prediction_outcomes are NEVER pruned — they are the live
//     track record. This worker does not touch them.
//   - scores + score_outcomes older than SIGNALDECK_SCORES_RETENTION_D (default
//     90d) are archived + pruned: high-volume minute-cadence churn whose
//     resolved IC only needs recent windows on the honesty page.
//   - features older than SIGNALDECK_FEATURES_RETENTION_D (default 180d) are
//     archived + pruned ONLY when their prediction has already resolved — an
//     unlabeled training row is never deleted.
//
// UNMANAGED-TABLE SWEEP (phase 3 — the 2026-07 reaudit's ~513MB of tables with
// no retention path, measured live via dbstat): filings (125MB) and insights
// (55MB) age out past 180d/90d; prediction_postmortems (35MB) age into the
// cold archive past 180d (the misses' EXPLANATIONS age out — the predictions
// they explain stay forever); research_weeks (165MB) is already at the
// one-row-per-(symbol, week) grain by PRIMARY KEY, so its tier bounds the
// WINDOW instead — rows past the active research window (default 7y, i.e. the
// full 2020→present base today: the tier bounds growth from here on rather
// than cutting into the era evidence) are archived + pruned, and
// pipeline.HistoryBackfillWorker clamps its daily recompute floor to the SAME
// window so pruned rows are never resurrected. score_outcomes (51MB) already
// had its 90d tier above; predictions (82MB) are the permanent track record
// and get NO tier by doctrine.
//
// Bars/snapshots/anomalies retention stays entirely in the Downsampler; this
// worker never prunes any bar timeframe.
//
// research_loop_runs and research_loop_hypotheses are deliberately EXCLUDED
// from every tier here and must stay excluded: they are the research audit
// trail (which searches ran, under which correction, and which rules were
// killed by which gate), not derived data. An honest null result is the
// strongest evidence this platform produces, and a null that ages out of the
// database is a null nobody can check. Both tables grow at roughly one row
// per rule per day, which is nothing next to the tiers below.
type DerivedRetention struct {
	St  *store.Store
	Arc *archive.Archiver // cold-archive sink (required for archive-before-prune)
	// Retention windows (0 ⇒ env/default). Explicit so tests drive exact cutoffs.
	KeepScores        time.Duration // scores + score_outcomes;   default 90d  (env SIGNALDECK_SCORES_RETENTION_D)
	KeepFeatures      time.Duration // resolved features;          default 180d (env SIGNALDECK_FEATURES_RETENTION_D)
	KeepFilings       time.Duration // filings feed;               default 180d (env SIGNALDECK_FILINGS_RETENTION_D)
	KeepInsights      time.Duration // generated insights;         default 90d  (env SIGNALDECK_INSIGHTS_RETENTION_D)
	KeepPostmortems   time.Duration // prediction_postmortems;     default 180d (env SIGNALDECK_POSTMORTEM_RETENTION_D)
	KeepResearchWeeks time.Duration // research_weeks window;      default 7y   (env SIGNALDECK_RESEARCH_WEEKS_RETENTION_D)
}

// Name implements workers.Worker.
func (d *DerivedRetention) Name() string { return "derived-retention" }

// Interval implements workers.Worker.
func (d *DerivedRetention) Interval() time.Duration { return time.Hour }

// Run archives+prunes the derived tiers, each fail-safe and independently.
func (d *DerivedRetention) Run(ctx context.Context) (string, error) {
	now := time.Now()
	names, err := d.St.SymbolNameMap(ctx)
	if err != nil {
		return "", err
	}
	scoresCut := now.Add(-d.retentionScores()).Unix()
	featuresCut := now.Add(-d.retentionFeatures()).Unix()

	prunedScores, scSkip := archivePruneDerived(ctx, d, "scores", scoresCut, now,
		func(ctx context.Context, cutoff int64, limit int) ([]store.ScoreRow, error) {
			return d.St.ScoresBefore(ctx, cutoff, limit)
		},
		func(r store.ScoreRow) int64 { return r.Ts },
		func(ctx context.Context, rows []store.ScoreRow) error {
			_, err := d.Arc.ArchiveScores(ctx, rows, names)
			return err
		},
		d.St.DeleteScoresBefore)

	prunedOut, outSkip := archivePruneDerived(ctx, d, "score_outcomes", scoresCut, now,
		func(ctx context.Context, cutoff int64, limit int) ([]store.ScoreOutcomeRow, error) {
			return d.St.ScoreOutcomesBefore(ctx, cutoff, limit)
		},
		func(r store.ScoreOutcomeRow) int64 { return r.Ts },
		func(ctx context.Context, rows []store.ScoreOutcomeRow) error {
			_, err := d.Arc.ArchiveScoreOutcomes(ctx, rows, names)
			return err
		},
		d.St.DeleteScoreOutcomesBefore)

	prunedFeat, featSkip := archivePruneDerived(ctx, d, "features", featuresCut, now,
		func(ctx context.Context, cutoff int64, limit int) ([]store.FeatureArchiveRow, error) {
			return d.St.ResolvedFeaturesBefore(ctx, cutoff, limit)
		},
		func(r store.FeatureArchiveRow) int64 { return r.Ts },
		func(ctx context.Context, rows []store.FeatureArchiveRow) error {
			_, err := d.Arc.ArchiveFeatures(ctx, rows, names)
			return err
		},
		d.St.DeleteResolvedFeaturesBefore)

	// Phase-3 tiers (unmanaged-table sweep) — same generic loop, same fail-safe.
	filingsCut := now.Add(-d.retentionFilings()).Unix()
	insightsCut := now.Add(-d.retentionInsights()).Unix()
	postmortemCut := now.Add(-d.retentionPostmortems()).Unix()
	weeksCut := now.Add(-d.retentionResearchWeeks()).Unix()

	prunedFil, filSkip := archivePruneDerived(ctx, d, "filings", filingsCut, now,
		func(ctx context.Context, cutoff int64, limit int) ([]store.FilingArchiveRow, error) {
			return d.St.FilingsBefore(ctx, cutoff, limit)
		},
		func(r store.FilingArchiveRow) int64 { return r.FiledTs },
		func(ctx context.Context, rows []store.FilingArchiveRow) error {
			_, err := d.Arc.ArchiveFilings(ctx, rows, names)
			return err
		},
		d.St.DeleteFilingsBefore)

	prunedIns, insSkip := archivePruneDerived(ctx, d, "insights", insightsCut, now,
		func(ctx context.Context, cutoff int64, limit int) ([]store.InsightArchiveRow, error) {
			return d.St.InsightsBefore(ctx, cutoff, limit)
		},
		func(r store.InsightArchiveRow) int64 { return r.Ts },
		func(ctx context.Context, rows []store.InsightArchiveRow) error {
			_, err := d.Arc.ArchiveInsights(ctx, rows, names)
			return err
		},
		d.St.DeleteInsightsBefore)

	prunedPM, pmSkip := archivePruneDerived(ctx, d, "prediction_postmortems", postmortemCut, now,
		func(ctx context.Context, cutoff int64, limit int) ([]store.PostmortemArchiveRow, error) {
			return d.St.PostmortemsBefore(ctx, cutoff, limit)
		},
		func(r store.PostmortemArchiveRow) int64 { return r.Ts },
		func(ctx context.Context, rows []store.PostmortemArchiveRow) error {
			_, err := d.Arc.ArchivePostmortems(ctx, rows, names)
			return err
		},
		d.St.DeletePostmortemsBefore)

	prunedRW, rwSkip := archivePruneDerived(ctx, d, "research_weeks", weeksCut, now,
		func(ctx context.Context, cutoff int64, limit int) ([]store.ResearchWeekArchiveRow, error) {
			return d.St.ResearchWeeksBefore(ctx, cutoff, limit)
		},
		func(r store.ResearchWeekArchiveRow) int64 { return r.Ts },
		func(ctx context.Context, rows []store.ResearchWeekArchiveRow) error {
			_, err := d.Arc.ArchiveResearchWeeks(ctx, rows, names)
			return err
		},
		d.St.DeleteResearchWeeksBefore)

	msg := fmt.Sprintf("archived+pruned %d scores, %d score_outcomes, %d resolved features, %d filings, %d insights, %d postmortems, %d research_weeks (predictions kept forever)",
		prunedScores, prunedOut, prunedFeat, prunedFil, prunedIns, prunedPM, prunedRW)
	if scSkip || outSkip || featSkip || filSkip || insSkip || pmSkip || rwSkip {
		msg += " (SOME PRUNES SKIPPED — archive failed, data retained; see dq)"
	}
	return msg, nil
}

func (d *DerivedRetention) retentionScores() time.Duration {
	if d.KeepScores > 0 {
		return d.KeepScores
	}
	return time.Duration(envIntOr("SIGNALDECK_SCORES_RETENTION_D", 90)) * 24 * time.Hour
}

func (d *DerivedRetention) retentionFeatures() time.Duration {
	if d.KeepFeatures > 0 {
		return d.KeepFeatures
	}
	return time.Duration(envIntOr("SIGNALDECK_FEATURES_RETENTION_D", 180)) * 24 * time.Hour
}

func (d *DerivedRetention) retentionFilings() time.Duration {
	if d.KeepFilings > 0 {
		return d.KeepFilings
	}
	return time.Duration(envIntOr("SIGNALDECK_FILINGS_RETENTION_D", 180)) * 24 * time.Hour
}

func (d *DerivedRetention) retentionInsights() time.Duration {
	if d.KeepInsights > 0 {
		return d.KeepInsights
	}
	return time.Duration(envIntOr("SIGNALDECK_INSIGHTS_RETENTION_D", 90)) * 24 * time.Hour
}

func (d *DerivedRetention) retentionPostmortems() time.Duration {
	if d.KeepPostmortems > 0 {
		return d.KeepPostmortems
	}
	return time.Duration(envIntOr("SIGNALDECK_POSTMORTEM_RETENTION_D", 180)) * 24 * time.Hour
}

func (d *DerivedRetention) retentionResearchWeeks() time.Duration {
	if d.KeepResearchWeeks > 0 {
		return d.KeepResearchWeeks
	}
	return time.Duration(RetentionResearchWeeksDays()) * 24 * time.Hour
}

// RetentionResearchWeeksDays exposes the ACTIVE research_weeks window in days
// (env-resolved, default 7y) — shared with pipeline.HistoryBackfillWorker,
// whose daily recompute floor MUST move in lockstep with this prune cutoff or
// pruned rows resurrect daily and re-archive hourly (churn loop). One source
// of truth, mirroring RetentionSnapsHours & co below.
func RetentionResearchWeeksDays() int {
	return envIntOr("SIGNALDECK_RESEARCH_WEEKS_RETENTION_D", 7*365)
}

// dqSkip logs + records the fail-safe (mirrors Downsampler.dqSkip).
func (d *DerivedRetention) dqSkip(ctx context.Context, now time.Time, table, reason string) {
	slog.Warn("derived retention prune skipped (fail-safe)", "table", table, "reason", reason)
	_ = d.St.InsertDQ(ctx, md.DQEvent{
		Ts:     now.Unix(),
		Kind:   "archive_skip",
		Detail: fmt.Sprintf("prune of %s skipped, data retained: %s", table, reason),
	})
}

// archivePruneDerived is the generic archive-before-prune batch loop for a
// derived table — the exact contract of Downsampler.archivePruneBars, factored
// once over a row type T: read rows below cutoff (oldest-first, bounded),
// archive them, then prune ONLY [<upper) where the archived set equals the
// pruned set. On a FULL batch the trailing max-ts group is dropped so the two
// sets stay identical and maxTs strictly advances (terminating); on any archive
// error NOTHING is pruned for this (or any) batch and a dq event fires.
func archivePruneDerived[T any](
	ctx context.Context, d *DerivedRetention, table string, cutoff int64, now time.Time,
	read func(ctx context.Context, cutoff int64, limit int) ([]T, error),
	tsOf func(T) int64,
	archive func(ctx context.Context, rows []T) error,
	prune func(ctx context.Context, upper int64) (int64, error),
) (pruned int64, skipped bool) {
	if d.Arc == nil {
		d.dqSkip(ctx, now, table, "no cold-archive sink configured")
		return 0, true
	}
	for {
		rows, err := read(ctx, cutoff, archiveBatch)
		if err != nil {
			d.dqSkip(ctx, now, table, "read for archive failed: "+err.Error())
			return pruned, true
		}
		if len(rows) == 0 {
			return pruned, skipped
		}
		// Same trailing-max-ts trim as Downsampler.archivePruneBars (see its
		// comment): on a full batch drop the max-ts group so archived == pruned.
		full := len(rows) == archiveBatch
		toArchive := rows
		upper := cutoff
		if full {
			maxTs := tsOf(rows[len(rows)-1])
			cut := len(rows)
			for cut > 0 && tsOf(rows[cut-1]) == maxTs {
				cut--
			}
			if cut == 0 {
				upper = maxTs + 1 // degenerate: whole batch at one ts
			} else {
				toArchive = rows[:cut]
				upper = maxTs // exclusive: max-ts group waits for next read
			}
		}
		if err := archive(ctx, toArchive); err != nil {
			d.dqSkip(ctx, now, table, "archive write failed: "+err.Error())
			return pruned, true
		}
		n, err := prune(ctx, upper)
		if err != nil {
			return pruned, true
		}
		pruned += n
		if !full {
			return pruned, skipped
		}
	}
}

// diskFree returns the available bytes on the filesystem holding dir.
// Platform implementations live in diskfree_unix.go / diskfree_windows.go.
