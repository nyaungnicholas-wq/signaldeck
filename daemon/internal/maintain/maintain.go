// Package maintain holds the housekeeping agents: bar rollups + retention,
// score-outcome resolution (the honesty backtest's feeder), and the
// data-quality auditor. All are periodic workers.
package maintain

import (
	"context"
	"fmt"
	"strconv"
	"time"

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
// snapshots_1s keep plain pruning: they are bid/ask quote midpoints, not
// trades, so "rolling them up" into 1m OHLCV would fabricate bars that
// collide with the real exchange 1m bars already ingested. High volume, low
// value — deleted after KeepSnaps by design.
type Downsampler struct {
	St *store.Store
	// retention
	Keep1m    time.Duration // default 90 days
	KeepSnaps time.Duration // default 7 days
}

// Name implements workers.Worker.
func (d *Downsampler) Name() string { return "downsampler" }

// Interval implements workers.Worker.
func (d *Downsampler) Interval() time.Duration { return 5 * time.Minute }

// Run rolls up recent minutes and prunes old rows.
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
	keep1m := d.Keep1m
	if keep1m == 0 {
		keep1m = 90 * 24 * time.Hour
	}
	keepSnaps := d.KeepSnaps
	if keepSnaps == 0 {
		keepSnaps = 7 * 24 * time.Hour
	}
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
	prunedBars, err := d.St.PruneBars(ctx, md.TF1m, cutoff1m)
	if err != nil {
		return "", err
	}
	prunedSnaps, err := d.St.PruneSnaps(ctx, now.Add(-keepSnaps).Unix())
	if err != nil {
		return "", err
	}
	if err := d.St.PruneWorkerRuns(ctx, 2000); err != nil {
		return "", err
	}
	return fmt.Sprintf("rolled up %d symbols; pruned %d 1m bars, %d snaps", len(syms), prunedBars, prunedSnaps), nil
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

// usMarketLikelyOpen is a deliberately loose Mon–Fri 13:00–21:30 UTC window
// (covers EST/EDT regular hours). Staleness of stock bars outside it is
// normal, not an incident. Documented imprecision beats a tz dependency.
func usMarketLikelyOpen(t time.Time) bool {
	u := t.UTC()
	if wd := u.Weekday(); wd == time.Saturday || wd == time.Sunday {
		return false
	}
	mins := u.Hour()*60 + u.Minute()
	return mins >= 13*60 && mins <= 21*60+30
}

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
			stale = latest > 0 && age > 20*60 && usMarketLikelyOpen(now)
			detail = fmt.Sprintf("last 1m bar %dm old during likely market hours", age/60)
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
